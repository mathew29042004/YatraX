package booking

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/paygw"
)

// confirmNow confirms a held booking without a payment (test shortcut).
func confirmNow(t *testing.T, s *Service, b *Booking) *Booking {
	t.Helper()
	ctx := context.Background()
	if err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error { return s.confirm(ctx, tx, b.ID) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// holdAny holds for users[i], or the next user without three unpaid holds left over from
// earlier tests (the expiry sweeper clears those on its own schedule).
func holdAny(t *testing.T, s *Service, users []string, i int, req HoldRequest) (*Booking, error) {
	t.Helper()
	for k := 0; k < len(users); k++ {
		b, err := s.Hold(context.Background(), users[(i+k)%len(users)], "", req)
		if !errors.Is(err, ErrTooManyHolds) {
			return b, err
		}
	}
	return nil, ErrTooManyHolds
}

func trialBalanceZero(t *testing.T, s *Service) {
	t.Helper()
	tb, err := ledger.TrialBalance(context.Background(), s.DB)
	if err != nil || tb != 0 {
		t.Fatalf("ledger trial balance %d (err %v), must be 0", tb, err)
	}
}

func TestPromotionChain(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	tr := s.Net.Trains["12601"] // Mangaluru Mail: sleeper coaches
	from, to := tr.Stops[0].Station, tr.Stops[len(tr.Stops)-1].Station
	date := "2026-11-26"
	cat, _ := s.catalog(tr, "SL")
	gn := cat.Capacity("GN")
	rac := 0
	for _, u := range cat.Units {
		if u.RAC {
			rac++
		}
	}
	book := func(i, n int) *Booking {
		pax := make([]PassengerInput, n)
		for j := range pax {
			pax[j] = PassengerInput{Name: fmt.Sprintf("P%d-%d", i, j), Age: 30, Gender: "M"}
		}
		b, err := holdAny(t, s, users, i, HoldRequest{Train: "12601", From: from, To: to, Date: date, Class: "SL", Quota: "GN", Passengers: pax})
		if err != nil {
			t.Fatalf("booking %d: %v", i, err)
		}
		return confirmNow(t, s, b)
	}
	// Fill General: one single passenger first (we'll cancel them), then parties of 6, then singles.
	first := book(0, 1)
	left := gn - 1
	i := 1
	for ; left >= 6; i++ {
		book(i, 6)
		left -= 6
	}
	for ; left > 0; i++ {
		book(i, 1)
		left--
	}
	racBooking := book(i, 1)
	i++
	if racBooking.Passengers[0].Status != "RAC" {
		t.Fatalf("expected RAC once General is full, got %+v", racBooking.Passengers[0])
	}
	for n := 1; n < rac; n++ {
		book(i, 1)
		i++
	}
	wlBooking := book(i, 1)
	if wlBooking.Passengers[0].Status != "WL" {
		t.Fatalf("expected WL once RAC is full, got %+v", wlBooking.Passengers[0])
	}

	res, err := s.Cancel(ctx, first.UserID, first.ID, CancelRequest{RefundTo: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Booking.Status != "CANCELLED" || res.Quote.Refund == 0 {
		t.Fatalf("cancelled booking %s refund %d", res.Booking.Status, res.Quote.Refund)
	}
	r, _ := s.Get(ctx, racBooking.ID)
	w, _ := s.Get(ctx, wlBooking.ID)
	if r.Passengers[0].Status != "CNF" {
		t.Errorf("RAC 1 should now be confirmed: %+v", r.Passengers[0])
	}
	if w.Passengers[0].Status != "RAC" {
		t.Errorf("WL 1 should now be on RAC: %+v", w.Passengers[0])
	}
	if w.Passengers[0].BookedStatus != "WL 1" {
		t.Errorf("booked status should stay WL 1, got %q", w.Passengers[0].BookedStatus)
	}
	if len(res.Promotions) != 2 {
		t.Errorf("want 2 promotions (RAC→CNF, WL→RAC), got %+v", res.Promotions)
	}
	// Running the engine again changes nothing.
	again, err := s.Promote(ctx, r.RunID, "SL")
	if err != nil || len(again) != 0 {
		t.Errorf("promotion must be idempotent: %v %+v", err, again)
	}
	noOverlaps(t, s)
	trialBalanceZero(t, s)
}

func TestPartialCancellation(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	b, err := s.Hold(ctx, users[60], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-27", Class: "3A", Quota: "GN",
		Passengers: []PassengerInput{{Name: "One", Age: 40, Gender: "M"}, {Name: "Two", Age: 38, Gender: "F"}, {Name: "Three", Age: 12, Gender: "F"}}})
	if err != nil {
		t.Fatal(err)
	}
	b = confirmNow(t, s, b)
	q, err := s.CancelQuote(ctx, b.UserID, b.ID, []int{2})
	if err != nil || len(q.Passengers) != 1 || q.Passengers[0].Deduction != 180 {
		t.Fatalf("quote: %v %+v", err, q)
	}
	res, err := s.Cancel(ctx, b.UserID, b.ID, CancelRequest{Passengers: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	p := res.Booking.Passengers
	if res.Booking.Status != "CONFIRMED" || p[1].Status != "CAN" || p[0].Status != "CNF" || p[2].Status != "CNF" {
		t.Fatalf("after cancelling passenger 2: %s %+v", res.Booking.Status, p)
	}
	if p[1].Refund == nil || *p[1].Refund != p[1].Fare-180 {
		t.Errorf("passenger 2 refund %v, want fare − ₹180", p[1].Refund)
	}
	if _, err := s.Cancel(ctx, b.UserID, b.ID, CancelRequest{Passengers: []int{2}}); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancelling the same passenger twice: %v", err)
	}
	res, err = s.Cancel(ctx, b.UserID, b.ID, CancelRequest{})
	if err != nil || res.Booking.Status != "CANCELLED" {
		t.Fatalf("cancelling the rest: %v %s", err, res.Booking.Status)
	}
	trialBalanceZero(t, s)
}

func TestWalletRefundThenPayWithWallet(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	user := users[61]
	b, _ := s.Hold(ctx, user, "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-28", Class: "2A", Quota: "GN", Passengers: pax("Wallet User")})
	order := pendingPayment(t, s, b)
	if err := webhook(t, s, "evt_wallet_1", order, "captured"); err != nil {
		t.Fatal(err)
	}
	res, err := s.Cancel(ctx, user, b.ID, CancelRequest{RefundTo: "wallet"})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := s.Wallet(ctx, user)
	if w.Balance != ledger.Paise(res.Quote.Refund) || len(w.History) != 1 {
		t.Fatalf("wallet %d paise, want %d; history %+v", w.Balance, ledger.Paise(res.Quote.Refund), w.History)
	}
	// Spend it on a cheaper ticket.
	cheap, _ := s.Hold(ctx, user, "", HoldRequest{Train: "12627", From: "SBC", To: "YNK", Date: "2026-11-29", Class: "SL", Quota: "GN", Passengers: pax("Short Hop")})
	paid, err := s.PayWithWallet(ctx, user, cheap.ID)
	if err != nil || paid.Status != "CONFIRMED" {
		t.Fatalf("pay with wallet: %v %v", err, paid)
	}
	w2, _ := s.Wallet(ctx, user)
	if w2.Balance != w.Balance-ledger.Paise(cheap.TotalFare) {
		t.Errorf("wallet after paying %d, want %d", w2.Balance, w.Balance-ledger.Paise(cheap.TotalFare))
	}
	pricey, _ := s.Hold(ctx, user, "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-30", Class: "2A", Quota: "GN",
		Passengers: []PassengerInput{{Name: "A B", Age: 30, Gender: "M"}, {Name: "C D", Age: 30, Gender: "M"}}})
	if _, err := s.PayWithWallet(ctx, user, pricey.ID); !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("overspending the wallet: %v", err)
	}
	trialBalanceZero(t, s)
}

func TestChartPreparation(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	tr := s.Net.Trains["12601"]
	from, to := tr.Stops[0].Station, tr.Stops[len(tr.Stops)-1].Station
	date := "2026-12-01"
	cat, _ := s.catalog(tr, "SL")
	capacity := cat.Capacity("GN") + len(cat.Units) - cat.Capacity("") // General berths + RAC halves
	var wl *Booking
	var cnf *Booking
	for i := 0; wl == nil; i++ {
		n := 6
		if i*6 > capacity-12 {
			n = 1
		}
		pax := make([]PassengerInput, n)
		for j := range pax {
			pax[j] = PassengerInput{Name: fmt.Sprintf("C%d-%d", i, j), Age: 30, Gender: "F"}
		}
		b, err := holdAny(t, s, users, i, HoldRequest{Train: "12601", From: from, To: to, Date: date, Class: "SL", Quota: "GN", Passengers: pax})
		if err != nil {
			t.Fatal(err)
		}
		b = confirmNow(t, s, b)
		if cnf == nil {
			cnf = b
		}
		if b.Passengers[0].Status == "WL" {
			wl = b
		}
	}
	origin := time.Date(2026, 12, 1, 0, 0, 0, 0, network.IST)
	ch, err := s.PrepareChart(ctx, "12601", origin, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, wl.ID)
	// Unsold Tatkal/Ladies/Senior berths go to the queue at chart time, so the waitlisted
	// passenger is either promoted or, if nothing was left, cancelled with ₹60 kept.
	switch got.Status {
	case "CONFIRMED":
		if got.Passengers[0].Status == "WL" {
			t.Errorf("a waitlisted-only booking must not survive the chart")
		}
	case "CANCELLED":
		if got.Refunds[0].Amount != got.Passengers[0].Fare-RACWLClerkage {
			t.Errorf("auto-cancel refund %d, want fare − 60", got.Refunds[0].Amount)
		}
	default:
		t.Errorf("waitlisted booking after chart: %s", got.Status)
	}
	if ch.Version != 1 || len(ch.Classes) == 0 {
		t.Fatalf("chart: %+v", ch)
	}
	stored, err := s.GetChart(ctx, "12601", origin, 1)
	if err != nil || len(stored.Classes) != len(ch.Classes) {
		t.Fatalf("stored chart: %v", err)
	}
	// After the chart, a confirmed cancellation refunds nothing.
	q, _ := s.CancelQuote(ctx, cnf.UserID, cnf.ID, nil)
	if q.Refund != 0 || !q.ChartPrepared {
		t.Errorf("after chart: refund %d", q.Refund)
	}
	// And TDRs become possible.
	td, err := s.FileTDR(ctx, cnf.UserID, cnf.ID, "train_late", "Waited 4 hours")
	if err != nil {
		t.Fatal(err)
	}
	decided, err := s.DecideTDR(ctx, users[0], td.ID, true, cnf.Passengers[0].Fare/2)
	if err != nil || decided.Status != "APPROVED" {
		t.Fatalf("decide TDR: %v %+v", err, decided)
	}
	if _, err := s.DecideTDR(ctx, users[0], td.ID, false, 0); !errors.Is(err, ErrTDRDecided) {
		t.Errorf("deciding twice: %v", err)
	}
	noOverlaps(t, s)
	trialBalanceZero(t, s)
}

func TestBoardingChangeFreesEarlierSegments(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	berth := gnBerth(t, s, "12623", "SL", "S4", 5)
	b, err := s.Hold(ctx, users[70], "", HoldRequest{Train: "12623", From: "MAS", To: "ERN", Date: "2026-12-02", Class: "SL", Quota: "GN",
		Passengers: []PassengerInput{{Name: "Changes Plans", Age: 30, Gender: "M", Coach: "S4", Berth: berth}}})
	if err != nil {
		t.Fatal(err)
	}
	confirmNow(t, s, b)
	nb, _, err := s.ChangeBoarding(ctx, users[70], b.ID, "SA")
	if err != nil {
		t.Fatal(err)
	}
	if nb.From != "SA" || nb.OriginalFrom != "MAS" || nb.JourneyDate != "2026-12-03" {
		t.Errorf("after change: from %s (was %s), boarding date %s", nb.From, nb.OriginalFrom, nb.JourneyDate)
	}
	if _, err := s.Hold(ctx, users[71], "", HoldRequest{Train: "12623", From: "MAS", To: "SA", Date: "2026-12-02", Class: "SL", Quota: "GN",
		Passengers: []PassengerInput{{Name: "Takes First Leg", Age: 30, Gender: "M", Coach: "S4", Berth: berth}}}); err != nil {
		t.Errorf("MAS→SA should be free on the same berth now: %v", err)
	}
	if _, _, err := s.ChangeBoarding(ctx, users[70], b.ID, "ED"); !errors.Is(err, ErrBoardingChange) {
		t.Errorf("second change must be refused: %v", err)
	}
	noOverlaps(t, s)
}

// No charge without a ticket: a payment the gateway took but never told us about is
// found by reconciliation and refunded through the gateway.
func TestReconcileRefundsAbandonedPayments(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	now := clock
	s.Now = func() time.Time { return now }

	gw := &paygw.Server{DB: s.DB, KeyID: "k", KeySecret: "secret", WebhookSecret: testWebhookSecret, WebhookURL: "http://127.0.0.1:1/unused"}
	if err := gw.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gw.Router())
	defer srv.Close()
	p := &Payments{Gateway: &paygw.Client{BaseURL: srv.URL, PublicURL: srv.URL, KeyID: "k", KeySecret: "secret"}, WebhookSecret: testWebhookSecret, AppURL: "http://app"}

	b, err := s.Hold(ctx, users[80], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-12-04", Class: "3A", Quota: "GN", Passengers: pax("Lost Redirect")})
	if err != nil {
		t.Fatal(err)
	}
	url, err := s.StartPayment(ctx, p, users[80], b.ID)
	if err != nil {
		t.Fatal(err)
	}
	gw.SetMode(paygw.ModeTimeout) // money taken; no redirect, no webhook
	res, err := srv.Client().PostForm(url+"/pay", map[string][]string{"method": {"upi"}, "outcome": {"success"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	now = now.Add(30 * time.Minute) // the hold is long gone
	rep, err := s.Reconcile(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Captured != 1 || rep.RefundsProcessed != 1 {
		t.Fatalf("reconcile report %+v: want the capture found and refunded", rep)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Status != "FAILED" || got.Payment.Status != "REFUNDED" || len(got.Refunds) != 1 || got.Refunds[0].Status != "PROCESSED" {
		t.Fatalf("booking %s payment %s refunds %+v", got.Status, got.Payment.Status, got.Refunds)
	}
	// A second run finds nothing new for this payment.
	rep2, _ := s.Reconcile(ctx, p)
	if rep2.Captured != 0 || rep2.RefundsProcessed != 0 {
		t.Errorf("second reconcile should change nothing: %+v", rep2)
	}
	// This booking's money is back where it started: captured then refunded.
	var net int64
	s.DB.QueryRow(ctx, `SELECT coalesce(sum(e.amount), 0) FROM ledger_entries e JOIN ledger_accounts a ON a.id = e.account_id
		JOIN ledger_txns t ON t.id = e.txn_id WHERE a.code = 'asset:gateway' AND (t.ref = $1 OR t.ref = $2)`, b.ID, got.Refunds[0].ID).Scan(&net)
	if net != 0 {
		t.Errorf("gateway balance for this booking is %d paise; the refund should cancel the capture", net)
	}
	trialBalanceZero(t, s)
}

// Opening a run for booking must not contradict what search showed: if search said
// "WL n", the passenger ends up at about WL n, not confirmed and not Regret.
func TestBookingMatchesWhatSearchShowed(t *testing.T) {
	s, users := setup(t)
	ctx := context.Background()
	prov := Provider{S: s}
	tested := 0
	for _, num := range []string{"12310", "12394", "12304", "12566", "12302"} {
		tr := s.Net.Trains[num]
		from, to := tr.StopIndex("NDLS"), tr.StopIndex("PNBE")
		if from < 0 || to < 0 || from > to {
			continue
		}
		for d := 1; d <= 16 && tested < 3; d++ {
			for _, class := range []string{"SL", "3A", "2A"} {
				if tr.Capacity(s.Net, class) == 0 {
					continue
				}
				origin := time.Date(2026, 11, d, 0, 0, 0, 0, network.IST)
				if _, opened := s.runID(num, origin); opened {
					continue
				}
				req := availability.Request{Train: tr, OriginDate: origin, Class: class, Quota: "GN", FromIdx: from, ToIdx: to}
				shown := prov.Status(ctx, req, clock)
				if shown.Code != availability.WL && shown.Code != availability.RAC {
					continue
				}
				board := origin.AddDate(0, 0, tr.Stops[from].Time()/1440)
				b, err := s.Hold(ctx, users[90+tested], "", HoldRequest{Train: num, From: "NDLS", To: "PNBE", Date: board.Format(time.DateOnly),
					Class: class, Quota: "GN", Passengers: pax("Saw The Search")})
				if err != nil {
					t.Fatalf("%s %s on %d Nov: search showed %s but booking failed: %v", num, class, d, shown.Label, err)
				}
				p := b.Passengers[0]
				got := p.WLNo
				if p.Status == "RAC" {
					got = p.RACNo
				}
				if p.Status != string(shown.Code) || got < shown.Count || got > shown.Count+3 {
					t.Errorf("%s %s on %d Nov: search showed %s; booking got %s %d", num, class, d, shown.Label, p.Status, got)
				}
				t.Logf("%s %s on %d Nov: search showed %s, booking got %s %d", num, class, d, shown.Label, p.Status, got)
				tested++
				break
			}
		}
	}
	if tested == 0 {
		t.Skip("no unopened waitlisted run found in the test window")
	}
}
