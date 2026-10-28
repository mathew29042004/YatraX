package catering

import (
	"strings"
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

func setup(t *testing.T) (*network.Train, *live.Engine, time.Time) {
	once.Do(func() {
		w, err := seed.Generate(42, 10)
		if err != nil {
			t.Fatal(err)
		}
		net = seed.Network(w)
	})
	origin := time.Date(2026, 11, 10, 0, 0, 0, 0, network.IST)
	return net.Trains["12623"], live.NewEngine(net, nil, time.Now), origin
}

// The ordering window at a stop: open well ahead, closed for short halts, inside the
// cut-off and once the train is there; and the ETA follows the train's delay.
func TestStopCheck(t *testing.T) {
	tr, eng, origin := setup(t)
	sa, last := tr.StopIndex("SA"), len(tr.Stops)-1
	now := origin.Add(12 * time.Hour) // before the 19:45 departure from Chennai
	s := &Service{Now: func() time.Time { return now }}
	check := func(seq, prep int, extra ...live.Incident) (time.Time, string) {
		return s.stopCheck(tr, eng.StatusWith(tr, origin, now, extra), seq, prep)
	}

	eta, why := check(sa, 30)
	if why != "" || eta.IsZero() {
		t.Fatalf("Salem should be open hours ahead: %q", why)
	}
	if _, why := check(last, 5); !strings.Contains(why, "halt is too short") {
		t.Fatalf("the terminus has no halt to hand food over: %q", why)
	}

	// The demo: the train runs 40 minutes late from Chennai, and Salem's ETA moves with it.
	late, why := check(sa, 30, live.Incident{StopSeq: 0, DelayMin: 40, Reason: "late rake"})
	if why != "" || late.Sub(eta) < 30*time.Minute {
		t.Fatalf("40 min late at the start should move Salem's ETA by most of that: %v → %v (%q)", eta, late, why)
	}

	// Inside the cut-off (prep + 15 min before arrival) orders close.
	now = eta.Add(-40 * time.Minute)
	if _, why := check(sa, 30); !strings.Contains(why, "too close") {
		t.Fatalf("40 min before arrival with 30 min prep should be too close: %q", why)
	}
	if _, why := check(sa, 10); why != "" {
		t.Fatalf("a 10-minute dish 40 min out should still be fine: %q", why)
	}

	// Once the train has reached the station it's too late.
	now = eta.Add(10 * time.Minute)
	if _, why := check(sa, 1); !strings.Contains(why, "already reached") {
		t.Fatalf("after arrival: %q", why)
	}
}
