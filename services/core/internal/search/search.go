// Package search finds trains between two stations on a date, with fares and
// availability for every class.
package search

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/fare"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

const MaxFlexDays = 3

var (
	ErrUnknownStation = errors.New("unknown station")
	ErrSameStation    = errors.New("from and to must differ")
)

type Query struct {
	From, To string
	Date     time.Time // travel date at the boarding station (midnight IST)
	Quota    string
	Class    string // optional: only trains with this class
	Nearby   bool   // include stations within network.NearbyKm of From and To
	FlexDays int    // also report availability this many days either side
}

type StationRef struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type DateStatus struct {
	Date         string              `json:"date"`
	Availability availability.Status `json:"availability"`
}

type ClassResult struct {
	Code         string              `json:"code"`
	Name         string              `json:"name"`
	Fare         *fare.Quote         `json:"fare,omitempty"`
	Availability availability.Status `json:"availability"`
	ByDate       []DateStatus        `json:"by_date,omitempty"`
}

type TrainResult struct {
	Number       string        `json:"number"`
	Name         string        `json:"name"`
	Type         string        `json:"type"`
	TypeName     string        `json:"type_name"`
	From         StationRef    `json:"from"`
	To           StationRef    `json:"to"`
	Departure    time.Time     `json:"departure"`
	Arrival      time.Time     `json:"arrival"`
	DurationMin  int           `json:"duration_min"`
	DistanceKm   int           `json:"distance_km"`
	FromPlatform int           `json:"from_platform"`
	RunningDays  []bool        `json:"running_days"`
	RunsOnDate   bool          `json:"runs_on_date"`
	Pantry       bool          `json:"pantry"`
	Flexi        bool          `json:"flexi"`
	Classes      []ClassResult `json:"classes"`
}

type Result struct {
	From         StationRef    `json:"from"`
	To           StationRef    `json:"to"`
	FromStations []StationRef  `json:"from_stations"`
	ToStations   []StationRef  `json:"to_stations"`
	Date         string        `json:"date"`
	Quota        string        `json:"quota"`
	Dates        []string      `json:"dates,omitempty"`
	GeneratedAt  time.Time     `json:"generated_at"`
	Trains       []TrainResult `json:"trains"`
}

type Searcher struct {
	Net   *network.Network
	Avail availability.Provider
}

func (s *Searcher) refs(codes []string) []StationRef {
	out := make([]StationRef, len(codes))
	for i, c := range codes {
		out[i] = s.ref(c)
	}
	return out
}

func (s *Searcher) ref(code string) StationRef {
	st := s.Net.Stations[code]
	return StationRef{code, st.Name, st.State}
}

func days(mask int16) []bool {
	out := make([]bool, 7)
	for i := range out {
		out[i] = mask&(1<<i) != 0
	}
	return out
}

// leg is one train's best boarding/alighting pair between the two station sets.
type leg struct {
	t        *network.Train
	from, to int
}

func (s *Searcher) Search(ctx context.Context, q Query, now time.Time) (*Result, error) {
	if s.Net.Stations[q.From] == nil || s.Net.Stations[q.To] == nil {
		return nil, ErrUnknownStation
	}
	if q.From == q.To {
		return nil, ErrSameStation
	}
	fromSet, toSet := []string{q.From}, []string{q.To}
	if q.Nearby {
		fromSet, toSet = s.Net.Nearby(q.From), s.Net.Nearby(q.To)
	}
	q.FlexDays = min(max(q.FlexDays, 0), MaxFlexDays)

	res := &Result{
		From: s.ref(q.From), To: s.ref(q.To),
		FromStations: s.refs(fromSet), ToStations: s.refs(toSet),
		Date: q.Date.Format(time.DateOnly), Quota: q.Quota, GeneratedAt: now, Trains: []TrainResult{},
	}
	var dates []time.Time
	for d := -q.FlexDays; d <= q.FlexDays; d++ {
		dt := q.Date.AddDate(0, 0, d)
		dates = append(dates, dt)
		if q.FlexDays > 0 {
			res.Dates = append(res.Dates, dt.Format(time.DateOnly))
		}
	}

	for _, l := range s.legs(fromSet, toSet, q) {
		tr := s.build(ctx, l, q, dates, now)
		if tr.RunsOnDate || q.FlexDays > 0 && anyRuns(l.t, l.from, dates) {
			res.Trains = append(res.Trains, tr)
		}
	}
	sort.Slice(res.Trains, func(i, j int) bool {
		a, b := res.Trains[i], res.Trains[j]
		if !a.Departure.Equal(b.Departure) {
			return a.Departure.Before(b.Departure)
		}
		return a.Number < b.Number
	})
	return res, nil
}

// legs finds, for every train calling at a from-station and later at a to-station,
// the pair to offer: the exact requested stations when the train serves them,
// otherwise the first from-station and first to-station after it.
func (s *Searcher) legs(fromSet, toSet []string, q Query) []leg {
	rank := func(set []string, code string) int {
		for i, c := range set {
			if c == code {
				return i
			}
		}
		return -1
	}
	best := map[string]leg{}
	for _, f := range fromSet {
		for _, t := range s.Net.Calls[f] {
			fi := t.StopIndex(f)
			if t.Stops[fi].Dep == nil {
				continue
			}
			if q.Class != "" && t.Capacity(s.Net, q.Class) == 0 {
				continue
			}
			for ti := fi + 1; ti < len(t.Stops); ti++ {
				if rank(toSet, t.Stops[ti].Station) < 0 {
					continue
				}
				cand := leg{t, fi, ti}
				if cur, ok := best[t.Number]; !ok || better(cand, cur, fromSet, toSet, rank) {
					best[t.Number] = cand
				}
				break
			}
		}
	}
	out := make([]leg, 0, len(best))
	for _, l := range best {
		out = append(out, l)
	}
	return out
}

func better(a, b leg, fromSet, toSet []string, rank func([]string, string) int) bool {
	ra := rank(fromSet, a.t.Stops[a.from].Station) + rank(toSet, a.t.Stops[a.to].Station)
	rb := rank(fromSet, b.t.Stops[b.from].Station) + rank(toSet, b.t.Stops[b.to].Station)
	return ra < rb
}

// originDate is the day the train left its origin, given the travel date at stop idx.
func originDate(t *network.Train, idx int, travel time.Time) time.Time {
	return travel.AddDate(0, 0, -t.Stops[idx].Time()/1440)
}

func anyRuns(t *network.Train, from int, dates []time.Time) bool {
	for _, d := range dates {
		if t.RunsOn(originDate(t, from, d)) {
			return true
		}
	}
	return false
}

func (s *Searcher) build(ctx context.Context, l leg, q Query, dates []time.Time, now time.Time) TrainResult {
	t, n := l.t, s.Net
	od := originDate(t, l.from, q.Date)
	depMin, arrMin := t.Stops[l.from].Time(), *t.Stops[l.to].Arr
	tt := n.TrainTypes[t.Type]
	tr := TrainResult{
		Number: t.Number, Name: t.Name, Type: t.Type, TypeName: tt.Name,
		From:         s.ref(t.Stops[l.from].Station),
		To:           s.ref(t.Stops[l.to].Station),
		Departure:    od.Add(time.Duration(depMin) * time.Minute),
		Arrival:      od.Add(time.Duration(arrMin) * time.Minute),
		DurationMin:  arrMin - depMin,
		DistanceKm:   t.Stops[l.to].DistKm - t.Stops[l.from].DistKm,
		FromPlatform: t.Stops[l.from].Platform,
		RunningDays:  days(t.RunningDays),
		RunsOnDate:   t.RunsOn(od),
		Pantry:       t.Pantry,
		Flexi:        tt.FlexiFare,
	}
	for _, c := range t.Classes(n) {
		cr := ClassResult{Code: c, Name: n.Classes[c].Name}
		if quote, err := fare.Compute(n, t.Type, c, tr.DistanceKm, q.Quota); err == nil {
			cr.Fare = &quote
		}
		req := availability.Request{Train: t, OriginDate: od, Class: c, Quota: q.Quota, FromIdx: l.from, ToIdx: l.to}
		cr.Availability = s.Avail.Status(ctx, req, now)
		if len(dates) > 1 {
			for _, d := range dates {
				req.OriginDate = originDate(t, l.from, d)
				cr.ByDate = append(cr.ByDate, DateStatus{d.Format(time.DateOnly), s.Avail.Status(ctx, req, now)})
			}
		}
		tr.Classes = append(tr.Classes, cr)
	}
	return tr
}

// Calendar returns availability for one train, class and quota over a run of travel dates.
func (s *Searcher) Calendar(ctx context.Context, t *network.Train, fromIdx, toIdx int, class, quota string, start time.Time, n int, now time.Time) []DateStatus {
	out := make([]DateStatus, 0, n)
	for i := range n {
		d := start.AddDate(0, 0, i)
		req := availability.Request{Train: t, OriginDate: originDate(t, fromIdx, d), Class: class, Quota: quota, FromIdx: fromIdx, ToIdx: toIdx}
		out = append(out, DateStatus{d.Format(time.DateOnly), s.Avail.Status(ctx, req, now)})
	}
	return out
}
