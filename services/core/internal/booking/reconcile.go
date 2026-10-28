package booking

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
)

// StalePaymentAge: a payment still unresolved this long after it started is checked
// with the gateway, in case the redirect and the webhook were both lost.
const StalePaymentAge = 2 * time.Minute

type ReconcileReport struct {
	Checked          int `json:"checked"`
	Captured         int `json:"captured"`
	Failed           int `json:"failed"`
	Abandoned        int `json:"abandoned"`
	RefundsProcessed int `json:"refunds_processed"`
	RefundsFailed    int `json:"refunds_failed"`
}

// Reconcile settles payments the gateway knows about but we don't, then pays out
// queued refunds. It is safe to run any number of times.
func (s *Service) Reconcile(ctx context.Context, p *Payments) (ReconcileReport, error) {
	return s.ReconcileWith(ctx, p, StalePaymentAge)
}

// ReconcileWith treats payments older than staleAfter as unresolved.
func (s *Service) ReconcileWith(ctx context.Context, p *Payments, staleAfter time.Duration) (ReconcileReport, error) {
	var rep ReconcileReport
	if err := s.reconcilePayments(ctx, p, staleAfter, &rep); err != nil {
		return rep, err
	}
	err := s.processRefunds(ctx, p, &rep)
	return rep, err
}

func (s *Service) reconcilePayments(ctx context.Context, p *Payments, staleAfter time.Duration, rep *ReconcileReport) error {
	rows, err := s.DB.Query(ctx, `
		SELECT p.order_id, b.status::text FROM payments p JOIN bookings b ON b.id = p.booking_id
		 WHERE p.status = 'CREATED' AND p.method IS DISTINCT FROM 'wallet' AND p.created_at < $1
		 ORDER BY p.created_at LIMIT 500`, s.Now().Add(-staleAfter))
	if err != nil {
		return err
	}
	type stale struct{ order, bookingStatus string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (stale, error) {
		var x stale
		return x, r.Scan(&x.order, &x.bookingStatus)
	})
	if err != nil {
		return err
	}
	for _, st := range list {
		rep.Checked++
		o, err := p.Gateway.GetOrder(ctx, st.order)
		if err != nil {
			slog.Warn("reconcile: gateway lookup failed", "order", st.order, "err", err)
			continue
		}
		settled := false
		for _, gp := range o.Payments {
			if gp.Status != "captured" {
				continue
			}
			if err := s.settle(ctx, "reconcile", st.order, gp.ID, gp.Method, true, "", nil, nil); err != nil {
				slog.Warn("reconcile: settle", "order", st.order, "err", err)
			} else {
				rep.Captured++
			}
			settled = true
			break
		}
		if settled {
			continue
		}
		if n := len(o.Payments); n > 0 && o.Payments[n-1].Status == "failed" {
			last := o.Payments[n-1]
			if err := s.settle(ctx, "reconcile", st.order, last.ID, last.Method, false, last.Error, nil, nil); err == nil {
				rep.Failed++
			}
			continue
		}
		// Nobody paid and the hold is gone: the customer walked away.
		if st.bookingStatus != "HELD" && st.bookingStatus != "PAYMENT_PENDING" {
			if _, err := s.DB.Exec(ctx, `UPDATE payments SET status = 'FAILED', failure_reason = 'Abandoned at checkout', updated_at = now()
				WHERE order_id = $1 AND status = 'CREATED'`, st.order); err == nil {
				rep.Abandoned++
			}
		}
	}
	return nil
}

// processRefunds pays queued refunds back to the original payment method.
func (s *Service) processRefunds(ctx context.Context, p *Payments, rep *ReconcileReport) error {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.booking_id, r.amount, coalesce(pay.gateway_payment_id, ''), coalesce(pay.id::text, '')
		  FROM refunds r LEFT JOIN payments pay ON pay.id = r.payment_id
		 WHERE r.status = 'PENDING' AND r.destination = 'source' AND r.attempts < 10
		 ORDER BY r.created_at LIMIT 100`)
	if err != nil {
		return err
	}
	type due struct {
		id, booking string
		amount      int
		gwPayment   string
		payment     string
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.id, &d.booking, &d.amount, &d.gwPayment, &d.payment)
	})
	if err != nil {
		return err
	}
	for _, d := range list {
		if d.gwPayment == "" {
			s.DB.Exec(ctx, `UPDATE refunds SET attempts = attempts + 1, last_error = 'no gateway payment to refund' WHERE id = $1`, d.id)
			rep.RefundsFailed++
			continue
		}
		gr, err := p.Gateway.Refund(ctx, d.gwPayment, d.amount*100, d.id)
		if err != nil {
			s.DB.Exec(ctx, `UPDATE refunds SET attempts = attempts + 1, last_error = $2 WHERE id = $1`, d.id, err.Error())
			metrics.Refunds.WithLabelValues("error").Inc()
			rep.RefundsFailed++
			continue
		}
		err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE refunds SET status = 'PROCESSED', gateway_refund_id = $2, processed_at = $3, attempts = attempts + 1
				WHERE id = $1 AND status = 'PENDING'`, d.id, gr.ID, s.Now())
			if err != nil || tag.RowsAffected() == 0 {
				return err // already processed by another worker
			}
			if err := ledger.Post(ctx, tx, "refund", d.id, "Refund to original payment method",
				ledger.Line{Account: ledger.Refunds, Amount: ledger.Paise(d.amount)},
				ledger.Line{Account: ledger.Gateway, Amount: -ledger.Paise(d.amount)},
			); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payments SET refunded_amount = refunded_amount + $2,
				status = CASE WHEN status = 'REFUND_PENDING' THEN 'REFUNDED'::payment_status ELSE status END, updated_at = now()
				WHERE id = $1`, d.payment, d.amount); err != nil {
				return err
			}
			return outbox(ctx, tx, "payment.events", d.booking, map[string]any{
				"type": "refund.processed", "booking_id": d.booking, "refund_id": d.id, "amount": d.amount, "destination": "source",
			})
		})
		if err != nil {
			slog.Error("refund bookkeeping failed after the gateway paid it; will retry", "refund", d.id, "err", err)
			continue
		}
		metrics.Refunds.WithLabelValues("processed").Inc()
		rep.RefundsProcessed++
	}
	return nil
}

// RunReconciler reconciles on a timer.
func (s *Service) RunReconciler(ctx context.Context, p *Payments, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		rep, err := s.Reconcile(ctx, p)
		if err != nil {
			slog.Warn("reconcile", "err", err)
		} else if rep != (ReconcileReport{}) {
			slog.Info("reconciled", "report", rep)
		}
	}
}
