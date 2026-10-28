// Package stays books retiring rooms, dormitory beds and cloak-room lockers at stations.
// As on Indian Railways, a stay is booked against a ticket to or from that station, around
// the time of the journey; it's paid from the YatraX Pay wallet or at the counter on arrival.
package stays

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/ledger"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrNotAllowed = errors.New("not allowed")
)

func reject(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNotAllowed, fmt.Sprintf(format, a...))
}

// Revenue from station services (rooms and lockers).
const revenueAccount = "revenue:station_services"

func stationCash(code string) string { return "asset:station_cash:" + code }

type Unit struct {
	Type  string      `json:"type"`
	Name  string      `json:"name"`
	Price map[int]int `json:"price"` // hours -> rupees per unit
	Total int         `json:"total"`
	Free  int         `json:"free"` // for the requested time
}

var unitNames = map[string]string{"ac_room": "AC double room", "dorm_bed": "Dormitory bed", "locker": "Cloak-room locker"}

// Prices per unit, in rupees.
var prices = map[string]map[int]int{
	"ac_room":  {12: 600, 24: 1000, 48: 2000},
	"dorm_bed": {12: 150, 24: 250, 48: 500},
	"locker":   {12: 30, 24: 30, 48: 60}, // charged per day or part of one
}

type Service struct {
	DB  *pgxpool.Pool
	Net *network.Network
	Now func() time.Time
}

// capacity is how many of each unit a station has, by its size.
func capacity(st *network.Station, unit string) int {
	rooms := map[int]int{1: 24, 2: 8, 3: 2}[st.Importance]
	switch unit {
	case "ac_room":
		return rooms
	case "dorm_bed":
		return rooms * 2
	case "locker":
		return map[int]int{1: 120, 2: 40, 3: 10}[st.Importance]
	}
	return 0
}

// reserved counts units held at a station that overlap [from, from+hours).
func (s *Service) reserved(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, code, unit string, from time.Time, hours int) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT coalesce(sum(units), 0) FROM station_stays
		WHERE station_code = $1 AND unit_type = $2 AND status IN ('booked', 'checked_in')
		  AND check_in < $3 AND check_in + make_interval(hours => hours) > $4`,
		code, unit, from.Add(time.Duration(hours)*time.Hour), from).Scan(&n)
	return n, err
}

// Options lists what a station offers and what's free for a stay starting at `from`.
func (s *Service) Options(ctx context.Context, code string, from time.Time, hours int) ([]Unit, error) {
	st := s.Net.Stations[code]
	if st == nil {
		return nil, ErrNotFound
	}
	if _, ok := prices["ac_room"][hours]; !ok {
		hours = 24
	}
	out := []Unit{}
	for _, t := range []string{"ac_room", "dorm_bed", "locker"} {
		total := capacity(st, t)
		used, err := s.reserved(ctx, s.DB, code, t, from, hours)
		if err != nil {
			return nil, err
		}
		out = append(out, Unit{Type: t, Name: unitNames[t], Price: prices[t], Total: total, Free: max(0, total-used)})
	}
	return out, nil
}

type Stay struct {
	ID          int64     `json:"id"`
	Code        string    `json:"code"`
	Station     string    `json:"station"`
	StationName string    `json:"station_name"`
	PNR         string    `json:"pnr"`
	Guest       string    `json:"guest"`
	Unit        string    `json:"unit"`
	UnitName    string    `json:"unit_name"`
	Units       int       `json:"units"`
	CheckIn     time.Time `json:"check_in"`
	CheckOut    time.Time `json:"check_out"`
	Hours       int       `json:"hours"`
	Amount      int       `json:"amount"`
	Payment     string    `json:"payment"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

const stayCols = `SELECT s.id, s.code, s.station_code, st.name, coalesce(b.pnr, ''), u.full_name, s.unit_type, s.units, s.check_in, s.hours,
	s.amount, s.payment, s.status, s.created_at
	FROM station_stays s JOIN stations st ON st.code = s.station_code JOIN bookings b ON b.id = s.booking_id JOIN users u ON u.id = s.user_id `

func (s *Service) list(ctx context.Context, where string, args ...any) ([]Stay, error) {
	rows, err := s.DB.Query(ctx, stayCols+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Stay{}
	for rows.Next() {
		var x Stay
		if err := rows.Scan(&x.ID, &x.Code, &x.Station, &x.StationName, &x.PNR, &x.Guest, &x.Unit, &x.Units, &x.CheckIn, &x.Hours,
			&x.Amount, &x.Payment, &x.Status, &x.CreatedAt); err != nil {
			return nil, err
		}
		x.UnitName = unitNames[x.Unit]
		x.CheckOut = x.CheckIn.Add(time.Duration(x.Hours) * time.Hour)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) get(ctx context.Context, id int64) (*Stay, error) {
	list, err := s.list(ctx, `WHERE s.id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

type Request struct {
	BookingID string    `json:"booking_id"`
	Station   string    `json:"station"`
	Unit      string    `json:"unit"`
	Units     int       `json:"units"`
	CheckIn   time.Time `json:"check_in"`
	Hours     int       `json:"hours"`
	Payment   string    `json:"payment"` // wallet or counter
}

// Book reserves rooms, beds or lockers for a passenger.
func (s *Service) Book(ctx context.Context, userID string, r Request) (*Stay, error) {
	r.Station = strings.ToUpper(r.Station)
	st := s.Net.Stations[r.Station]
	price, ok := prices[r.Unit][r.Hours]
	switch {
	case st == nil:
		return nil, reject("unknown station")
	case !ok:
		return nil, reject("pick a room, a dormitory bed or a locker, for 12, 24 or 48 hours")
	case r.Units < 1 || r.Units > 6:
		return nil, reject("1 to 6 at a time")
	case r.Payment != "wallet" && r.Payment != "counter":
		return nil, reject("pay from the wallet or at the counter")
	case r.CheckIn.Before(s.Now().Add(-time.Hour)):
		return nil, reject("the stay must start in the future")
	}
	// The ticket must be the passenger's, confirmed, and to or from this station, within a
	// day of the journey either way.
	var from, to string
	var departs time.Time
	var travellers int
	err := s.DB.QueryRow(ctx, `SELECT b.from_station, b.to_station, b.journey_date::timestamptz,
			(SELECT count(*) FROM booking_passengers p WHERE p.booking_id = b.id AND p.status <> 'CAN')
		FROM bookings b WHERE b.id = $1 AND b.user_id = $2 AND b.status = 'CONFIRMED'`, r.BookingID, userID).Scan(&from, &to, &departs, &travellers)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, reject("stays are booked against one of your confirmed tickets")
	} else if err != nil {
		return nil, err
	}
	if from != r.Station && to != r.Station {
		return nil, reject("your ticket doesn't start or end at %s", st.Name)
	}
	day := time.Date(departs.Year(), departs.Month(), departs.Day(), 0, 0, 0, 0, network.IST)
	if r.CheckIn.Before(day.AddDate(0, 0, -1)) || r.CheckIn.After(day.AddDate(0, 0, 3)) {
		return nil, reject("stays are for the day before the journey to two days after it")
	}
	if r.Unit != "locker" && r.Units > max(1, (travellers+1)/2)*2 {
		return nil, reject("at most %d for %d travellers", max(1, (travellers+1)/2)*2, travellers)
	}
	amount := price * r.Units
	var id int64
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		// One booker at a time per station and kind, so the last room can't be sold twice.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('stay:' || $1 || ':' || $2))`, r.Station, r.Unit); err != nil {
			return err
		}
		used, err := s.reserved(ctx, tx, r.Station, r.Unit, r.CheckIn, r.Hours)
		if err != nil {
			return err
		}
		if used+r.Units > capacity(st, r.Unit) {
			return reject("only %d %s free then", max(0, capacity(st, r.Unit)-used), strings.ToLower(unitNames[r.Unit])+"s")
		}
		prefix := "RR"
		if r.Unit == "locker" {
			prefix = "CL"
		}
		if err := tx.QueryRow(ctx, `INSERT INTO station_stays (code, station_code, user_id, booking_id, unit_type, units, check_in, hours, amount, payment)
			VALUES ($1 || lpad(nextval('station_stay_seq')::text, 6, '0'), $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
			prefix, r.Station, userID, r.BookingID, r.Unit, r.Units, r.CheckIn, r.Hours, amount, r.Payment).Scan(&id); err != nil {
			return err
		}
		if r.Payment == "wallet" {
			var balance int64
			if balance, err = ledger.Balance(ctx, tx, ledger.Wallet(userID)); err != nil {
				return err
			}
			if -balance < ledger.Paise(amount) {
				return reject("your wallet has ₹%d; pay at the counter instead", -balance/100)
			}
			return ledger.Post(ctx, tx, "stay_payment", fmt.Sprint(id), fmt.Sprintf("%s at %s", unitNames[r.Unit], st.Name),
				ledger.Line{Account: ledger.Wallet(userID), Amount: ledger.Paise(amount)},
				ledger.Line{Account: revenueAccount, Amount: -ledger.Paise(amount)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

func (s *Service) Mine(ctx context.Context, userID string) ([]Stay, error) {
	return s.list(ctx, `WHERE s.user_id = $1 ORDER BY s.check_in DESC LIMIT 50`, userID)
}

// Cancel frees a stay before check-in; a wallet payment comes back in full.
func (s *Service) Cancel(ctx context.Context, userID string, id int64) (*Stay, error) {
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var amount int
		var payment, station string
		err := tx.QueryRow(ctx, `UPDATE station_stays SET status = 'cancelled', updated_at = now()
			WHERE id = $1 AND user_id = $2 AND status = 'booked' RETURNING amount, payment, station_code`, id, userID).Scan(&amount, &payment, &station)
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("only a stay not yet checked in can be cancelled")
		} else if err != nil {
			return err
		}
		if payment == "wallet" {
			return ledger.Post(ctx, tx, "stay_refund", fmt.Sprint(id), "Stay cancelled",
				ledger.Line{Account: revenueAccount, Amount: ledger.Paise(amount)},
				ledger.Line{Account: ledger.Wallet(userID), Amount: -ledger.Paise(amount)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

// Desk lists a station's stays for the counter: today's and upcoming first.
func (s *Service) Desk(ctx context.Context, code string) ([]Stay, error) {
	return s.list(ctx, `WHERE s.station_code = $1 AND s.status <> 'cancelled' AND s.check_in + make_interval(hours => s.hours) > $2::timestamptz - interval '1 day'
		ORDER BY (s.status = 'completed'), s.check_in LIMIT 100`, code, s.Now())
}

// Advance checks a guest in (collecting a counter payment) or out.
func (s *Service) Advance(ctx context.Context, code string, id int64, action string) (*Stay, error) {
	from, to := map[string]string{"check_in": "booked", "check_out": "checked_in"}[action], map[string]string{"check_in": "checked_in", "check_out": "completed"}[action]
	if from == "" {
		return nil, reject("action must be check_in or check_out")
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var amount int
		var payment string
		err := tx.QueryRow(ctx, `UPDATE station_stays SET status = $3, updated_at = now() WHERE id = $1 AND station_code = $2 AND status = $4
			RETURNING amount, payment`, id, code, to, from).Scan(&amount, &payment)
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("this stay can't be %s now", strings.ReplaceAll(action, "_", " "))
		} else if err != nil {
			return err
		}
		if action == "check_in" && payment == "counter" {
			return ledger.Post(ctx, tx, "stay_counter", fmt.Sprint(id), "Paid at the counter",
				ledger.Line{Account: stationCash(code), Amount: ledger.Paise(amount)},
				ledger.Line{Account: revenueAccount, Amount: -ledger.Paise(amount)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

// InUse counts units occupied right now, for the station's facilities panel.
func (s *Service) InUse(ctx context.Context, code string) (rooms, lockers int, err error) {
	now := s.Now()
	if rooms, err = s.reserved(ctx, s.DB, code, "ac_room", now, 1); err != nil {
		return
	}
	lockers, err = s.reserved(ctx, s.DB, code, "locker", now, 1)
	return
}
