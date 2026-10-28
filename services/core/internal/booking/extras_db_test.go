package booking

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

func points(t *testing.T, s *Service, user string) int {
	t.Helper()
	l, err := s.Loyalty(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return l.Balance
}

// Insurance is paid with the ticket and kept by the insurer; points come with the ticket and
// go if it's cancelled; redeemed points become wallet credit. The books balance throughout.
func TestInsuranceAndPoints(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	user := users[281]
	b, err := s.Hold(ctx, user, "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-21", Class: "3A", Quota: "GN",
		Insurance: true, Passengers: []PassengerInput{{Name: "Meera Iyer", Age: 41, Gender: "F"}, {Name: "Ravi Iyer", Age: 44, Gender: "M"}}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Insurance != 2*InsurancePremium || b.TotalFare != b.Fare.Total*2+b.Insurance {
		t.Fatalf("premium %d, total %d for fare %d × 2", b.Insurance, b.TotalFare, b.Fare.Total)
	}
	before := points(t, s, user)
	b = confirmNow(t, s, b)
	want := (b.TotalFare - b.Insurance) / RupeesPerPoint
	if got := points(t, s, user) - before; got != want || b.PointsEarned != want {
		t.Fatalf("earned %d points (booking says %d), want %d", got, b.PointsEarned, want)
	}
	insurer, _ := ledger.Balance(ctx, s.DB, ledger.Insurer)
	if insurer > -ledger.Paise(b.Insurance) {
		t.Fatalf("insurer account %d paise, expected at least %d owed", insurer, ledger.Paise(b.Insurance))
	}
	trialBalanceZero(t, s)

	if _, err := s.Cancel(ctx, user, b.ID, CancelRequest{RefundTo: "wallet"}); err != nil {
		t.Fatal(err)
	}
	if got := points(t, s, user); got != before {
		t.Fatalf("points after cancelling: %d, want %d (taken back)", got, before)
	}
	trialBalanceZero(t, s)

	// Earn enough to redeem, then turn points into wallet credit.
	for i := 0; points(t, s, user) < RedeemBlock && i < 10; i++ {
		b2, err := s.Hold(ctx, user, "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: fmt.Sprintf("2026-11-%02d", 10+i), Class: "1A", Quota: "GN",
			Passengers: []PassengerInput{{Name: "Meera Iyer", Age: 41, Gender: "F"}, {Name: "Ravi Iyer", Age: 44, Gender: "M"}}})
		if err != nil {
			t.Fatal(err)
		}
		confirmNow(t, s, b2)
	}
	w0, _ := s.Wallet(ctx, user)
	l, err := s.RedeemPoints(ctx, user, RedeemBlock)
	if err != nil {
		t.Fatal(err)
	}
	w1, _ := s.Wallet(ctx, user)
	if w1.Balance-w0.Balance != ledger.Paise(RedeemValue) {
		t.Fatalf("wallet grew by %d paise, want %d", w1.Balance-w0.Balance, ledger.Paise(RedeemValue))
	}
	if _, err := s.RedeemPoints(ctx, user, l.Balance+RedeemBlock); err == nil {
		t.Fatal("can't redeem more points than you have")
	}
	trialBalanceZero(t, s)
}

// The divyangjan quota needs the UDID card, gives the concession, and allows one escort.
func TestDivyangjanQuota(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	req := HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-22", Class: "SL", Quota: "HP",
		Passengers: []PassengerInput{{Name: "Arjun Rao", Age: 35, Gender: "M"}, {Name: "Lakshmi Rao", Age: 33, Gender: "F"}}}
	var v ValidationError
	if _, err := s.Hold(ctx, users[282], "", req); !errors.As(err, &v) {
		t.Fatalf("no UDID card: want a validation error, got %v", err)
	}
	req.Passengers[0].IDNumber = "tn01 2345 6789 0123"
	b, err := s.Hold(ctx, users[282], "", req)
	if err != nil {
		t.Fatal(err)
	}
	if b.Fare.Concession == 0 || b.Passengers[0].IDNumber != "••••0123" || b.Passengers[1].IDNumber != "" {
		t.Fatalf("concession %d, ids %q %q", b.Fare.Concession, b.Passengers[0].IDNumber, b.Passengers[1].IDNumber)
	}
	req.Passengers = append(req.Passengers, PassengerInput{Name: "Third Person", Age: 30, Gender: "F"})
	if _, err := s.Hold(ctx, users[283], "", req); !errors.As(err, &v) {
		t.Fatal("the divyangjan quota takes the passenger and one escort only")
	}
}

// At chart time an opted-in party moves up a class into free berths; others stay.
func TestAutoUpgradeAtChart(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	date := "2026-11-23"
	party := []PassengerInput{{Name: "Kavya Nair", Age: 29, Gender: "F"}, {Name: "Hari Nair", Age: 31, Gender: "M"}}
	up, err := s.Hold(ctx, users[284], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: date, Class: "SL", Quota: "GN", AutoUpgrade: true, Passengers: party})
	if err != nil {
		t.Fatal(err)
	}
	up = confirmNow(t, s, up)
	stay, err := s.Hold(ctx, users[285], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: date, Class: "SL", Quota: "GN", Passengers: pax("Not Opted")})
	if err != nil {
		t.Fatal(err)
	}
	stay = confirmNow(t, s, stay)
	origin, _ := time.ParseInLocation(time.DateOnly, up.OriginDate, network.IST)
	ch, err := s.PrepareChart(ctx, "12627", origin, 1)
	if err != nil {
		t.Fatal(err)
	}
	up, _ = s.Get(ctx, up.ID)
	stay, _ = s.Get(ctx, stay.ID)
	if ch.Upgrades < 2 || up.Class != "3A" || up.Passengers[0].UpgradedFrom != "SL" || up.Passengers[0].Status != "CNF" {
		t.Fatalf("upgrades %d; opted-in party now in %s (from %q)", ch.Upgrades, up.Class, up.Passengers[0].UpgradedFrom)
	}
	if up.Passengers[0].Coach != up.Passengers[1].Coach {
		t.Error("the party should stay together")
	}
	if stay.Class != "SL" {
		t.Error("a passenger who didn't opt in must not be moved")
	}
	if up.TotalFare != up.Fare.Total*2 {
		t.Error("an upgrade is free")
	}
	noOverlaps(t, s)
}

// Two waitlisted tickets for one trip: when one confirms, the other is refunded in full.
func TestConfirmationGuarantee(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	dates := []string{"2026-11-24", "2026-11-25"}
	// Fill 1A on both days until it waitlists.
	var first []*Booking
	for d, date := range dates {
		for i := 0; i < 20; i++ {
			b, err := holdAny(t, s, users, 200+d*20+i, HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: date, Class: "1A", Quota: "GN",
				Passengers: []PassengerInput{{Name: fmt.Sprintf("Fill %d", i), Age: 40, Gender: "M"}, {Name: fmt.Sprintf("Fill %db", i), Age: 38, Gender: "F"}}})
			if err != nil {
				t.Fatal(err)
			}
			b = confirmNow(t, s, b)
			if d == 0 && fullyConfirmed(b) {
				first = append(first, b)
			}
			if !fullyConfirmed(b) {
				break
			}
		}
	}
	user := users[290]
	one := []PassengerInput{{Name: "Guarantee Seeker", Age: 36, Gender: "F"}}
	pair, err := s.HoldGuarantee(ctx, user, "g-1", GuaranteeRequest{
		First:  HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: dates[0], Class: "1A", Quota: "GN", Passengers: one},
		Second: HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: dates[1], Class: "1A", Quota: "GN", Passengers: one},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, b := confirmNow(t, s, pair.First), confirmNow(t, s, pair.Second)
	if fullyConfirmed(a) || fullyConfirmed(b) || a.GuaranteeWith != b.ID {
		t.Fatalf("both should be waiting and linked: %s %s, link %q", a.Passengers[0].Status, b.Passengers[0].Status, a.GuaranteeWith)
	}
	// Two parties on day one cancel (the waitlisted party ahead of ours takes two berths);
	// the promotion engine confirms our passenger there.
	for _, c := range first[:2] {
		if _, err := s.Cancel(ctx, c.UserID, c.ID, CancelRequest{RefundTo: "wallet"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []*Booking{a} {
		if _, err := s.Promote(ctx, x.RunID, "1A"); err != nil {
			t.Fatal(err)
		}
	}
	a, _ = s.Get(ctx, a.ID)
	if !fullyConfirmed(a) {
		t.Fatalf("after a cancellation the first ticket should confirm, still %s", a.Passengers[0].Status)
	}
	if err := s.settleGuarantee(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	b, _ = s.Get(ctx, b.ID)
	if b.Status != "CANCELLED" || len(b.Refunds) == 0 || b.Refunds[0].Amount != b.Passengers[0].Fare {
		t.Fatalf("the other ticket: %s, refunds %+v (fare %d)", b.Status, b.Refunds, b.Passengers[0].Fare)
	}
	if err := s.settleGuarantee(ctx, a.ID); err != nil { // idempotent
		t.Fatal(err)
	}
	trialBalanceZero(t, s)

	// When the first choice has berths, a guarantee makes no sense.
	if _, err := s.HoldGuarantee(ctx, users[291], "", GuaranteeRequest{
		First:  HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-29", Class: "3A", Quota: "GN", Passengers: one},
		Second: HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-30", Class: "3A", Quota: "GN", Passengers: one},
	}); err == nil {
		t.Fatal("a guarantee with a confirmed first choice must be refused")
	}
}
