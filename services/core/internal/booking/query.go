package booking

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/fare"
	"github.com/yatrax/yatrax/services/core/internal/inventory"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

type Passenger struct {
	Seq       int    `json:"seq"`
	Name      string `json:"name"`
	Age       int    `json:"age"`
	Gender    string `json:"gender"`
	Pref      string `json:"berth_pref,omitempty"`
	Status    string `json:"status"` // CNF RAC WL
	Coach     string `json:"coach,omitempty"`
	Berth     int    `json:"berth,omitempty"`
	BerthType string `json:"berth_type,omitempty"`
	RACNo     int    `json:"rac_no,omitempty"`
	WLNo      int    `json:"wl_no,omitempty"`
	Fare      int    `json:"fare"`
	// BookedStatus is what the ticket said when issued ("WL 23"); Status/Position say where it stands now.
	BookedStatus string `json:"booked_status,omitempty"`
	Position     int    `json:"position,omitempty"` // current RAC or WL position
	Refund       *int   `json:"refund,omitempty"`
	IDNumber     string `json:"id_number,omitempty"`     // masked: last four only
	UpgradedFrom string `json:"upgraded_from,omitempty"` // the class before an auto-upgrade
}

// Carbon compares the journey's emissions with flying it (kg CO₂ for the whole party).
type Carbon struct {
	TrainKg  int `json:"train_kg"`
	FlightKg int `json:"flight_kg"`
	SavedKg  int `json:"saved_kg"`
}

// Per passenger-km estimates: electric/diesel mix for Indian Railways long-distance trains,
// and a domestic flight with the extra distance planes fly between the same cities.
const (
	trainKgPerKm  = 0.012
	flightKgPerKm = 0.158 * 1.1
)

type RefundInfo struct {
	ID          string     `json:"id"`
	Amount      int        `json:"amount"`
	Destination string     `json:"destination"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

type PaymentInfo struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	Method  string `json:"method,omitempty"`
	Amount  int    `json:"amount"`
}

type Booking struct {
	ID            string       `json:"id"`
	PNR           string       `json:"pnr,omitempty"`
	UserID        string       `json:"-"`
	Status        string       `json:"status"`
	Train         string       `json:"train"`
	TrainName     string       `json:"train_name"`
	From          string       `json:"from"`
	FromName      string       `json:"from_name"`
	To            string       `json:"to"`
	ToName        string       `json:"to_name"`
	JourneyDate   string       `json:"journey_date"`
	OriginDate    string       `json:"origin_date"` // the day the train leaves its first station (for live status)
	Departure     time.Time    `json:"departure"`
	Arrival       time.Time    `json:"arrival"`
	DistanceKm    int          `json:"distance_km"`
	Class         string       `json:"class"`
	ClassName     string       `json:"class_name"`
	Quota         string       `json:"quota"`
	TotalFare     int          `json:"total_fare"`
	Fare          fare.Quote   `json:"fare"`
	HoldExpiresAt *time.Time   `json:"hold_expires_at,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	ConfirmedAt   *time.Time   `json:"confirmed_at,omitempty"`
	Passengers    []Passenger  `json:"passengers"`
	Payment       *PaymentInfo `json:"payment,omitempty"`
	Refunds       []RefundInfo `json:"refunds,omitempty"`
	ChartPrepared bool         `json:"chart_prepared"`
	CancelledAt   *time.Time   `json:"cancelled_at,omitempty"`
	OriginalFrom  string       `json:"original_from,omitempty"`
	TDR           *TDR         `json:"tdr,omitempty"`
	Insurance     int          `json:"insurance_premium"`
	AutoUpgrade   bool         `json:"auto_upgrade"`
	PointsEarned  int          `json:"points_earned"`
	GuaranteeWith string       `json:"guarantee_with,omitempty"` // the other booking of a confirmation guarantee
	Carbon        *Carbon      `json:"carbon,omitempty"`
	FromIdx       int          `json:"-"`
	ToIdx         int          `json:"-"`
	RunID         int64        `json:"-"`
	Source        string       `json:"-"`
}

// decorate fills in names and timetable times from the network.
func (s *Service) decorate(b *Booking) {
	t := s.Net.Trains[b.Train]
	b.ChartPrepared = s.chartPrepared(b.RunID)
	b.TrainName = t.Name
	b.FromName = s.Net.Stations[b.From].Name
	b.ToName = s.Net.Stations[b.To].Name
	b.ClassName = s.Net.Classes[b.Class].Name
	board, _ := time.ParseInLocation(time.DateOnly, b.JourneyDate, network.IST)
	origin := board.AddDate(0, 0, -t.Stops[b.FromIdx].Time()/1440)
	b.Departure = origin.Add(time.Duration(t.Stops[b.FromIdx].Time()) * time.Minute)
	b.Arrival = origin.Add(time.Duration(*t.Stops[b.ToIdx].Arr) * time.Minute)
	b.DistanceKm = t.Stops[b.ToIdx].DistKm - t.Stops[b.FromIdx].DistKm
	if n := len(b.Passengers); n > 0 && b.DistanceKm > 0 {
		pkm := float64(b.DistanceKm * n)
		c := &Carbon{TrainKg: int(pkm*trainKgPerKm + 0.5), FlightKg: int(pkm*flightKgPerKm + 0.5)}
		c.SavedKg = c.FlightKg - c.TrainKg
		b.Carbon = c
	}
}

const bookingCols = `id, coalesce(pnr, ''), user_id, status::text, train_number, from_station, to_station, journey_date,
	class_code, quota, total_fare, fare, hold_expires_at, created_at, confirmed_at, from_idx, to_idx, run_id, source,
	cancelled_at, coalesce(original_from, ''), (SELECT tr.origin_date FROM train_runs tr WHERE tr.id = run_id),
	insurance_premium, coalesce((preferences->>'auto_upgrade')::boolean, false),
	(SELECT coalesce(sum(delta), 0) FROM loyalty_points lp WHERE lp.ref = bookings.id::text AND lp.reason IN ('earned', 'reversed')),
	coalesce((SELECT CASE WHEN g.booking_a = bookings.id THEN g.booking_b ELSE g.booking_a END::text FROM guarantee_pairs g
	          WHERE g.booking_a = bookings.id OR g.booking_b = bookings.id), '')`

func scanBooking(row pgx.Row) (*Booking, error) {
	var b Booking
	var jd, od time.Time
	err := row.Scan(&b.ID, &b.PNR, &b.UserID, &b.Status, &b.Train, &b.From, &b.To, &jd, &b.Class, &b.Quota,
		&b.TotalFare, &b.Fare, &b.HoldExpiresAt, &b.CreatedAt, &b.ConfirmedAt, &b.FromIdx, &b.ToIdx, &b.RunID, &b.Source,
		&b.CancelledAt, &b.OriginalFrom, &od, &b.Insurance, &b.AutoUpgrade, &b.PointsEarned, &b.GuaranteeWith)
	b.JourneyDate, b.OriginDate = jd.Format(time.DateOnly), od.Format(time.DateOnly)
	if b.Status != "HELD" && b.Status != "PAYMENT_PENDING" {
		b.HoldExpiresAt = nil
	}
	return &b, err
}

func (s *Service) loadPassengers(ctx context.Context, b *Booking) error {
	// Current RAC/WL position = how many confirmed passengers are still ahead in the same queue, plus one.
	rows, err := s.DB.Query(ctx, `
		SELECT p.seq, p.name, p.age, p.gender, p.berth_pref, p.status::text, coalesce(p.coach_no, ''), coalesce(p.berth_no, 0),
		       coalesce(p.berth_type, ''), coalesce(p.rac_no, 0), coalesce(p.wl_no, 0), p.fare, coalesce(p.booked_status, ''), p.refund,
		       CASE WHEN p.id_number IS NULL THEN '' ELSE '••••' || right(p.id_number, 4) END, coalesce(p.upgraded_from, ''),
		       CASE WHEN p.status IN ('RAC', 'WL') THEN 1 + (
		           SELECT count(*) FROM booking_passengers q JOIN bookings c ON c.id = q.booking_id
		            WHERE c.run_id = b.run_id AND c.class_code = b.class_code AND c.quota = b.quota AND c.status = 'CONFIRMED'
		              AND q.status = p.status
		              AND (CASE WHEN p.status = 'RAC' THEN q.rac_no < p.rac_no ELSE q.wl_no < p.wl_no END))
		       ELSE 0 END
		  FROM booking_passengers p JOIN bookings b ON b.id = p.booking_id
		 WHERE p.booking_id = $1 ORDER BY p.seq`, b.ID)
	if err != nil {
		return err
	}
	b.Passengers, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Passenger, error) {
		var p Passenger
		err := r.Scan(&p.Seq, &p.Name, &p.Age, &p.Gender, &p.Pref, &p.Status, &p.Coach, &p.Berth, &p.BerthType, &p.RACNo, &p.WLNo, &p.Fare,
			&p.BookedStatus, &p.Refund, &p.IDNumber, &p.UpgradedFrom, &p.Position)
		return p, err
	})
	return err
}

// Get loads a booking with passengers and its latest payment.
func (s *Service) Get(ctx context.Context, id string) (*Booking, error) {
	b, err := scanBooking(s.DB.QueryRow(ctx, `SELECT `+bookingCols+` FROM bookings WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if err := s.loadPassengers(ctx, b); err != nil {
		return nil, err
	}
	var p PaymentInfo
	err = s.DB.QueryRow(ctx, `SELECT order_id, status::text, coalesce(method, ''), amount FROM payments
		WHERE booking_id = $1 ORDER BY created_at DESC LIMIT 1`, id).Scan(&p.OrderID, &p.Status, &p.Method, &p.Amount)
	if err == nil {
		b.Payment = &p
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id, amount, destination, status::text, reason, created_at, processed_at
		FROM refunds WHERE booking_id = $1 ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	b.Refunds, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (RefundInfo, error) {
		var x RefundInfo
		return x, r.Scan(&x.ID, &x.Amount, &x.Destination, &x.Status, &x.Reason, &x.CreatedAt, &x.ProcessedAt)
	})
	if err != nil {
		return nil, err
	}
	if b.TDR, err = s.TDRForBooking(ctx, id); err != nil {
		return nil, err
	}
	s.decorate(b)
	return b, nil
}

// GetFor loads a booking only if it belongs to userID.
func (s *Service) GetFor(ctx context.Context, userID, id string) (*Booking, error) {
	b, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if b.UserID != userID {
		return nil, ErrNotFound
	}
	return b, nil
}

// List returns a user's own bookings, newest first.
func (s *Service) List(ctx context.Context, userID string, limit int) ([]*Booking, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+bookingCols+` FROM bookings
		WHERE user_id = $1 AND source = 'user' AND status <> 'EXPIRED' ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, b := range out {
		if err := s.loadPassengers(ctx, b); err != nil {
			return nil, err
		}
		s.decorate(b)
	}
	return out, nil
}

// PNRStatus is the public view of a ticket: no personal details beyond initials.
func (s *Service) PNRStatus(ctx context.Context, pnr string) (*Booking, error) {
	var id string
	err := s.DB.QueryRow(ctx, `SELECT id FROM bookings WHERE pnr = $1`, strings.TrimSpace(pnr)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	b, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range b.Passengers {
		p := &b.Passengers[i]
		p.Name = initials(p.Name)
		p.Pref = ""
	}
	b.Payment = nil
	b.Refunds = nil
	b.TDR = nil
	return b, nil
}

func initials(name string) string {
	var b strings.Builder
	for _, w := range strings.Fields(name) {
		b.WriteString(strings.ToUpper(w[:1]) + ". ")
	}
	return strings.TrimSpace(b.String())
}

// ── Seat map ───────────────────────────────────────────────────────────────

type SeatState struct {
	Berth  int    `json:"berth"`
	Type   string `json:"type"`
	Bay    int    `json:"bay"`
	Quota  string `json:"quota"`
	RAC    bool   `json:"rac,omitempty"`
	Status string `json:"status"` // free, booked, quota (another quota's berth)
}

type CoachMap struct {
	Coach    string      `json:"coach"`
	Position int         `json:"position"`
	Free     int         `json:"free"`
	Seats    []SeatState `json:"seats"`
}

type SeatMap struct {
	Train   string     `json:"train"`
	Class   string     `json:"class"`
	Quota   string     `json:"quota"`
	From    string     `json:"from"`
	To      string     `json:"to"`
	Layout  string     `json:"layout"`
	Coaches []CoachMap `json:"coaches"`
}

// SeatMap shows which berths are free for a journey. Opening it creates the run.
func (s *Service) SeatMap(ctx context.Context, r HoldRequest) (*SeatMap, error) {
	j, err := s.resolve(r)
	if err != nil {
		return nil, err
	}
	cat, err := s.catalog(j.train, j.class)
	if err != nil {
		return nil, err
	}
	runID, err := s.ensureRun(ctx, j.train, j.originDate, Segment{j.class, j.quota, j.from, j.to})
	if err != nil {
		return nil, err
	}
	if rt := s.runTrain(runID, j.train); rt != j.train { // extra coaches show on the seat map too
		if cat, err = s.catalog(rt, j.class); err != nil {
			return nil, err
		}
	}
	occ, _, err := s.cachedOccupancy(ctx, runID, j.class)
	if err != nil {
		return nil, err
	}
	need := inventory.SegMask(j.from, j.to)
	m := &SeatMap{Train: j.train.Number, Class: j.class, Quota: j.quota, From: j.train.Stops[j.from].Station,
		To: j.train.Stops[j.to].Station, Layout: s.Net.Classes[j.class].Layout}
	idx := map[string]int{}
	for _, u := range cat.Units {
		if u.Slot == 1 {
			continue // show an RAC berth once
		}
		ci, ok := idx[u.Coach]
		if !ok {
			ci = len(m.Coaches)
			idx[u.Coach] = ci
			m.Coaches = append(m.Coaches, CoachMap{Coach: u.Coach, Position: u.CoachPos})
		}
		st := "free"
		switch {
		case occ[u.Key()]&need != 0:
			st = "booked"
		case u.RAC || u.Quota != j.quota:
			st = "quota"
		default:
			m.Coaches[ci].Free++
		}
		m.Coaches[ci].Seats = append(m.Coaches[ci].Seats, SeatState{Berth: u.Berth, Type: u.Type, Bay: u.Bay, Quota: u.Quota, RAC: u.RAC, Status: st})
	}
	return m, nil
}
