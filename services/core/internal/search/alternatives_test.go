package search

import (
	"context"
	"testing"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// fullThrough waitlists any journey covering both of two stations and has berths otherwise:
// the usual shape of a busy train, full end to end but free on shorter stretches.
type fullThrough struct{ a, b string }

func (f fullThrough) Status(_ context.Context, r availability.Request, _ time.Time) availability.Status {
	if r.Train.StopIndex(f.a) >= r.FromIdx && r.Train.StopIndex(f.a) >= 0 && r.Train.StopIndex(f.b) <= r.ToIdx && r.Train.StopIndex(f.b) > r.FromIdx {
		return availability.WaitlistStatus(40, 300, 0, 10)
	}
	return availability.AvailableStatus(50)
}

func TestAlternatives(t *testing.T) {
	s := searcher(t)
	s.Avail = fullThrough{"MAS", "ERN"}
	alts, err := s.Alternatives(context.Background(), Query{From: "MAS", To: "ERN", Date: date(2026, 11, 2), Class: "SL"}, now)
	if err != nil {
		t.Fatal(err)
	}
	splits, conns := 0, 0
	for _, a := range alts {
		if len(a.Legs) != 2 || a.Fare != a.Legs[0].Fare+a.Legs[1].Fare {
			t.Fatalf("%s via %s: %d legs, fare %d", a.Kind, a.Via.Code, len(a.Legs), a.Fare)
		}
		l0, l1 := a.Legs[0], a.Legs[1]
		if l0.From.Code != "MAS" || l1.To.Code != "ERN" || l0.To.Code != a.Via.Code || l1.From.Code != a.Via.Code {
			t.Fatalf("legs don't join up: %s→%s, %s→%s via %s", l0.From.Code, l0.To.Code, l1.From.Code, l1.To.Code, a.Via.Code)
		}
		if l0.Availability.Code != availability.AVL || l1.Availability.Code != availability.AVL {
			t.Fatal("every leg must have berths")
		}
		switch a.Kind {
		case "split":
			splits++
			if l0.Train != l1.Train || !l1.Departure.After(l0.Departure) {
				t.Fatal("a split journey is one train")
			}
		case "connection":
			conns++
			if wait := l1.Departure.Sub(l0.Arrival); wait < MinConnection || wait > MaxConnection || a.WaitMin != int(wait.Minutes()) {
				t.Fatalf("connection wait %v outside %v–%v", wait, MinConnection, MaxConnection)
			}
		}
		via := s.Net.Stations[a.Via.Code]
		if network.DistanceKm(s.Net.Stations["MAS"], via)+network.DistanceKm(via, s.Net.Stations["ERN"]) > 1.3*network.DistanceKm(s.Net.Stations["MAS"], s.Net.Stations["ERN"])+50 {
			t.Errorf("%s is out of the way", a.Via.Code)
		}
	}
	if splits == 0 {
		t.Error("trains full end to end but free on shorter stretches should give split journeys")
	}
	if len(alts) > maxAlternates {
		t.Errorf("%d alternatives, cap is %d", len(alts), maxAlternates)
	}
	t.Logf("%d split journeys, %d connections", splits, conns)
}
