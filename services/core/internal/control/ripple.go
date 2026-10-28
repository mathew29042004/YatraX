package control

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// The ripple model: holding one train delays others in two ways.
//   - Platform occupancy: a train due on the same platform after it must wait until it
//     clears (plus a two-minute margin).
//   - Line headway: a train following it between the same two stations can't leave
//     until Headway after it has (signals keep one train per block section).
//
// Each train delayed this way can delay others in turn, a few hops deep. It's a
// deliberately simple discrete-event model — no crossings on single line, no recovery
// time, no platform reassignment — so it errs on the side of showing the knock-on.
const (
	Headway      = 5 * time.Minute
	PlatformGap  = 2 * time.Minute
	OriginDwell  = 20 * time.Minute // starting trains stand on their platform this long before leaving
	TerminusGap  = 15 * time.Minute // terminating trains clear their platform this long after arriving
	MaxHops      = 3
	MaxAffected  = 40
	RippleWindow = 8 * time.Hour
)

type Hold struct {
	Train    string    `json:"train"`
	Origin   time.Time `json:"-"`
	Station  string    `json:"station"`
	DelayMin int       `json:"delay_min"`
}

type Effect struct {
	Train      string `json:"train"`
	Name       string `json:"name"`
	OriginDate string `json:"origin_date"`
	AddedMin   int    `json:"added_min"`
	Station    string `json:"station"` // where the knock-on starts
	Cause      string `json:"cause"`
	Via        string `json:"via"` // the train that delays it
	Hop        int    `json:"hop"`
	Passengers int    `json:"passengers"`
}

type Ripple struct {
	Train        string    `json:"train"`
	Name         string    `json:"name"`
	OriginDate   string    `json:"origin_date"`
	Station      string    `json:"station"`
	DelayMin     int       `json:"delay_min"`
	ArrivalNow   time.Time `json:"arrival_now"`   // at its destination, as things stand
	ArrivalAfter time.Time `json:"arrival_after"` // if held
	Passengers   int       `json:"passengers"`
	Affected     []Effect  `json:"affected"`
	TotalMin     int       `json:"total_added_min"`  // across the knock-on trains
	TotalPax     int       `json:"total_passengers"` // on the held train and the knock-on trains
}

// call is one stop of a run, with the times the model works on.
type call struct {
	station  string
	platform int
	arr, dep *time.Time
}

type runTL struct {
	train  *network.Train
	origin string
	calls  []call
}

func timeline(t *network.Train, s live.Status) runTL {
	r := runTL{train: t, origin: s.OriginDate}
	for _, v := range s.Stops {
		c := call{station: v.Station, platform: v.Platform}
		if v.ExpArr != nil {
			a := *v.ExpArr
			c.arr = &a
		}
		if v.ExpDep != nil {
			d := *v.ExpDep
			c.dep = &d
		}
		r.calls = append(r.calls, c)
	}
	return r
}

// occupancy is when a run holds its platform at a call.
func (c call) occupancy() (time.Time, time.Time) {
	switch {
	case c.arr != nil && c.dep != nil:
		return *c.arr, *c.dep
	case c.dep != nil:
		return c.dep.Add(-OriginDwell), *c.dep
	default:
		return *c.arr, c.arr.Add(TerminusGap)
	}
}

// shifted is a copy of a timeline with every time from call k on moved later by d.
func (r runTL) shifted(k int, d time.Duration) runTL {
	out := runTL{train: r.train, origin: r.origin, calls: make([]call, len(r.calls))}
	for i, c := range r.calls {
		nc := c
		if i >= k {
			if c.arr != nil {
				a := c.arr.Add(d)
				nc.arr = &a
			}
			if c.dep != nil {
				x := c.dep.Add(d)
				nc.dep = &x
			}
		}
		out.calls[i] = nc
	}
	return out
}

func key(train, origin string) string { return train + "|" + origin }

func (c *Service) name(code string) string {
	if s := c.Net.Stations[code]; s != nil {
		return s.Name
	}
	return code
}

// WhatIf simulates holding a train at a station without changing anything.
func (c *Service) WhatIf(ctx context.Context, h Hold) (*Ripple, error) {
	t := c.Net.Trains[h.Train]
	if t == nil {
		return nil, fmt.Errorf("no train %s", h.Train)
	}
	k := t.StopIndex(h.Station)
	if k < 0 || k == len(t.Stops)-1 {
		return nil, fmt.Errorf("%s doesn't depart from %s", h.Train, h.Station)
	}
	if h.DelayMin < 1 || h.DelayMin > 1440 {
		return nil, fmt.Errorf("hold for 1 to 1440 minutes")
	}
	now := c.Now()
	before := c.Live.Status(t, h.Origin, now)
	if d := before.Stops[k].ExpDep; d != nil && d.Before(now) {
		return nil, fmt.Errorf("%s has already left %s", h.Train, c.name(h.Station))
	}
	if before.Disruption == "cancelled" {
		return nil, fmt.Errorf("%s on %s is cancelled", h.Train, before.OriginDate)
	}
	after := c.Live.StatusWith(t, h.Origin, now, []live.Incident{{StopSeq: k, DelayMin: h.DelayMin, Reason: "what-if"}})
	last := len(t.Stops) - 1
	out := &Ripple{Train: t.Number, Name: t.Name, OriginDate: before.OriginDate, Station: h.Station, DelayMin: h.DelayMin,
		ArrivalNow: *before.Stops[last].ExpArr, ArrivalAfter: *after.Stops[last].ExpArr, Affected: []Effect{}}

	base := map[string]runTL{} // runs as they stand, loaded as needed
	// Look around the hold itself (a what-if can be for a run days ahead), never into the past.
	centre := *after.Stops[k].ExpDep
	if centre.Before(now) {
		centre = now
	}
	today := live.Midnight(centre)
	horizon := centre.Add(RippleWindow)
	// runsAt lists the runs calling at a station within the window.
	runsAt := func(code string) []runTL {
		var list []runTL
		for _, tr := range c.Net.Calls[code] {
			i := tr.StopIndex(code)
			for d := -3; d <= 1; d++ {
				origin := today.AddDate(0, 0, d)
				if !tr.RunsOn(origin) {
					continue
				}
				at := origin.Add(time.Duration(tr.Stops[i].Time()) * time.Minute)
				if at.Before(centre.Add(-6*time.Hour)) || at.After(horizon) {
					continue
				}
				kk := key(tr.Number, origin.Format(time.DateOnly))
				r, ok := base[kk]
				if !ok {
					r = timeline(tr, c.Live.Status(tr, origin, now))
					base[kk] = r
				}
				list = append(list, r)
			}
		}
		return list
	}

	type item struct {
		orig, now runTL
		from      int // first call that moved
		hop       int
	}
	held := key(t.Number, before.OriginDate)
	queue := []item{{orig: timeline(t, before), now: timeline(t, after), from: k, hop: 0}}
	delayed := map[string]runTL{held: queue[0].now}
	effects := map[string]*Effect{}

	for len(queue) > 0 && len(effects) < MaxAffected {
		x := queue[0]
		queue = queue[1:]
		if x.hop >= MaxHops {
			continue
		}
		for ci := x.from; ci < len(x.now.calls); ci++ {
			xc, xo := x.now.calls[ci], x.orig.calls[ci]
			if xc.dep != nil && xc.dep.After(horizon) || xc.dep == nil && xc.arr != nil && xc.arr.After(horizon) {
				break
			}
			_, xe := xc.occupancy()
			os, _ := xo.occupancy()
			for _, y := range runsAt(xc.station) {
				yk := key(y.train.Number, y.origin)
				if yk == key(x.now.train.Number, x.now.origin) || yk == held {
					continue
				}
				cur := y
				if d, ok := delayed[yk]; ok {
					cur = d
				}
				yi := y.train.StopIndex(xc.station)
				yc, yo := cur.calls[yi], y.calls[yi]
				ys, _ := yc.occupancy()
				yos, _ := yo.occupancy()
				var need time.Duration
				cause := ""
				// Same platform, and y was due after x: y waits for x to clear.
				if yc.platform == xc.platform && !yos.Before(os) && ys.Before(xe.Add(PlatformGap)) && ys.After(now) {
					need = xe.Add(PlatformGap).Sub(ys)
					cause = fmt.Sprintf("platform %d at %s is occupied by %s", xc.platform, c.name(xc.station), x.now.train.Number)
				}
				// Following x out of this station towards x's next stop, too close behind.
				if xc.dep != nil && yc.dep != nil && ci+1 < len(x.now.calls) && yo.dep != nil && xo.dep != nil {
					next := x.now.calls[ci+1].station
					if ni := y.train.StopIndex(next); ni > yi && !yo.dep.Before(*xo.dep) && yc.dep.Before(xc.dep.Add(Headway)) && yc.dep.After(now) {
						if n := xc.dep.Add(Headway).Sub(*yc.dep); n > need {
							need = n
							cause = fmt.Sprintf("following %s from %s towards %s", x.now.train.Number, c.name(xc.station), c.name(next))
						}
					}
				}
				if need < 2*time.Minute {
					continue
				}
				need = need.Round(time.Minute)
				shifted := cur.shifted(yi, need)
				delayed[yk] = shifted
				e := effects[yk]
				if e == nil {
					e = &Effect{Train: y.train.Number, Name: y.train.Name, OriginDate: y.origin, Station: xc.station, Cause: cause,
						Via: x.now.train.Number, Hop: x.hop + 1}
					effects[yk] = e
				}
				e.AddedMin += int(need.Minutes())
				queue = append(queue, item{orig: y, now: shifted, from: yi, hop: x.hop + 1})
				if len(effects) >= MaxAffected {
					break
				}
			}
		}
	}

	for _, e := range effects {
		out.Affected = append(out.Affected, *e)
		out.TotalMin += e.AddedMin
	}
	sort.Slice(out.Affected, func(a, b int) bool {
		if out.Affected[a].Hop != out.Affected[b].Hop {
			return out.Affected[a].Hop < out.Affected[b].Hop
		}
		return out.Affected[a].AddedMin > out.Affected[b].AddedMin
	})
	if err := c.countPassengers(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// countPassengers fills in how many booked passengers each train carries.
func (c *Service) countPassengers(ctx context.Context, r *Ripple) error {
	if c.DB == nil {
		return nil
	}
	trains := []string{r.Train}
	dates := []string{r.OriginDate}
	for _, e := range r.Affected {
		trains, dates = append(trains, e.Train), append(dates, e.OriginDate)
	}
	rows, err := c.DB.Query(ctx, `
		SELECT tr.train_number || '|' || tr.origin_date::text, count(*)
		  FROM train_runs tr JOIN bookings b ON b.run_id = tr.id JOIN booking_passengers p ON p.booking_id = b.id
		 WHERE b.status = 'CONFIRMED' AND p.status IN ('CNF', 'RAC', 'WL')
		   AND (tr.train_number, tr.origin_date) IN (SELECT * FROM unnest($1::text[], $2::date[]))
		 GROUP BY 1`, trains, dates)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := map[string]int{}
	for rows.Next() {
		var k string
		var c int
		if err := rows.Scan(&k, &c); err != nil {
			return err
		}
		n[k] = c
	}
	r.Passengers = n[key(r.Train, r.OriginDate)]
	r.TotalPax = r.Passengers
	for i := range r.Affected {
		r.Affected[i].Passengers = n[key(r.Affected[i].Train, r.Affected[i].OriginDate)]
		r.TotalPax += r.Affected[i].Passengers
	}
	return rows.Err()
}
