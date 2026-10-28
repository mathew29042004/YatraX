package booking

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
)

// TDR (ticket deposit receipt) reasons a passenger can claim a refund for after
// the chart is prepared, when ordinary cancellation no longer refunds anything.
var TDRReasons = map[string]string{
	"train_late":        "Train late by more than 3 hours and I did not travel",
	"ac_failure":        "AC failure in an AC coach",
	"lower_class":       "Travelled in a lower class than booked",
	"party_partly":      "Party partially travelled",
	"not_travelled_rac": "RAC or waitlisted and did not travel",
	"other":             "Other",
}

var (
	ErrTDRNotEligible = errors.New("a TDR can be filed only for a confirmed booking after its chart is prepared")
	ErrTDRExists      = errors.New("a TDR is already filed for this booking")
	ErrTDRDecided     = errors.New("this TDR has already been decided")
)

type TDR struct {
	ID         string     `json:"id"`
	BookingID  string     `json:"booking_id"`
	PNR        string     `json:"pnr"`
	Train      string     `json:"train"`
	Journey    string     `json:"journey_date"`
	Reason     string     `json:"reason"`
	ReasonText string     `json:"reason_text"`
	Details    string     `json:"details"`
	Status     string     `json:"status"`
	Refund     *int       `json:"refund,omitempty"`
	MaxRefund  int        `json:"max_refund"`
	CreatedAt  time.Time  `json:"created_at"`
	DecidedAt  *time.Time `json:"decided_at,omitempty"`
}

func (s *Service) FileTDR(ctx context.Context, userID, bookingID, reason, details string) (*TDR, error) {
	b, err := s.GetFor(ctx, userID, bookingID)
	if err != nil {
		return nil, err
	}
	if _, ok := TDRReasons[reason]; !ok {
		return nil, invalid("Pick a TDR reason")
	}
	if len(details) > 500 {
		return nil, invalid("Keep the details under 500 characters")
	}
	departed := s.Now().After(b.Departure)
	if b.Status != "CONFIRMED" || !(s.chartPrepared(b.RunID) || departed) {
		return nil, ErrTDRNotEligible
	}
	var id string
	err = s.DB.QueryRow(ctx, `INSERT INTO tdrs (booking_id, user_id, reason, details) VALUES ($1, $2, $3, $4) RETURNING id`,
		bookingID, userID, reason, details).Scan(&id)
	if isUniqueViolation(err) {
		return nil, ErrTDRExists
	} else if err != nil {
		return nil, err
	}
	return s.getTDR(ctx, id)
}

const tdrCols = `t.id, t.booking_id, coalesce(b.pnr, ''), b.train_number, b.journey_date, t.reason, t.details, t.status::text, t.refund,
	(SELECT coalesce(sum(fare), 0) FROM booking_passengers WHERE booking_id = b.id AND status <> 'CAN'), t.created_at, t.decided_at`

func scanTDR(r pgx.Row) (*TDR, error) {
	var t TDR
	var jd time.Time
	err := r.Scan(&t.ID, &t.BookingID, &t.PNR, &t.Train, &jd, &t.Reason, &t.Details, &t.Status, &t.Refund, &t.MaxRefund, &t.CreatedAt, &t.DecidedAt)
	t.Journey = jd.Format(time.DateOnly)
	t.ReasonText = TDRReasons[t.Reason]
	return &t, err
}

func (s *Service) getTDR(ctx context.Context, id string) (*TDR, error) {
	t, err := scanTDR(s.DB.QueryRow(ctx, `SELECT `+tdrCols+` FROM tdrs t JOIN bookings b ON b.id = t.booking_id WHERE t.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// TDRForBooking returns the booking's TDR, if any.
func (s *Service) TDRForBooking(ctx context.Context, bookingID string) (*TDR, error) {
	t, err := scanTDR(s.DB.QueryRow(ctx, `SELECT `+tdrCols+` FROM tdrs t JOIN bookings b ON b.id = t.booking_id WHERE t.booking_id = $1`, bookingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// ListTDRs is the admin queue, pending first.
func (s *Service) ListTDRs(ctx context.Context, status string) ([]*TDR, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+tdrCols+` FROM tdrs t JOIN bookings b ON b.id = t.booking_id
		WHERE $1 = '' OR t.status::text = $1 ORDER BY (t.status = 'PENDING') DESC, t.created_at DESC LIMIT 100`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TDR{}
	for rows.Next() {
		t, err := scanTDR(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DecideTDR approves (with a refund up to the fare of travelling passengers) or rejects a claim.
func (s *Service) DecideTDR(ctx context.Context, adminID, id string, approve bool, refund int) (*TDR, error) {
	t, err := s.getTDR(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status != "PENDING" {
		return nil, ErrTDRDecided
	}
	if approve && (refund <= 0 || refund > t.MaxRefund) {
		return nil, invalid("Refund must be between ₹1 and ₹%d", t.MaxRefund)
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		status := "REJECTED"
		var amount *int
		if approve {
			status, amount = "APPROVED", &refund
		}
		tag, err := tx.Exec(ctx, `UPDATE tdrs SET status = $2, refund = $3, decided_by = $4, decided_at = $5 WHERE id = $1 AND status = 'PENDING'`,
			id, status, amount, adminID, s.Now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTDRDecided
		}
		if approve {
			var userID, source string
			var paymentID *string
			if err := tx.QueryRow(ctx, `SELECT b.user_id, b.source,
				(SELECT id FROM payments WHERE booking_id = b.id AND status IN ('CAPTURED', 'REFUNDED') ORDER BY updated_at DESC LIMIT 1)
				FROM bookings b WHERE b.id = $1`, t.BookingID).Scan(&userID, &source, &paymentID); err != nil {
				return err
			}
			if err := ledger.Post(ctx, tx, "tdr_refund_due", t.BookingID, "TDR approved",
				ledger.Line{Account: ledger.Fares, Amount: ledger.Paise(refund)},
				ledger.Line{Account: ledger.Refunds, Amount: -ledger.Paise(refund)},
			); err != nil {
				return err
			}
			dest := "source"
			if paymentID == nil {
				dest = "wallet"
			}
			if _, err := s.createRefund(ctx, tx, t.BookingID, userID, paymentID, refund, dest, "TDR approved: "+TDRReasons[t.Reason]); err != nil {
				return err
			}
		}
		return outbox(ctx, tx, "booking.events", t.BookingID, map[string]any{
			"type": "tdr.decided", "booking_id": t.BookingID, "tdr_id": id, "status": status, "refund": refund,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.getTDR(ctx, id)
}
