package analytics

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/live"
)

// Punctuality is measured where passengers feel it: arrival at the destination. A run
// counts as on time if it arrives no more than OnTimeMin minutes late.

type PunctualityRow struct {
	Key      string  `json:"key"` // zone code, division id or date
	Name     string  `json:"name"`
	Runs     int     `json:"runs"`
	OnTime   float64 `json:"on_time"`   // share of runs
	AvgDelay float64 `json:"avg_delay"` // minutes, over all runs
	Late60   int     `json:"late_60"`   // runs over an hour late
}

type TrainPunctuality struct {
	Number   string  `json:"number"`
	Name     string  `json:"name"`
	Runs     int     `json:"runs"`
	OnTime   float64 `json:"on_time"`
	AvgDelay float64 `json:"avg_delay"`
	Worst    int     `json:"worst"`
}

type Punctuality struct {
	Scope     Scope              `json:"scope"`
	From      string             `json:"from"`
	To        string             `json:"to"`
	Summary   PunctualityRow     `json:"summary"`
	Cancelled int                `json:"cancelled"`
	Zones     []PunctualityRow   `json:"zones"`     // the league table, always all of India
	Divisions []PunctualityRow   `json:"divisions"` // within the scope's zone
	Daily     []PunctualityRow   `json:"daily"`
	Worst     []TrainPunctuality `json:"worst"`
	Best      []TrainPunctuality `json:"best"`
	Reasons   []Count            `json:"reasons"` // why late runs were late
}

type tally struct {
	runs, onTime, late60, delay int
}

func (t *tally) add(o live.Outcome) {
	t.runs++
	t.delay += o.ArrDelay
	if o.ArrDelay <= OnTimeMin {
		t.onTime++
	}
	if o.ArrDelay > 60 {
		t.late60++
	}
}

func (t tally) row(key, name string) PunctualityRow {
	r := PunctualityRow{Key: key, Name: name, Runs: t.runs, Late60: t.late60}
	if t.runs > 0 {
		r.OnTime = float64(t.onTime) / float64(t.runs)
		r.AvgDelay = float64(t.delay) / float64(t.runs)
	}
	return r
}

// day returns how every train's run from that date went, computed once and kept for a while
// (injected incidents can change a day's results, so it is refreshed every 30 seconds).
func (s *Service) day(d time.Time) map[string]live.Outcome {
	key := d.Format(time.DateOnly)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcomes == nil {
		s.outcomes, s.outcomeAt = map[string]map[string]live.Outcome{}, map[string]time.Time{}
	}
	if m, ok := s.outcomes[key]; ok && time.Since(s.outcomeAt[key]) < 30*time.Second {
		return m
	}
	m := map[string]live.Outcome{}
	for num, t := range s.Net.Trains {
		if o, ok := s.Live.Outcome(t, d); ok {
			m[num] = o
		}
	}
	s.outcomes[key], s.outcomeAt[key] = m, time.Now()
	return m
}

// Punctuality covers runs that left in the `days` days before today (railway time), which
// have all reached their destinations.
func (s *Service) Punctuality(ctx context.Context, sc Scope, days int) (*Punctuality, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	now := s.Now()
	today := live.Midnight(now)
	// Long-distance runs take up to three days; runs from the last two days may still be running.
	end := today.AddDate(0, 0, -2)
	start := end.AddDate(0, 0, -days)
	p := &Punctuality{Scope: sc, From: start.Format(time.DateOnly), To: end.AddDate(0, 0, -1).Format(time.DateOnly)}

	var all tally
	zones := map[string]*tally{}
	divs := map[int]*tally{}
	trains := map[string]*tally{}
	worst := map[string]int{}
	reasons := map[string]int{}
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		var daily tally
		for num, o := range s.day(d) {
			own := s.owner[num]
			if !o.Cancelled && o.Arrival.After(now) {
				continue // a very long run still on its way
			}
			if o.Cancelled {
				if s.in(sc, num) {
					p.Cancelled++
				}
				continue
			}
			if zones[own.zone] == nil {
				zones[own.zone] = &tally{}
			}
			zones[own.zone].add(o)
			if !s.in(sc, num) {
				continue
			}
			all.add(o)
			daily.add(o)
			if sc.Zone != "" {
				if divs[own.division] == nil {
					divs[own.division] = &tally{}
				}
				divs[own.division].add(o)
			}
			if trains[num] == nil {
				trains[num] = &tally{}
			}
			trains[num].add(o)
			worst[num] = max(worst[num], o.ArrDelay)
			if o.ArrDelay > OnTimeMin && o.Reason != "" {
				reasons[o.Reason]++
			}
		}
		p.Daily = append(p.Daily, daily.row(d.Format(time.DateOnly), d.Format("Mon 2 Jan")))
	}
	p.Summary = all.row("", "")

	for _, z := range s.zones {
		if t := zones[z.Code]; t != nil {
			p.Zones = append(p.Zones, t.row(z.Code, z.Name))
		}
		if z.Code == sc.Zone {
			for _, d := range z.Divisions {
				if t := divs[d.ID]; t != nil {
					p.Divisions = append(p.Divisions, t.row(strconv.Itoa(d.ID), d.Name))
				}
			}
		}
	}
	byOnTime := func(a, b PunctualityRow) int {
		return cmp.Or(cmp.Compare(b.OnTime, a.OnTime), cmp.Compare(a.AvgDelay, b.AvgDelay))
	}
	slices.SortFunc(p.Zones, byOnTime)
	slices.SortFunc(p.Divisions, byOnTime)

	var tp []TrainPunctuality
	for num, t := range trains {
		if t.runs < 2 {
			continue
		}
		r := t.row(num, "")
		tp = append(tp, TrainPunctuality{Number: num, Name: s.Net.Trains[num].Name, Runs: t.runs, OnTime: r.OnTime, AvgDelay: r.AvgDelay, Worst: worst[num]})
	}
	slices.SortFunc(tp, func(a, b TrainPunctuality) int {
		return cmp.Or(cmp.Compare(b.AvgDelay, a.AvgDelay), cmp.Compare(a.Number, b.Number))
	})
	p.Worst = slices.Clone(tp[:min(10, len(tp))])
	slices.SortFunc(tp, func(a, b TrainPunctuality) int {
		return cmp.Or(cmp.Compare(b.OnTime, a.OnTime), cmp.Compare(a.AvgDelay, b.AvgDelay), cmp.Compare(a.Number, b.Number))
	})
	p.Best = tp[:min(10, len(tp))]

	for r, n := range reasons {
		p.Reasons = append(p.Reasons, Count{Key: r, Count: n})
	}
	slices.SortFunc(p.Reasons, func(a, b Count) int { return cmp.Or(b.Count-a.Count, cmp.Compare(a.Key, b.Key)) })
	for _, l := range []*[]PunctualityRow{&p.Zones, &p.Divisions, &p.Daily} {
		if *l == nil {
			*l = []PunctualityRow{}
		}
	}
	if p.Worst == nil {
		p.Worst, p.Best = []TrainPunctuality{}, []TrainPunctuality{}
	}
	if p.Reasons == nil {
		p.Reasons = []Count{}
	}
	return p, nil
}
