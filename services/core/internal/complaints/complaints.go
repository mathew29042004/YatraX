// Package complaints is the RailMadad desk: passengers and TTEs report problems on a train,
// each is routed by rule to the department that can act (housekeeping, catering, RPF,
// medical…) at the next halt, and staff acknowledge and resolve it — so the dashboards can
// show how long that took. It also handles the passenger's SOS: a medical emergency, a
// security alert to the RPF, or asking for a female TTE.
package complaints

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrNotAllowed = errors.New("not allowed")
)

func reject(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNotAllowed, fmt.Sprintf(format, a...))
}

// Categories passengers can choose; TTEs and the catering desk use the same list.
var Categories = []string{"cleanliness", "food", "water", "electrical", "staff", "security", "medical", "women_safety", "other"}

type Service struct {
	DB   *pgxpool.Pool
	Net  *network.Network
	Live *live.Engine
	Now  func() time.Time // railway time
}

type Complaint struct {
	ID          int64      `json:"id"`
	PNR         string     `json:"pnr,omitempty"`
	BookingID   string     `json:"booking_id,omitempty"`
	Train       string     `json:"train,omitempty"`
	TrainName   string     `json:"train_name,omitempty"`
	OriginDate  string     `json:"origin_date,omitempty"`
	Coach       string     `json:"coach,omitempty"`
	Berth       int        `json:"berth,omitempty"`
	Category    string     `json:"category"`
	Description string     `json:"description"`
	Source      string     `json:"source"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	Department  string     `json:"department"`
	Station     string     `json:"station,omitempty"`
	StationName string     `json:"station_name,omitempty"`
	ReportedBy  string     `json:"reported_by,omitempty"`
	AssignedTo  string     `json:"assigned_to,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	AckAt       *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	Resolution  string     `json:"resolution,omitempty"`
	ResolvedBy  string     `json:"resolved_by,omitempty"`
}

const selectComplaint = `SELECT c.id, coalesce(b.pnr, ''), coalesce(c.booking_id::text, ''), coalesce(r.train_number, ''), coalesce(t.name, ''),
	coalesce(r.origin_date::text, ''), coalesce(c.coach_no, ''), coalesce(c.berth_no, 0), c.category, c.description, c.source, c.status,
	c.priority, c.department, coalesce(c.station_code, ''), coalesce(st.name, ''), coalesce(u.full_name, ''), coalesce(a.full_name, ''),
	c.created_at, c.acknowledged_at, c.resolved_at, coalesce(c.resolution, ''), coalesce(rb.full_name, '')
	FROM complaints c
	LEFT JOIN train_runs r ON r.id = c.run_id LEFT JOIN trains t ON t.number = r.train_number
	LEFT JOIN bookings b ON b.id = c.booking_id LEFT JOIN stations st ON st.code = c.station_code
	LEFT JOIN users u ON u.id = c.reported_by LEFT JOIN users a ON a.id = c.assigned_to LEFT JOIN users rb ON rb.id = c.resolved_by `

func (s *Service) list(ctx context.Context, where string, args ...any) ([]Complaint, error) {
	rows, err := s.DB.Query(ctx, selectComplaint+where, args...)
	if err != nil {
		return nil, err
	}
	out := []Complaint{}
	for rows.Next() {
		var c Complaint
		if err := rows.Scan(&c.ID, &c.PNR, &c.BookingID, &c.Train, &c.TrainName, &c.OriginDate, &c.Coach, &c.Berth, &c.Category, &c.Description,
			&c.Source, &c.Status, &c.Priority, &c.Department, &c.Station, &c.StationName, &c.ReportedBy, &c.AssignedTo,
			&c.CreatedAt, &c.AckAt, &c.ResolvedAt, &c.Resolution, &c.ResolvedBy); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) get(ctx context.Context, id int64) (*Complaint, error) {
	list, err := s.list(ctx, `WHERE c.id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// trip is what a complaint needs to know about the passenger's booking.
type trip struct {
	runID          int64
	train          *network.Train
	origin         time.Time
	from, to       int
	coach          string
	berth          int
	pnr, passenger string
}

func (s *Service) trip(ctx context.Context, userID, bookingID string) (*trip, error) {
	var t trip
	var num string
	var origin time.Time
	err := s.DB.QueryRow(ctx, `SELECT b.run_id, b.train_number, r.origin_date, b.from_idx, b.to_idx, coalesce(b.pnr, ''),
			coalesce((SELECT coach_no FROM booking_passengers WHERE booking_id = b.id AND coach_no IS NOT NULL ORDER BY seq LIMIT 1), ''),
			coalesce((SELECT berth_no FROM booking_passengers WHERE booking_id = b.id AND berth_no IS NOT NULL ORDER BY seq LIMIT 1), 0),
			(SELECT name FROM booking_passengers WHERE booking_id = b.id ORDER BY seq LIMIT 1)
		FROM bookings b JOIN train_runs r ON r.id = b.run_id
		WHERE b.id = $1 AND b.user_id = $2 AND b.status = 'CONFIRMED'`, bookingID, userID).
		Scan(&t.runID, &num, &origin, &t.from, &t.to, &t.pnr, &t.coach, &t.berth, &t.passenger)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	t.train = s.Net.Trains[num]
	if t.train == nil {
		return nil, ErrNotFound
	}
	t.origin = live.Midnight(time.Date(origin.Year(), origin.Month(), origin.Day(), 12, 0, 0, 0, network.IST))
	return &t, nil
}

// nextHalt is where help can board: the train's next stop if it's moving, the station it's
// standing at, or the passenger's boarding station before departure.
func (s *Service) nextHalt(t *trip) (string, live.Status) {
	st := s.Live.Status(t.train, t.origin, s.Now())
	idx := t.from
	switch st.State {
	case "at_station":
		idx = max(st.LastIdx, t.from)
	case "running":
		idx = max(st.NextIdx, t.from)
	case "arrived":
		idx = t.to
	}
	return t.train.Stops[min(idx, t.to)].Station, st
}

func (s *Service) stationName(code string) string {
	if st := s.Net.Stations[code]; st != nil {
		return st.Name
	}
	return code
}

// File records a passenger's complaint about their journey.
func (s *Service) File(ctx context.Context, userID, bookingID, category, description, coach string, berth int) (*Complaint, error) {
	category = strings.ToLower(strings.TrimSpace(category))
	description = strings.TrimSpace(description)
	if !slices.Contains(Categories, category) {
		return nil, reject("pick one of: %s", strings.Join(Categories, ", "))
	}
	if len(description) < 5 || len(description) > 1000 {
		return nil, reject("describe the problem in 5–1000 characters")
	}
	t, err := s.trip(ctx, userID, bookingID)
	if err != nil {
		return nil, err
	}
	if coach == "" {
		coach, berth = t.coach, t.berth
	}
	station, _ := s.nextHalt(t)
	var id int64
	err = s.DB.QueryRow(ctx, `INSERT INTO complaints (run_id, booking_id, coach_no, berth_no, category, description, reported_by, source, station_code)
		VALUES ($1, $2, nullif($3, ''), nullif($4, 0), $5, $6, $7, 'passenger', $8) RETURNING id`,
		t.runID, bookingID, coach, berth, category, description, userID, station).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.get(ctx, id)
}

type SOSResult struct {
	Complaint *Complaint `json:"complaint"`
	Message   string     `json:"message"`
	Helpline  string     `json:"helpline"`
}

// SOS raises an urgent alert for the passenger's coach and berth at the next halt. For a
// female TTE it finds the nearest one on duty in the zone and asks her to attend.
func (s *Service) SOS(ctx context.Context, userID, bookingID, kind string) (*SOSResult, error) {
	t, err := s.trip(ctx, userID, bookingID)
	if err != nil {
		return nil, err
	}
	station, st := s.nextHalt(t)
	if st.State == "arrived" || st.State == "not_running" || st.State == "cancelled" {
		return nil, reject("this train isn't running now; for help call 139")
	}
	where := "your coach"
	if t.coach != "" {
		where = fmt.Sprintf("%s/%d", t.coach, t.berth)
	}
	var category, text, helpline, msg string
	switch kind {
	case "medical":
		category, helpline = "medical", "139 (railway helpline) or 108 (ambulance)"
		text = "Medical emergency: " + t.passenger + " at " + where
		msg = fmt.Sprintf("A doctor and first-aid team will meet the train at %s. The TTE has been told.", s.stationName(station))
	case "security":
		category, helpline = "security", "139 (RPF security helpline)"
		text = "Security alert at " + where
		msg = fmt.Sprintf("The RPF at %s has been alerted and will board there. The TTE has been told.", s.stationName(station))
	case "female_tte":
		category, helpline = "women_safety", "139 (RPF) · 1091 (women's helpline)"
		text = "Passenger asked for a female TTE at " + where
	default:
		return nil, reject("kind must be medical, security or female_tte")
	}

	var assigned *string
	if kind == "female_tte" {
		zone := ""
		if sn := s.Net.Stations[station]; sn != nil {
			zone = sn.Zone
		}
		var id, name, emp string
		err := s.DB.QueryRow(ctx, `SELECT u.id, u.full_name, coalesce(u.employee_id, '') FROM users u JOIN staff_postings p ON p.user_id = u.id
			WHERE u.role = 'tte' AND u.gender = 'F' AND NOT u.disabled AND p.zone_code = $1
			ORDER BY (p.station_code = $2) DESC, (SELECT count(*) FROM complaints c WHERE c.assigned_to = u.id AND c.status <> 'resolved'), u.employee_id
			LIMIT 1`, zone, station).Scan(&id, &name, &emp)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			msg = "No female TTE is on duty in this zone right now; the RPF has been alerted instead."
		case err != nil:
			return nil, err
		default:
			assigned = &id
			msg = fmt.Sprintf("%s (%s), a female TTE, has been asked to come to %s; she will board by %s at the latest. The RPF has been told.",
				name, emp, where, s.stationName(station))
		}
	}

	var cid int64
	err = s.DB.QueryRow(ctx, `INSERT INTO complaints (run_id, booking_id, coach_no, berth_no, category, description, reported_by, source, station_code, assigned_to)
		VALUES ($1, $2, nullif($3, ''), nullif($4, 0), $5, $6, $7, 'sos', $8, $9) RETURNING id`,
		t.runID, bookingID, t.coach, t.berth, category, text, userID, station, assigned).Scan(&cid)
	if err != nil {
		return nil, err
	}
	if assigned != nil {
		s.DB.Exec(ctx, `INSERT INTO notifications (user_id, kind, title, body, link, dedupe_key) VALUES ($1, 'sos', $2, $3, '', $4)
			ON CONFLICT (dedupe_key) DO NOTHING`, *assigned, "Passenger needs you at "+where,
			fmt.Sprintf("%s %s · PNR %s · board by %s", t.train.Number, t.train.Name, t.pnr, s.stationName(station)), fmt.Sprintf("sos:%d", cid))
	}
	c, err := s.get(ctx, cid)
	if err != nil {
		return nil, err
	}
	return &SOSResult{Complaint: c, Message: msg, Helpline: helpline}, nil
}

// Mine lists a passenger's complaints, newest first.
func (s *Service) Mine(ctx context.Context, userID, bookingID string) ([]Complaint, error) {
	return s.list(ctx, `WHERE c.reported_by = $1 AND ($2 = '' OR c.booking_id::text = $2) ORDER BY c.id DESC LIMIT 50`, userID, bookingID)
}

// Queue lists complaints for staff: open ones first (urgent first), then recently resolved.
// station narrows it to one station's (a station master's) queue.
func (s *Service) Queue(ctx context.Context, station string, includeResolved bool) ([]Complaint, error) {
	return s.list(ctx, `WHERE ($1 = '' OR c.station_code = $1) AND ($2 OR c.status <> 'resolved')
		ORDER BY (c.status = 'resolved'), CASE c.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 ELSE 2 END, c.created_at DESC LIMIT 200`,
		station, includeResolved)
}

// Acknowledge marks a complaint as being handled.
func (s *Service) Acknowledge(ctx context.Context, staffID string, id int64, station string) (*Complaint, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE complaints SET status = 'in_progress', acknowledged_at = now(), acknowledged_by = $2
		WHERE id = $1 AND status = 'open' AND ($3 = '' OR station_code = $3)`, id, staffID, station)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, reject("only an open complaint at your station can be acknowledged")
	}
	return s.get(ctx, id)
}

// Resolve closes a complaint with what was done; the reporter is told.
func (s *Service) Resolve(ctx context.Context, staffID string, id int64, station, resolution string) (*Complaint, error) {
	resolution = strings.TrimSpace(resolution)
	if len(resolution) < 3 {
		return nil, reject("say what was done")
	}
	var reporter *string
	var source string
	err := s.DB.QueryRow(ctx, `UPDATE complaints SET status = 'resolved', resolved_at = now(), resolved_by = $2, resolution = $4,
			acknowledged_at = coalesce(acknowledged_at, now()), acknowledged_by = coalesce(acknowledged_by, $2)
		WHERE id = $1 AND status <> 'resolved' AND ($3 = '' OR station_code = $3) RETURNING reported_by, source`,
		id, staffID, station, resolution).Scan(&reporter, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, reject("only an unresolved complaint at your station can be resolved")
	} else if err != nil {
		return nil, err
	}
	if reporter != nil && (source == "passenger" || source == "sos") {
		s.DB.Exec(ctx, `INSERT INTO notifications (user_id, kind, title, body, link, dedupe_key) VALUES ($1, 'complaint', $2, $3, '', $4)
			ON CONFLICT (dedupe_key) DO NOTHING`, *reporter, fmt.Sprintf("Complaint #%d resolved", id), resolution, fmt.Sprintf("complaint-resolved:%d", id))
	}
	return s.get(ctx, id)
}
