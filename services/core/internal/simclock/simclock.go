// Package simclock is the railway's clock. It can run at real time, faster (up to a day
// a minute), be paused, or jump. Train movement, live status and alerts follow it;
// bookings, holds and payments stay on real time so a 15-minute hold is still 15 minutes.
//
// The state lives in Postgres (one row) and changes are announced on Redis, so every
// core instance agrees on the time without a tick loop of its own.
package simclock

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// MaxSpeed runs a simulated day in one real minute.
const MaxSpeed = 1440

const channel = "sim.clock"

type State struct {
	AnchorReal time.Time `json:"-"`
	AnchorSim  time.Time `json:"-"`
	Speed      float64   `json:"speed"`
	Paused     bool      `json:"paused"`
}

// At is the simulated time at a real instant.
func (s State) At(real time.Time) time.Time {
	if s.Paused {
		return s.AnchorSim
	}
	return s.AnchorSim.Add(time.Duration(float64(real.Sub(s.AnchorReal)) * s.Speed))
}

type Clock struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
	Real  func() time.Time

	mu sync.RWMutex
	st State
}

func New(db *pgxpool.Pool, rdb *redis.Client) *Clock {
	now := time.Now()
	return &Clock{DB: db, Redis: rdb, Real: time.Now, st: State{AnchorReal: now, AnchorSim: now, Speed: 1}}
}

// Now is the current simulated time.
func (c *Clock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.st.At(c.Real())
}

func (c *Clock) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.st
}

// Load reads the clock from the database (creating it at real time if it's missing).
func (c *Clock) Load(ctx context.Context) error {
	if _, err := c.DB.Exec(ctx, `INSERT INTO sim_clock DEFAULT VALUES ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	var st State
	err := c.DB.QueryRow(ctx, `SELECT anchor_real, anchor_sim, speed, paused FROM sim_clock WHERE id = 1`).
		Scan(&st.AnchorReal, &st.AnchorSim, &st.Speed, &st.Paused)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.st = st
	c.mu.Unlock()
	return nil
}

// Run keeps the clock in step with changes made through any instance.
func (c *Clock) Run(ctx context.Context) {
	sub := c.Redis.Subscribe(ctx, channel)
	defer sub.Close()
	msgs := sub.Channel()
	t := time.NewTicker(5 * time.Second) // in case a notification is missed
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-msgs:
		case <-t.C:
		}
		if err := c.Load(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("simclock: load", "err", err)
		}
	}
}

// Change describes an adjustment. Nil fields keep their current value.
type Change struct {
	Speed  *float64   `json:"speed"`
	Paused *bool      `json:"paused"`
	JumpTo *time.Time `json:"jump_to"`
	Reset  bool       `json:"reset"` // back to real time, running at 1×
}

var ErrSpeed = errors.New("speed must be between 1 and 1440")

// Set applies a change. The simulated time is continuous across speed changes: the
// new anchor is wherever the clock is right now (or the jump target).
func (c *Clock) Set(ctx context.Context, ch Change, by string) (State, error) {
	if ch.Speed != nil && (*ch.Speed < 1 || *ch.Speed > MaxSpeed) {
		return State{}, ErrSpeed
	}
	real := c.Real()
	c.mu.Lock()
	next := c.st
	next.AnchorSim, next.AnchorReal = c.st.At(real), real
	c.mu.Unlock()
	if ch.Reset {
		next = State{AnchorReal: real, AnchorSim: real, Speed: 1}
	}
	if ch.Speed != nil {
		next.Speed = *ch.Speed
	}
	if ch.Paused != nil {
		next.Paused = *ch.Paused
	}
	if ch.JumpTo != nil {
		next.AnchorSim = *ch.JumpTo
	}
	_, err := c.DB.Exec(ctx, `UPDATE sim_clock SET anchor_real = $1, anchor_sim = $2, speed = $3, paused = $4,
		updated_by = nullif($5, '')::uuid, updated_at = now() WHERE id = 1`,
		next.AnchorReal, next.AnchorSim, next.Speed, next.Paused, by)
	if err != nil {
		return State{}, err
	}
	c.mu.Lock()
	c.st = next
	c.mu.Unlock()
	c.Redis.Publish(ctx, channel, "changed")
	return next, nil
}
