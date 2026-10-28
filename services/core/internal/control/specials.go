package control

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

// Special trains: Indian Railways clears festival waitlists with specials numbered 0xxxx
// that follow a regular train's route. YatraX keeps one ready for each of the busiest
// trains, two hours behind it, and runs it on the dates control picks.

// specialBases are the regular trains that have a special ready.
var specialBases = []string{"12623", "12627", "12301", "12951", "12841", "12859", "12163", "12621", "12295", "12723"}

const specialShiftMin = 120

func specialNumber(base string) string { return "0" + base[1:] }

// EnsureSpecialTemplates creates the special trains if they're missing. It runs before the
// network is loaded, so the specials are part of it from the start.
func EnsureSpecialTemplates(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		for _, base := range specialBases {
			num := specialNumber(base)
			tag, err := tx.Exec(ctx, `INSERT INTO trains (number, name, type_code, origin, destination, running_days, distance_km, pantry, special_of)
				SELECT $2, name || ' Special', type_code, origin, destination, 0, distance_km, pantry, number FROM trains WHERE number = $1
				ON CONFLICT (number) DO NOTHING`, base, num)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				continue // already there, or the base train isn't in the network
			}
			if _, err := tx.Exec(ctx, `INSERT INTO train_stops (train_number, seq, station_code, arrival_min, departure_min, distance_km, platform)
				SELECT $2, seq, station_code, arrival_min + $3, departure_min + $3, distance_km, platform FROM train_stops WHERE train_number = $1`,
				base, num, specialShiftMin); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO train_coaches (train_number, coach_no, class_code, position)
				SELECT $2, coach_no, class_code, position FROM train_coaches WHERE train_number = $1`, base, num); err != nil {
				return err
			}
		}
		return nil
	})
}

type Special struct {
	Number    string   `json:"number"`
	Name      string   `json:"name"`
	Base      string   `json:"base"`
	BaseName  string   `json:"base_name"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Departure string   `json:"departure"` // HH:MM from the origin
	Runs      []string `json:"runs"`      // dates it runs, from today on
}

// Specials lists the special trains and the dates each runs.
func (c *Service) Specials(ctx context.Context) ([]Special, error) {
	today := c.Now().In(network.IST).Format(time.DateOnly)
	out := []Special{}
	for _, base := range specialBases {
		t := c.Net.Trains[specialNumber(base)]
		if t == nil {
			continue
		}
		b := c.Net.Trains[base]
		dep := t.Stops[0].Time()
		sp := Special{Number: t.Number, Name: t.Name, Base: base, BaseName: b.Name, From: t.Origin, To: t.Destination,
			Departure: fmt.Sprintf("%02d:%02d", dep/60%24, dep%60), Runs: []string{}}
		rows, err := c.DB.Query(ctx, `SELECT origin_date::text FROM special_runs WHERE train_number = $1 AND origin_date >= $2 ORDER BY 1`, t.Number, today)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				rows.Close()
				return nil, err
			}
			sp.Runs = append(sp.Runs, d)
		}
		rows.Close()
		out = append(out, sp)
	}
	return out, nil
}

// RunSpecial puts a special train on for a date. It opens for booking at once on this
// instance; others pick it up with the next incident refresh (a few seconds).
func (c *Service) RunSpecial(ctx context.Context, number string, date time.Time, reason, by string) (*Special, error) {
	t := c.Net.Trains[strings.TrimSpace(number)]
	if t == nil || t.SpecialOf == "" {
		return nil, fmt.Errorf("%w: %s is not a special train", ErrSection, number)
	}
	today := c.Now().In(network.IST)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, network.IST)
	if date.Before(today) || date.After(today.AddDate(0, 0, 120)) {
		return nil, fmt.Errorf("%w: pick a date in the next 120 days", ErrSection)
	}
	if _, err := c.DB.Exec(ctx, `INSERT INTO special_runs (train_number, origin_date, reason, created_by) VALUES ($1, $2, $3, nullif($4, '')::uuid)
		ON CONFLICT DO NOTHING`, t.Number, date, reason, by); err != nil {
		return nil, err
	}
	t.AddSpecialRun(date)
	list, err := c.Specials(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Number == t.Number {
			return &list[i], nil
		}
	}
	return nil, nil
}
