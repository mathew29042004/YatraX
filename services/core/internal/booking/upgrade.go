package booking

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/inventory"
)

// upgradePath is the class a passenger moves up to at no extra cost, as Indian Railways'
// auto-upgradation does when higher-class berths are still empty at chart time.
var upgradePath = []struct{ from, to string }{
	// Top first, so a 2A passenger moving to 1A frees a 2A berth for a 3A passenger, and so on.
	{"2A", "1A"}, {"3A", "2A"}, {"3E", "3A"}, {"SL", "3A"}, {"CC", "EC"}, {"2S", "CC"},
}

// autoUpgrade moves opted-in, fully confirmed parties up a class where the whole party fits
// in berths that are free for their whole journey. Their old berths then go to the RAC and
// waitlist through the normal promotion that follows. Call under every class lock of the run.
func (s *Service) autoUpgrade(ctx context.Context, tx pgx.Tx, runID int64, classes []string) (int, error) {
	has := map[string]bool{}
	for _, c := range classes {
		has[c] = true
	}
	moved := 0
	for _, step := range upgradePath {
		if !has[step.from] || !has[step.to] {
			continue
		}
		rows, err := tx.Query(ctx, `
			SELECT b.id, b.train_number, b.from_idx, b.to_idx, p.id, p.age, p.gender, p.berth_pref
			  FROM bookings b JOIN booking_passengers p ON p.booking_id = b.id
			 WHERE b.run_id = $1 AND b.class_code = $2 AND b.status = 'CONFIRMED' AND b.source = 'user'
			   AND coalesce((b.preferences->>'auto_upgrade')::boolean, false)
			   AND NOT EXISTS (SELECT 1 FROM booking_passengers q WHERE q.booking_id = b.id AND q.status <> 'CNF')
			 ORDER BY b.confirmed_at, b.id, p.seq`, runID, step.from)
		if err != nil {
			return moved, err
		}
		type pax struct {
			id, pref string
			age      int
			gender   string
		}
		type party struct {
			id, train string
			from, to  int
			pax       []pax
		}
		var parties []*party
		for rows.Next() {
			var bid, train, pid, gender, pref string
			var from, to, age int
			if err := rows.Scan(&bid, &train, &from, &to, &pid, &age, &gender, &pref); err != nil {
				rows.Close()
				return moved, err
			}
			if len(parties) == 0 || parties[len(parties)-1].id != bid {
				parties = append(parties, &party{id: bid, train: train, from: from, to: to})
			}
			p := parties[len(parties)-1]
			p.pax = append(p.pax, pax{pid, pref, age, gender})
		}
		rows.Close()
		if err := rows.Err(); err != nil || len(parties) == 0 {
			if err != nil {
				return moved, err
			}
			continue
		}
		cat, err := s.catalog(s.runTrain(runID, s.Net.Trains[parties[0].train]), step.to)
		if err != nil {
			return moved, err
		}
		occ, _, err := loadOccupancy(ctx, tx, runID, step.to)
		if err != nil {
			return moved, err
		}
		for _, p := range parties {
			req := inventory.Request{Quota: "GN", AnyQuota: true, From: p.from, To: p.to, Together: len(p.pax) > 1}
			for _, x := range p.pax {
				req.Pax = append(req.Pax, inventory.Passenger{Age: x.age, Gender: x.gender}) // berth preferences were for the old class
			}
			trial := occ.Clone()
			placed, err := cat.Allocate(trial, req)
			if err != nil {
				return moved, err
			}
			ok := true
			for _, pl := range placed {
				if pl.Status != inventory.CNF {
					ok = false
				}
			}
			if !ok {
				continue // only whole parties, only confirmed berths
			}
			occ = trial
			for i, pl := range placed {
				if _, err := tx.Exec(ctx, `DELETE FROM seat_allocations WHERE passenger_id = $1`, p.pax[i].id); err != nil {
					return moved, err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO seat_allocations (run_id, class_code, coach_no, berth_no, slot, seg, booking_id, passenger_id)
					VALUES ($1,$2,$3,$4,$5,int4range($6,$7),$8,$9)`,
					runID, step.to, pl.Unit.Coach, pl.Unit.Berth, pl.Unit.Slot, p.from, p.to, p.id, p.pax[i].id); err != nil {
					return moved, err
				}
				if _, err := tx.Exec(ctx, `UPDATE booking_passengers SET coach_no = $2, berth_no = $3, berth_type = $4, upgraded_from = $5 WHERE id = $1`,
					p.pax[i].id, pl.Unit.Coach, pl.Unit.Berth, pl.Unit.Type, step.from); err != nil {
					return moved, err
				}
				moved++
			}
			if _, err := tx.Exec(ctx, `UPDATE bookings SET class_code = $2, updated_at = now() WHERE id = $1`, p.id, step.to); err != nil {
				return moved, err
			}
			if err := outbox(ctx, tx, "booking.events", p.id, map[string]any{"type": "booking.upgraded", "booking_id": p.id, "from": step.from, "to": step.to}); err != nil {
				return moved, err
			}
		}
	}
	return moved, nil
}
