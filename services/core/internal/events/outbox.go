package events

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/yatrax/yatrax/services/core/internal/metrics"
)

var relayPausedUntil atomic.Int64

// PauseRelay stops publishing for d (chaos mode's "event bus down"); events keep queueing in
// the outbox and drain when the pause ends. ResumeRelay ends it early.
func PauseRelay(d time.Duration) { relayPausedUntil.Store(time.Now().Add(d).UnixNano()) }
func ResumeRelay()               { relayPausedUntil.Store(0) }
func RelayPaused() bool          { return time.Now().UnixNano() < relayPausedUntil.Load() }

// RelayOutbox publishes rows from the outbox table to Redpanda, oldest first, and
// marks them sent. A row is only marked after Redpanda acknowledges it, so events
// may be delivered more than once but never lost; consumers dedupe by event content.
func (p *Producer) RelayOutbox(ctx context.Context, db *pgxpool.Pool) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if RelayPaused() {
			continue
		}
		for {
			n, err := p.relayBatch(ctx, db)
			if err != nil {
				slog.Warn("outbox relay", "err", err)
				break
			}
			if n == 0 {
				break
			}
		}
	}
}

func (p *Producer) relayBatch(ctx context.Context, db *pgxpool.Pool) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, topic, key, payload FROM outbox WHERE published_at IS NULL
			ORDER BY id LIMIT 200 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		var ids []int64
		var recs []*kgo.Record
		for rows.Next() {
			var id int64
			var topic, key string
			var payload []byte
			if err := rows.Scan(&id, &topic, &key, &payload); err != nil {
				return err
			}
			ids = append(ids, id)
			recs = append(recs, &kgo.Record{Topic: topic, Key: []byte(key), Value: payload})
		}
		if err := rows.Err(); err != nil || len(recs) == 0 {
			return err
		}
		if err := p.client.ProduceSync(ctx, recs...).FirstErr(); err != nil {
			metrics.EventsPublished.WithLabelValues("outbox", "error").Add(float64(len(recs)))
			return err
		}
		for _, r := range recs {
			metrics.EventsPublished.WithLabelValues(r.Topic, "ok").Inc()
		}
		n = len(ids)
		_, err = tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids)
		return err
	})
	return n, err
}
