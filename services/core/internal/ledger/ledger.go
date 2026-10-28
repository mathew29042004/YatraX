// Package ledger records every movement of money as a balanced double-entry
// transaction (amounts in paise; debits positive, credits negative). Wallet
// balances, refunds owed and money held at the gateway are sums of entries, never
// stored numbers, and a database trigger rejects any transaction that doesn't balance.
//
// Accounts used:
//
//	asset:gateway             money the gateway holds for us (captures minus refunds to source)
//	liability:fares           fares paid for journeys not yet travelled
//	liability:refunds         refunds owed but not yet paid out
//	liability:wallet:<user>   a passenger's YatraX Pay balance
//	revenue:cancellation      cancellation charges kept
//	revenue:penalties         penalties on excess-fare tickets issued on board
//	asset:tte_cash:<user>     cash a TTE has collected on board and not yet deposited
//	liability:catering_escrow prepaid food orders not yet delivered
//	liability:vendor:<id>     what YatraX owes a catering vendor (negative: commission the vendor owes)
//	revenue:catering_commission YatraX's share of delivered food orders
//	liability:insurer         travel-insurance premiums owed to the insurer
//	expense:loyalty           wallet credit given for loyalty points
package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const (
	Gateway      = "asset:gateway"
	Fares        = "liability:fares"
	Refunds      = "liability:refunds"
	Cancellation = "revenue:cancellation"
	Penalties    = "revenue:penalties"

	Insurer            = "liability:insurer"
	LoyaltyExpense     = "expense:loyalty"
	CateringEscrow     = "liability:catering_escrow"
	CateringCommission = "revenue:catering_commission"
)

// Vendor is what YatraX owes a catering vendor.
func Vendor(vendorID string) string { return "liability:vendor:" + vendorID }

// TTECash is the cash a TTE has collected on board.
func TTECash(userID string) string { return "asset:tte_cash:" + userID }

// Wallet is a passenger's wallet account.
func Wallet(userID string) string { return "liability:wallet:" + userID }

type Line struct {
	Account string
	Amount  int64 // paise; debit positive, credit negative
}

var ErrUnbalanced = errors.New("ledger lines do not sum to zero")

// Paise converts whole rupees.
func Paise(rupees int) int64 { return int64(rupees) * 100 }

// Post writes one balanced transaction inside tx.
func Post(ctx context.Context, tx pgx.Tx, kind, ref, memo string, lines ...Line) error {
	var sum int64
	for _, l := range lines {
		sum += l.Amount
	}
	if sum != 0 || len(lines) < 2 {
		return fmt.Errorf("%w: %s %s %v", ErrUnbalanced, kind, ref, lines)
	}
	var txnID int64
	if err := tx.QueryRow(ctx, `INSERT INTO ledger_txns (kind, ref, memo) VALUES ($1, $2, $3) RETURNING id`, kind, ref, memo).Scan(&txnID); err != nil {
		return err
	}
	for _, l := range lines {
		if l.Amount == 0 {
			continue
		}
		var accountID int64
		var userID any
		if len(l.Account) > len("liability:wallet:") && l.Account[:len("liability:wallet:")] == "liability:wallet:" {
			userID = l.Account[len("liability:wallet:"):]
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO ledger_accounts (code, user_id) VALUES ($1, $2)
			ON CONFLICT (code) DO UPDATE SET code = EXCLUDED.code RETURNING id`, l.Account, userID).Scan(&accountID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (txn_id, account_id, amount) VALUES ($1, $2, $3)`, txnID, accountID, l.Amount); err != nil {
			return err
		}
	}
	return nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Balance is an account's balance in paise, debit-positive. Liabilities therefore
// come out negative; use Owed for "how much do we owe".
func Balance(ctx context.Context, q querier, account string) (int64, error) {
	var b int64
	err := q.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount), 0) FROM ledger_entries e JOIN ledger_accounts a ON a.id = e.account_id
		 WHERE a.code = $1`, account).Scan(&b)
	return b, err
}

// Owed is a liability's balance as a positive number of paise.
func Owed(ctx context.Context, q querier, account string) (int64, error) {
	b, err := Balance(ctx, q, account)
	return -b, err
}

// TrialBalance sums every entry; it must always be zero.
func TrialBalance(ctx context.Context, q querier) (int64, error) {
	var b int64
	err := q.QueryRow(ctx, `SELECT coalesce(sum(amount), 0) FROM ledger_entries`).Scan(&b)
	return b, err
}
