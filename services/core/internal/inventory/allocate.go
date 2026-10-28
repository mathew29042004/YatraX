package inventory

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
)

// Occupancy maps each unit to a bitmask of booked segments: bit i set means the
// segment from stop i to stop i+1 is taken.
type Occupancy map[Key]uint64

// SegMask covers stops [from, to): the segments a passenger boarding at stop index
// from and leaving at stop index to occupies.
func SegMask(from, to int) uint64 {
	if to-from >= 64 {
		return ^uint64(0) << uint(from)
	}
	return ((uint64(1) << uint(to-from)) - 1) << uint(from)
}

// Clone copies the occupancy so a trial allocation doesn't modify the original.
func (o Occupancy) Clone() Occupancy {
	c := make(Occupancy, len(o))
	for k, v := range o {
		c[k] = v
	}
	return c
}

// Status of an allocated passenger.
const (
	CNF = "CNF"
	RAC = "RAC"
	WL  = "WL"
)

type Passenger struct {
	Age    int
	Gender string
	Pref   string // LB MB UB SL SU W A, or "" for no preference
	Want   *Key   // a specific berth picked on the seat map
}

type Request struct {
	Quota    string
	From, To int
	Pax      []Passenger
	Together bool // try to seat the party in one bay
	// AnyQuota lets the passenger take a berth from any quota: at chart
	// preparation, unsold quota berths are released to everyone.
	AnyQuota bool
}

func (r Request) fits(u Unit) bool { return r.AnyQuota || u.Quota == r.Quota }

type Placement struct {
	Status string
	Unit   *Unit // nil for WL
}

var (
	ErrBerthTaken = errors.New("that berth is no longer free")
	ErrBadBerth   = errors.New("that berth is not available in this class and quota")
)

// fitWaste counts the free segments that would be stranded on either side of the
// request: 0 means the passenger exactly fills a gap between existing bookings.
func fitWaste(mask uint64, from, to, segments int) int {
	before := 0
	for i := from - 1; i >= 0 && mask&(1<<uint(i)) == 0; i-- {
		before++
	}
	after := 0
	for i := to; i < segments && mask&(1<<uint(i)) == 0; i++ {
		after++
	}
	return before + after
}

// candidate scoring: lower is better.
func (c *Catalog) score(i int, occ Occupancy, req Request, p Passenger, partyCoach string) int {
	u := c.Units[i]
	s := fitWaste(occ[u.Key()], req.From, req.To, c.Segments) * 10
	if p.Pref != "" && u.Type != p.Pref {
		s += 10_000
	}
	if p.Pref == "" && p.Age >= 60 && u.Type != "LB" && u.Type != "W" {
		s += 2_000 // seniors without a preference get a lower berth when one is free
	}
	if partyCoach != "" && u.Coach != partyCoach {
		s += 5_000
	}
	return s
}

func (c *Catalog) free(i int, occ Occupancy, need uint64) bool {
	return occ[c.Units[i].Key()]&need == 0
}

// Allocate places every passenger: a confirmed berth in the quota if one is free
// for the whole journey, else an RAC half-berth (General quota), else the waitlist.
// It updates occ with the units it takes.
func (c *Catalog) Allocate(occ Occupancy, req Request) ([]Placement, error) {
	if req.From < 0 || req.To > c.Segments || req.From >= req.To {
		return nil, fmt.Errorf("bad segment %d–%d", req.From, req.To)
	}
	need := SegMask(req.From, req.To)
	out := make([]Placement, len(req.Pax))
	done := make([]bool, len(req.Pax))
	take := func(pi, ui int, status string) {
		u := c.Units[ui]
		occ[u.Key()] |= need
		out[pi] = Placement{Status: status, Unit: &u}
		done[pi] = true
	}

	// 1. Berths chosen on the seat map.
	for pi, p := range req.Pax {
		if p.Want == nil {
			continue
		}
		ui := c.Index(*p.Want)
		if ui < 0 || c.Units[ui].RAC || !req.fits(c.Units[ui]) {
			return nil, ErrBadBerth
		}
		if !c.free(ui, occ, need) {
			return nil, ErrBerthTaken
		}
		take(pi, ui, CNF)
	}

	// 2. Keep a party together: the bay with the most fitting free berths.
	pending := 0
	for _, d := range done {
		if !d {
			pending++
		}
	}
	if req.Together && pending > 1 {
		type bayKey struct {
			coach string
			bay   int
		}
		groups := map[bayKey][]int{}
		var order []bayKey
		for i, u := range c.Units {
			if u.RAC || !req.fits(u) || !c.free(i, occ, need) {
				continue
			}
			k := bayKey{u.Coach, u.Bay}
			if _, ok := groups[k]; !ok {
				order = append(order, k)
			}
			groups[k] = append(groups[k], i)
		}
		best, bestScore := bayKey{}, int(^uint(0)>>1)
		for _, k := range order {
			if len(groups[k]) < pending {
				continue
			}
			sc := 0
			for _, ui := range groups[k] {
				sc += fitWaste(occ[c.Units[ui].Key()], req.From, req.To, c.Segments)
			}
			if sc < bestScore {
				best, bestScore = k, sc
			}
		}
		if bays := groups[best]; len(bays) >= pending {
			for pi, p := range req.Pax {
				if done[pi] {
					continue
				}
				sort.SliceStable(bays, func(a, b int) bool {
					return c.score(bays[a], occ, req, p, "") < c.score(bays[b], occ, req, p, "")
				})
				for j, ui := range bays {
					if c.free(ui, occ, need) {
						take(pi, ui, CNF)
						bays = append(bays[:j], bays[j+1:]...)
						break
					}
				}
			}
		}
	}

	// 3. Everyone else: best-scoring free berth, staying in the party's coach where possible.
	partyCoach := ""
	for _, pl := range out {
		if pl.Unit != nil {
			partyCoach = pl.Unit.Coach
			break
		}
	}
	for pi, p := range req.Pax {
		if done[pi] {
			continue
		}
		bestI, bestS := -1, 0
		for i, u := range c.Units {
			if u.RAC || !req.fits(u) || !c.free(i, occ, need) {
				continue
			}
			if s := c.score(i, occ, req, p, partyCoach); bestI < 0 || s < bestS {
				bestI, bestS = i, s
			}
		}
		if bestI >= 0 {
			take(pi, bestI, CNF)
			if partyCoach == "" {
				partyCoach = c.Units[bestI].Coach
			}
		}
	}

	// 4. RAC half-berths (General quota only), then the waitlist.
	for pi := range req.Pax {
		if done[pi] {
			continue
		}
		if req.Quota == "GN" {
			bestI, bestS := -1, 0
			for i, u := range c.Units {
				if !u.RAC || !c.free(i, occ, need) {
					continue
				}
				if s := fitWaste(occ[u.Key()], req.From, req.To, c.Segments); bestI < 0 || s < bestS {
					bestI, bestS = i, s
				}
			}
			if bestI >= 0 {
				take(pi, bestI, RAC)
				continue
			}
		}
		out[pi] = Placement{Status: WL}
		done[pi] = true
	}
	return out, nil
}

// FreeCount counts confirmable units in a quota free for the whole journey.
func (c *Catalog) FreeCount(occ Occupancy, quota string, from, to int) int {
	need := SegMask(from, to)
	n := 0
	for i, u := range c.Units {
		if !u.RAC && u.Quota == quota && c.free(i, occ, need) {
			n++
		}
	}
	return n
}

// FreeRAC counts RAC half-berths free for the whole journey.
func (c *Catalog) FreeRAC(occ Occupancy, from, to int) int {
	need := SegMask(from, to)
	n := 0
	for i, u := range c.Units {
		if u.RAC && c.free(i, occ, need) {
			n++
		}
	}
	return n
}

// Utilisation is the fraction of the class's berth-segments in use, for metrics and tests.
func (c *Catalog) Utilisation(occ Occupancy) float64 {
	used := 0
	for _, u := range c.Units {
		used += bits.OnesCount64(occ[u.Key()])
	}
	return float64(used) / float64(len(c.Units)*c.Segments)
}
