package booking

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/fare"
	"github.com/yatrax/yatrax/services/core/internal/inventory"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

type PassengerInput struct {
	Name   string `json:"name"`
	Age    int    `json:"age"`
	Gender string `json:"gender"`
	Pref   string `json:"berth_pref"`
	// Coach and Berth pick a specific berth from the seat map (optional).
	Coach string `json:"coach,omitempty"`
	Berth int    `json:"berth,omitempty"`
	// IDNumber is the UDID card (divyangjan quota), defence warrant, or passport (foreign tourist).
	IDNumber string `json:"id_number,omitempty"`
}

type HoldRequest struct {
	Train         string           `json:"train"`
	From          string           `json:"from"`
	To            string           `json:"to"`
	Date          string           `json:"date"` // boarding date, YYYY-MM-DD
	Class         string           `json:"class"`
	Quota         string           `json:"quota"`
	Passengers    []PassengerInput `json:"passengers"`
	Together      bool             `json:"together"`
	OnlyConfirmed bool             `json:"only_confirmed"`
	AutoUpgrade   bool             `json:"auto_upgrade"` // move to a higher class at no extra cost if berths are free at chart time
	Insurance     bool             `json:"insurance"`    // travel insurance, InsurancePremium per passenger

	group bool // part of an approved group booking: held longer, outside the per-user hold limit
}

// GroupHoldTTL is how long an approved group's berths stay held for the leader to pay.
const GroupHoldTTL = 48 * time.Hour

// InsurancePremium is the travel-insurance premium per passenger, in rupees (cover ₹10 lakh).
const InsurancePremium = 1

// journey is a validated request resolved against the network.
type journey struct {
	train      *network.Train
	from, to   int
	originDate time.Time
	boardDate  time.Time
	class      string
	quota      string
	distKm     int
}

var berthTypes = map[string][]string{
	"sleeper_8": {"LB", "MB", "UB", "SL", "SU"},
	"sleeper_6": {"LB", "UB", "SL", "SU"},
	"sleeper_4": {"LB", "UB"},
	"sitting_6": {"W", "M", "A"},
	"sitting_5": {"W", "M", "A"},
	"sitting_4": {"W", "A"},
}

func (s *Service) resolve(r HoldRequest) (*journey, error) {
	t := s.Net.Trains[r.Train]
	if t == nil {
		return nil, invalid("No such train %s", r.Train)
	}
	from, to := t.StopIndex(strings.ToUpper(r.From)), t.StopIndex(strings.ToUpper(r.To))
	if from < 0 || to < 0 || from >= to {
		return nil, invalid("Train %s does not run from %s to %s", t.Number, r.From, r.To)
	}
	board, err := time.ParseInLocation(time.DateOnly, r.Date, network.IST)
	if err != nil {
		return nil, invalid("Dates look like 2026-11-02")
	}
	class, quota := strings.ToUpper(r.Class), strings.ToUpper(r.Quota)
	if quota == "" {
		quota = "GN"
	}
	if t.Capacity(s.Net, class) == 0 || !s.Net.Classes[class].Bookable {
		return nil, invalid("Train %s has no %s class", t.Number, class)
	}
	if _, ok := fare.Quotas[quota]; !ok || !availability.QuotaAllowed(s.Net, class, quota) {
		return nil, invalid("%s has no %s quota", class, quota)
	}
	return &journey{
		train: t, from: from, to: to, class: class, quota: quota,
		boardDate:  board,
		originDate: board.AddDate(0, 0, -t.Stops[from].Time()/1440),
		distKm:     t.Stops[to].DistKm - t.Stops[from].DistKm,
	}, nil
}

func (s *Service) validatePassengers(j *journey, pax []PassengerInput) error {
	limit := MaxPassengers
	if j.quota == "TQ" || j.quota == "PT" {
		limit = MaxTatkalPax
	}
	if j.quota == "HP" {
		limit = 2 // the passenger and one escort
	}
	if len(pax) == 0 || len(pax) > limit {
		return invalid("A %s booking can have 1 to %d passengers", j.quota, limit)
	}
	types := berthTypes[s.Net.Classes[j.class].Layout]
	women := 0
	for i := range pax {
		p := &pax[i]
		p.Name = strings.TrimSpace(p.Name)
		p.Gender = strings.ToUpper(p.Gender)
		p.Pref = strings.ToUpper(p.Pref)
		switch {
		case len(p.Name) < 2 || len(p.Name) > 40:
			return invalid("Passenger %d needs a name (2–40 characters)", i+1)
		case p.Age < 5 || p.Age > 125:
			return invalid("Passenger %d: age must be 5–125 (children under 5 travel free without a ticket)", i+1)
		case p.Gender != "M" && p.Gender != "F" && p.Gender != "O":
			return invalid("Passenger %d: gender must be M, F or O", i+1)
		case p.Pref != "" && !slices.Contains(types, p.Pref):
			return invalid("Passenger %d: %s has no %s berths", i+1, j.class, p.Pref)
		case (p.Coach == "") != (p.Berth == 0):
			return invalid("Passenger %d: pick both a coach and a berth, or neither", i+1)
		}
		if p.Gender == "F" {
			women++
		}
		if j.quota == "SS" && !(p.Age >= 60 || p.Gender == "F" && p.Age >= 45) {
			return invalid("The senior / lower berth quota is for passengers 60+, or women 45+")
		}
		if j.quota == "LD" && p.Gender != "F" && p.Age >= 12 {
			return invalid("The ladies quota is for women, and boys under 12 travelling with them")
		}
		p.IDNumber = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(p.IDNumber), " ", ""))
		switch j.quota {
		case "HP":
			if i == 0 && !idPattern(p.IDNumber, 10, 20) {
				return invalid("The divyangjan quota needs the passenger's UDID card number (10–20 letters and digits)")
			}
		case "DF":
			if !idPattern(p.IDNumber, 6, 20) {
				return invalid("Passenger %d: the defence quota needs a warrant number", i+1)
			}
		case "FT":
			if !idPattern(p.IDNumber, 6, 9) {
				return invalid("Passenger %d: the foreign tourist quota needs a passport number (6–9 letters and digits)", i+1)
			}
		}
	}
	if j.quota == "LD" && women == 0 {
		return invalid("The ladies quota needs at least one woman in the party")
	}
	if j.quota == "SS" && len(pax) > 2 {
		return invalid("The senior / lower berth quota allows at most 2 passengers")
	}
	return nil
}

func idPattern(v string, lo, hi int) bool {
	if len(v) < lo || len(v) > hi {
		return false
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Hold allocates berths and holds them for HoldTTL while the user pays. Reusing an
// idempotency key returns the original booking instead of making another.
func (s *Service) Hold(ctx context.Context, userID, idemKey string, r HoldRequest) (*Booking, error) {
	if idemKey != "" {
		var id string
		err := s.DB.QueryRow(ctx, `SELECT id FROM bookings WHERE user_id = $1 AND idempotency_key = $2`, userID, idemKey).Scan(&id)
		if err == nil {
			return s.Get(ctx, id)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	j, err := s.resolve(r)
	if err != nil {
		return nil, err
	}
	// Validation tidies names and codes in place: work on our own copy, never the caller's.
	r.Passengers = slices.Clone(r.Passengers)
	if err := s.validatePassengers(j, r.Passengers); err != nil {
		return nil, err
	}
	now := s.Now()
	if (j.quota == "TQ" || j.quota == "PT") && s.TatkalRush != nil && s.TatkalRush(j.originDate, s.Net.Classes[j.class].AC) {
		return nil, ErrUseWaitingRoom
	}
	gateReq := availability.Request{Train: j.train, OriginDate: j.originDate, Class: j.class, Quota: j.quota, FromIdx: j.from, ToIdx: j.to}
	if st, ok := availability.Gate(s.Net, gateReq, now); !ok {
		return nil, ValidationError{"Booking is not open: " + st.Label}
	}
	var active int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE user_id = $1 AND status IN ('HELD', 'PAYMENT_PENDING') AND hold_expires_at > $2`,
		userID, now).Scan(&active); err != nil {
		return nil, err
	}
	if active >= MaxActiveHolds && !r.group {
		return nil, ErrTooManyHolds
	}
	ttl := s.HoldTTL
	if r.group {
		ttl = GroupHoldTTL
	}
	quote, err := fare.Compute(s.Net, j.train.Type, j.class, j.distKm, j.quota)
	if err != nil {
		return nil, invalid("%v", err)
	}
	cat, err := s.catalog(j.train, j.class)
	if err != nil {
		return nil, err
	}
	// Match the run to what search showed for this journey, unless the passenger
	// picked berths on the seat map: opening the map already did that, and background
	// demand set off by their own booking must never take a berth they chose.
	var focus []Segment
	if !slices.ContainsFunc(r.Passengers, func(p PassengerInput) bool { return p.Coach != "" }) {
		focus = append(focus, Segment{j.class, j.quota, j.from, j.to})
	}
	runID, err := s.ensureRun(ctx, j.train, j.originDate, focus...)
	if err != nil {
		return nil, err
	}
	if err := s.checkDisruption(runID, j.train, j.from, j.to); err != nil {
		return nil, err
	}
	if rt := s.runTrain(runID, j.train); rt != j.train { // this run has extra coaches
		if cat, err = s.catalog(rt, j.class); err != nil {
			return nil, err
		}
	}

	// After the chart, unsold quota berths are open to everyone.
	req := inventory.Request{Quota: j.quota, From: j.from, To: j.to, Together: r.Together, AnyQuota: s.chartPrepared(runID)}
	for _, p := range r.Passengers {
		ip := inventory.Passenger{Age: p.Age, Gender: p.Gender, Pref: p.Pref}
		if p.Coach != "" {
			ip.Want = &inventory.Key{Coach: strings.ToUpper(p.Coach), Berth: p.Berth}
		}
		req.Pax = append(req.Pax, ip)
	}

	var bookingID string
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, runID, j.class); err != nil {
			return err
		}
		if _, err := s.expireStale(ctx, tx, runID, j.class); err != nil {
			return err
		}
		occ, queue, err := loadOccupancy(ctx, tx, runID, j.class)
		if err != nil {
			return err
		}
		placements, err := cat.Allocate(occ, req)
		if errors.Is(err, inventory.ErrBerthTaken) {
			return ErrConflict
		} else if errors.Is(err, inventory.ErrBadBerth) {
			return invalid("That berth can't be booked in the %s quota", j.quota)
		} else if err != nil {
			return err
		}
		nRAC, nWL := 0, 0
		for _, p := range placements {
			switch p.Status {
			case inventory.RAC:
				nRAC++
			case inventory.WL:
				nWL++
			}
		}
		if s.chartPrepared(runID) && nRAC+nWL > 0 {
			return ErrChartPrepared // after the chart only free berths are sold ("current booking")
		}
		if r.OnlyConfirmed && nRAC+nWL > 0 {
			return ErrNotConfirmed
		}
		capacity := float64(cat.Capacity(j.quota))
		if nWL > 0 && queue[j.quota].wl+nWL > availability.WaitlistLimit(j.quota, capacity) {
			return ErrRegret
		}
		prefs, _ := json.Marshal(map[string]bool{"together": r.Together, "only_confirmed": r.OnlyConfirmed, "auto_upgrade": r.AutoUpgrade})
		premium := 0
		if r.Insurance {
			premium = InsurancePremium * len(placements)
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO bookings (user_id, run_id, train_number, journey_date, from_station, to_station, from_idx, to_idx,
			                      class_code, quota, status, source, total_fare, fare, preferences, hold_expires_at, idempotency_key, insurance_premium)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'HELD','user',$11,$12,$13,$14,nullif($15,''),$16)
			RETURNING id`,
			userID, runID, j.train.Number, j.boardDate, j.train.Stops[j.from].Station, j.train.Stops[j.to].Station,
			j.from, j.to, j.class, j.quota, quote.Total*len(placements)+premium, quote, prefs, now.Add(ttl), idemKey, premium).Scan(&bookingID)
		if err != nil {
			return err
		}
		return s.insertPassengers(ctx, tx, runID, j, bookingID, r.Passengers, placements, quote.Total, nRAC, nWL)
	})
	s.invalidate(runID, j.class)
	switch {
	case isExclusionViolation(err):
		return nil, ErrConflict
	case isUniqueViolation(err) && idemKey != "":
		// A concurrent request with the same key won the race; return its booking.
		return s.Hold(ctx, userID, idemKey, r)
	case err != nil:
		return nil, err
	}
	return s.Get(ctx, bookingID)
}

// insertPassengers stores passengers and their allocations, numbering RAC and WL in order.
func (s *Service) insertPassengers(ctx context.Context, tx pgx.Tx, runID int64, j *journey, bookingID string,
	pax []PassengerInput, placements []inventory.Placement, farePerHead, nRAC, nWL int) error {
	racNo, wlNo := 0, 0
	var err error
	if nRAC > 0 {
		if racNo, err = nextNumbers(ctx, tx, runID, j.class, j.quota, "RAC", nRAC); err != nil {
			return err
		}
	}
	if nWL > 0 {
		if wlNo, err = nextNumbers(ctx, tx, runID, j.class, j.quota, "WL", nWL); err != nil {
			return err
		}
	}
	for i, pl := range placements {
		p := pax[i]
		var coach, btype *string
		var berth, rac, wl *int
		switch pl.Status {
		case inventory.RAC:
			n := racNo
			racNo++
			rac = &n
		case inventory.WL:
			n := wlNo
			wlNo++
			wl = &n
		}
		if pl.Unit != nil {
			coach, berth, btype = &pl.Unit.Coach, &pl.Unit.Berth, &pl.Unit.Type
		}
		var pid string
		if err := tx.QueryRow(ctx, `
			INSERT INTO booking_passengers (booking_id, seq, name, age, gender, berth_pref, status, coach_no, berth_no, berth_type, rac_no, wl_no, fare, id_number)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,nullif($14,'')) RETURNING id`,
			bookingID, i+1, p.Name, p.Age, p.Gender, p.Pref, pl.Status, coach, berth, btype, rac, wl, farePerHead, p.IDNumber).Scan(&pid); err != nil {
			return err
		}
		if pl.Unit != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO seat_allocations (run_id, class_code, coach_no, berth_no, slot, seg, booking_id, passenger_id)
				VALUES ($1,$2,$3,$4,$5,int4range($6,$7),$8,$9)`,
				runID, j.class, pl.Unit.Coach, pl.Unit.Berth, pl.Unit.Slot, j.from, j.to, bookingID, pid); err != nil {
				return err
			}
		}
	}
	return outbox(ctx, tx, "booking.events", bookingID, map[string]any{
		"type": "booking.held", "booking_id": bookingID, "train": j.train.Number, "class": j.class, "quota": j.quota,
		"passengers": len(pax), "rac": nRAC, "wl": nWL,
	})
}

// Release gives up a hold before paying.
func (s *Service) Release(ctx context.Context, userID, bookingID string) error {
	var runID int64
	var class string
	if err := s.DB.QueryRow(ctx, `SELECT run_id, class_code FROM bookings WHERE id = $1 AND user_id = $2`, bookingID, userID).
		Scan(&runID, &class); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, runID, class); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE bookings SET status = 'EXPIRED', updated_at = now()
			WHERE id = $1 AND status IN ('HELD', 'PAYMENT_PENDING')`, bookingID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotHeld
		}
		if _, err := tx.Exec(ctx, `DELETE FROM seat_allocations WHERE booking_id = $1`, bookingID); err != nil {
			return err
		}
		return outbox(ctx, tx, "booking.events", bookingID, map[string]any{"type": "booking.released", "booking_id": bookingID})
	})
	s.invalidate(runID, class)
	return err
}

// ExpireNow ends a hold immediately (development tool for testing late payments).
func (s *Service) ExpireNow(ctx context.Context, bookingID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE bookings SET hold_expires_at = $2
		WHERE id = $1 AND status IN ('HELD', 'PAYMENT_PENDING')`, bookingID, s.Now().Add(-time.Second))
	return err
}

func outbox(ctx context.Context, tx pgx.Tx, topic, key string, payload map[string]any) error {
	_, err := tx.Exec(ctx, `INSERT INTO outbox (topic, key, payload) VALUES ($1, $2, $3)`, topic, key, payload)
	return err
}

// JourneyOrigin returns the day a requested journey's train leaves its origin, and
// the class code, after validating the train, stations and class.
func (s *Service) JourneyOrigin(r HoldRequest) (time.Time, string, error) {
	j, err := s.resolve(r)
	if err != nil {
		return time.Time{}, "", err
	}
	return j.originDate, j.class, nil
}
