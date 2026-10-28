package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/yatrax/yatrax/services/core/internal/metrics"
	"github.com/yatrax/yatrax/services/core/internal/network"
)

// EventsChannel carries train events to WebSocket clients on every instance.
const EventsChannel = "live.events"

// ApproachNotice is how long before a train reaches a stop its passengers are told.
const ApproachNotice = 30 * time.Minute

// delayBuckets: passengers hear about a delay when it crosses one of these.
var delayBuckets = []int{15, 30, 60, 120, 180, 300}

func bucket(delay int) int {
	b := 0
	for _, x := range delayBuckets {
		if delay >= x {
			b = x
		}
	}
	return b
}

// runWatch is what the event engine remembers about a run between ticks.
type runWatch struct {
	departed  bool
	arrived   bool
	bucket    int
	platforms map[int]int  // stop -> changed platform already announced
	approach  map[int]bool // stops whose approach has been announced
}

type watchedRun struct {
	train  *network.Train
	origin time.Time
}

// Watcher turns the movement of trains that carry passengers into events. One
// instance leads (a Redis lock); the others stand by.
type Watcher struct {
	Engine   *Engine
	Redis    *redis.Client
	instance string

	state   map[string]*runWatch
	runs    []watchedRun
	loaded  time.Time
	loadDay time.Time // simulated day the run list was loaded for
}

func NewWatcher(e *Engine, rdb *redis.Client, instance string) *Watcher {
	return &Watcher{Engine: e, Redis: rdb, instance: instance, state: map[string]*runWatch{}}
}

// Run ticks once a real second.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ok, err := w.Redis.SetNX(ctx, "live:leader", w.instance, 3*time.Second).Result()
		if err != nil {
			continue
		}
		if !ok {
			if v, _ := w.Redis.Get(ctx, "live:leader").Result(); v != w.instance {
				w.state = map[string]*runWatch{} // a new leader starts fresh
				continue
			}
			w.Redis.Expire(ctx, "live:leader", 3*time.Second)
		}
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("live: tick", "err", err)
		}
	}
}

// WatchKey is a Redis sorted set of extra runs to watch ("train|date", scored by expiry),
// for runs the control room is following that have no app passengers on board.
const WatchKey = "live:watch"

// Watch asks the watcher (on whichever instance leads) to follow a run for a while.
func Watch(ctx context.Context, rdb *redis.Client, train string, origin time.Time, ttl time.Duration) error {
	return rdb.ZAdd(ctx, WatchKey, redis.Z{Score: float64(time.Now().Add(ttl).Unix()), Member: runKey(train, Midnight(origin))}).Err()
}

// loadRuns finds runs with passengers' confirmed bookings around the simulated date, plus any
// the control room asked to watch.
func (w *Watcher) loadRuns(ctx context.Context, now time.Time) error {
	today := Midnight(now)
	rows, err := w.Engine.DB.Query(ctx, `SELECT DISTINCT r.train_number, r.origin_date FROM bookings b
		JOIN train_runs r ON r.id = b.run_id
		WHERE b.status = 'CONFIRMED' AND b.source = 'user' AND r.origin_date BETWEEN $1 AND $2`,
		today.AddDate(0, 0, -4), today.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	defer rows.Close()
	var runs []watchedRun
	for rows.Next() {
		var num string
		var d time.Time
		if err := rows.Scan(&num, &d); err != nil {
			return err
		}
		if t := w.Engine.Net.Trains[num]; t != nil {
			runs = append(runs, watchedRun{t, time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, network.IST)})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range runs {
		seen[runKey(r.train.Number, r.origin)] = true
	}
	w.Redis.ZRemRangeByScore(ctx, WatchKey, "-inf", fmt.Sprint(time.Now().Unix()))
	extra, _ := w.Redis.ZRange(ctx, WatchKey, 0, -1).Result()
	for _, k := range extra {
		num, date, ok := strings.Cut(k, "|")
		d, err := time.ParseInLocation(time.DateOnly, date, network.IST)
		if t := w.Engine.Net.Trains[num]; ok && err == nil && t != nil && !seen[k] {
			runs = append(runs, watchedRun{t, d})
			seen[k] = true
		}
	}
	w.runs, w.loaded, w.loadDay = runs, time.Now(), today
	return nil
}

// Tick compares each watched run with the last tick and emits what changed.
func (w *Watcher) Tick(ctx context.Context) error {
	now := w.Engine.Now()
	if time.Since(w.loaded) > 10*time.Second || !Midnight(now).Equal(w.loadDay) {
		if err := w.loadRuns(ctx, now); err != nil {
			return err
		}
	}
	for _, r := range w.runs {
		key := runKey(r.train.Number, r.origin)
		s := w.Engine.Status(r.train, r.origin, now)
		prev, seen := w.state[key]
		cur := w.observe(s)
		w.state[key] = cur
		if !seen {
			continue // first sight (or a clock jump onto a new leader): record, don't replay history
		}
		for _, ev := range w.diff(r.train, s, prev, cur) {
			if err := w.emit(ctx, key, ev); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Watcher) observe(s Status) *runWatch {
	c := &runWatch{platforms: map[int]int{}, approach: map[int]bool{}}
	c.departed = s.State == "running" || s.State == "at_station" || s.State == "arrived"
	c.arrived = s.State == "arrived"
	if s.State != "not_running" {
		c.bucket = bucket(s.DelayMin) // a late start counts too: people are waiting on the platform
	}
	for _, v := range s.Stops {
		if v.PlatformChanged {
			c.platforms[v.Seq] = v.Platform
		}
		// At the origin "approaching" means "about to leave": boarding passengers get a reminder.
		when := v.ExpArr
		if v.Seq == 0 {
			when = v.ExpDep
		}
		if v.State == "upcoming" && when != nil && !s.SimNow.Before(when.Add(-ApproachNotice)) {
			c.approach[v.Seq] = true
		}
	}
	return c
}

func (w *Watcher) diff(t *network.Train, s Status, prev, cur *runWatch) []map[string]any {
	base := func(typ string) map[string]any {
		return map[string]any{"type": typ, "train": t.Number, "train_name": t.Name, "origin_date": s.OriginDate,
			"delay": s.DelayMin, "sim_time": s.SimNow.Format(time.RFC3339)}
	}
	var out []map[string]any
	if cur.departed && !prev.departed {
		ev := base("train.departed")
		ev["station"], ev["seq"] = t.Stops[0].Station, 0
		out = append(out, ev)
	}
	if cur.bucket > prev.bucket {
		ev := base("train.delayed")
		ev["bucket"], ev["reason"], ev["at_seq"] = cur.bucket, s.Reason, s.LastIdx
		exp := map[string]string{}
		for _, v := range s.Stops {
			if v.State != "upcoming" {
				continue
			}
			if v.ExpArr != nil {
				exp[fmt.Sprint(v.Seq)] = v.ExpArr.Format(time.RFC3339)
			} else if v.ExpDep != nil {
				exp[fmt.Sprint(v.Seq)] = v.ExpDep.Format(time.RFC3339) // the origin: when it now leaves
			}
		}
		ev["expected"] = exp
		out = append(out, ev)
	}
	for seq, pf := range cur.platforms {
		if prev.platforms[seq] != pf { // a new change, or a station master changing it again
			v := s.Stops[seq]
			ev := base("platform.changed")
			ev["seq"], ev["station"], ev["platform"], ev["old_platform"] = seq, v.Station, v.Platform, t.Stops[seq].Platform
			out = append(out, ev)
		}
	}
	for seq := range cur.approach {
		if !prev.approach[seq] {
			v := s.Stops[seq]
			ev := base("train.approaching")
			eta := v.ExpArr
			if eta == nil {
				eta = v.ExpDep
			}
			ev["seq"], ev["station"], ev["platform"], ev["eta"] = seq, v.Station, v.Platform, eta.Format(time.RFC3339)
			out = append(out, ev)
		}
	}
	if cur.arrived && !prev.arrived {
		last := len(t.Stops) - 1
		ev := base("train.arrived")
		ev["seq"], ev["station"] = last, t.Stops[last].Station
		out = append(out, ev)
	}
	return out
}

// emit writes an event to the outbox (for notifications) and to Redis (for live screens).
func (w *Watcher) emit(ctx context.Context, key string, ev map[string]any) error {
	if _, err := w.Engine.DB.Exec(ctx, `INSERT INTO outbox (topic, key, payload) VALUES ('train.events', $1, $2)`, key, ev); err != nil {
		return err
	}
	metrics.TrainEvents.WithLabelValues(ev["type"].(string)).Inc()
	b, _ := json.Marshal(ev)
	return w.Redis.Publish(ctx, EventsChannel, b).Err()
}
