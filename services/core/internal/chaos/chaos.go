// Package chaos is chaos mode: drills that break part of the system on purpose — the payment
// gateway, the database, the event bus, a train — and record, step by step, how it recovers on
// its own. Nothing here repairs anything: each drill only injects the fault and watches the
// mechanisms that already exist (reconciliation, connection pooling, the outbox, the live
// watcher) do their job.
package chaos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/yatrax/yatrax/services/core/internal/booking"
	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

var (
	ErrBusy    = errors.New("another drill is running")
	ErrUnknown = errors.New("unknown drill")
)

type Service struct {
	DB         *pgxpool.Pool
	Redis      *redis.Client
	Booking    *booking.Service
	Payments   *booking.Payments
	Live       *live.Engine
	Net        *network.Network
	Now        func() time.Time // railway time
	GatewayURL string           // server-to-server
	HTTP       *http.Client

	mu sync.Mutex
}

// Drill describes one experiment for the control room.
type Drill struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Breaks   string `json:"breaks"`
	Expect   string `json:"expect"`
	Deadline int    `json:"deadline_sec"` // recovery must happen within this
	run      func(ctx context.Context, r *recorder) error
}

func (s *Service) Drills() []Drill {
	return []Drill{
		{Kind: "gateway_outage", Title: "Payment gateway loses its callbacks",
			Breaks:   "The gateway takes passengers' money but never redirects them back or sends its webhook, for three payments.",
			Expect:   "The reconciler finds the captured payments within about two minutes and confirms the tickets; nobody pays without a ticket.",
			Deadline: 300, run: s.gatewayOutage},
		{Kind: "db_failover", Title: "Database failover",
			Breaks:   "Every connection to Postgres is cut at once, as when the primary fails over to a standby.",
			Expect:   "The connection pools reconnect within seconds; requests in flight fail and retry; background workers carry on.",
			Deadline: 60, run: s.dbFailover},
		{Kind: "event_bus_down", Title: "Event bus down",
			Breaks:   "Publishing to Redpanda stops for 20 seconds while events keep happening.",
			Expect:   "Events wait in the outbox (written in the same transaction as the change) and all drain once the bus is back; none are lost.",
			Deadline: 90, run: s.eventBusDown},
		{Kind: "train_breakdown", Title: "Train breakdown",
			Breaks:   "A running train's engine fails before its next stop: 90 minutes' delay.",
			Expect:   "Live status, boards and ETAs update; the watcher announces the delay to passengers and screens; food orders retime.",
			Deadline: 120, run: s.trainBreakdown},
	}
}

type Step struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // info, fault, ok, recovered, failed
	Text string    `json:"text"`
}

type Run struct {
	ID          int64          `json:"id"`
	Kind        string         `json:"kind"`
	Title       string         `json:"title"`
	Status      string         `json:"status"`
	StartedBy   string         `json:"started_by"`
	StartedAt   time.Time      `json:"started_at"`
	EndedAt     *time.Time     `json:"ended_at,omitempty"`
	RecoveredIn *float64       `json:"recovered_in,omitempty"`
	Steps       []Step         `json:"steps"`
	Metrics     map[string]any `json:"metrics"`
}

// recorder keeps a drill's timeline in the database as it happens, so every instance (and
// the control room, polling) sees progress live.
type recorder struct {
	s       *Service
	id      int64
	mu      sync.Mutex
	steps   []Step
	metrics map[string]any
	faultAt time.Time
	healed  *float64
}

func (r *recorder) step(kind, format string, a ...any) {
	r.mu.Lock()
	r.steps = append(r.steps, Step{At: time.Now(), Kind: kind, Text: fmt.Sprintf(format, a...)})
	r.mu.Unlock()
	r.save()
}

func (r *recorder) fault(format string, a ...any) {
	r.faultAt = time.Now()
	r.step("fault", format, a...)
}

func (r *recorder) recovered(format string, a ...any) { r.recoveredSince(r.faultAt, format, a...) }

// recoveredSince times recovery from a later moment, for drills whose fault lasts a set time
// (recovery starts when the fault ends).
func (r *recorder) recoveredSince(from time.Time, format string, a ...any) {
	secs := time.Since(from).Seconds()
	r.healed = &secs
	r.step("recovered", format+fmt.Sprintf(" — recovered in %.1f s", secs), a...)
}

func (r *recorder) metric(k string, v any) {
	r.mu.Lock()
	r.metrics[k] = v
	r.mu.Unlock()
}

// save writes the timeline; it retries, since one drill cuts the database connections.
func (r *recorder) save() {
	r.mu.Lock()
	steps, _ := json.Marshal(r.steps)
	metrics, _ := json.Marshal(r.metrics)
	r.mu.Unlock()
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := r.s.DB.Exec(ctx, `UPDATE chaos_runs SET steps = $2, metrics = $3, recovered_in = $4 WHERE id = $1`, r.id, steps, metrics, r.healed)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	slog.Warn("chaos: could not save the timeline", "run", r.id)
}

func (r *recorder) finish(status string) {
	r.save()
	for i := 0; i < 20; i++ {
		if _, err := r.s.DB.Exec(context.Background(), `UPDATE chaos_runs SET status = $2, ended_at = now() WHERE id = $1`, r.id, status); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Start begins a drill in the background and returns its run at once.
func (s *Service) Start(ctx context.Context, kind, userID string) (*Run, error) {
	var d *Drill
	for _, x := range s.Drills() {
		if x.Kind == kind {
			d = &x
			break
		}
	}
	if d == nil {
		return nil, ErrUnknown
	}
	if !s.mu.TryLock() {
		return nil, ErrBusy
	}
	var id int64
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		// One drill at a time across instances; a drill that died mid-way frees the slot after 15 minutes.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4242)`); err != nil {
			return err
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chaos_runs WHERE status = 'running' AND started_at > now() - interval '15 minutes')`).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrBusy
		}
		return tx.QueryRow(ctx, `INSERT INTO chaos_runs (kind, started_by) VALUES ($1, $2) RETURNING id`, kind, userID).Scan(&id)
	})
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	rec := &recorder{s: s, id: id, metrics: map[string]any{}}
	go func() {
		defer s.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(d.Deadline+60)*time.Second)
		defer cancel()
		rec.step("info", "Drill started: %s", d.Title)
		err := d.run(ctx, rec)
		switch {
		case err != nil:
			rec.step("failed", "%v", err)
			rec.finish("failed")
		case rec.healed == nil:
			rec.step("failed", "No recovery within %d s", d.Deadline)
			rec.finish("failed")
		default:
			rec.finish("recovered")
		}
	}()
	return s.Get(ctx, id)
}

func (s *Service) list(ctx context.Context, where string, args ...any) ([]Run, error) {
	rows, err := s.DB.Query(ctx, `SELECT c.id, c.kind, c.status, coalesce(u.full_name, ''), c.started_at, c.ended_at, c.recovered_in, c.steps, c.metrics
		FROM chaos_runs c LEFT JOIN users u ON u.id = c.started_by `+where, args...)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	for _, d := range s.Drills() {
		titles[d.Kind] = d.Title
	}
	out := []Run{}
	for rows.Next() {
		var r Run
		var steps, metrics []byte
		if err := rows.Scan(&r.ID, &r.Kind, &r.Status, &r.StartedBy, &r.StartedAt, &r.EndedAt, &r.RecoveredIn, &steps, &metrics); err != nil {
			return nil, err
		}
		json.Unmarshal(steps, &r.Steps)
		json.Unmarshal(metrics, &r.Metrics)
		r.Title = titles[r.Kind]
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, id int64) (*Run, error) {
	list, err := s.list(ctx, `WHERE c.id = $1`, id)
	if err != nil || len(list) == 0 {
		return nil, errors.Join(err, pgx.ErrNoRows)
	}
	return &list[0], nil
}

// Recent lists the latest drills, newest first.
func (s *Service) Recent(ctx context.Context) ([]Run, error) {
	return s.list(ctx, `ORDER BY c.id DESC LIMIT 20`)
}

// wait polls cond every interval until it returns true, an error, or the context ends.
func wait(ctx context.Context, every time.Duration, cond func() (bool, error)) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		ok, err := cond()
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return nil // the caller reports the missing recovery
		case <-t.C:
		}
	}
}
