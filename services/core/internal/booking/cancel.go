package booking

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
)

var (
	ErrNotCancellable = errors.New("this booking can't be cancelled")
	ErrNoSuchSeq      = errors.New("no such passenger on this booking")
)

type CancelQuote struct {
	BookingID     string   `json:"booking_id"`
	Passengers    []Refund `json:"passengers"`
	Refund        int      `json:"refund"`
	Deduction     int      `json:"deduction"`
	ChartPrepared bool     `json:"chart_prepared"`
	// PaidByWallet means a refund to source goes to the wallet anyway.
	PaidByWallet bool `json:"paid_by_wallet"`
}

type CancelRequest struct {
	Passengers []int  `json:"passengers"` // seq numbers; empty cancels everyone
	RefundTo   string `json:"refund_to"`  // "source" (original payment) or "wallet"
}

type CancelResult struct {
	Quote      *CancelQuote `json:"quote"`
	Booking    *Booking     `json:"booking"`
	RefundID   string       `json:"refund_id,omitempty"`
	RefundTo   string       `json:"refund_to,omitempty"`
	Promotions []Promotion  `json:"promotions"`
}

// quote prices a cancellation of the given passengers (all live ones if seqs is empty).
func (s *Service) quote(b *Booking, seqs []int) (*CancelQuote, error) {
	if b.Status != "CONFIRMED" {
		return nil, ErrNotCancellable
	}
	q := &CancelQuote{BookingID: b.ID, ChartPrepared: s.chartPrepared(b.RunID)}
	for _, n := range seqs {
		if n < 1 || n > len(b.Passengers) {
			return nil, ErrNoSuchSeq
		}
	}
	now := s.Now()
	for _, p := range b.Passengers {
		if p.Status == "CAN" || (len(seqs) > 0 && !slices.Contains(seqs, p.Seq)) {
			continue
		}
		r := RefundFor(b.Class, b.Quota, p.Status, p.Fare, b.Departure, now, q.ChartPrepared)
		r.Seq = p.Seq
		q.Passengers = append(q.Passengers, r)
		q.Refund += r.Refund
		q.Deduction += r.Deduction
	}
	if len(q.Passengers) == 0 {
		return nil, ErrNotCancellable
	}
	return q, nil
}

func (s *Service) CancelQuote(ctx context.Context, userID, bookingID string, seqs []int) (*CancelQuote, error) {
	b, err := s.GetFor(ctx, userID, bookingID)
	if err != nil {
		return nil, err
	}
	q, err := s.quote(b, seqs)
	if err != nil {
		return nil, err
	}
	q.PaidByWallet = s.paidByWallet(ctx, bookingID)
	return q, nil
}

func (s *Service) paidByWallet(ctx context.Context, bookingID string) bool {
	var method string
	s.DB.QueryRow(ctx, `SELECT coalesce(method, '') FROM payments WHERE booking_id = $1 AND status IN ('CAPTURED', 'REFUNDED')
		ORDER BY updated_at DESC LIMIT 1`, bookingID).Scan(&method)
	return method == "wallet"
}

// Cancel cancels passengers, refunds them by the rules, and promotes whoever is next in line.
func (s *Service) Cancel(ctx context.Context, userID, bookingID string, req CancelRequest) (*CancelResult, error) {
	b, err := s.GetFor(ctx, userID, bookingID)
	if err != nil {
		return nil, err
	}
	if req.RefundTo == "" {
		req.RefundTo = "source"
	}
	if req.RefundTo != "source" && req.RefundTo != "wallet" {
		return nil, invalid("refund_to must be source or wallet")
	}
	res := &CancelResult{}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, b.RunID, b.Class); err != nil {
			return err
		}
		// Re-read under the lock: a concurrent cancellation may have got here first.
		fresh, err := s.getTx(ctx, tx, bookingID)
		if err != nil {
			return err
		}
		q, err := s.quote(fresh, req.Passengers)
		if err != nil {
			return err
		}
		res.Quote = q
		res.RefundID, res.RefundTo, err = s.cancelInTx(ctx, tx, fresh, q, req.RefundTo, "Cancelled by passenger")
		if err != nil {
			return err
		}
		res.Promotions, err = s.promote(ctx, tx, b.RunID, b.Class, q.ChartPrepared)
		return err
	})
	s.invalidate(b.RunID, b.Class)
	if err != nil {
		return nil, err
	}
	if res.Promotions == nil {
		res.Promotions = []Promotion{} // JSON [] rather than null
	}
	res.Booking, err = s.Get(ctx, bookingID)
	return res, err
}

// cancelInTx marks the quoted passengers cancelled, frees their berths, books the
// money movements, and creates the refund. It returns the refund id and where it goes.
func (s *Service) cancelInTx(ctx context.Context, tx pgx.Tx, b *Booking, q *CancelQuote, refundTo, reason string) (string, string, error) {
	now := s.Now()
	fares := 0
	seqs := make([]int, 0, len(q.Passengers))
	for _, r := range q.Passengers {
		fares += r.Fare
		seqs = append(seqs, r.Seq)
		if _, err := tx.Exec(ctx, `UPDATE booking_passengers SET status = 'CAN', cancelled_at = $3, refund = $4
			WHERE booking_id = $1 AND seq = $2`, b.ID, r.Seq, now, r.Refund); err != nil {
			return "", "", err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM seat_allocations a USING booking_passengers p
		WHERE a.passenger_id = p.id AND p.booking_id = $1 AND p.seq = ANY($2)`, b.ID, seqs); err != nil {
		return "", "", err
	}
	var live int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM booking_passengers WHERE booking_id = $1 AND status <> 'CAN'`, b.ID).Scan(&live); err != nil {
		return "", "", err
	}
	if live == 0 {
		if _, err := tx.Exec(ctx, `UPDATE bookings SET status = 'CANCELLED', cancelled_at = $2, updated_at = now() WHERE id = $1`, b.ID, now); err != nil {
			return "", "", err
		}
		if err := s.reversePoints(ctx, tx, b.ID); err != nil {
			return "", "", err
		}
	}
	event := map[string]any{"type": "booking.cancelled", "booking_id": b.ID, "passengers": seqs, "refund": q.Refund, "full": live == 0}
	if b.Source != "user" {
		// Bot passengers paid offline: no money moves through YatraX.
		return "", "", outbox(ctx, tx, "booking.events", b.ID, event)
	}
	if err := ledger.Post(ctx, tx, "cancellation", b.ID, reason,
		ledger.Line{Account: ledger.Fares, Amount: ledger.Paise(fares)},
		ledger.Line{Account: ledger.Refunds, Amount: -ledger.Paise(q.Refund)},
		ledger.Line{Account: ledger.Cancellation, Amount: -ledger.Paise(q.Deduction)},
	); err != nil {
		return "", "", err
	}
	refundID := ""
	if q.Refund > 0 {
		var paymentID *string
		var method string
		tx.QueryRow(ctx, `SELECT id, coalesce(method, '') FROM payments WHERE booking_id = $1 AND status IN ('CAPTURED', 'REFUNDED')
			ORDER BY updated_at DESC LIMIT 1`, b.ID).Scan(&paymentID, &method)
		if method == "wallet" || paymentID == nil {
			refundTo = "wallet"
		}
		var err error
		refundID, err = s.createRefund(ctx, tx, b.ID, b.UserID, paymentID, q.Refund, refundTo, reason)
		if err != nil {
			return "", "", err
		}
		event["refund_id"], event["refund_to"] = refundID, refundTo
	}
	return refundID, refundTo, outbox(ctx, tx, "booking.events", b.ID, event)
}

// createRefund records a refund already booked to liability:refunds. Wallet refunds
// are paid at once; refunds to the original payment are queued for the refund worker.
func (s *Service) createRefund(ctx context.Context, tx pgx.Tx, bookingID, userID string, paymentID *string, amount int, dest, reason string) (string, error) {
	var id string
	status := "PENDING"
	var processed *time.Time
	if dest == "wallet" {
		status = "PROCESSED"
		now := s.Now()
		processed = &now
	}
	if err := tx.QueryRow(ctx, `INSERT INTO refunds (booking_id, payment_id, amount, destination, reason, status, processed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, bookingID, paymentID, amount, dest, reason, status, processed).Scan(&id); err != nil {
		return "", err
	}
	if dest == "wallet" {
		if err := ledger.Post(ctx, tx, "refund", id, "Refund to YatraX Pay wallet",
			ledger.Line{Account: ledger.Refunds, Amount: ledger.Paise(amount)},
			ledger.Line{Account: ledger.Wallet(userID), Amount: -ledger.Paise(amount)},
		); err != nil {
			return "", err
		}
		if err := outbox(ctx, tx, "payment.events", bookingID, map[string]any{
			"type": "refund.processed", "booking_id": bookingID, "refund_id": id, "amount": amount, "destination": "wallet",
		}); err != nil {
			return "", err
		}
	}
	return id, nil
}

// getTx loads a booking inside a transaction (no payment details).
func (s *Service) getTx(ctx context.Context, tx pgx.Tx, id string) (*Booking, error) {
	b, err := scanBooking(tx.QueryRow(ctx, `SELECT `+bookingCols+` FROM bookings WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT seq, name, age, gender, berth_pref, status::text, coalesce(coach_no, ''), coalesce(berth_no, 0),
		coalesce(berth_type, ''), coalesce(rac_no, 0), coalesce(wl_no, 0), fare FROM booking_passengers WHERE booking_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	b.Passengers, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Passenger, error) {
		var p Passenger
		err := r.Scan(&p.Seq, &p.Name, &p.Age, &p.Gender, &p.Pref, &p.Status, &p.Coach, &p.Berth, &p.BerthType, &p.RACNo, &p.WLNo, &p.Fare)
		return p, err
	})
	if err != nil {
		return nil, err
	}
	s.decorate(b)
	return b, nil
}
