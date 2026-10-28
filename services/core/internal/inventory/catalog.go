// Package inventory decides which berth each passenger gets.
//
// A train run's inventory is static (the coaches and their numbered berths) plus a
// set of allocations, each holding one berth for a range of stops. This package is
// pure: it builds the berth catalog, assigns quotas to berths, and allocates
// passengers over in-memory occupancy. The booking package loads and stores the
// occupancy; Postgres's exclusion constraint is the final guard against overlaps.
package inventory

import (
	"fmt"
	"math"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/layout"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// MaxSegments is how many stop-to-stop segments a bitmask can track.
const MaxSegments = 64

// Unit is one allocatable place: a berth or seat, or one of the two halves of an
// RAC side-lower berth.
type Unit struct {
	Coach    string `json:"coach"`
	CoachPos int    `json:"coach_pos"`
	Berth    int    `json:"berth"`
	Slot     int    `json:"slot"` // 0, or 1 for the second RAC passenger
	Type     string `json:"type"` // LB MB UB SL SU W M A
	Bay      int    `json:"bay"`
	Quota    string `json:"quota"`
	RAC      bool   `json:"rac"`
}

// Key identifies a unit within a run.
type Key struct {
	Coach string
	Berth int
	Slot  int
}

func (u Unit) Key() Key { return Key{u.Coach, u.Berth, u.Slot} }

// Catalog lists every unit of one class on one train.
type Catalog struct {
	Train    string
	Class    string
	Segments int // len(stops) - 1
	Units    []Unit
	index    map[Key]int
}

// Index returns the position of a unit in Units, or -1.
func (c *Catalog) Index(k Key) int {
	if i, ok := c.index[k]; ok {
		return i
	}
	return -1
}

// Capacity counts confirmable units (excluding RAC halves) in a quota, or all quotas if quota is "".
func (c *Catalog) Capacity(quota string) int {
	n := 0
	for _, u := range c.Units {
		if !u.RAC && (quota == "" || u.Quota == quota) {
			n++
		}
	}
	return n
}

// seniorLowerPerCoach is how many lower berths each coach keeps for the senior quota.
var seniorLowerPerCoach = map[string]int{"SL": 4, "3A": 3, "2A": 2}

// racBaysPerCoach: the side-lower berths in the last bays of each sleeper coach are
// sold as RAC (two passengers each) once confirmed berths run out.
const racBaysPerCoach = 2

// NewCatalog builds the unit list for a train's class. Quotas are assigned
// deterministically per coach so every service agrees on them:
//   - RAC: side-lower berths in the last two bays (sleeper layouts only), General quota
//   - Tatkal: the last ~22% of bays; Premium Tatkal: the ~6% of bays before those
//   - Ladies: the first six berths of the middle coach
//   - Senior / lower berth: a few lower berths per coach
//   - Divyangjan, Defence, Foreign tourist: two berths each in the last coach (bays 1, 2, 3)
//   - everything else: General
func NewCatalog(n *network.Network, t *network.Train, class string) (*Catalog, error) {
	cc := n.Classes[class]
	if cc == nil || !cc.Bookable {
		return nil, fmt.Errorf("class %s is not reserved", class)
	}
	segments := len(t.Stops) - 1
	if segments > MaxSegments {
		return nil, fmt.Errorf("train %s has %d segments; at most %d supported", t.Number, segments, MaxSegments)
	}
	var coaches []network.Coach
	for _, c := range t.Coaches {
		if c.Class == class {
			coaches = append(coaches, c)
		}
	}
	if len(coaches) == 0 {
		return nil, fmt.Errorf("train %s has no %s coaches", t.Number, class)
	}
	seats, err := layout.Seats(cc.Layout, cc.Seats)
	if err != nil {
		return nil, err
	}
	bays := seats[len(seats)-1].Bay
	sleeper := cc.Layout == "sleeper_8" || cc.Layout == "sleeper_6"
	racBays := 0
	if sleeper {
		racBays = racBaysPerCoach
	}
	tatkal := availability.QuotaAllowed(n, class, "TQ")
	tqBays, ptBays := 0, 0
	if tatkal {
		tqBays = int(math.Round(float64(bays) * 0.22))
		ptBays = max(1, int(math.Round(float64(bays)*0.06)))
	}
	middle, last := len(coaches)/2, len(coaches)-1

	cat := &Catalog{Train: t.Number, Class: class, Segments: segments, index: map[Key]int{}}
	add := func(u Unit) {
		cat.index[u.Key()] = len(cat.Units)
		cat.Units = append(cat.Units, u)
	}
	for ci, c := range coaches {
		seniorLeft := seniorLowerPerCoach[class]
		for si, s := range seats {
			u := Unit{Coach: c.No, CoachPos: c.Position, Berth: s.Number, Type: s.Type, Bay: s.Bay, Quota: "GN"}
			switch {
			case sleeper && s.Type == "SL" && s.Bay > bays-racBays:
				u.RAC = true
				add(u)
				u.Slot = 1
				add(u)
				continue
			case tatkal && s.Bay > bays-racBays-tqBays && s.Bay <= bays-racBays:
				u.Quota = "TQ"
			case tatkal && s.Bay > bays-racBays-tqBays-ptBays && s.Bay <= bays-racBays-tqBays:
				u.Quota = "PT"
			case availability.QuotaAllowed(n, class, "LD") && ci == middle && si < 6:
				u.Quota = "LD"
			case quotaPair(n, class, "HP", ci == last, cc.Layout, si, 1):
				u.Quota = "HP" // divyangjan: the last coach's first lower and middle berths, by the door
			case quotaPair(n, class, "DF", ci == last, cc.Layout, si, 2):
				u.Quota = "DF"
			case quotaPair(n, class, "FT", ci == last, cc.Layout, si, 3):
				u.Quota = "FT"
			case seniorLeft > 0 && s.Type == "LB" && s.Bay >= 3:
				u.Quota = "SS"
				seniorLeft--
			}
			add(u)
		}
	}
	return cat, nil
}

// quotaPair reports whether seat index si is one of the two berths a small quota keeps in
// the last coach: in sleepers, the first two berths of bay `bay` (lower and middle, or lower
// and upper in 2A and 1A); in chair cars, two seats per quota from the front.
func quotaPair(n *network.Network, class, quota string, lastCoach bool, layoutName string, si, bay int) bool {
	if !lastCoach || !availability.QuotaAllowed(n, class, quota) {
		return false
	}
	per := map[string]int{"sleeper_8": 8, "sleeper_6": 6, "sleeper_4": 4}[layoutName]
	if per == 0 {
		return si >= (bay-1)*2 && si < bay*2 // chair cars: seats 1–2, 3–4, 5–6
	}
	start := (bay - 1) * per
	return si == start || si == start+1
}
