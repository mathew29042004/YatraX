package booking

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
)

var ErrInsufficientFunds = errors.New("your YatraX Pay balance is too low for this booking")

type WalletEntry struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Memo   string    `json:"memo"`
	Ref    string    `json:"ref"`
	Amount int64     `json:"amount"` // paise; positive = money in
}

type Wallet struct {
	Balance int64         `json:"balance"` // paise
	History []WalletEntry `json:"history"`
}

func (s *Service) Wallet(ctx context.Context, userID string) (*Wallet, error) {
	owed, err := ledger.Owed(ctx, s.DB, ledger.Wallet(userID))
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `
		SELECT t.created_at, t.kind, t.memo, t.ref, -e.amount
		  FROM ledger_entries e JOIN ledger_accounts a ON a.id = e.account_id JOIN ledger_txns t ON t.id = e.txn_id
		 WHERE a.code = $1 ORDER BY t.created_at DESC LIMIT 50`, ledger.Wallet(userID))
	if err != nil {
		return nil, err
	}
	hist, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WalletEntry, error) {
		var w WalletEntry
		return w, r.Scan(&w.At, &w.Kind, &w.Memo, &w.Ref, &w.Amount)
	})
	if err != nil {
		return nil, err
	}
	return &Wallet{Balance: owed, History: hist}, nil
}

// PayWithWallet pays for a held booking from the passenger's wallet and confirms it at once.
func (s *Service) PayWithWallet(ctx context.Context, userID, bookingID string) (*Booking, error) {
	b, err := s.GetFor(ctx, userID, bookingID)
	if err != nil {
		return nil, err
	}
	if (b.Status != "HELD" && b.Status != "PAYMENT_PENDING") || b.HoldExpiresAt == nil || !b.HoldExpiresAt.After(s.Now()) {
		return nil, ErrNotHeld
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, b.RunID, b.Class); err != nil {
			return err
		}
		// Serialise spending from this wallet so two payments can't both see the same balance.
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_accounts (code, user_id) VALUES ($1, $2) ON CONFLICT (code) DO NOTHING`,
			ledger.Wallet(userID), userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM ledger_accounts WHERE code = $1 FOR UPDATE`, ledger.Wallet(userID)); err != nil {
			return err
		}
		var status string
		var holdUntil *time.Time
		if err := tx.QueryRow(ctx, `SELECT status::text, hold_expires_at FROM bookings WHERE id = $1 FOR UPDATE`, bookingID).Scan(&status, &holdUntil); err != nil {
			return err
		}
		if (status != "HELD" && status != "PAYMENT_PENDING") || holdUntil == nil || !holdUntil.After(s.Now()) {
			return ErrNotHeld
		}
		owed, err := ledger.Owed(ctx, tx, ledger.Wallet(userID))
		if err != nil {
			return err
		}
		amount := ledger.Paise(b.TotalFare)
		if owed < amount {
			return ErrInsufficientFunds
		}
		buf := make([]byte, 8)
		rand.Read(buf)
		if _, err := tx.Exec(ctx, `INSERT INTO payments (booking_id, order_id, amount, method, status) VALUES ($1, $2, $3, 'wallet', 'CAPTURED')`,
			bookingID, "wallet_"+hex.EncodeToString(buf), b.TotalFare); err != nil {
			return err
		}
		if err := ledger.Post(ctx, tx, "wallet_payment", bookingID, "Paid with YatraX Pay",
			ledger.Line{Account: ledger.Wallet(userID), Amount: amount},
			ledger.Line{Account: ledger.Fares, Amount: -amount},
		); err != nil {
			return err
		}
		return s.confirm(ctx, tx, bookingID)
	})
	s.invalidate(b.RunID, b.Class)
	if err != nil {
		return nil, err
	}
	metrics.Payments.WithLabelValues("wallet").Inc()
	return s.Get(ctx, bookingID)
}
