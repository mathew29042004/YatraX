package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The group booking desk: weddings, pilgrimages and school trips need more berths than one
// booking allows. The leader asks; the commercial desk at the boarding station approves, and
// the berths are held together (in parties of up to six) for 48 hours while the leader pays.

const (
	MinGroup = 7
	MaxGroup = 60
)

type GroupRequest struct {
	Train      string           `json:"train"`
	From       string           `json:"from"`
	To         string           `json:"to"`
	Date       string           `json:"date"`
	Class      string           `json:"class"`
	Purpose    string           `json:"purpose"`
	Contact    string           `json:"contact"`
	Passengers []PassengerInput `json:"passengers"`
}

type Group struct {
	ID         int64            `json:"id"`
	Leader     string           `json:"leader"`
	Train      string           `json:"train"`
	TrainName  string           `json:"train_name"`
	From       string           `json:"from"`
	To         string           `json:"to"`
	Date       string           `json:"date"`
	Class      string           `json:"class"`
	Purpose    string           `json:"purpose"`
	Contact    string           `json:"contact"`
	Passengers []PassengerInput `json:"passengers"`
	Status     string           `json:"status"`
	Note       string           `json:"note,omitempty"`
	Bookings   []string         `json:"bookings"`
	DecidedBy  string           `json:"decided_by,omitempty"`
	DecidedAt  *time.Time       `json:"decided_at,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
}

var GroupPurposes = []string{"wedding", "pilgrimage", "school trip", "sports team", "office trip", "other"}

func (s *Service) RequestGroup(ctx context.Context, userID string, g GroupRequest) (*Group, error) {
	g.Purpose = strings.ToLower(strings.TrimSpace(g.Purpose))
	g.Contact = strings.TrimSpace(g.Contact)
	if n := len(g.Passengers); n < MinGroup || n > MaxGroup {
		return nil, invalid("A group is %d to %d passengers; for fewer, book normally", MinGroup, MaxGroup)
	}
	if !slices.Contains(GroupPurposes, g.Purpose) {
		return nil, invalid("Purpose must be one of: %s", strings.Join(GroupPurposes, ", "))
	}
	if len(g.Contact) < 10 {
		return nil, invalid("Give a contact phone number")
	}
	j, err := s.resolve(HoldRequest{Train: g.Train, From: g.From, To: g.To, Date: g.Date, Class: g.Class, Quota: "GN"})
	if err != nil {
		return nil, err
	}
	for i, p := range g.Passengers {
		if strings.TrimSpace(p.Name) == "" || p.Age < 5 || p.Age > 125 {
			return nil, invalid("Passenger %d needs a name and an age of 5–125", i+1)
		}
	}
	pax, _ := json.Marshal(g.Passengers)
	var id int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO group_requests (user_id, train_number, from_station, to_station, journey_date, class_code, purpose, contact, passengers)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, userID, j.train.Number, j.train.Stops[j.from].Station, j.train.Stops[j.to].Station,
		j.boardDate, j.class, g.Purpose, g.Contact, pax).Scan(&id); err != nil {
		return nil, err
	}
	return s.group(ctx, id)
}

const groupCols = `g.id, u.full_name, g.train_number, t.name, g.from_station, g.to_station, g.journey_date::text, g.class_code, g.purpose, g.contact,
	g.passengers, g.status, g.note, g.bookings::text[], coalesce(d.full_name, ''), g.decided_at, g.created_at
	FROM group_requests g JOIN users u ON u.id = g.user_id JOIN trains t ON t.number = g.train_number LEFT JOIN users d ON d.id = g.decided_by `

func (s *Service) groups(ctx context.Context, where string, args ...any) ([]Group, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+groupCols+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var pax []byte
		if err := rows.Scan(&g.ID, &g.Leader, &g.Train, &g.TrainName, &g.From, &g.To, &g.Date, &g.Class, &g.Purpose, &g.Contact,
			&pax, &g.Status, &g.Note, &g.Bookings, &g.DecidedBy, &g.DecidedAt, &g.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal(pax, &g.Passengers)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Service) group(ctx context.Context, id int64) (*Group, error) {
	list, err := s.groups(ctx, `WHERE g.id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (s *Service) MyGroups(ctx context.Context, userID string) ([]Group, error) {
	return s.groups(ctx, `WHERE g.user_id = $1 ORDER BY g.id DESC`, userID)
}

// GroupQueue is the commercial desk's list: a station's own departures, or everything.
func (s *Service) GroupQueue(ctx context.Context, station string) ([]Group, error) {
	return s.groups(ctx, `WHERE ($1 = '' OR g.from_station = $1) ORDER BY (g.status <> 'pending'), g.journey_date, g.id LIMIT 100`, station)
}

// DecideGroup approves (holding the berths) or rejects a request.
func (s *Service) DecideGroup(ctx context.Context, staffID string, id int64, station string, approve bool, note string) (*Group, error) {
	g, err := s.group(ctx, id)
	if err != nil {
		return nil, err
	}
	if station != "" && g.From != station {
		return nil, ErrNotFound
	}
	if g.Status != "pending" {
		return nil, invalid("This request was already %s", g.Status)
	}
	note = strings.TrimSpace(note)
	if !approve {
		if _, err := s.DB.Exec(ctx, `UPDATE group_requests SET status = 'rejected', note = $2, decided_by = $3, decided_at = now() WHERE id = $1 AND status = 'pending'`,
			id, note, staffID); err != nil {
			return nil, err
		}
		var leader string
		s.DB.QueryRow(ctx, `SELECT user_id FROM group_requests WHERE id = $1`, id).Scan(&leader)
		s.notify(ctx, leader, "group", "Group request not approved", note, "/groups", fmt.Sprintf("group-rejected:%d", id))
		return s.group(ctx, id)
	}
	var leader string
	if err := s.DB.QueryRow(ctx, `SELECT user_id FROM group_requests WHERE id = $1`, id).Scan(&leader); err != nil {
		return nil, err
	}
	// Parties of up to six, seated together; all confirmed, or nothing is held.
	var held []string
	release := func() {
		for _, b := range held {
			s.Release(context.Background(), leader, b)
		}
	}
	for start := 0; start < len(g.Passengers); start += MaxPassengers {
		end := min(start+MaxPassengers, len(g.Passengers))
		b, err := s.Hold(ctx, leader, fmt.Sprintf("group-%d-%d", id, start), HoldRequest{Train: g.Train, From: g.From, To: g.To, Date: g.Date,
			Class: g.Class, Quota: "GN", Passengers: g.Passengers[start:end], Together: true, OnlyConfirmed: true, group: true})
		if err != nil {
			release()
			var v ValidationError
			if errors.Is(err, ErrNotConfirmed) || errors.As(err, &v) {
				return nil, invalid("Not enough confirmed berths for the whole group (%d of %d held): %v", start, len(g.Passengers), err)
			}
			return nil, err
		}
		held = append(held, b.ID)
	}
	tag, err := s.DB.Exec(ctx, `UPDATE group_requests SET status = 'approved', note = $2, bookings = $3, decided_by = $4, decided_at = now()
		WHERE id = $1 AND status = 'pending'`, id, note, held, staffID)
	if err != nil || tag.RowsAffected() == 0 {
		release()
		if err == nil {
			err = invalid("This request was decided by someone else")
		}
		return nil, err
	}
	s.notify(ctx, leader, "group", fmt.Sprintf("Group of %d approved", len(g.Passengers)),
		fmt.Sprintf("%s %s · %s · berths held for 48 hours: pay for each booking from My trips", g.Train, g.TrainName, g.Date), "/groups", fmt.Sprintf("group:%d", id))
	return s.group(ctx, id)
}

// CancelGroup withdraws a request still waiting for approval.
func (s *Service) CancelGroup(ctx context.Context, userID string, id int64) (*Group, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE group_requests SET status = 'cancelled' WHERE id = $1 AND user_id = $2 AND status = 'pending'`, id, userID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, invalid("Only a request still waiting for approval can be withdrawn")
	}
	return s.group(ctx, id)
}
