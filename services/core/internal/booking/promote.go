package booking

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/inventory"
)

// Promotion is one passenger moving up: RAC → CNF, or WL → RAC / CNF.
type Promotion struct {
	BookingID string `json:"booking_id"`
	UserID    string `json:"-"`
	Seq       int    `json:"seq"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// statusLabel renders a passenger's place the way a ticket shows it.
func statusLabel(status, coach string, berth, rac, wl int) string {
	switch status {
	case "CNF":
		return fmt.Sprintf("CNF %s/%d", coach, berth)
	case "RAC":
		return fmt.Sprintf("RAC %d %s/%d", rac, coach, berth)
	case "WL":
		return fmt.Sprintf("WL %d", wl)
	}
	return status
}

// promote moves queued passengers onto freed places, in order: every RAC passenger
// is offered a confirmed berth first (lowest RAC number first), then every
// waitlisted passenger a confirmed berth or an RAC half-berth (lowest WL number first).
//
// It works from the current state rather than from what just changed, so it is
// idempotent: running it again finds nothing more to do. Call it under the
// run/class lock. anyQuota pools all quotas, which happens at chart preparation.
func (s *Service) promote(ctx context.Context, tx pgx.Tx, runID int64, class string, anyQuota bool) ([]Promotion, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.id, p.booking_id, b.user_id, b.source, p.seq, p.status::text, p.age, p.gender, p.berth_pref,
		       coalesce(p.rac_no, 0), coalesce(p.wl_no, 0), coalesce(a.coach_no, ''), coalesce(a.berth_no, 0), coalesce(a.slot, 0),
		       b.quota, b.from_idx, b.to_idx, b.train_number
		  FROM booking_passengers p
		  JOIN bookings b ON b.id = p.booking_id
		  LEFT JOIN seat_allocations a ON a.passenger_id = p.id
		 WHERE b.run_id = $1 AND b.class_code = $2 AND b.status = 'CONFIRMED' AND p.status IN ('RAC', 'WL')
		 ORDER BY CASE p.status WHEN 'RAC' THEN 0 ELSE 1 END, coalesce(p.rac_no, p.wl_no), b.confirmed_at, p.seq`, runID, class)
	if err != nil {
		return nil, err
	}
	type queued struct {
		id, booking, user, source string
		seq                       int
		status                    string
		age                       int
		gender, pref              string
		rac, wl                   int
		coach                     string
		berth, slot               int
		quota                     string
		from, to                  int
		train                     string
	}
	queue, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (queued, error) {
		var q queued
		err := r.Scan(&q.id, &q.booking, &q.user, &q.source, &q.seq, &q.status, &q.age, &q.gender, &q.pref,
			&q.rac, &q.wl, &q.coach, &q.berth, &q.slot, &q.quota, &q.from, &q.to, &q.train)
		return q, err
	})
	if err != nil || len(queue) == 0 {
		return nil, err
	}
	cat, err := s.catalog(s.runTrain(runID, s.Net.Trains[queue[0].train]), class)
	if err != nil {
		return nil, err
	}
	occ, _, err := loadOccupancy(ctx, tx, runID, class)
	if err != nil {
		return nil, err
	}

	var out []Promotion
	for _, q := range queue {
		trial := occ.Clone()
		placed, err := cat.Allocate(trial, inventory.Request{
			Quota: q.quota, From: q.from, To: q.to, AnyQuota: anyQuota,
			Pax: []inventory.Passenger{{Age: q.age, Gender: q.gender, Pref: q.pref}},
		})
		if err != nil {
			return nil, err
		}
		pl := placed[0]
		upgrade := (q.status == "RAC" && pl.Status == inventory.CNF) ||
			(q.status == "WL" && (pl.Status == inventory.CNF || pl.Status == inventory.RAC))
		if !upgrade {
			continue
		}
		occ = trial
		if q.status == "RAC" {
			// Their old half-berth is now free for the next RAC passenger.
			occ[inventory.Key{Coach: q.coach, Berth: q.berth, Slot: q.slot}] &^= inventory.SegMask(q.from, q.to)
		}
		rac := q.rac
		if pl.Status == inventory.RAC {
			if rac, err = nextNumbers(ctx, tx, runID, class, q.quota, "RAC", 1); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM seat_allocations WHERE passenger_id = $1`, q.id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO seat_allocations (run_id, class_code, coach_no, berth_no, slot, seg, booking_id, passenger_id)
			VALUES ($1,$2,$3,$4,$5,int4range($6,$7),$8,$9)`,
			runID, class, pl.Unit.Coach, pl.Unit.Berth, pl.Unit.Slot, q.from, q.to, q.booking, q.id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE booking_passengers SET status = $2, coach_no = $3, berth_no = $4, berth_type = $5, rac_no = nullif($6, 0)
			WHERE id = $1`, q.id, pl.Status, pl.Unit.Coach, pl.Unit.Berth, pl.Unit.Type, rac); err != nil {
			return nil, err
		}
		p := Promotion{BookingID: q.booking, UserID: q.user, Seq: q.seq,
			From: statusLabel(q.status, q.coach, q.berth, q.rac, q.wl),
			To:   statusLabel(pl.Status, pl.Unit.Coach, pl.Unit.Berth, rac, 0)}
		out = append(out, p)
		if q.source == "user" {
			if err := outbox(ctx, tx, "booking.events", q.booking, map[string]any{
				"type": "passenger.promoted", "booking_id": q.booking, "seq": q.seq, "from": p.From, "to": p.To,
			}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// Promote runs the promotion engine for a run's class in its own transaction.
func (s *Service) Promote(ctx context.Context, runID int64, class string) ([]Promotion, error) {
	var out []Promotion
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, runID, class); err != nil {
			return err
		}
		if _, err := s.expireStale(ctx, tx, runID, class); err != nil {
			return err
		}
		var err error
		out, err = s.promote(ctx, tx, runID, class, s.chartPrepared(runID))
		return err
	})
	s.invalidate(runID, class)
	return out, err
}
