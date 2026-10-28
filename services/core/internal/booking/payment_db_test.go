package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yatrax/yatrax/services/core/internal/paygw"
	"github.com/yatrax/yatrax/services/core/internal/ticket"
)

const testWebhookSecret = "test-webhook-secret"

// pendingPayment attaches a gateway order to a held booking without calling a gateway.
func pendingPayment(t *testing.T, s *Service, b *Booking) string {
	t.Helper()
	order := fmt.Sprintf("order_test_%s", b.ID[:8])
	if _, err := s.DB.Exec(context.Background(),
		`INSERT INTO payments (booking_id, order_id, amount, status) VALUES ($1, $2, $3, 'CREATED')`, b.ID, order, b.TotalFare); err != nil {
		t.Fatal(err)
	}
	s.DB.Exec(context.Background(), `UPDATE bookings SET status = 'PAYMENT_PENDING' WHERE id = $1`, b.ID)
	return order
}

func webhook(t *testing.T, s *Service, eventID, order, status string) error {
	t.Helper()
	body, _ := json.Marshal(paygw.Event{ID: eventID, Type: "payment." + status,
		Payment: paygw.Payment{ID: "pay_" + eventID, OrderID: order, Method: "upi", Status: status}})
	return s.HandleWebhook(context.Background(), &Payments{WebhookSecret: testWebhookSecret}, body, paygw.SignWebhook(testWebhookSecret, body))
}

func paymentStatus(t *testing.T, s *Service, order string) string {
	var st string
	s.DB.QueryRow(context.Background(), `SELECT status::text FROM payments WHERE order_id = $1`, order).Scan(&st)
	return st
}

func TestWebhookConfirmsOnceEvenWhenRepeated(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	b, err := s.Hold(ctx, users[40], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-19", Class: "3A", Quota: "GN", Passengers: pax("Webhook Twice")})
	if err != nil {
		t.Fatal(err)
	}
	order := pendingPayment(t, s, b)
	for range 3 { // the same event delivered three times
		if err := webhook(t, s, "evt_dup_1", order, "captured"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Status != "CONFIRMED" || len(got.PNR) != 10 {
		t.Fatalf("status %s pnr %q", got.Status, got.PNR)
	}
	var events int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM payment_events WHERE order_id = $1`, order).Scan(&events)
	if events != 1 || paymentStatus(t, s, order) != "CAPTURED" {
		t.Errorf("events=%d payment=%s", events, paymentStatus(t, s, order))
	}
}

func TestBadSignatureIsRejected(t *testing.T) {
	s, _ := setup(t)
	body := []byte(`{"id":"evt_forged","event":"payment.captured","payment":{"order_id":"x","status":"captured"}}`)
	if err := s.HandleWebhook(context.Background(), &Payments{WebhookSecret: testWebhookSecret}, body, "deadbeef"); err != ErrBadSignature {
		t.Fatalf("forged webhook: %v", err)
	}
}

func TestFailedPaymentLetsTheCustomerRetry(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	b, _ := s.Hold(ctx, users[41], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-19", Class: "SL", Quota: "GN", Passengers: pax("Card Declined")})
	order := pendingPayment(t, s, b)
	if err := webhook(t, s, "evt_fail_1", order, "failed"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Status != "HELD" || paymentStatus(t, s, order) != "FAILED" {
		t.Fatalf("after a failed payment: booking %s, payment %s", got.Status, paymentStatus(t, s, order))
	}
	if got.Passengers[0].Coach == "" {
		t.Error("the berth should still be held for a retry")
	}
}

func TestLatePaymentGetsANewBerth(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	b, _ := s.Hold(ctx, users[42], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-20", Class: "3A", Quota: "GN", Passengers: pax("Slow Bank")})
	order := pendingPayment(t, s, b)
	s.ExpireNow(ctx, b.ID)
	if err := webhook(t, s, "evt_late_1", order, "captured"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Status != "CONFIRMED" || got.Passengers[0].Status != "CNF" {
		t.Fatalf("late payment with berths free should still confirm: %s %+v", got.Status, got.Passengers[0])
	}
}

// No charge without a ticket: money that arrives when nothing can be booked is marked for refund.
func TestLatePaymentWithNoRoomIsRefunded(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	req := HoldRequest{Train: "12007", From: "MAS", To: "MYS", Date: "2026-11-23", Class: "EC", Quota: "GN", OnlyConfirmed: true,
		Passengers: []PassengerInput{{Name: "Only If Confirmed", Age: 44, Gender: "M"}}}
	b, err := s.Hold(ctx, users[43], "", req)
	if err != nil {
		t.Fatal(err)
	}
	order := pendingPayment(t, s, b)
	s.ExpireNow(ctx, b.ID)
	// While the hold has lapsed, the rest of the EC General quota sells out.
	cat, _ := s.catalog(s.Net.Trains["12007"], "EC")
	for i := 0; ; i++ {
		_, err := s.Hold(ctx, users[50+i%200], "", HoldRequest{Train: "12007", From: "MAS", To: "MYS", Date: "2026-11-23", Class: "EC", Quota: "GN",
			OnlyConfirmed: true, Passengers: pax(fmt.Sprint("Filler ", i))})
		if err == ErrNotConfirmed || i > cat.Capacity("GN")+5 {
			break
		}
		if err == ErrTooManyHolds {
			continue
		}
	}
	if err := webhook(t, s, "evt_late_full", order, "captured"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Status != "FAILED" || paymentStatus(t, s, order) != "REFUND_PENDING" {
		t.Fatalf("booking %s payment %s; want FAILED + REFUND_PENDING", got.Status, paymentStatus(t, s, order))
	}
	var outboxed int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE key = $1 AND payload->>'type' = 'payment.refund_required'`, b.ID).Scan(&outboxed)
	if outboxed != 1 {
		t.Error("a refund_required event should be queued for the refund worker")
	}
}

func TestTicketRoundTrip(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()
	signer, err := ticket.Load(ctx, s.DB)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Hold(ctx, users[44], "", HoldRequest{Train: "12627", From: "SBC", To: "NDLS", Date: "2026-11-24", Class: "2A", Quota: "GN", Passengers: pax("Qr Code")})
	if _, err := s.Ticket(b, signer); err != ErrNotFound {
		t.Errorf("an unpaid booking must not get a ticket: %v", err)
	}
	order := pendingPayment(t, s, b)
	if err := webhook(t, s, "evt_qr_1", order, "captured"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, b.ID)
	tok, err := s.Ticket(got, signer)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ticket.Verify(signer.Pub, tok)
	if err != nil || p.PNR != got.PNR || p.Pax[0][2] != "CNF" {
		t.Fatalf("ticket round trip: %v %+v", err, p)
	}
}
