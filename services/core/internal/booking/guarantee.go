package booking

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Confirmation guarantee: when both trains a passenger could take are waitlisted, hold the
// trip on both; the moment one confirms, the other is cancelled with a full refund (no
// clerkage), so they end up with exactly one confirmed ticket or, at chart time, none and
// their money back.

type GuaranteeRequest struct {
	First  HoldRequest `json:"first"`
	Second HoldRequest `json:"second"`
}

type GuaranteePair struct {
	First  *Booking `json:"first"`
	Second *Booking `json:"second"`
}

func fullyConfirmed(b *Booking) bool {
	for _, p := range b.Passengers {
		if p.Status != "CNF" {
			return false
		}
	}
	return len(b.Passengers) > 0
}

// HoldGuarantee holds both journeys and links them.
func (s *Service) HoldGuarantee(ctx context.Context, userID, idemKey string, req GuaranteeRequest) (*GuaranteePair, error) {
	if len(req.First.Passengers) != len(req.Second.Passengers) {
		return nil, invalid("Both choices must be for the same passengers")
	}
	if req.First.Train == req.Second.Train && req.First.Date == req.Second.Date && req.First.Class == req.Second.Class {
		return nil, invalid("Pick two different trains (or classes) for the guarantee")
	}
	key := func(suffix string) string {
		if idemKey == "" {
			return ""
		}
		return idemKey + suffix
	}
	req.First.OnlyConfirmed, req.Second.OnlyConfirmed = false, false
	a, err := s.Hold(ctx, userID, key(":a"), req.First)
	if err != nil {
		return nil, err
	}
	if fullyConfirmed(a) {
		s.Release(ctx, userID, a.ID)
		return nil, invalid("Your first choice has confirmed berths: book it directly, no guarantee needed")
	}
	b, err := s.Hold(ctx, userID, key(":b"), req.Second)
	if err != nil {
		s.Release(ctx, userID, a.ID)
		return nil, err
	}
	if fullyConfirmed(b) {
		s.Release(ctx, userID, a.ID)
		s.Release(ctx, userID, b.ID)
		return nil, invalid("Your second choice has confirmed berths: book it directly, no guarantee needed")
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO guarantee_pairs (user_id, booking_a, booking_b) VALUES ($1, $2, $3)
		ON CONFLICT (booking_a) DO NOTHING`, userID, a.ID, b.ID); err != nil {
		return nil, err
	}
	if a, err = s.Get(ctx, a.ID); err != nil {
		return nil, err
	}
	if b, err = s.Get(ctx, b.ID); err != nil {
		return nil, err
	}
	return &GuaranteePair{First: a, Second: b}, nil
}

// settleGuarantee runs when a booking confirms or a passenger moves up: if one booking of a
// pair is now fully confirmed, the other goes. Idempotent; safe to call for any booking.
func (s *Service) settleGuarantee(ctx context.Context, bookingID string) error {
	var unpaid []string
	var userID string
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var pairID int64
		var aID, bID string
		err := tx.QueryRow(ctx, `SELECT id, user_id, booking_a, booking_b FROM guarantee_pairs
			WHERE (booking_a = $1 OR booking_b = $1) AND settled_at IS NULL FOR UPDATE`, bookingID).Scan(&pairID, &userID, &aID, &bID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		a, err := s.getTx(ctx, tx, aID)
		if err != nil {
			return err
		}
		b, err := s.getTx(ctx, tx, bID)
		if err != nil {
			return err
		}
		keep, drop := a, b
		switch {
		case a.Status == "CONFIRMED" && fullyConfirmed(a):
		case b.Status == "CONFIRMED" && fullyConfirmed(b):
			keep, drop = b, a
		default:
			return nil // neither has confirmed yet
		}
		switch drop.Status {
		case "CONFIRMED":
			if err := lock(ctx, tx, drop.RunID, drop.Class); err != nil {
				return err
			}
			q := fullQuote(drop)
			for i := range q.Passengers {
				q.Passengers[i].Rule = "Confirmation guarantee: your other ticket confirmed, so this one is refunded in full"
			}
			if _, _, err := s.cancelInTx(ctx, tx, drop, q, "source", "Confirmation guarantee: the other ticket confirmed"); err != nil {
				return err
			}
		case "HELD", "PAYMENT_PENDING":
			unpaid = append(unpaid, drop.ID)
		}
		if _, err := tx.Exec(ctx, `UPDATE guarantee_pairs SET settled_at = now(), kept = $2 WHERE id = $1`, pairID, keep.ID); err != nil {
			return err
		}
		return outbox(ctx, tx, "booking.events", keep.ID, map[string]any{
			"type": "guarantee.settled", "booking_id": keep.ID, "dropped": drop.ID, "pnr": keep.PNR,
			"note": fmt.Sprintf("%s confirmed; %s cancelled with a full refund", keep.Train, drop.Train),
		})
	})
	for _, id := range unpaid {
		s.Release(ctx, userID, id)
	}
	return err
}
