package booking

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
)

// SimulateCancellations cancels n confirmed bot (background) passengers' bookings on
// a run, as other travellers would, and promotes the queue. Operations staff use it
// to show the waitlist moving; the live simulation (Phase 5) does this continuously.
func (s *Service) SimulateCancellations(ctx context.Context, train string, originDate time.Time, class string, n int) (int, []Promotion, error) {
	t := s.Net.Trains[train]
	if t == nil || t.Capacity(s.Net, class) == 0 {
		return 0, nil, invalid("Train %s has no %s class", train, class)
	}
	if n < 1 || n > 200 {
		return 0, nil, invalid("Cancel between 1 and 200 bookings")
	}
	runID, err := s.ensureRun(ctx, t, originDate)
	if err != nil {
		return 0, nil, err
	}
	cancelled := 0
	var promos []Promotion
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, runID, class); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT b.id FROM bookings b
			 WHERE b.run_id = $1 AND b.class_code = $2 AND b.status = 'CONFIRMED' AND b.source = 'background'
			   AND EXISTS (SELECT 1 FROM booking_passengers p WHERE p.booking_id = b.id AND p.status = 'CNF')
			 ORDER BY random() LIMIT $3`, runID, class, n)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			b, err := s.getTx(ctx, tx, id)
			if err != nil {
				return err
			}
			q := &CancelQuote{BookingID: id}
			for _, p := range b.Passengers {
				if p.Status != "CAN" {
					q.Passengers = append(q.Passengers, Refund{Seq: p.Seq, Status: p.Status, Fare: p.Fare})
				}
			}
			if _, _, err := s.cancelInTx(ctx, tx, b, q, "source", "Cancelled by passenger"); err != nil {
				return err
			}
			cancelled++
		}
		promos, err = s.promote(ctx, tx, runID, class, s.chartPrepared(runID))
		return err
	})
	s.invalidate(runID, class)
	return cancelled, promos, err
}

type Finance struct {
	GatewayHeld         int64 `json:"gateway_held"`         // paise
	FaresUnearned       int64 `json:"fares_unearned"`       // paise
	RefundsOwed         int64 `json:"refunds_owed"`         // paise
	CancellationRevenue int64 `json:"cancellation_revenue"` // paise
	WalletsTotal        int64 `json:"wallets_total"`        // paise
	TrialBalance        int64 `json:"trial_balance"`        // must be 0
	RefundsPending      int   `json:"refunds_pending"`
	RefundsProcessed    int   `json:"refunds_processed"`
	PaymentsUnresolved  int   `json:"payments_unresolved"`
	TDRsPending         int   `json:"tdrs_pending"`
}

// FinanceSummary is the money position from the ledger, for the admin dashboard.
func (s *Service) FinanceSummary(ctx context.Context) (*Finance, error) {
	f := &Finance{}
	var err error
	if f.GatewayHeld, err = ledger.Balance(ctx, s.DB, ledger.Gateway); err != nil {
		return nil, err
	}
	if f.FaresUnearned, err = ledger.Owed(ctx, s.DB, ledger.Fares); err != nil {
		return nil, err
	}
	if f.RefundsOwed, err = ledger.Owed(ctx, s.DB, ledger.Refunds); err != nil {
		return nil, err
	}
	if f.CancellationRevenue, err = ledger.Owed(ctx, s.DB, ledger.Cancellation); err != nil {
		return nil, err
	}
	if f.TrialBalance, err = ledger.TrialBalance(ctx, s.DB); err != nil {
		return nil, err
	}
	err = s.DB.QueryRow(ctx, `SELECT
		coalesce((SELECT -sum(e.amount) FROM ledger_entries e JOIN ledger_accounts a ON a.id = e.account_id WHERE a.code LIKE 'liability:wallet:%'), 0),
		(SELECT count(*) FROM refunds WHERE status = 'PENDING'),
		(SELECT count(*) FROM refunds WHERE status = 'PROCESSED'),
		(SELECT count(*) FROM payments WHERE status = 'CREATED' AND method IS DISTINCT FROM 'wallet'),
		(SELECT count(*) FROM tdrs WHERE status = 'PENDING')`).
		Scan(&f.WalletsTotal, &f.RefundsPending, &f.RefundsProcessed, &f.PaymentsUnresolved, &f.TDRsPending)
	return f, err
}

// Integrity checks the invariants the platform promises, straight from the database.
type Integrity struct {
	OverlappingAllocations int   `json:"overlapping_allocations"` // berth sold twice: must be 0
	PaidWithoutTicket      int   `json:"paid_without_ticket"`     // captured, no ticket, no refund: must be 0
	TrialBalance           int64 `json:"trial_balance"`           // ledger: must be 0
	ConfirmedPassengers    int   `json:"confirmed_passengers"`
	TatkalBookings         int   `json:"tatkal_bookings"`
}

func (s *Service) Integrity(ctx context.Context) (*Integrity, error) {
	var in Integrity
	err := s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM seat_allocations a JOIN seat_allocations b
		   ON a.run_id = b.run_id AND a.coach_no = b.coach_no AND a.berth_no = b.berth_no AND a.slot = b.slot
		  AND a.id < b.id AND a.seg && b.seg),
		(SELECT count(*) FROM payments p JOIN bookings k ON k.id = p.booking_id
		  WHERE p.status = 'CAPTURED' AND k.confirmed_at IS NULL -- a ticket cancelled later (even for ₹0 after the chart) was still issued
		    AND NOT EXISTS (SELECT 1 FROM refunds r WHERE r.payment_id = p.id)),
		(SELECT coalesce(sum(amount), 0) FROM ledger_entries),
		(SELECT count(*) FROM booking_passengers p JOIN bookings k ON k.id = p.booking_id WHERE k.status = 'CONFIRMED' AND p.status = 'CNF'),
		(SELECT count(*) FROM bookings WHERE quota IN ('TQ', 'PT') AND status IN ('HELD', 'PAYMENT_PENDING', 'CONFIRMED'))`).
		Scan(&in.OverlappingAllocations, &in.PaidWithoutTicket, &in.TrialBalance, &in.ConfirmedPassengers, &in.TatkalBookings)
	return &in, err
}
