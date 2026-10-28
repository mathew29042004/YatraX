package control

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/seed"
)

var (
	once sync.Once
	net  *network.Network
)

func svc(t *testing.T, now time.Time) *Service {
	once.Do(func() {
		w, err := seed.Generate(42, 10)
		if err != nil {
			t.Fatal(err)
		}
		net = seed.Network(w)
	})
	clock := func() time.Time { return now }
	return &Service{Net: net, Live: live.NewEngine(net, nil, clock), Now: clock}
}

func day(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, network.IST) }

func TestSectionChart(t *testing.T) {
	c := svc(t, day(10).Add(20*time.Hour))
	s, err := c.Section("MAS", "SA", 4)
	if err != nil {
		t.Fatal(err)
	}
	if s.Stations[0].Code != "MAS" || s.Stations[len(s.Stations)-1].Code != "SA" || s.Stations[0].Km != 0 {
		t.Fatalf("stations: %v … %v", s.Stations[0], s.Stations[len(s.Stations)-1])
	}
	for i := 1; i < len(s.Stations); i++ {
		if s.Stations[i].Km <= s.Stations[i-1].Km {
			t.Fatal("section distances must grow")
		}
	}
	if len(s.Trains) < 10 {
		t.Fatalf("only %d trains on Chennai – Salem in the evening", len(s.Trains))
	}
	up, down, moving := 0, 0, 0
	for _, p := range s.Trains {
		if len(p.Points) < 2 {
			t.Fatalf("%s has %d points", p.Train, len(p.Points))
		}
		if p.Direction == "up" {
			up++
		} else {
			down++
		}
		if p.NowKm != nil {
			moving++
			if *p.NowKm < 0 || *p.NowKm > s.Stations[len(s.Stations)-1].Km {
				t.Errorf("%s is off the section at km %.1f", p.Train, *p.NowKm)
			}
		}
	}
	if up == 0 || down == 0 || moving == 0 {
		t.Errorf("want trains both ways and some on the move: %d up, %d down, %d moving", up, down, moving)
	}
	if _, err := c.Section("MAS", "MAS", 4); err != ErrSection {
		t.Errorf("a section needs two different stations: %v", err)
	}
}

func TestWhatIfRipple(t *testing.T) {
	c := svc(t, day(10).Add(18*time.Hour+30*time.Minute))
	r, err := c.WhatIf(context.Background(), Hold{Train: "12623", Origin: day(10), Station: "MAS", DelayMin: 90})
	if err != nil {
		t.Fatal(err)
	}
	if !r.ArrivalAfter.After(r.ArrivalNow.Add(60 * time.Minute)) {
		t.Errorf("a 90-minute hold should make it much later: %v → %v", r.ArrivalNow, r.ArrivalAfter)
	}
	if len(r.Affected) == 0 {
		t.Fatal("holding the Trivandrum Mail 90 minutes at Chennai Central in the evening rush delays nobody?")
	}
	for _, e := range r.Affected {
		if e.AddedMin < 2 || e.Hop < 1 || e.Hop > MaxHops || e.Cause == "" || e.Via == "" || e.Train == "12623" {
			t.Errorf("odd effect: %+v", e)
		}
	}
	again, _ := c.WhatIf(context.Background(), Hold{Train: "12623", Origin: day(10), Station: "MAS", DelayMin: 90})
	if len(again.Affected) != len(r.Affected) || again.TotalMin != r.TotalMin {
		t.Error("the same what-if should give the same answer")
	}
	small, _ := c.WhatIf(context.Background(), Hold{Train: "12623", Origin: day(10), Station: "MAS", DelayMin: 1})
	if small.TotalMin > r.TotalMin {
		t.Error("a one-minute hold shouldn't ripple more than a 90-minute one")
	}
	if _, err := c.WhatIf(context.Background(), Hold{Train: "12623", Origin: day(10), Station: "TVC", DelayMin: 10}); err == nil {
		t.Error("can't hold a train at its terminus")
	}
}
