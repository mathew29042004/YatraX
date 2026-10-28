package search

import (
	"context"
	"testing"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/seed"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, network.IST) // a Monday

func searcher(t *testing.T) *Searcher {
	t.Helper()
	w, err := seed.Generate(42, 10)
	if err != nil {
		t.Fatal(err)
	}
	n := seed.Network(w)
	return &Searcher{Net: n, Avail: availability.Simulated{N: n}}
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, network.IST) }

func TestChennaiToErnakulam(t *testing.T) {
	s := searcher(t)
	res, err := s.Search(context.Background(), Query{From: "MAS", To: "ERN", Date: date(2026, 11, 2), Quota: "GN"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trains) < 3 {
		t.Fatalf("want several MAS→ERS trains, got %d", len(res.Trains))
	}
	found := false
	for i, tr := range res.Trains {
		if i > 0 && tr.Departure.Before(res.Trains[i-1].Departure) {
			t.Error("results not sorted by departure")
		}
		if tr.From.Code != "MAS" || tr.To.Code != "ERN" || !tr.RunsOnDate {
			t.Errorf("%s: got %s→%s runs=%v", tr.Number, tr.From.Code, tr.To.Code, tr.RunsOnDate)
		}
		if !tr.Arrival.After(tr.Departure) || tr.DurationMin <= 0 {
			t.Errorf("%s: arrival before departure", tr.Number)
		}
		for _, c := range tr.Classes {
			if c.Fare == nil || c.Fare.Total <= 0 {
				t.Errorf("%s %s: missing fare", tr.Number, c.Code)
			}
			switch c.Availability.Code {
			case availability.AVL, availability.RAC, availability.WL, availability.Regret:
			default:
				t.Errorf("%s %s: unexpected status %s a week out", tr.Number, c.Code, c.Availability.Code)
			}
		}
		if tr.Number == "12623" {
			found = true
			if tr.Departure.Format("2006-01-02 15:04") != "2026-11-02 19:45" { // the real timetable
				t.Errorf("Trivandrum Mail departs %v", tr.Departure)
			}
		}
	}
	if !found {
		t.Error("12623 Trivandrum Mail missing from MAS→ERS")
	}
}

func TestNearbyStationsWidenSearch(t *testing.T) {
	s := searcher(t)
	near := s.Net.Nearby("MAS")
	if len(near) < 2 || near[0] != "MAS" {
		t.Fatalf("Nearby(MAS) = %v", near)
	}
	exact, _ := s.Search(context.Background(), Query{From: "MAS", To: "TJ", Date: date(2026, 11, 2), Quota: "GN"}, now)
	wide, _ := s.Search(context.Background(), Query{From: "MAS", To: "TJ", Date: date(2026, 11, 2), Quota: "GN", Nearby: true}, now)
	if len(exact.Trains) != 0 {
		t.Errorf("no train runs Chennai Central → Thanjavur directly, got %d", len(exact.Trains))
	}
	if len(wide.Trains) == 0 {
		t.Error("with nearby stations, Chennai Egmore → Thanjavur trains should appear")
	}
	for _, tr := range wide.Trains {
		if tr.From.Code != "MS" && tr.From.Code != "TBM" {
			t.Errorf("%s boards at %s", tr.Number, tr.From.Code)
		}
	}
}

func TestBoardingDateAccountsForOvernightRunning(t *testing.T) {
	// The Kerala Express leaves TVC at 11:15 and reaches Jolarpettai at 00:40,
	// so boarding at Jolarpettai on the 3rd means the train left TVC on the 2nd.
	s := searcher(t)
	res, _ := s.Search(context.Background(), Query{From: "JTJ", To: "NDLS", Date: date(2026, 11, 3), Quota: "GN"}, now)
	for _, tr := range res.Trains {
		if tr.Number == "12625" {
			if tr.Departure.Format("2006-01-02") != "2026-11-03" {
				t.Errorf("boards %v, want on 3 Nov", tr.Departure)
			}
			return
		}
	}
	t.Error("Kerala Express not found JTJ→NDLS")
}

func TestFlexibleDates(t *testing.T) {
	s := searcher(t)
	res, _ := s.Search(context.Background(), Query{From: "MAS", To: "MYS", Date: date(2026, 11, 4), Quota: "GN", FlexDays: 3}, now)
	if len(res.Dates) != 7 {
		t.Fatalf("want 7 dates, got %v", res.Dates)
	}
	var weekly *TrainResult
	for i := range res.Trains {
		if res.Trains[i].Number == "22682" {
			weekly = &res.Trains[i]
		}
	}
	if weekly == nil {
		t.Fatal("the weekly Chennai – Mysore SF should appear in the ±3 day grid")
	}
	running := 0
	for _, d := range weekly.Classes[0].ByDate {
		if d.Availability.Code != availability.NotRunning {
			running++
		}
	}
	if running != 1 {
		t.Errorf("a weekly train should run on 1 of the 7 days, got %d", running)
	}
}

func TestBookingRules(t *testing.T) {
	s := searcher(t)
	tr := s.Net.Trains["12623"]
	from, to := tr.StopIndex("MAS"), tr.StopIndex("ERN")
	status := func(d time.Time, class, quota string, at time.Time) string {
		return s.Avail.Status(context.Background(), availability.Request{Train: tr, OriginDate: d, Class: class, Quota: quota, FromIdx: from, ToIdx: to}, at).Code
	}
	if got := status(date(2027, 1, 20), "SL", "GN", now); got != availability.NotOpen {
		t.Errorf("86 days out: %s, want NOT_OPEN", got)
	}
	if got := status(date(2026, 11, 2), "SL", "TQ", now); got != availability.TatkalNotOpen {
		t.Errorf("Tatkal a week out: %s, want TATKAL_NOT_OPEN", got)
	}
	tatkalDay := time.Date(2026, 11, 1, 11, 30, 0, 0, network.IST)
	if got := status(date(2026, 11, 2), "SL", "TQ", tatkalDay); got == availability.TatkalNotOpen {
		t.Error("Sleeper Tatkal should be open at 11:30 the day before")
	}
	if got := status(date(2026, 10, 26), "SL", "GN", time.Date(2026, 10, 26, 20, 0, 0, 0, network.IST)); got != availability.Departed {
		t.Errorf("after 19:30 departure: %s, want DEPARTED", got)
	}
	if got := status(date(2026, 11, 2), "1A", "TQ", tatkalDay); got != availability.NoQuota && got != availability.NotRunning {
		// 12623 may not have 1A; either way there is no 1A Tatkal.
		if tr.Capacity(s.Net, "1A") > 0 {
			t.Errorf("1A Tatkal: %s, want NO_QUOTA", got)
		}
	}
	a := status(date(2026, 11, 20), "3A", "GN", now)
	b := status(date(2026, 11, 20), "3A", "GN", now)
	if a != b {
		t.Error("availability must be deterministic")
	}
}

func TestFestivalRaisesDemand(t *testing.T) {
	s := searcher(t)
	tr := s.Net.Trains["12309"] // Patna Rajdhani, PNBE → NDLS; its return 12310 heads to Bihar
	ret := s.Net.Trains[tr.Return]
	from, to := 0, len(ret.Stops)-1
	beforeChhath := availability.FestivalFactor(s.Net, ret, from, to, date(2026, 11, 13))
	ordinary := availability.FestivalFactor(s.Net, ret, from, to, date(2026, 12, 2))
	if beforeChhath <= ordinary || ordinary != 1 {
		t.Errorf("Delhi→Patna before Chhath %.2f, ordinary day %.2f", beforeChhath, ordinary)
	}
}

func TestRejectsBadQueries(t *testing.T) {
	s := searcher(t)
	if _, err := s.Search(context.Background(), Query{From: "MAS", To: "XXX", Date: date(2026, 11, 2), Quota: "GN"}, now); err != ErrUnknownStation {
		t.Errorf("unknown station: %v", err)
	}
	if _, err := s.Search(context.Background(), Query{From: "MAS", To: "MAS", Date: date(2026, 11, 2), Quota: "GN"}, now); err != ErrSameStation {
		t.Errorf("same station: %v", err)
	}
}
