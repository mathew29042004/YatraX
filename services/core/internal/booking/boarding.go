package booking

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrBoardingChange = errors.New("the boarding point can be changed once, up to 24 hours before departure and before the chart is prepared")

// ChangeBoarding moves the boarding point to a later station on the route. The
// fare is not refunded; the segments before the new station are released and
// can go to waitlisted passengers.
func (s *Service) ChangeBoarding(ctx context.Context, userID, bookingID, station string) (*Booking, []Promotion, error) {
	b, err := s.GetFor(ctx, userID, bookingID)
	if err != nil {
		return nil, nil, err
	}
	t := s.Net.Trains[b.Train]
	newFrom := t.StopIndex(strings.ToUpper(station))
	if newFrom <= b.FromIdx || newFrom >= b.ToIdx {
		return nil, nil, invalid("Pick a station after %s and before %s", b.From, b.To)
	}
	var promos []Promotion
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, b.RunID, b.Class); err != nil {
			return err
		}
		var status string
		var changed bool
		if err := tx.QueryRow(ctx, `SELECT status::text, boarding_changed FROM bookings WHERE id = $1 FOR UPDATE`, bookingID).Scan(&status, &changed); err != nil {
			return err
		}
		if status != "CONFIRMED" || changed || s.chartPrepared(b.RunID) || b.Departure.Sub(s.Now()) < BoardingChangeCutoff {
			return ErrBoardingChange
		}
		origin := b.Departure.Add(-time.Duration(t.Stops[b.FromIdx].Time()) * time.Minute)
		origin = time.Date(origin.Year(), origin.Month(), origin.Day(), 0, 0, 0, 0, origin.Location())
		boardDate := origin.AddDate(0, 0, t.Stops[newFrom].Time()/1440)
		if _, err := tx.Exec(ctx, `UPDATE seat_allocations SET seg = int4range($2, upper(seg)) WHERE booking_id = $1`, bookingID, newFrom); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings SET original_from = from_station, from_station = $2, from_idx = $3, journey_date = $4,
			boarding_changed = true, updated_at = now() WHERE id = $1`, bookingID, t.Stops[newFrom].Station, newFrom, boardDate); err != nil {
			return err
		}
		if err := outbox(ctx, tx, "booking.events", bookingID, map[string]any{
			"type": "boarding.changed", "booking_id": bookingID, "from": b.From, "to": t.Stops[newFrom].Station,
		}); err != nil {
			return err
		}
		promos, err = s.promote(ctx, tx, b.RunID, b.Class, false)
		return err
	})
	s.invalidate(b.RunID, b.Class)
	if err != nil {
		return nil, nil, err
	}
	nb, err := s.Get(ctx, bookingID)
	return nb, promos, err
}
