package control

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// The digital twin: the live model is deterministic for a given run and its recorded
// incidents, so any past day can be replayed exactly by winding the railway clock back to it.
// Replay lists what happened that day, so the control room can jump from event to event.

type ReplayEvent struct {
	At         time.Time `json:"at"`   // railway time
	Kind       string    `json:"kind"` // incident, breakdown, disruption, platform, late
	Train      string    `json:"train"`
	Name       string    `json:"name"`
	OriginDate string    `json:"origin_date"`
	Text       string    `json:"text"`
}

type ReplayDay struct {
	Date      string        `json:"date"`
	Runs      int           `json:"runs"`    // runs that left that day and have arrived
	OnTime    float64       `json:"on_time"` // within 15 min at the destination
	AvgDelay  float64       `json:"avg_delay"`
	Late60    int           `json:"late_60"`
	Cancelled int           `json:"cancelled"`
	Events    []ReplayEvent `json:"events"`
}

// Replay is the day's story: control actions, disruptions and the worst late arrivals.
func (c *Service) Replay(ctx context.Context, day time.Time) (*ReplayDay, error) {
	day = live.Midnight(day)
	end := day.AddDate(0, 0, 1)
	out := &ReplayDay{Date: day.Format(time.DateOnly), Events: []ReplayEvent{}}
	add := func(at time.Time, kind string, t *network.Train, origin time.Time, text string) {
		if at.Before(day) || !at.Before(end) {
			return
		}
		out.Events = append(out.Events, ReplayEvent{At: at, Kind: kind, Train: t.Number, Name: t.Name, OriginDate: origin.Format(time.DateOnly), Text: text})
	}
	stopAt := func(origin time.Time, s network.Stop) time.Time {
		return origin.Add(time.Duration(s.Time()) * time.Minute)
	}
	station := func(code string) string {
		if st := c.Net.Stations[code]; st != nil {
			return st.Name
		}
		return code
	}

	// Runs from up to three days earlier can still be on the move that day.
	rows, err := c.DB.Query(ctx, `SELECT train_number, origin_date, stop_seq, delay_min, reason, en_route FROM live_incidents
		WHERE origin_date BETWEEN $1 AND $2 ORDER BY id`, day.AddDate(0, 0, -3), day)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var num, reason string
		var d time.Time
		var seq, delay int
		var enRoute bool
		if err := rows.Scan(&num, &d, &seq, &delay, &reason, &enRoute); err != nil {
			return nil, err
		}
		t := c.Net.Trains[num]
		if t == nil || seq >= len(t.Stops) {
			continue
		}
		origin := live.Midnight(time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, network.IST))
		s := t.Stops[seq]
		if enRoute {
			at := origin.Add(time.Duration(*s.Arr-20) * time.Minute)
			add(at, "breakdown", t, origin, fmt.Sprintf("Lost %d min before %s: %s", delay, station(s.Station), reason))
		} else {
			add(stopAt(origin, s), "incident", t, origin, fmt.Sprintf("Held %d min at %s: %s", delay, station(s.Station), reason))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = c.DB.Query(ctx, `SELECT r.train_number, r.origin_date, d.kind, coalesce(d.at_station, ''), d.reason FROM run_disruptions d
		JOIN train_runs r ON r.id = d.run_id WHERE r.origin_date BETWEEN $1 AND $2`, day.AddDate(0, 0, -3), day)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var num, kind, at, reason string
		var d time.Time
		if err := rows.Scan(&num, &d, &kind, &at, &reason); err != nil {
			return nil, err
		}
		t := c.Net.Trains[num]
		if t == nil {
			continue
		}
		origin := live.Midnight(time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, network.IST))
		text := map[string]string{"cancelled": "Cancelled", "short_terminated": "Terminated short at " + station(at), "diverted": "Diverted"}[kind]
		add(stopAt(origin, t.Stops[0]), "disruption", t, origin, text+": "+reason)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = c.DB.Query(ctx, `SELECT train_number, origin_date, station_code, platform FROM platform_assignments WHERE origin_date BETWEEN $1 AND $2`,
		day.AddDate(0, 0, -3), day)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var num, code string
		var d time.Time
		var pf int
		if err := rows.Scan(&num, &d, &code, &pf); err != nil {
			return nil, err
		}
		t := c.Net.Trains[num]
		if t == nil || t.StopIndex(code) < 0 {
			continue
		}
		origin := live.Midnight(time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, network.IST))
		add(stopAt(origin, t.Stops[t.StopIndex(code)]), "platform", t, origin, fmt.Sprintf("Moved to platform %d at %s", pf, station(code)))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// How the day's departures went, and its worst arrivals.
	now := c.Now()
	type late struct {
		t *network.Train
		o live.Outcome
	}
	var worst []late
	delay, onTime := 0, 0
	for _, t := range c.Net.Trains {
		o, ok := c.Live.Outcome(t, day)
		if !ok {
			continue
		}
		if o.Cancelled {
			out.Cancelled++
			continue
		}
		if o.Arrival.After(now) {
			continue
		}
		out.Runs++
		delay += o.ArrDelay
		if o.ArrDelay <= 15 {
			onTime++
		}
		if o.ArrDelay > 60 {
			out.Late60++
			worst = append(worst, late{t, o})
		}
	}
	if out.Runs > 0 {
		out.OnTime, out.AvgDelay = float64(onTime)/float64(out.Runs), float64(delay)/float64(out.Runs)
	}
	slices.SortFunc(worst, func(a, b late) int { return cmp.Or(b.o.ArrDelay-a.o.ArrDelay, cmp.Compare(a.t.Number, b.t.Number)) })
	for _, w := range worst[:min(8, len(worst))] {
		// Shown when it arrived, which may be the next day: keep it on this day's list anyway.
		out.Events = append(out.Events, ReplayEvent{At: w.o.Arrival, Kind: "late", Train: w.t.Number, Name: w.t.Name, OriginDate: day.Format(time.DateOnly),
			Text: fmt.Sprintf("Reached %s %d min late (%s)", station(w.t.Destination), w.o.ArrDelay, w.o.Reason)})
	}
	slices.SortFunc(out.Events, func(a, b ReplayEvent) int { return a.At.Compare(b.At) })
	return out, nil
}
