package search

import (
	"context"
	"sort"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/fare"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// When no direct train has berths, two other ways usually do:
//   - a split journey: the same train on two tickets, changing berth at a station on the way
//     (berths free from A to X and from X to B, but not from A to B);
//   - a connection: one train to a junction, another onward, with time to change.

const (
	MinConnection = 40 * time.Minute
	MaxConnection = 6 * time.Hour
	maxAlternates = 6
)

type AltLeg struct {
	Train        string              `json:"train"`
	Name         string              `json:"name"`
	From         StationRef          `json:"from"`
	To           StationRef          `json:"to"`
	Date         string              `json:"date"` // travel date at the boarding station
	Departure    time.Time           `json:"departure"`
	Arrival      time.Time           `json:"arrival"`
	Class        string              `json:"class"`
	Fare         int                 `json:"fare"`
	Availability availability.Status `json:"availability"`
}

type Alternative struct {
	Kind        string     `json:"kind"` // split, connection
	Via         StationRef `json:"via"`
	Legs        []AltLeg   `json:"legs"`
	Fare        int        `json:"fare"` // per passenger, both tickets
	DurationMin int        `json:"duration_min"`
	WaitMin     int        `json:"wait_min,omitempty"` // time to change trains
	Note        string     `json:"note"`
}

func (s *Searcher) altLeg(ctx context.Context, t *network.Train, from, to int, travel time.Time, class string, now time.Time) (AltLeg, bool) {
	od := originDate(t, from, travel)
	if !t.RunsOn(od) {
		return AltLeg{}, false
	}
	q, err := fare.Compute(s.Net, t.Type, class, t.Stops[to].DistKm-t.Stops[from].DistKm, "GN")
	if err != nil {
		return AltLeg{}, false
	}
	st := s.Avail.Status(ctx, availability.Request{Train: t, OriginDate: od, Class: class, Quota: "GN", FromIdx: from, ToIdx: to}, now)
	return AltLeg{
		Train: t.Number, Name: t.Name, From: s.ref(t.Stops[from].Station), To: s.ref(t.Stops[to].Station),
		Date: travel.Format(time.DateOnly), Class: class, Fare: q.Total, Availability: st,
		Departure: od.Add(time.Duration(t.Stops[from].Time()) * time.Minute),
		Arrival:   od.Add(time.Duration(*t.Stops[to].Arr) * time.Minute),
	}, st.Code == availability.AVL
}

// Alternatives suggests split journeys and connections in one class (default SL) when
// the direct trains for that class are waitlisted.
func (s *Searcher) Alternatives(ctx context.Context, q Query, now time.Time) ([]Alternative, error) {
	if s.Net.Stations[q.From] == nil || s.Net.Stations[q.To] == nil {
		return nil, ErrUnknownStation
	}
	if q.Class == "" {
		q.Class = "SL"
	}
	out := []Alternative{}
	direct := map[string]bool{}

	// Split journeys on trains that serve both stations.
	for _, l := range s.legs([]string{q.From}, []string{q.To}, q) {
		t := l.t
		direct[t.Number] = true
		if t.Capacity(s.Net, q.Class) == 0 {
			continue
		}
		whole, ok := s.altLeg(ctx, t, l.from, l.to, q.Date, q.Class, now)
		if ok || whole.Availability.Code == availability.NotRunning || whole.Departure.IsZero() {
			continue // has berths already, or doesn't run
		}
		// Try changing at each halt, the middle of the journey first.
		mid := (l.from + l.to) / 2
		for d := 0; d < l.to-l.from; d++ {
			k := mid + (d+1)/2*(1-2*(d%2))
			if k <= l.from || k >= l.to || t.Stops[k].Dep == nil {
				continue
			}
			a, okA := s.altLeg(ctx, t, l.from, k, q.Date, q.Class, now)
			if !okA {
				continue
			}
			bTravel := q.Date.AddDate(0, 0, (t.Stops[k].Time()/1440)-(t.Stops[l.from].Time()/1440))
			b, okB := s.altLeg(ctx, t, k, l.to, bTravel, q.Class, now)
			if !okB {
				continue
			}
			out = append(out, Alternative{Kind: "split", Via: s.ref(t.Stops[k].Station), Legs: []AltLeg{a, b}, Fare: a.Fare + b.Fare,
				DurationMin: int(b.Arrival.Sub(a.Departure).Minutes()),
				Note:        "Same train, two tickets: you may change berth (even coach) at " + s.Net.Stations[t.Stops[k].Station].Name})
			break
		}
	}

	// Connections through a major junction (not one next to the destination: that's not a change).
	near := map[string]bool{}
	for _, c := range s.Net.Nearby(q.To) {
		near[c] = true
	}
	from, to := s.Net.Stations[q.From], s.Net.Stations[q.To]
	straight := network.DistanceKm(from, to)
	evaluated := 0
	for _, a := range s.Net.Calls[q.From] {
		if direct[a.Number] || a.Capacity(s.Net, q.Class) == 0 || evaluated > 400 {
			continue
		}
		fi := a.StopIndex(q.From)
		if a.Stops[fi].Dep == nil {
			continue
		}
		for xi := fi + 1; xi < len(a.Stops) && evaluated <= 400; xi++ {
			x := a.Stops[xi].Station
			st := s.Net.Stations[x]
			if st == nil || st.Importance != 1 || near[x] {
				continue
			}
			if network.DistanceKm(from, st)+network.DistanceKm(st, to) > 1.3*straight+50 {
				continue // not on the way: going there and back is no alternative
			}
			for _, b := range s.Net.Calls[x] {
				if b.Number == a.Number || b.Capacity(s.Net, q.Class) == 0 {
					continue
				}
				bx, bt := b.StopIndex(x), b.StopIndex(q.To)
				if bt <= bx || b.Stops[bx].Dep == nil {
					continue
				}
				evaluated++
				legA, okA := s.altLeg(ctx, a, fi, xi, q.Date, q.Class, now)
				if legA.Departure.IsZero() {
					break // a doesn't run that day
				}
				if !okA {
					continue
				}
				// b leaves x on the day legA arrives, or the next.
				var legB AltLeg
				var okB bool
				for dd := 0; dd <= 1 && !okB; dd++ {
					day := time.Date(legA.Arrival.Year(), legA.Arrival.Month(), legA.Arrival.Day(), 0, 0, 0, 0, network.IST).AddDate(0, 0, dd)
					cand, ok := s.altLeg(ctx, b, bx, bt, day, q.Class, now)
					if wait := cand.Departure.Sub(legA.Arrival); ok && wait >= MinConnection && wait <= MaxConnection {
						legB, okB = cand, true
					}
				}
				if !okB {
					continue
				}
				out = append(out, Alternative{Kind: "connection", Via: s.ref(x), Legs: []AltLeg{legA, legB}, Fare: legA.Fare + legB.Fare,
					DurationMin: int(legB.Arrival.Sub(legA.Departure).Minutes()), WaitMin: int(legB.Departure.Sub(legA.Arrival).Minutes()),
					Note: "Change trains at " + s.Net.Stations[x].Name})
			}
		}
	}

	// Same train first (simplest), then earliest arrival; for the same pair of trains, the
	// junction with the most comfortable change (about 75 minutes).
	comfort := func(a Alternative) int {
		d := a.WaitMin - 75
		if d < 0 {
			d = -d
		}
		return d
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind == "split"
		}
		if ea, eb := a.Legs[len(a.Legs)-1].Arrival, b.Legs[len(b.Legs)-1].Arrival; !ea.Equal(eb) {
			return ea.Before(eb)
		}
		return comfort(a) < comfort(b)
	})
	seen := map[string]bool{}
	keep := []Alternative{}
	for _, a := range out {
		k := a.Kind + a.Legs[0].Train // one suggestion per first train, for variety
		if seen[k] || len(keep) >= maxAlternates {
			continue
		}
		seen[k] = true
		keep = append(keep, a)
	}
	return keep, nil
}
