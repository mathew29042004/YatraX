package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
)

// Loyalty: a point for every ₹100 of fare on a confirmed ticket, taken back if the whole
// ticket is cancelled; points turn into YatraX Pay credit (100 points = ₹25), which pays for
// the next ticket — that's the discount.
const (
	RupeesPerPoint = 100 // fare per point earned
	RedeemBlock    = 100 // points redeemed at a time
	RedeemValue    = 25  // rupees of wallet credit per block
)

func (s *Service) earnPoints(ctx context.Context, tx pgx.Tx, bookingID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO loyalty_points (user_id, delta, reason, ref)
		SELECT user_id, (total_fare - insurance_premium) / $2, 'earned', id::text FROM bookings
		 WHERE id = $1 AND source = 'user' AND (total_fare - insurance_premium) >= $2
		ON CONFLICT (ref, reason) DO NOTHING`, bookingID, RupeesPerPoint)
	return err
}

// reversePoints takes back what a fully cancelled ticket earned.
func (s *Service) reversePoints(ctx context.Context, tx pgx.Tx, bookingID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO loyalty_points (user_id, delta, reason, ref)
		SELECT user_id, -delta, 'reversed', ref FROM loyalty_points WHERE ref = $1 AND reason = 'earned'
		ON CONFLICT (ref, reason) DO NOTHING`, bookingID)
	return err
}

type PointsEntry struct {
	Delta  int       `json:"delta"`
	Reason string    `json:"reason"`
	PNR    string    `json:"pnr,omitempty"`
	At     time.Time `json:"at"`
}

type Loyalty struct {
	Balance     int           `json:"balance"`
	Redeemable  int           `json:"redeemable"`   // points that can be turned into credit now
	CreditValue int           `json:"credit_value"` // rupees those are worth
	Rules       string        `json:"rules"`
	History     []PointsEntry `json:"history"`
}

func (s *Service) Loyalty(ctx context.Context, userID string) (*Loyalty, error) {
	l := &Loyalty{History: []PointsEntry{},
		Rules: fmt.Sprintf("1 point for every ₹%d of fare; %d points = ₹%d of YatraX Pay credit", RupeesPerPoint, RedeemBlock, RedeemValue)}
	if err := s.DB.QueryRow(ctx, `SELECT coalesce(sum(delta), 0) FROM loyalty_points WHERE user_id = $1`, userID).Scan(&l.Balance); err != nil {
		return nil, err
	}
	l.Redeemable = max(0, l.Balance) / RedeemBlock * RedeemBlock
	l.CreditValue = l.Redeemable / RedeemBlock * RedeemValue
	rows, err := s.DB.Query(ctx, `SELECT lp.delta, lp.reason, coalesce(b.pnr, ''), lp.created_at FROM loyalty_points lp
		LEFT JOIN bookings b ON b.id::text = lp.ref WHERE lp.user_id = $1 ORDER BY lp.id DESC LIMIT 30`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e PointsEntry
		if err := rows.Scan(&e.Delta, &e.Reason, &e.PNR, &e.At); err != nil {
			return nil, err
		}
		l.History = append(l.History, e)
	}
	return l, rows.Err()
}

// RedeemPoints turns points into wallet credit.
func (s *Service) RedeemPoints(ctx context.Context, userID string, points int) (*Loyalty, error) {
	if points < RedeemBlock || points%RedeemBlock != 0 {
		return nil, invalid("Redeem points in blocks of %d", RedeemBlock)
	}
	credit := points / RedeemBlock * RedeemValue
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		// One redemption at a time per passenger, so the balance can't be spent twice.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('loyalty:' || $1))`, userID); err != nil {
			return err
		}
		var balance int
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(delta), 0) FROM loyalty_points WHERE user_id = $1`, userID).Scan(&balance); err != nil {
			return err
		}
		if points > balance {
			return invalid("You have %d points", balance)
		}
		ref := fmt.Sprintf("redeem:%s:%d", userID, time.Now().UnixNano())
		if _, err := tx.Exec(ctx, `INSERT INTO loyalty_points (user_id, delta, reason, ref) VALUES ($1, $2, 'redeemed', $3)`, userID, -points, ref); err != nil {
			return err
		}
		return ledger.Post(ctx, tx, "loyalty_redeem", ref, fmt.Sprintf("%d loyalty points redeemed", points),
			ledger.Line{Account: ledger.LoyaltyExpense, Amount: ledger.Paise(credit)},
			ledger.Line{Account: ledger.Wallet(userID), Amount: -ledger.Paise(credit)},
		)
	})
	if err != nil {
		return nil, err
	}
	return s.Loyalty(ctx, userID)
}
