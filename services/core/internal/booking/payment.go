package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/inventory"
	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
	"github.com/yatrax/yatrax/services/core/internal/paygw"
	"github.com/yatrax/yatrax/services/core/internal/ticket"
)

var (
	ErrBadSignature = errors.New("payment signature does not match")
	ErrNoPayment    = errors.New("no payment found for this booking")
)

// Payments configures the saga's connection to the gateway.
type Payments struct {
	Gateway       *paygw.Client
	WebhookSecret string
	AppURL        string // passenger app, for the redirect back after paying
	Signer        *ticket.Signer
}

// StartPayment creates (or reuses) a gateway order for a held booking and returns
// the checkout page URL.
func (s *Service) StartPayment(ctx context.Context, p *Payments, userID, bookingID string) (string, error) {
	b, err := s.GetFor(ctx, userID, bookingID)
	if err != nil {
		return "", err
	}
	if (b.Status != "HELD" && b.Status != "PAYMENT_PENDING") || b.HoldExpiresAt == nil || !b.HoldExpiresAt.After(s.Now()) {
		return "", ErrNotHeld
	}
	var orderID string
	err = s.DB.QueryRow(ctx, `SELECT order_id FROM payments WHERE booking_id = $1 AND status = 'CREATED' ORDER BY created_at DESC LIMIT 1`,
		bookingID).Scan(&orderID)
	if err == nil {
		return p.Gateway.CheckoutURL(orderID), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	o, err := p.Gateway.CreateOrder(ctx, b.TotalFare*100, b.ID, p.AppURL+"/booking/"+b.ID)
	if err != nil {
		return "", err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO payments (booking_id, order_id, amount, status, created_at, updated_at) VALUES ($1, $2, $3, 'CREATED', $4, $4)`,
			b.ID, o.ID, b.TotalFare, s.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE bookings SET status = 'PAYMENT_PENDING', updated_at = now() WHERE id = $1 AND status = 'HELD'`, b.ID)
		return err
	})
	if err != nil {
		return "", err
	}
	return p.Gateway.CheckoutURL(o.ID), nil
}

// HandleWebhook applies a signed gateway event. Redelivered events are no-ops.
func (s *Service) HandleWebhook(ctx context.Context, p *Payments, body []byte, sig string) error {
	if !paygw.VerifyWebhook(p.WebhookSecret, body, sig) {
		return ErrBadSignature
	}
	var ev paygw.Event
	if err := json.Unmarshal(body, &ev); err != nil {
		return fmt.Errorf("bad webhook body: %w", err)
	}
	return s.settle(ctx, "webhook", ev.Payment.OrderID, ev.Payment.ID, ev.Payment.Method, ev.Payment.Status == "captured", ev.Payment.Error, &ev, body)
}

// VerifyReturn handles the customer's redirect back from checkout. The redirect is
// only a hint: we check its signature, then ask the gateway what really happened.
func (s *Service) VerifyReturn(ctx context.Context, p *Payments, userID, bookingID, orderID, paymentID, signature string) (*Booking, error) {
	if _, err := s.GetFor(ctx, userID, bookingID); err != nil {
		return nil, err
	}
	var owner string
	if err := s.DB.QueryRow(ctx, `SELECT booking_id FROM payments WHERE order_id = $1`, orderID).Scan(&owner); err != nil || owner != bookingID {
		return nil, ErrNoPayment
	}
	if signature != "" && !paygw.VerifyCallback(p.Gateway.KeySecret, orderID, paymentID, signature) {
		return nil, ErrBadSignature
	}
	o, err := p.Gateway.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	for _, gp := range o.Payments {
		if gp.ID == paymentID {
			if err := s.settle(ctx, "redirect", orderID, gp.ID, gp.Method, gp.Status == "captured", gp.Error, nil, nil); err != nil {
				return nil, err
			}
			break
		}
	}
	return s.Get(ctx, bookingID)
}

// settle records a payment outcome and moves the booking on. It is safe to call
// any number of times for the same payment, from the redirect, the webhook and the
// reconciliation job alike. source says which of those found the result.
func (s *Service) settle(ctx context.Context, source, orderID, gatewayPaymentID, method string, captured bool, reason string,
	ev *paygw.Event, rawEvent []byte) error {
	var bookingID, class, userID string
	var runID int64
	var amount int
	var holdUntil *time.Time
	err := s.DB.QueryRow(ctx, `SELECT b.id, b.run_id, b.class_code, b.user_id, p.amount, b.hold_expires_at
		FROM payments p JOIN bookings b ON b.id = p.booking_id WHERE p.order_id = $1`,
		orderID).Scan(&bookingID, &runID, &class, &userID, &amount, &holdUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoPayment
	} else if err != nil {
		return err
	}
	var outcome string
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if ev != nil {
			tag, err := tx.Exec(ctx, `INSERT INTO payment_events (event_id, order_id, type, payload) VALUES ($1, $2, $3, $4)
				ON CONFLICT (event_id) DO NOTHING`, ev.ID, orderID, ev.Type, rawEvent)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				outcome = "duplicate_event"
				return nil
			}
		}
		if err := lock(ctx, tx, runID, class); err != nil {
			return err
		}
		if _, err := s.expireStale(ctx, tx, runID, class); err != nil {
			return err
		}
		var payStatus, bookStatus string
		if err := tx.QueryRow(ctx, `SELECT p.status::text, b.status::text FROM payments p JOIN bookings b ON b.id = p.booking_id
			WHERE p.order_id = $1 FOR UPDATE OF p, b`, orderID).Scan(&payStatus, &bookStatus); err != nil {
			return err
		}
		if payStatus != "CREATED" && payStatus != "FAILED" {
			outcome = "already_settled"
			return nil
		}
		if !captured {
			outcome = "failed"
			if _, err := tx.Exec(ctx, `UPDATE payments SET status = 'FAILED', gateway_payment_id = $2, method = $3, failure_reason = $4,
				updated_at = now() WHERE order_id = $1`, orderID, gatewayPaymentID, method, reason); err != nil {
				return err
			}
			// Let the customer try again while the hold lasts.
			if _, err := tx.Exec(ctx, `UPDATE bookings SET status = 'HELD', updated_at = now() WHERE id = $1 AND status = 'PAYMENT_PENDING'`,
				bookingID); err != nil {
				return err
			}
			return outbox(ctx, tx, "payment.events", bookingID, map[string]any{"type": "payment.failed", "booking_id": bookingID, "order_id": orderID})
		}

		if _, err := tx.Exec(ctx, `UPDATE payments SET status = 'CAPTURED', gateway_payment_id = $2, method = $3, updated_at = now()
			WHERE order_id = $1`, orderID, gatewayPaymentID, method); err != nil {
			return err
		}
		if err := ledger.Post(ctx, tx, "capture", bookingID, "Payment captured by the gateway",
			ledger.Line{Account: ledger.Gateway, Amount: ledger.Paise(amount)},
			ledger.Line{Account: ledger.Fares, Amount: -ledger.Paise(amount)},
		); err != nil {
			return err
		}
		switch bookStatus {
		case "HELD", "PAYMENT_PENDING":
			outcome = "confirmed"
			return s.confirm(ctx, tx, bookingID)
		case "EXPIRED":
			// Paid after the hold ran out. Shortly after, find berths again; much later,
			// or found only by reconciliation (the customer gave up), refund instead of
			// surprising them with a ticket they think failed.
			if source != "reconcile" && holdUntil != nil && s.Now().Sub(*holdUntil) <= LateGrace {
				ok, err := s.reallocate(ctx, tx, runID, bookingID)
				if err != nil {
					return err
				}
				if ok {
					outcome = "confirmed_late"
					return s.confirm(ctx, tx, bookingID)
				}
			}
			outcome = "refund_late"
			if _, err := tx.Exec(ctx, `UPDATE bookings SET status = 'FAILED', updated_at = now() WHERE id = $1`, bookingID); err != nil {
				return err
			}
			return s.markRefund(ctx, tx, bookingID, orderID, userID, amount, "Payment arrived after the berths were released")
		default:
			// Already confirmed with another payment, or failed: never keep money without a ticket.
			outcome = "refund_duplicate"
			return s.markRefund(ctx, tx, bookingID, orderID, userID, amount, "Duplicate payment: booking was already "+strings.ToLower(bookStatus))
		}
	})
	s.invalidate(runID, class)
	if err == nil {
		metrics.Payments.WithLabelValues(outcome).Inc()
		slog.Info("payment settled", "order", orderID, "booking", bookingID, "outcome", outcome)
	}
	return err
}

// markRefund refunds a whole captured payment that bought no ticket. The refund
// worker pays it back to the original payment method.
func (s *Service) markRefund(ctx context.Context, tx pgx.Tx, bookingID, orderID, userID string, amount int, why string) error {
	var paymentID string
	if err := tx.QueryRow(ctx, `UPDATE payments SET status = 'REFUND_PENDING', failure_reason = $2, updated_at = now() WHERE order_id = $1
		RETURNING id`, orderID, why).Scan(&paymentID); err != nil {
		return err
	}
	if err := ledger.Post(ctx, tx, "refund_due", bookingID, why,
		ledger.Line{Account: ledger.Fares, Amount: ledger.Paise(amount)},
		ledger.Line{Account: ledger.Refunds, Amount: -ledger.Paise(amount)},
	); err != nil {
		return err
	}
	refundID, err := s.createRefund(ctx, tx, bookingID, userID, &paymentID, amount, "source", why)
	if err != nil {
		return err
	}
	return outbox(ctx, tx, "payment.events", bookingID, map[string]any{
		"type": "payment.refund_required", "booking_id": bookingID, "order_id": orderID, "refund_id": refundID, "amount": amount, "reason": why,
	})
}

func (s *Service) confirm(ctx context.Context, tx pgx.Tx, bookingID string) error {
	pnrs, err := nextPNRs(ctx, tx, 1)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE bookings SET status = 'CONFIRMED', pnr = $2, confirmed_at = $3, hold_expires_at = NULL, updated_at = now()
		WHERE id = $1`, bookingID, pnrs[0], s.Now()); err != nil {
		return err
	}
	if err := setBookedStatus(ctx, tx, bookingID); err != nil {
		return err
	}
	var premium int
	if err := tx.QueryRow(ctx, `SELECT insurance_premium FROM bookings WHERE id = $1`, bookingID).Scan(&premium); err != nil {
		return err
	}
	if premium > 0 {
		// The policy starts with the ticket: the premium is the insurer's, not a fare to refund.
		if err := ledger.Post(ctx, tx, "insurance", bookingID, "Travel insurance premium",
			ledger.Line{Account: ledger.Fares, Amount: ledger.Paise(premium)},
			ledger.Line{Account: ledger.Insurer, Amount: -ledger.Paise(premium)},
		); err != nil {
			return err
		}
	}
	if err := s.earnPoints(ctx, tx, bookingID); err != nil {
		return err
	}
	return outbox(ctx, tx, "booking.events", bookingID, map[string]any{"type": "booking.confirmed", "booking_id": bookingID, "pnr": pnrs[0]})
}

// reallocate re-runs allocation for a booking whose hold expired before payment
// arrived. Seat-map choices are dropped; preferences still apply.
func (s *Service) reallocate(ctx context.Context, tx pgx.Tx, runID int64, bookingID string) (bool, error) {
	var trainNo, class, quota string
	var from, to int
	var prefsRaw []byte
	if err := tx.QueryRow(ctx, `SELECT train_number, class_code, quota, from_idx, to_idx, preferences FROM bookings WHERE id = $1`, bookingID).
		Scan(&trainNo, &class, &quota, &from, &to, &prefsRaw); err != nil {
		return false, err
	}
	var prefs struct {
		Together      bool `json:"together"`
		OnlyConfirmed bool `json:"only_confirmed"`
	}
	json.Unmarshal(prefsRaw, &prefs)
	t := s.Net.Trains[trainNo]
	cat, err := s.catalog(s.runTrain(runID, t), class)
	if err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT id, age, gender, berth_pref FROM booking_passengers WHERE booking_id = $1 ORDER BY seq`, bookingID)
	if err != nil {
		return false, err
	}
	type row struct {
		id           string
		age          int
		gender, pref string
	}
	pax, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.id, &x.age, &x.gender, &x.pref)
	})
	if err != nil {
		return false, err
	}
	req := inventory.Request{Quota: quota, From: from, To: to, Together: prefs.Together}
	for _, p := range pax {
		req.Pax = append(req.Pax, inventory.Passenger{Age: p.age, Gender: p.gender, Pref: p.pref})
	}
	occ, queue, err := loadOccupancy(ctx, tx, runID, class)
	if err != nil {
		return false, err
	}
	placements, err := cat.Allocate(occ, req)
	if err != nil {
		return false, err
	}
	nRAC, nWL := 0, 0
	for _, pl := range placements {
		switch pl.Status {
		case inventory.RAC:
			nRAC++
		case inventory.WL:
			nWL++
		}
	}
	if prefs.OnlyConfirmed && nRAC+nWL > 0 {
		return false, nil
	}
	if nWL > 0 && queue[quota].wl+nWL > availability.WaitlistLimit(quota, float64(cat.Capacity(quota))) {
		return false, nil
	}
	racNo, wlNo := 0, 0
	if nRAC > 0 {
		if racNo, err = nextNumbers(ctx, tx, runID, class, quota, "RAC", nRAC); err != nil {
			return false, err
		}
	}
	if nWL > 0 {
		if wlNo, err = nextNumbers(ctx, tx, runID, class, quota, "WL", nWL); err != nil {
			return false, err
		}
	}
	for i, pl := range placements {
		var coach, btype *string
		var berth, rac, wl *int
		switch pl.Status {
		case inventory.RAC:
			n := racNo
			racNo++
			rac = &n
		case inventory.WL:
			n := wlNo
			wlNo++
			wl = &n
		}
		if pl.Unit != nil {
			coach, berth, btype = &pl.Unit.Coach, &pl.Unit.Berth, &pl.Unit.Type
		}
		if _, err := tx.Exec(ctx, `UPDATE booking_passengers SET status = $2, coach_no = $3, berth_no = $4, berth_type = $5, rac_no = $6, wl_no = $7
			WHERE id = $1`, pax[i].id, pl.Status, coach, berth, btype, rac, wl); err != nil {
			return false, err
		}
		if pl.Unit != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO seat_allocations (run_id, class_code, coach_no, berth_no, slot, seg, booking_id, passenger_id)
				VALUES ($1,$2,$3,$4,$5,int4range($6,$7),$8,$9)`, runID, class, pl.Unit.Coach, pl.Unit.Berth, pl.Unit.Slot, from, to, bookingID, pax[i].id); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// Ticket returns the signed QR token for a confirmed booking.
func (s *Service) Ticket(b *Booking, signer *ticket.Signer) (string, error) {
	if b.Status != "CONFIRMED" || b.PNR == "" {
		return "", ErrNotFound
	}
	p := ticket.Payload{PNR: b.PNR, Train: b.Train, Date: b.JourneyDate, From: b.From, To: b.To, Class: b.Class, Quota: b.Quota, Issued: time.Now().Unix()}
	if b.ConfirmedAt != nil {
		p.Issued = b.ConfirmedAt.Unix()
	}
	for _, x := range b.Passengers {
		place := ""
		switch x.Status {
		case "CNF":
			place = fmt.Sprintf("%s/%d", x.Coach, x.Berth)
		case "RAC":
			place = fmt.Sprintf("RAC %d %s/%d", x.RACNo, x.Coach, x.Berth)
		default:
			place = fmt.Sprintf("WL %d", x.WLNo)
		}
		p.Pax = append(p.Pax, []string{x.Name, fmt.Sprint(x.Age, x.Gender), x.Status, place})
	}
	return signer.Sign(p)
}

// setBookedStatus freezes what each passenger's ticket said when it was issued.
func setBookedStatus(ctx context.Context, tx pgx.Tx, bookingIDs ...string) error {
	_, err := tx.Exec(ctx, `UPDATE booking_passengers SET booked_status = CASE status::text
			WHEN 'CNF' THEN 'CNF ' || coach_no || '/' || berth_no
			WHEN 'RAC' THEN 'RAC ' || rac_no || ' ' || coach_no || '/' || berth_no
			ELSE 'WL ' || wl_no END
		WHERE booking_id = ANY($1::uuid[]) AND booked_status IS NULL`, bookingIDs)
	return err
}
