package booking

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

func book(t *testing.T, s *Service, user, train, from, to string, date time.Time, class string, pax int) *Booking {
	t.Helper()
	var ps []PassengerInput
	for i := 0; i < pax; i++ {
		ps = append(ps, PassengerInput{Name: fmt.Sprintf("Traveller %d", i+1), Age: 30 + i, Gender: "F"})
	}
	b, err := s.Hold(context.Background(), user, "", HoldRequest{Train: train, From: from, To: to, Date: date.Format(time.DateOnly), Class: class, Quota: "GN", Passengers: ps})
	if err != nil {
		t.Fatalf("hold %s %s→%s %s: %v", train, from, to, class, err)
	}
	return confirmNow(t, s, b)
}

func TestExtraCoachClearsWaitlist(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	date := time.Date(2026, 11, 27, 0, 0, 0, 0, network.IST)
	wl := 0
	for i := 0; i < 80 && wl < 6; i++ {
		b, err := s.Hold(ctx, users[150+i%100], "", HoldRequest{Train: "12623", From: "MAS", To: "TVC", Date: date.Format(time.DateOnly), Class: "2A", Quota: "GN",
			Passengers: []PassengerInput{{Name: "Long", Age: 40, Gender: "M"}, {Name: "Queue", Age: 38, Gender: "F"}}})
		if err != nil {
			continue
		}
		b = confirmNow(t, s, b)
		for _, p := range b.Passengers {
			if p.Status == "WL" {
				wl++
			}
		}
	}
	if wl == 0 {
		t.Fatal("setup: no waitlist in 2A")
	}
	res, err := s.AddCoaches(ctx, "12623", date, "2A", 1, users[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Coaches) != 1 || len(res.Promotions) == 0 || res.Waitlisted >= wl {
		t.Fatalf("added %v: %d promotions, %d still waitlisted (was %d)", res.Coaches, len(res.Promotions), res.Waitlisted, wl)
	}
	noOverlaps(t, s)
	onNew := false
	for _, p := range res.Promotions {
		if len(p.To) > 4 && p.To[4:4+len(res.Coaches[0])] == res.Coaches[0] {
			onNew = true
		}
	}
	if !onNew {
		t.Errorf("nobody was put on the new coach %s: %+v", res.Coaches[0], res.Promotions)
	}
	if _, err := s.AddCoaches(ctx, "12623", date, "2A", 9, users[0]); err == nil {
		t.Error("more than 4 coaches at once should be refused")
	}
}

func TestCancelRefundsAndRebooks(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	date := time.Date(2026, 11, 28, 0, 0, 0, 0, network.IST)
	a := book(t, s, users[260], "12623", "MAS", "ERN", date, "3A", 2)
	b := book(t, s, users[261], "12623", "MAS", "ERN", date, "SL", 1)

	res, err := s.Disrupt(ctx, "12623", date, Disruption{Kind: "cancelled", Reason: "Engine failure"}, users[0])
	if err != nil {
		t.Fatal(err)
	}
	if res.Bookings != 2 || res.Refunded != a.TotalFare+b.TotalFare {
		t.Errorf("affected %d bookings, refunded ₹%d; want 2 and ₹%d", res.Bookings, res.Refunded, a.TotalFare+b.TotalFare)
	}
	for _, x := range []*Booking{a, b} {
		got, _ := s.Get(ctx, x.ID)
		if got.Status != "CANCELLED" || len(got.Refunds) == 0 || got.Refunds[0].Amount != x.TotalFare {
			t.Errorf("%s: %s, refunds %+v", x.PNR, got.Status, got.Refunds)
		}
	}
	trialBalanceZero(t, s)
	if res.Rebooked == 0 {
		t.Fatalf("no alternative held for anyone: %+v", res.Affected)
	}
	for _, af := range res.Affected {
		if af.Alternative == nil {
			continue
		}
		alt, err := s.Get(ctx, af.Alternative.BookingID)
		if err != nil || alt.Train == "12623" || alt.From != "MAS" || alt.To != "ERN" || alt.HoldExpiresAt == nil || alt.HoldExpiresAt.Sub(s.Now()) < time.Hour {
			t.Errorf("alternative %+v: %v", alt, err)
		}
	}
	if _, err := s.Hold(ctx, users[262], "", HoldRequest{Train: "12623", From: "MAS", To: "ERN", Date: date.Format(time.DateOnly), Class: "SL", Quota: "GN", Passengers: pax("Too Late")}); !errors.Is(err, ErrDisrupted) {
		t.Errorf("booking a cancelled train: %v", err)
	}
	if _, err := s.Disrupt(ctx, "12623", date, Disruption{Kind: "cancelled"}, users[0]); !errors.Is(err, ErrAlreadyDone) {
		t.Errorf("cancelling twice: %v", err)
	}
}

func TestShortTerminateAndDivert(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	date := time.Date(2026, 11, 29, 0, 0, 0, 0, network.IST)
	far := book(t, s, users[270], "12623", "MAS", "TVC", date, "SL", 1)
	near := book(t, s, users[271], "12623", "MAS", "ED", date, "SL", 1)
	res, err := s.Disrupt(ctx, "12623", date, Disruption{Kind: "short_terminated", Station: "ERN", Reason: "Track washed away near Kottayam"}, users[0])
	if err != nil {
		t.Fatal(err)
	}
	tr := s.Net.Trains["12623"]
	share := float64(tr.Stops[tr.StopIndex("TVC")].DistKm-tr.Stops[tr.StopIndex("ERN")].DistKm) / float64(tr.Stops[tr.StopIndex("TVC")].DistKm)
	want := int(float64(far.Passengers[0].Fare)*share + 0.5)
	if res.Bookings != 1 || res.Affected[0].BookingID != far.ID || !res.Affected[0].Partial || res.Affected[0].Refund != want {
		t.Fatalf("short-termination: %+v (want a partial refund of ₹%d to the TVC passenger only)", res, want)
	}
	if got, _ := s.Get(ctx, far.ID); got.Status != "CONFIRMED" {
		t.Errorf("the TVC passenger still travels to ERN: %s", got.Status)
	}
	if got, _ := s.Get(ctx, near.ID); len(got.Refunds) != 0 {
		t.Error("the Erode passenger isn't affected")
	}
	trialBalanceZero(t, s)

	date2 := date.AddDate(0, 0, 1)
	sa := book(t, s, users[272], "12623", "MAS", "SA", date2, "SL", 1)
	thru := book(t, s, users[273], "12623", "MAS", "ERN", date2, "SL", 1)
	res, err = s.Disrupt(ctx, "12623", date2, Disruption{Kind: "diverted", Skip: []string{"SA"}, ExtraDelay: 75, Reason: "Diverted via Tiruchchirappalli"}, users[0])
	if err != nil {
		t.Fatal(err)
	}
	if res.Bookings != 1 || res.Affected[0].BookingID != sa.ID || res.Affected[0].Refund != sa.TotalFare {
		t.Errorf("diversion: %+v (want a full refund for the Salem passenger)", res)
	}
	if got, _ := s.Get(ctx, thru.ID); got.Status != "CONFIRMED" {
		t.Error("a passenger going through Salem keeps their ticket")
	}
	if _, err := s.Disrupt(ctx, "12623", date2.AddDate(0, 0, 1), Disruption{Kind: "diverted"}, users[0]); !errors.Is(err, ErrBadDisruption) {
		t.Errorf("a diversion must name what it skips: %v", err)
	}
}
