package live

import (
	"sync"
	"testing"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/seed"
)

var (
	netOnce sync.Once
	testNet *network.Network
)

func engine(t *testing.T) *Engine {
	netOnce.Do(func() {
		w, err := seed.Generate(42, 10)
		if err != nil {
			t.Fatal(err)
		}
		testNet = seed.Network(w)
	})
	return NewEngine(testNet, nil, time.Now)
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, network.IST) }

func TestPlanIsDeterministicAndTimesMoveForward(t *testing.T) {
	e := engine(t)
	late := 0
	for _, tr := range e.Net.Trains {
		origin := day(2026, 11, 9)
		a, b := Plan(e.Net, tr, origin, nil), Plan(e.Net, tr, origin, nil)
		prev := -1 << 30
		for i, s := range tr.Stops {
			if a[i] != b[i] {
				t.Fatalf("%s: plan differs between calls at stop %d", tr.Number, i)
			}
			if a[i].ArrDelay < 0 || a[i].DepDelay < 0 {
				t.Fatalf("%s: negative delay at %s", tr.Number, s.Station)
			}
			for _, m := range []*int{s.Arr, s.Dep} {
				if m == nil {
					continue
				}
				d := a[i].DepDelay
				if m == s.Arr {
					d = a[i].ArrDelay
				}
				if *m+d < prev {
					t.Fatalf("%s: expected time goes backwards at %s", tr.Number, s.Station)
				}
				prev = *m + d
			}
		}
		if a[len(a)-1].ArrDelay >= 15 {
			late++
		}
	}
	share := float64(late) / float64(len(e.Net.Trains))
	if share < 0.1 || share > 0.7 {
		t.Errorf("%.0f%% of trains reach their destination 15+ min late; want a believable 10–70%%", share*100)
	}
}

func TestStatusFollowsTheClock(t *testing.T) {
	e := engine(t)
	tr := e.Net.Trains["12623"] // Chennai 19:45 → Trivandrum 11:25 next day
	origin := day(2026, 11, 9)
	before := e.Status(tr, origin, origin.Add(18*time.Hour))
	if before.State != "scheduled" || before.LastIdx != -1 {
		t.Fatalf("at 18:00 the Trivandrum Mail hasn't left: %+v", before.State)
	}
	mid := e.Status(tr, origin, origin.Add(26*time.Hour)) // 02:00 next morning
	if mid.State != "running" && mid.State != "at_station" {
		t.Fatalf("at 02:00 it should be on its way, got %s (%s)", mid.State, mid.Message)
	}
	if mid.Progress <= 0 || mid.Progress >= 1 || mid.Lat == 0 {
		t.Errorf("mid-journey position looks wrong: progress %.2f at %.3f,%.3f", mid.Progress, mid.Lat, mid.Lon)
	}
	after := e.Status(tr, origin, origin.Add(48*time.Hour))
	if after.State != "arrived" || after.Progress != 1 {
		t.Errorf("two days later it has arrived, got %s", after.State)
	}
	if e.Status(tr, origin, origin.Add(26*time.Hour)).DistKm != mid.DistKm {
		t.Error("the same moment gave two different positions")
	}
}

func TestIncidentDelaysTheRestOfTheRun(t *testing.T) {
	e := engine(t)
	tr := e.Net.Trains["12623"]
	origin := day(2026, 11, 9)
	sa := tr.StopIndex("SA")
	base := Plan(e.Net, tr, origin, nil)
	held := Plan(e.Net, tr, origin, []Incident{{StopSeq: sa, DelayMin: 40, Reason: "Held for a crossing"}})
	if held[sa].DepDelay < base[sa].DepDelay+40 {
		t.Errorf("held at Salem: departs %d late, want at least %d", held[sa].DepDelay, base[sa].DepDelay+40)
	}
	if held[sa].ArrDelay != base[sa].ArrDelay {
		t.Error("a hold at Salem shouldn't change the arrival there")
	}
	if held[sa+1].ArrDelay <= base[sa+1].ArrDelay {
		t.Error("the hold should carry on to the next stop")
	}
	if held[sa].Reason != "Held for a crossing" {
		t.Errorf("reason = %q", held[sa].Reason)
	}
}

func TestMapAndBoard(t *testing.T) {
	e := engine(t)
	now := day(2026, 11, 10).Add(14 * time.Hour)
	running := e.Running(now)
	if len(running) < 300 {
		t.Errorf("only %d trains running at 14:00 on a weekday", len(running))
	}
	board := e.Board("MAS", now, 4*time.Hour)
	if len(board) == 0 {
		t.Fatal("Chennai Central board is empty")
	}
	for i := 1; i < len(board); i++ {
		a, b := board[i-1], board[i]
		ta, tb := a.ExpDep, b.ExpDep
		if ta == nil {
			ta = a.ExpArr
		}
		if tb == nil {
			tb = b.ExpArr
		}
		if tb.Before(*ta) {
			t.Fatal("board isn't in time order")
		}
	}
}

func TestCurrentRunPrefersTheTrainOnTheMove(t *testing.T) {
	e := engine(t)
	tr := e.Net.Trains["12623"]
	now := day(2026, 11, 10).Add(3 * time.Hour) // 03:00: the 9 Nov departure is running
	if got := e.CurrentRun(tr, now); !got.Equal(day(2026, 11, 9)) {
		t.Errorf("current run = %s, want 9 Nov", got.Format(time.DateOnly))
	}
	now = day(2026, 11, 10).Add(15 * time.Hour) // after arrival, before the evening departure
	if got := e.CurrentRun(tr, now); !got.Equal(day(2026, 11, 10)) {
		t.Errorf("current run = %s, want tonight's (10 Nov)", got.Format(time.DateOnly))
	}
}

func TestBuckets(t *testing.T) {
	for d, want := range map[int]int{0: 0, 14: 0, 15: 15, 59: 30, 61: 60, 400: 300} {
		if got := bucket(d); got != want {
			t.Errorf("bucket(%d) = %d, want %d", d, got, want)
		}
	}
}

// The dashboards' punctuality must agree with what the live status showed passengers.
func TestOutcomeMatchesLiveStatus(t *testing.T) {
	e := engine(t)
	origin := day(2026, 11, 2)
	runs, late := 0, 0
	for _, tr := range e.Net.Trains {
		o, ok := e.Outcome(tr, origin)
		if !ok {
			continue
		}
		st := e.Status(tr, origin, o.Arrival.Add(time.Minute))
		last := st.Stops[len(st.Stops)-1]
		if st.State != "arrived" || o.ArrDelay != max(0, last.ArrDelay) {
			t.Fatalf("%s: outcome %d min late, live status %s %d min", tr.Number, o.ArrDelay, st.State, last.ArrDelay)
		}
		if o.ArrDelay > 0 && o.Reason == "" {
			t.Fatalf("%s: late with no reason", tr.Number)
		}
		runs++
		if o.ArrDelay > 15 {
			late++
		}
	}
	if runs == 0 || late == 0 || late > runs/2 {
		t.Fatalf("%d of %d runs more than 15 min late: the delay model looks off", late, runs)
	}
}

// A breakdown between stations delays the arrival at the next stop; a hold at a station
// only delays the departure from it.
func TestEnRouteIncidentDelaysArrival(t *testing.T) {
	e := engine(t)
	tr := e.Net.Trains["12623"]
	origin := day(2026, 11, 3)
	base := Plan(e.Net, tr, origin, nil)
	k := 3
	broke := Plan(e.Net, tr, origin, []Incident{{StopSeq: k, DelayMin: 90, Reason: "Engine failure", EnRoute: true}})
	held := Plan(e.Net, tr, origin, []Incident{{StopSeq: k, DelayMin: 90, Reason: "Held"}})
	if got := broke[k].ArrDelay - base[k].ArrDelay; got != 90 || broke[k].Reason != "Engine failure" {
		t.Fatalf("breakdown before stop %d: arrival %+d min (%q), want +90", k, got, broke[k].Reason)
	}
	if held[k].ArrDelay != base[k].ArrDelay || held[k].DepDelay <= base[k].DepDelay {
		t.Fatalf("a hold at stop %d must keep the arrival and delay the departure", k)
	}
}
