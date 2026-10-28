package live

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/metrics"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// PlatformNotice is how long before arrival a platform change is announced.
const PlatformNotice = 20 * time.Minute

type Engine struct {
	Net *network.Network
	DB  *pgxpool.Pool
	Now func() time.Time // simulated time

	mu        sync.RWMutex
	incidents map[string][]Incident     // run key -> incidents
	assigned  map[string]map[string]int // run key -> station -> platform set by the station master
	disrupted map[string]disruption     // run key -> cancelled / short-terminated / diverted
}

func NewEngine(n *network.Network, db *pgxpool.Pool, now func() time.Time) *Engine {
	return &Engine{Net: n, DB: db, Now: now, incidents: map[string][]Incident{}, assigned: map[string]map[string]int{}, disrupted: map[string]disruption{}}
}

func runKey(train string, origin time.Time) string { return train + "|" + origin.Format(time.DateOnly) }

// Midnight is the start of a time's day in IST.
func Midnight(t time.Time) time.Time {
	t = t.In(network.IST)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, network.IST)
}

// LoadIncidents refreshes the injected delays, station masters' platform assignments and
// disruptions (cheap: there are few), for runs from the last 35 days on — the dashboards
// replay up to a month of past runs.
func (e *Engine) LoadIncidents(ctx context.Context) error {
	if err := e.loadSpecialRuns(ctx); err != nil {
		return err
	}
	rows, err := e.DB.Query(ctx, `SELECT train_number, origin_date, stop_seq, delay_min, reason, en_route FROM live_incidents
		WHERE origin_date >= current_date - 35 ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	m := map[string][]Incident{}
	for rows.Next() {
		var train string
		var d time.Time
		var in Incident
		if err := rows.Scan(&train, &d, &in.StopSeq, &in.DelayMin, &in.Reason, &in.EnRoute); err != nil {
			return err
		}
		k := runKey(train, time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, network.IST))
		m[k] = append(m[k], in)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = e.DB.Query(ctx, `SELECT train_number, origin_date, station_code, platform FROM platform_assignments
		WHERE origin_date >= current_date - 35`)
	if err != nil {
		return err
	}
	defer rows.Close()
	a := map[string]map[string]int{}
	for rows.Next() {
		var train, station string
		var d time.Time
		var pf int
		if err := rows.Scan(&train, &d, &station, &pf); err != nil {
			return err
		}
		k := runKey(train, time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, network.IST))
		if a[k] == nil {
			a[k] = map[string]int{}
		}
		a[k][station] = pf
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = e.DB.Query(ctx, `SELECT r.train_number, r.origin_date, d.kind, coalesce(d.at_station, ''), d.skip, d.extra_delay, d.reason
		FROM run_disruptions d JOIN train_runs r ON r.id = d.run_id WHERE r.origin_date >= current_date - 35`)
	if err != nil {
		return err
	}
	defer rows.Close()
	dm := map[string]disruption{}
	for rows.Next() {
		var train string
		var d time.Time
		var x disruption
		if err := rows.Scan(&train, &d, &x.kind, &x.at, &x.skip, &x.extraDelay, &x.reason); err != nil {
			return err
		}
		dm[runKey(train, time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, network.IST))] = x
	}
	e.mu.Lock()
	e.incidents, e.assigned, e.disrupted = m, a, dm
	e.mu.Unlock()
	return rows.Err()
}

// disruption is control cancelling, cutting short or diverting a run.
type disruption struct {
	kind, at, reason string
	skip             []string
	extraDelay       int
}

// RunIncidents keeps injected delays fresh on every instance.
func (e *Engine) RunIncidents(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := e.LoadIncidents(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("live: incidents", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) plan(t *network.Train, origin time.Time, extra []Incident) []stopPlan {
	e.mu.RLock()
	in := append(append([]Incident(nil), e.incidents[runKey(t.Number, origin)]...), extra...)
	e.mu.RUnlock()
	return Plan(e.Net, t, origin, in)
}

// ── Views ──────────────────────────────────────────────────────────────────

type StopView struct {
	Seq             int        `json:"seq"`
	Station         string     `json:"station"`
	Name            string     `json:"name"`
	DistKm          int        `json:"distance_km"`
	SchedArr        *time.Time `json:"sched_arr,omitempty"`
	SchedDep        *time.Time `json:"sched_dep,omitempty"`
	ExpArr          *time.Time `json:"exp_arr,omitempty"`
	ExpDep          *time.Time `json:"exp_dep,omitempty"`
	ArrDelay        int        `json:"arr_delay"`
	DepDelay        int        `json:"dep_delay"`
	Platform        int        `json:"platform"`
	PlatformChanged bool       `json:"platform_changed"`
	State           string     `json:"state"` // departed, here, upcoming
}

type Status struct {
	Train      string     `json:"train"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	OriginDate string     `json:"origin_date"`
	State      string     `json:"state"` // not_running, scheduled, running, at_station, arrived
	DelayMin   int        `json:"delay_min"`
	Reason     string     `json:"reason,omitempty"`
	LastIdx    int        `json:"last_idx"` // last stop reached (-1 before departure)
	NextIdx    int        `json:"next_idx"` // next stop ahead (-1 after arrival)
	Lat        float64    `json:"lat"`
	Lon        float64    `json:"lon"`
	DistKm     float64    `json:"distance_km"`
	Progress   float64    `json:"progress"` // share of the journey covered, 0–1
	Message    string     `json:"message"`
	Disruption string     `json:"disruption,omitempty"` // cancelled, short_terminated, diverted
	SimNow     time.Time  `json:"sim_now"`
	Stops      []StopView `json:"stops,omitempty"`
}

func at(origin time.Time, min int) time.Time { return origin.Add(time.Duration(min) * time.Minute) }

func clock(t time.Time) string { return t.In(network.IST).Format("15:04") }

// Status is a run's live position at simulated time now.
func (e *Engine) Status(t *network.Train, origin time.Time, now time.Time) Status {
	return e.StatusWith(t, origin, now, nil)
}

// StatusWith is Status with extra, hypothetical delays on top of the real ones: what the
// run would look like if control held it (the what-if simulator uses it; nothing is saved).
func (e *Engine) StatusWith(t *network.Train, origin time.Time, now time.Time, extra []Incident) Status {
	origin = Midnight(origin)
	st := Status{Train: t.Number, Name: t.Name, Type: t.Type, OriginDate: origin.Format(time.DateOnly), SimNow: now, LastIdx: -1, NextIdx: 0}
	if !t.RunsOn(origin) {
		st.State, st.Message = "not_running", fmt.Sprintf("%s does not run from %s on %s", t.Number, t.Origin, origin.Format("Mon 2 Jan"))
		return st
	}
	e.mu.RLock()
	assigned := e.assigned[runKey(t.Number, origin)]
	dis, disrupted := e.disrupted[runKey(t.Number, origin)]
	e.mu.RUnlock()
	skip := map[int]bool{}
	if disrupted && dis.kind == "diverted" {
		first := len(t.Stops)
		for _, code := range dis.skip {
			if i := t.StopIndex(code); i > 0 {
				skip[i] = true
				first = min(first, i)
			}
		}
		if first < len(t.Stops) && dis.extraDelay > 0 {
			extra = append(append([]Incident(nil), extra...), Incident{StopSeq: first - 1, DelayMin: dis.extraDelay, Reason: "Diverted: " + dis.reason})
		}
	}
	plan := e.plan(t, origin, extra)
	last := len(t.Stops) - 1
	views := make([]StopView, len(t.Stops))
	for i, s := range t.Stops {
		v := StopView{Seq: i, Station: s.Station, DistKm: s.DistKm, ArrDelay: plan[i].ArrDelay, DepDelay: plan[i].DepDelay,
			Platform: s.Platform, State: "upcoming"}
		if stn := e.Net.Stations[s.Station]; stn != nil {
			v.Name = stn.Name
		}
		if s.Arr != nil {
			sa, ea := at(origin, *s.Arr), at(origin, *s.Arr+plan[i].ArrDelay)
			v.SchedArr, v.ExpArr = &sa, &ea
		}
		if s.Dep != nil {
			sd, ed := at(origin, *s.Dep), at(origin, *s.Dep+plan[i].DepDelay)
			v.SchedDep, v.ExpDep = &sd, &ed
		}
		// A platform change is only known once it's announced.
		if plan[i].PlatformChanged && v.ExpArr != nil && !now.Before(v.ExpArr.Add(-PlatformNotice)) {
			v.Platform, v.PlatformChanged = plan[i].Platform, true
		}
		// The station master's word is final, and it's known as soon as it's given.
		if pf, ok := assigned[s.Station]; ok {
			v.Platform, v.PlatformChanged = pf, pf != s.Platform
		}
		views[i] = v
	}
	st.Stops = views

	// Where is it?
	switch {
	case now.Before(*views[0].ExpDep):
		st.State, st.DelayMin, st.Reason = "scheduled", plan[0].DepDelay, plan[0].Reason
		st.Message = fmt.Sprintf("Departs %s at %s", views[0].Name, clock(*views[0].ExpDep))
		if plan[0].DepDelay > 0 {
			st.Message += fmt.Sprintf(" (%d min late)", plan[0].DepDelay)
		}
		st.place(e.Net, t, 0, 0, 0)
	case !now.Before(*views[last].ExpArr):
		for i := range views {
			views[i].State = "departed"
		}
		views[last].State = "here"
		st.State, st.LastIdx, st.NextIdx, st.DelayMin, st.Progress = "arrived", last, -1, plan[last].ArrDelay, 1
		st.Message = fmt.Sprintf("Reached %s at %s", views[last].Name, clock(*views[last].ExpArr))
		st.place(e.Net, t, last, last, 0)
	default:
		for i := 0; i < last; i++ {
			views[i].State = "departed"
			next := views[i+1]
			if now.Before(*views[i].ExpDep) { // halted at stop i
				views[i].State = "here"
				st.State, st.LastIdx, st.NextIdx = "at_station", i, i+1
				st.DelayMin, st.Reason = plan[i].ArrDelay, plan[i].Reason
				if late := int(now.Sub(*views[i].SchedDep).Minutes()); late > st.DelayMin {
					st.DelayMin = late // held past its departure time: the delay grows while it waits
				}
				st.Message = fmt.Sprintf("At %s, platform %d · leaves %s", views[i].Name, views[i].Platform, clock(*views[i].ExpDep))
				st.place(e.Net, t, i, i, 0)
				break
			}
			if now.Before(*next.ExpArr) { // between i and i+1
				span := next.ExpArr.Sub(*views[i].ExpDep)
				f := 0.0
				if span > 0 {
					f = float64(now.Sub(*views[i].ExpDep)) / float64(span)
				}
				st.State, st.LastIdx, st.NextIdx = "running", i, i+1
				st.DelayMin, st.Reason = plan[i+1].ArrDelay, plan[i+1].Reason
				st.Message = fmt.Sprintf("Left %s · reaches %s at %s", views[i].Name, next.Name, clock(*next.ExpArr))
				st.place(e.Net, t, i, i+1, f)
				break
			}
		}
	}
	if st.DelayMin > 0 && st.State != "scheduled" {
		st.Message += fmt.Sprintf(" · %d min late", st.DelayMin)
	} else if st.State == "running" || st.State == "at_station" {
		st.Message += " · on time"
	}
	if disrupted {
		st.applyDisruption(t, dis, skip)
	}
	return st
}

// applyDisruption marks what control took away: a cancelled run, the stops after a
// short-termination, or the stations a diversion misses.
func (st *Status) applyDisruption(t *network.Train, d disruption, skip map[int]bool) {
	st.Disruption = d.kind
	st.Reason = d.reason
	switch d.kind {
	case "cancelled":
		for i := range st.Stops {
			st.Stops[i].State = "cancelled"
		}
		st.State, st.Message, st.LastIdx, st.NextIdx, st.DelayMin = "cancelled", "Cancelled: "+d.reason, -1, -1, 0
	case "short_terminated":
		at := t.StopIndex(d.at)
		if at < 0 {
			return
		}
		for i := at + 1; i < len(st.Stops); i++ {
			st.Stops[i].State = "cancelled"
		}
		st.Stops[at].SchedDep, st.Stops[at].ExpDep = nil, nil
		if arr := st.Stops[at].ExpArr; arr != nil && !st.SimNow.Before(*arr) {
			st.State, st.LastIdx, st.NextIdx = "arrived", at, -1
			st.Stops[at].State = "here"
			st.Message = fmt.Sprintf("Terminated at %s (%s)", st.Stops[at].Name, d.reason)
		} else {
			st.Message += fmt.Sprintf(" · terminating at %s", st.Stops[at].Name)
		}
	case "diverted":
		for i := range skip {
			st.Stops[i].State = "skipped"
		}
		st.Message += " · diverted: not calling at " + strings.Join(d.skip, ", ")
	}
}

// place sets position and progress between stops a and b (fraction f of the way).
func (st *Status) place(n *network.Network, t *network.Train, a, b int, f float64) {
	sa, sb := n.Stations[t.Stops[a].Station], n.Stations[t.Stops[b].Station]
	if sa == nil || sb == nil {
		return
	}
	st.Lat = sa.Lat + (sb.Lat-sa.Lat)*f
	st.Lon = sa.Lon + (sb.Lon-sa.Lon)*f
	st.DistKm = float64(t.Stops[a].DistKm) + float64(t.Stops[b].DistKm-t.Stops[a].DistKm)*f
	if t.DistKm > 0 {
		st.Progress = st.DistKm / float64(t.DistKm)
	}
}

// CurrentRun picks the run a train number most likely refers to at time now: the one on
// the move, else the next to depart, else the most recent.
func (e *Engine) CurrentRun(t *network.Train, now time.Time) time.Time {
	today := Midnight(now)
	var next, recent time.Time
	for d := -4; d <= 7; d++ {
		origin := today.AddDate(0, 0, d)
		if !t.RunsOn(origin) {
			continue
		}
		start := at(origin, t.Stops[0].Time())
		end := at(origin, *t.Stops[len(t.Stops)-1].Arr)
		switch {
		case !now.Before(start.Add(-time.Hour)) && now.Before(end.Add(6*time.Hour)):
			if s := e.Status(t, origin, now); s.State != "arrived" || now.Before(at(origin, *t.Stops[len(t.Stops)-1].Arr+s.DelayMin).Add(30*time.Minute)) {
				return origin
			}
			recent = origin
		case start.After(now):
			if next.IsZero() {
				next = origin
			}
		default:
			recent = origin
		}
	}
	if !next.IsZero() {
		return next
	}
	return recent
}

// Brief is one dot on the live map.
type Brief struct {
	Train      string  `json:"train"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	OriginDate string  `json:"origin_date"`
	State      string  `json:"state"`
	DelayMin   int     `json:"delay_min"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	Next       string  `json:"next"`
	Zone       string  `json:"zone"`
	Message    string  `json:"message"`
}

// Running lists every train on the move (or halted en route) at time now.
func (e *Engine) Running(now time.Time) []Brief {
	today := Midnight(now)
	var out []Brief
	for _, t := range e.Net.Trains {
		for d := 0; d <= 4; d++ {
			origin := today.AddDate(0, 0, -d)
			if !t.RunsOn(origin) {
				continue
			}
			start := at(origin, t.Stops[0].Time())
			end := at(origin, *t.Stops[len(t.Stops)-1].Arr)
			if now.Before(start) || now.After(end.Add(24*time.Hour)) {
				continue
			}
			s := e.Status(t, origin, now)
			if s.State != "running" && s.State != "at_station" {
				continue
			}
			b := Brief{Train: t.Number, Name: t.Name, Type: t.Type, OriginDate: s.OriginDate, State: s.State, DelayMin: s.DelayMin,
				Lat: s.Lat, Lon: s.Lon, Message: s.Message}
			if s.NextIdx >= 0 {
				b.Next = t.Stops[s.NextIdx].Station
				if stn := e.Net.Stations[b.Next]; stn != nil {
					b.Zone = stn.Zone
				}
			}
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Train < out[j].Train })
	return out
}

// BoardRow is one train on a station's arrivals and departures board.
type BoardRow struct {
	Train           string     `json:"train"`
	Name            string     `json:"name"`
	OriginDate      string     `json:"origin_date"`
	From            string     `json:"from"`
	FromName        string     `json:"from_name"`
	To              string     `json:"to"`
	ToName          string     `json:"to_name"`
	SchedArr        *time.Time `json:"sched_arr,omitempty"`
	SchedDep        *time.Time `json:"sched_dep,omitempty"`
	ExpArr          *time.Time `json:"exp_arr,omitempty"`
	ExpDep          *time.Time `json:"exp_dep,omitempty"`
	DelayMin        int        `json:"delay_min"`
	Platform        int        `json:"platform"`
	PlatformChanged bool       `json:"platform_changed"`
	State           string     `json:"state"` // expected, arriving, on_platform, departed, arrived (terminating here)
}

// Board lists trains calling at a station from an hour ago to `ahead` from now.
func (e *Engine) Board(code string, now time.Time, ahead time.Duration) []BoardRow {
	today := Midnight(now)
	var out []BoardRow
	for _, t := range e.Net.Calls[code] {
		i := t.StopIndex(code)
		if i < 0 {
			continue
		}
		for d := -4; d <= 1; d++ {
			origin := today.AddDate(0, 0, d)
			if !t.RunsOn(origin) {
				continue
			}
			sched := at(origin, t.Stops[i].Time())
			if sched.Before(now.Add(-6*time.Hour)) || sched.After(now.Add(ahead)) {
				continue
			}
			s := e.Status(t, origin, now)
			v := s.Stops[i]
			when := v.ExpDep
			if when == nil {
				when = v.ExpArr
			}
			if when.Before(now.Add(-time.Hour)) || when.After(now.Add(ahead)) {
				continue
			}
			row := BoardRow{Train: t.Number, Name: t.Name, OriginDate: s.OriginDate, From: t.Origin, To: t.Destination,
				SchedArr: v.SchedArr, SchedDep: v.SchedDep, ExpArr: v.ExpArr, ExpDep: v.ExpDep, Platform: v.Platform,
				PlatformChanged: v.PlatformChanged, DelayMin: max(v.ArrDelay, v.DepDelay), State: "expected"}
			if stn := e.Net.Stations[t.Origin]; stn != nil {
				row.FromName = stn.Name
			}
			if stn := e.Net.Stations[t.Destination]; stn != nil {
				row.ToName = stn.Name
			}
			switch {
			case v.State == "cancelled" || v.State == "skipped":
				row.State = "cancelled"
			case v.State == "departed":
				row.State = "departed"
			case v.State == "here" && i == len(t.Stops)-1:
				row.State = "arrived"
			case v.State == "here":
				row.State = "on_platform"
			case v.ExpArr != nil && now.After(v.ExpArr.Add(-10*time.Minute)):
				row.State = "arriving"
			}
			out = append(out, row)
		}
	}
	key := func(r BoardRow) time.Time {
		if r.ExpDep != nil {
			return *r.ExpDep
		}
		return *r.ExpArr
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i]).Before(key(out[j])) })
	return out
}

// RunGauges publishes how many trains are running and late, for Grafana.
func (e *Engine) RunGauges(ctx context.Context, speed func() float64) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		running := e.Running(e.Now())
		late := 0
		for _, b := range running {
			if b.DelayMin >= 15 {
				late++
			}
		}
		metrics.TrainsRunning.Set(float64(len(running)))
		metrics.TrainsLate.Set(float64(late))
		metrics.SimSpeed.Set(speed())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// loadSpecialRuns lets special trains run on the dates control has added.
func (e *Engine) loadSpecialRuns(ctx context.Context) error {
	rows, err := e.DB.Query(ctx, `SELECT train_number, origin_date FROM special_runs WHERE origin_date >= current_date - 7`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var num string
		var d time.Time
		if err := rows.Scan(&num, &d); err != nil {
			return err
		}
		if t := e.Net.Trains[num]; t != nil {
			t.AddSpecialRun(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, network.IST))
		}
	}
	return rows.Err()
}
