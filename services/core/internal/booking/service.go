// Package booking owns train runs, seat holds and bookings.
//
// Every write to a run's class happens inside one transaction holding a Postgres
// advisory lock for that (run, class), so allocations are decided against a
// consistent view. The exclusion constraint on seat_allocations backs that up: even
// a buggy writer cannot store two overlapping journeys on one berth.
package booking

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yatrax/yatrax/services/core/internal/availability"
	"github.com/yatrax/yatrax/services/core/internal/inventory"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

const (
	DefaultHoldTTL   = 10 * time.Minute
	MaxPassengers    = 6
	MaxTatkalPax     = 4
	MaxActiveHolds   = 3
	sweepEvery       = 10 * time.Second
	occupancyMaxAge  = 2 * time.Second
	lockNSRunClass   = 1 // advisory lock namespaces
	lockNSBackground = 2
)

var (
	ErrNotFound      = errors.New("booking not found")
	ErrNotConfirmed  = errors.New("not every passenger would get a confirmed berth")
	ErrRegret        = errors.New("the waitlist for this class is closed")
	ErrConflict      = errors.New("someone else just took that berth; please try again")
	ErrTooManyHolds  = errors.New("you already have three bookings waiting for payment")
	ErrNotHeld       = errors.New("this booking is no longer waiting for payment")
	ErrBookingClosed = errors.New("booking is not open for this train and date")
)

// ValidationError is a problem with the request that the user can fix.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return ValidationError{fmt.Sprintf(format, a...)} }

type Service struct {
	DB      *pgxpool.Pool
	Net     *network.Network
	Sim     availability.Simulated
	Now     func() time.Time
	HoldTTL time.Duration

	catalogs  sync.Map // "train|class" -> *inventory.Catalog
	runsMu    sync.RWMutex
	runs      map[string]int64 // "train|2026-11-02" -> run id, for runs that exist
	charted   map[int64]bool   // runs whose first chart is prepared
	occMu     sync.Mutex
	occ       map[string]occEntry // "run|class" -> cached occupancy
	people    []botPerson         // synthetic passengers who make background bookings
	filledAt  sync.Map            // run id -> time.Time of its last background top-up
	fillLocks sync.Map            // run id -> *sync.Mutex serialising top-ups within this process
	disrupted sync.Map            // run id -> *runDisruption (cancelled, cut short, diverted)
	extras    sync.Map            // run id -> []network.Coach added to that run
	runTrains sync.Map            // run id -> *network.Train including its extra coaches

	// TatkalRush, set by the waiting room, reports whether Tatkal for an origin date
	// and class type is in its opening rush (when it books only through the room).
	TatkalRush func(originDate time.Time, ac bool) bool
}

type occEntry struct {
	at    time.Time
	occ   inventory.Occupancy
	queue map[string]queueCount // quota -> active RAC / WL passengers
}

type queueCount struct{ rac, wl int }

func New(db *pgxpool.Pool, net *network.Network, now func() time.Time) *Service {
	s := &Service{DB: db, Net: net, Now: now, HoldTTL: DefaultHoldTTL,
		runs: map[string]int64{}, charted: map[int64]bool{}, occ: map[string]occEntry{}}
	// The demand model sizes quotas from the real coach layout, so a status shown
	// before a run is opened agrees with what booking finds afterwards.
	s.Sim = availability.Simulated{N: net, Capacity: func(t *network.Train, class, quota string) (float64, float64) {
		cat, err := s.catalog(t, class)
		if err != nil {
			return 0, 0
		}
		rac := 0.0
		if quota == "GN" {
			for _, u := range cat.Units {
				if u.RAC {
					rac++
				}
			}
		}
		return float64(cat.Capacity(quota)), rac
	}}
	return s
}

// Start loads existing runs and the bot passenger pool, and starts the hold sweeper.
func (s *Service) Start(ctx context.Context) error {
	rows, err := s.DB.Query(ctx, `SELECT id, train_number, origin_date, chart1_at IS NOT NULL FROM train_runs`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var train string
		var d time.Time
		var charted bool
		if err := rows.Scan(&id, &train, &d, &charted); err != nil {
			return err
		}
		s.runs[runKey(train, d)] = id
		s.charted[id] = charted
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := s.loadPeople(ctx); err != nil {
		return err
	}
	if err := s.loadDisruptions(ctx); err != nil {
		return err
	}
	go s.sweep(ctx)
	return nil
}

func runKey(train string, originDate time.Time) string {
	return train + "|" + originDate.Format(time.DateOnly)
}

func (s *Service) catalog(t *network.Train, class string) (*inventory.Catalog, error) {
	key := fmt.Sprint(t.Number, "|", class, "|", len(t.Coaches)) // a run with extra coaches has its own
	if c, ok := s.catalogs.Load(key); ok {
		return c.(*inventory.Catalog), nil
	}
	c, err := inventory.NewCatalog(s.Net, t, class)
	if err != nil {
		return nil, err
	}
	s.catalogs.Store(key, c)
	return c, nil
}

// runID returns the id of an existing run.
func (s *Service) runID(train string, originDate time.Time) (int64, bool) {
	s.runsMu.RLock()
	defer s.runsMu.RUnlock()
	id, ok := s.runs[runKey(train, originDate)]
	return id, ok
}

// ensureRun creates the run if needed and tops up its background (bot) bookings.
// focus, if given, is the journey being booked or viewed (see fillFocus).
func (s *Service) ensureRun(ctx context.Context, t *network.Train, originDate time.Time, focus ...Segment) (int64, error) {
	id, ok := s.runID(t.Number, originDate)
	if !ok {
		err := s.DB.QueryRow(ctx, `
			INSERT INTO train_runs (train_number, origin_date) VALUES ($1, $2)
			ON CONFLICT (train_number, origin_date) DO UPDATE SET train_number = EXCLUDED.train_number
			RETURNING id`, t.Number, originDate).Scan(&id)
		if err != nil {
			return 0, err
		}
		s.runsMu.Lock()
		s.runs[runKey(t.Number, originDate)] = id
		s.runsMu.Unlock()
	}
	// Only a run being opened now showed the demand model's numbers in search; once a
	// run exists, search shows its real inventory, so there is nothing to match.
	var f *Segment
	if len(focus) > 0 && !ok {
		f = &focus[0]
	}
	// Background demand grows over days, so topping it up every half minute is plenty. Doing it
	// on every booking would queue all buyers of a train behind the top-up's run-wide lock.
	fresh := func() bool {
		last, seen := s.filledAt.Load(id)
		return seen && f == nil && s.Now().Sub(last.(time.Time)) < BackgroundRefresh
	}
	if fresh() {
		return id, nil
	}
	// Buyers arriving together wait for one top-up instead of each taking the database lock in turn.
	mu, _ := s.fillLocks.LoadOrStore(id, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if fresh() {
		return id, nil
	}
	if err := s.fillBackground(ctx, id, t, originDate, f); err != nil {
		return 0, fmt.Errorf("background bookings: %w", err)
	}
	s.filledAt.Store(id, s.Now())
	return id, nil
}

// BackgroundRefresh is how often a run's background bookings are topped up.
const BackgroundRefresh = 30 * time.Second

func lockKey(ns int64, runID int64, class string) int64 {
	h := fnv.New32a()
	h.Write([]byte(class))
	return ns<<56 | runID<<8 | int64(h.Sum32()&0xff)
}

// lock takes the transaction-scoped advisory lock for a run's class.
func lock(ctx context.Context, tx pgx.Tx, runID int64, class string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey(lockNSRunClass, runID, class))
	return err
}

// expireStale releases holds whose time is up. Call it while holding the run/class lock.
func (s *Service) expireStale(ctx context.Context, tx pgx.Tx, runID int64, class string) (int, error) {
	rows, err := tx.Query(ctx, `
		UPDATE bookings SET status = 'EXPIRED', updated_at = now()
		 WHERE run_id = $1 AND class_code = $2 AND status IN ('HELD', 'PAYMENT_PENDING') AND hold_expires_at < $3
		RETURNING id`, runID, class, s.Now())
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM seat_allocations WHERE booking_id = ANY($1::uuid[])`, ids); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := outbox(ctx, tx, "booking.events", id, map[string]any{"type": "booking.expired", "booking_id": id}); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// loadOccupancy reads every allocation of a run's class into bitmasks, plus the
// number of passengers currently on RAC and the waitlist per quota.
func loadOccupancy(ctx context.Context, q querier, runID int64, class string) (inventory.Occupancy, map[string]queueCount, error) {
	rows, err := q.Query(ctx, `
		SELECT coach_no, berth_no, slot, lower(seg), upper(seg)
		  FROM seat_allocations WHERE run_id = $1 AND class_code = $2 AND NOT isempty(seg)`, runID, class)
	if err != nil {
		return nil, nil, err
	}
	occ := inventory.Occupancy{}
	for rows.Next() {
		var k inventory.Key
		var from, to int
		if err := rows.Scan(&k.Coach, &k.Berth, &k.Slot, &from, &to); err != nil {
			return nil, nil, err
		}
		occ[k] |= inventory.SegMask(from, to)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows, err = q.Query(ctx, `
		SELECT b.quota, p.status::text, count(*)
		  FROM booking_passengers p JOIN bookings b ON b.id = p.booking_id
		 WHERE b.run_id = $1 AND b.class_code = $2 AND b.status IN ('HELD', 'PAYMENT_PENDING', 'CONFIRMED')
		   AND p.status IN ('RAC', 'WL')
		 GROUP BY 1, 2`, runID, class)
	if err != nil {
		return nil, nil, err
	}
	queue := map[string]queueCount{}
	for rows.Next() {
		var quota, status string
		var n int
		if err := rows.Scan(&quota, &status, &n); err != nil {
			return nil, nil, err
		}
		qc := queue[quota]
		if status == "RAC" {
			qc.rac = n
		} else {
			qc.wl = n
		}
		queue[quota] = qc
	}
	return occ, queue, rows.Err()
}

// cachedOccupancy serves availability reads; writes invalidate it.
func (s *Service) cachedOccupancy(ctx context.Context, runID int64, class string) (inventory.Occupancy, map[string]queueCount, error) {
	key := fmt.Sprint(runID, "|", class)
	s.occMu.Lock()
	e, ok := s.occ[key]
	s.occMu.Unlock()
	if ok && time.Since(e.at) < occupancyMaxAge {
		return e.occ, e.queue, nil
	}
	occ, queue, err := loadOccupancy(ctx, s.DB, runID, class)
	if err != nil {
		return nil, nil, err
	}
	s.occMu.Lock()
	s.occ[key] = occEntry{time.Now(), occ, queue}
	s.occMu.Unlock()
	return occ, queue, nil
}

func (s *Service) invalidate(runID int64, class string) {
	s.occMu.Lock()
	delete(s.occ, fmt.Sprint(runID, "|", class))
	s.occMu.Unlock()
}

// nextNumbers reserves n consecutive RAC or WL numbers.
func nextNumbers(ctx context.Context, tx pgx.Tx, runID int64, class, quota, kind string, n int) (int, error) {
	var last int
	err := tx.QueryRow(ctx, `
		INSERT INTO queue_counters (run_id, class_code, quota, kind, last_no) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (run_id, class_code, quota, kind) DO UPDATE SET last_no = queue_counters.last_no + EXCLUDED.last_no
		RETURNING last_no`, runID, class, quota, kind, n).Scan(&last)
	return last - n + 1, err
}

// sweep expires overdue holds in the background, so seats come back even when
// nobody books that run again.
func (s *Service) sweep(ctx context.Context) {
	tick := time.NewTicker(sweepEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		rows, err := s.DB.Query(ctx, `
			SELECT DISTINCT run_id, class_code FROM bookings
			 WHERE status IN ('HELD', 'PAYMENT_PENDING') AND hold_expires_at < $1`, s.Now())
		if err != nil {
			slog.Warn("hold sweep", "err", err)
			continue
		}
		type rc struct {
			run   int64
			class string
		}
		due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rc, error) {
			var x rc
			return x, r.Scan(&x.run, &x.class)
		})
		if err != nil {
			slog.Warn("hold sweep", "err", err)
			continue
		}
		for _, d := range due {
			err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
				if err := lock(ctx, tx, d.run, d.class); err != nil {
					return err
				}
				n, err := s.expireStale(ctx, tx, d.run, d.class)
				if n > 0 {
					slog.Info("expired holds", "run", d.run, "class", d.class, "count", n)
				}
				return err
			})
			if err != nil {
				slog.Warn("hold sweep", "run", d.run, "err", err)
			}
			s.invalidate(d.run, d.class)
		}
	}
}

func isExclusionViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23P01"
}
