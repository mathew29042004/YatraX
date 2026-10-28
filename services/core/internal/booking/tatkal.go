package booking

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/fare"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// TatkalDailyLimit caps Tatkal bookings per user per day.
const TatkalDailyLimit = 2

var (
	ErrTatkalSoldOut  = errors.New("Tatkal berths for this journey are sold out")
	ErrTatkalLimit    = errors.New("you have reached the limit of 2 Tatkal bookings today")
	ErrUseWaitingRoom = errors.New("Tatkal is in its opening rush: join the waiting room to book")
	ErrNotTatkalQuota = errors.New("the fast path is only for Tatkal and Premium Tatkal")
	tatkalUnitsReady  sync.Map // "run|class" -> true once tatkal_units rows exist
)

// TatkalOptions carries what the waiting room vouches for.
type TatkalOptions struct {
	// RoomOpened: the passenger was admitted from a waiting room that has opened
	// for this journey date, so the clock-based Tatkal opening check is satisfied.
	RoomOpened bool
}

// TatkalHold is the high-concurrency booking path used by the waiting room. Instead
// of one lock per train run and class, each buyer locks individual Tatkal berth rows
// with FOR UPDATE SKIP LOCKED, so concurrent buyers simply take different berths. It
// confirms whole parties or fails (Tatkal has no waitlist here); the exclusion
// constraint remains the final guard against double sales.
func (s *Service) TatkalHold(ctx context.Context, userID, idemKey string, r HoldRequest, opts TatkalOptions) (*Booking, error) {
	start := time.Now()
	defer func() { metrics.TatkalHoldLatency.Observe(time.Since(start).Seconds()) }()
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
	if j.quota != "TQ" && j.quota != "PT" {
		return nil, ErrNotTatkalQuota
	}
	r.Passengers = slices.Clone(r.Passengers) // validation tidies them in place
	if err := s.validatePassengers(j, r.Passengers); err != nil {
		return nil, err
	}
	now := s.Now()
	gate := availability.Request{Train: j.train, OriginDate: j.originDate, Class: j.class, Quota: j.quota, FromIdx: j.from, ToIdx: j.to}
	if st, ok := availability.Gate(s.Net, gate, now); !ok && !(opts.RoomOpened && st.Code == availability.TatkalNotOpen) {
		return nil, ValidationError{"Booking is not open: " + st.Label}
	}
	var today int
	dayStart := availability.Midnight(now)
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE user_id = $1 AND quota IN ('TQ', 'PT')
		AND created_at >= $2 AND status IN ('HELD', 'PAYMENT_PENDING', 'CONFIRMED')`, userID, dayStart).Scan(&today); err != nil {
		return nil, err
	}
	if today >= TatkalDailyLimit {
		return nil, ErrTatkalLimit
	}
	quote, err := fare.Compute(s.Net, j.train.Type, j.class, j.distKm, j.quota)
	if err != nil {
		return nil, invalid("%v", err)
	}
	runID, err := s.ensureRun(ctx, j.train, j.originDate)
	if err != nil {
		return nil, err
	}
	if err := s.checkDisruption(runID, j.train, j.from, j.to); err != nil {
		return nil, err
	}
	if err := s.ensureTatkalUnits(ctx, runID, j.train, j.class); err != nil {
		return nil, err
	}

	var bookingID string
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		// Pick free Tatkal berths for this journey, skipping any another buyer is
		// taking right now. Preferred berth types first, then front coaches.
		//
		// The "is it free?" test runs against the snapshot taken when the statement
		// started, so a berth another buyer committed (and unlocked) a moment later can
		// slip through. Once our locks are held nobody else can take these rows, so a
		// second statement — which sees every commit before our locks — re-checks them,
		// and we pick again for any that turned out to be taken.
		type unit struct {
			coach string
			berth int
			typ   string
		}
		var units []unit
		exclude := []string{}
		for attempt := 0; attempt < 6 && len(units) < len(r.Passengers); attempt++ {
			rows, err := tx.Query(ctx, `
				SELECT u.coach_no, u.berth_no, u.berth_type FROM tatkal_units u
				 WHERE u.run_id = $1 AND u.class_code = $2 AND u.quota = $3
				   AND NOT (u.coach_no || '/' || u.berth_no = ANY($8::text[]))
				   AND NOT EXISTS (SELECT 1 FROM seat_allocations a
				                    WHERE a.run_id = u.run_id AND a.coach_no = u.coach_no AND a.berth_no = u.berth_no
				                      AND a.slot = 0 AND a.seg && int4range($4, $5))
				 ORDER BY (u.berth_type = $6) DESC, u.coach_pos, u.berth_no
				 LIMIT $7
				 FOR UPDATE OF u SKIP LOCKED`,
				runID, j.class, j.quota, j.from, j.to, r.Passengers[0].Pref, len(r.Passengers)-len(units), exclude)
			if err != nil {
				return err
			}
			picked, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (unit, error) {
				var u unit
				return u, r.Scan(&u.coach, &u.berth, &u.typ)
			})
			if err != nil {
				return err
			}
			if len(picked) == 0 {
				break
			}
			for _, u := range picked {
				var taken bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seat_allocations
					WHERE run_id = $1 AND coach_no = $2 AND berth_no = $3 AND slot = 0 AND seg && int4range($4, $5))`,
					runID, u.coach, u.berth, j.from, j.to).Scan(&taken); err != nil {
					return err
				}
				// Exclude it from further picks either way: SKIP LOCKED skips rows other
				// transactions hold, not our own, so a re-pick could return it again.
				exclude = append(exclude, fmt.Sprintf("%s/%d", u.coach, u.berth))
				if !taken {
					units = append(units, u)
				}
			}
		}
		if len(units) < len(r.Passengers) {
			return ErrTatkalSoldOut
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO bookings (user_id, run_id, train_number, journey_date, from_station, to_station, from_idx, to_idx,
			                      class_code, quota, status, source, total_fare, fare, preferences, hold_expires_at, idempotency_key, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'HELD','user',$11,$12,'{}',$13,nullif($14,''),$15)
			RETURNING id`,
			userID, runID, j.train.Number, j.boardDate, j.train.Stops[j.from].Station, j.train.Stops[j.to].Station,
			j.from, j.to, j.class, j.quota, quote.Total*len(units), quote, now.Add(s.HoldTTL), idemKey, now).Scan(&bookingID)
		if err != nil {
			return err
		}
		for i, u := range units {
			p := r.Passengers[i]
			var pid string
			if err := tx.QueryRow(ctx, `
				INSERT INTO booking_passengers (booking_id, seq, name, age, gender, berth_pref, status, coach_no, berth_no, berth_type, fare)
				VALUES ($1,$2,$3,$4,$5,$6,'CNF',$7,$8,$9,$10) RETURNING id`,
				bookingID, i+1, p.Name, p.Age, p.Gender, p.Pref, u.coach, u.berth, u.typ, quote.Total).Scan(&pid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO seat_allocations (run_id, class_code, coach_no, berth_no, slot, seg, booking_id, passenger_id)
				VALUES ($1,$2,$3,$4,0,int4range($5,$6),$7,$8)`, runID, j.class, u.coach, u.berth, j.from, j.to, bookingID, pid); err != nil {
				return err
			}
		}
		return outbox(ctx, tx, "booking.events", bookingID, map[string]any{
			"type": "booking.held", "booking_id": bookingID, "train": j.train.Number, "class": j.class, "quota": j.quota,
			"passengers": len(units), "path": "tatkal",
		})
	})
	s.invalidate(runID, j.class)
	switch {
	case errors.Is(err, ErrTatkalSoldOut):
		metrics.TatkalHolds.WithLabelValues("sold_out").Inc()
		return nil, err
	case isExclusionViolation(err):
		metrics.TatkalHolds.WithLabelValues("conflict").Inc()
		return nil, ErrConflict
	case isUniqueViolation(err) && idemKey != "":
		return s.TatkalHold(ctx, userID, idemKey, r, opts)
	case err != nil:
		metrics.TatkalHolds.WithLabelValues("error").Inc()
		return nil, err
	}
	metrics.TatkalHolds.WithLabelValues("held").Inc()
	return s.Get(ctx, bookingID)
}

// ensureTatkalUnits writes a run's Tatkal berths as lockable rows (once per run and class).
func (s *Service) ensureTatkalUnits(ctx context.Context, runID int64, t *network.Train, class string) error {
	key := fmt.Sprint(runID, "|", class)
	if _, ok := tatkalUnitsReady.Load(key); ok {
		return nil
	}
	cat, err := s.catalog(s.runTrain(runID, t), class)
	if err != nil {
		return err
	}
	var quotas, coaches, types []string
	var berths, positions []int16
	for _, u := range cat.Units {
		if !u.RAC && (u.Quota == "TQ" || u.Quota == "PT") {
			quotas = append(quotas, u.Quota)
			coaches = append(coaches, u.Coach)
			types = append(types, u.Type)
			berths = append(berths, int16(u.Berth))
			positions = append(positions, int16(u.CoachPos))
		}
	}
	if len(quotas) == 0 {
		tatkalUnitsReady.Store(key, true)
		return nil
	}
	_, err = s.DB.Exec(ctx, `
		INSERT INTO tatkal_units (run_id, class_code, quota, coach_no, berth_no, berth_type, coach_pos)
		SELECT $1, $2, q, c, b, t, p FROM unnest($3::text[], $4::text[], $5::smallint[], $6::text[], $7::smallint[]) AS x(q, c, b, t, p)
		ON CONFLICT DO NOTHING`, runID, class, quotas, coaches, berths, types, positions)
	if err == nil {
		tatkalUnitsReady.Store(key, true)
	}
	return err
}

var warming sync.Map

// WarmTatkal prepares a journey's run and Tatkal berth rows in the background, so the
// first booking after the waiting room opens doesn't pay that one-time cost.
func (s *Service) WarmTatkal(r HoldRequest) {
	j, err := s.resolve(r)
	if err != nil || (j.quota != "TQ" && j.quota != "PT") {
		return
	}
	key := j.train.Number + "|" + j.originDate.Format(time.DateOnly) + "|" + j.class
	if _, loaded := warming.LoadOrStore(key, true); loaded {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		runID, err := s.ensureRun(ctx, j.train, j.originDate)
		if err == nil {
			err = s.ensureTatkalUnits(ctx, runID, j.train, j.class)
		}
		if err != nil {
			warming.Delete(key)
		}
	}()
}
