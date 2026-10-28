package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Event is the common shape of outbox payloads: a type plus arbitrary fields.
type Event map[string]any

func (e Event) Type() string { s, _ := e["type"].(string); return s }

func (e Event) String(key string) string { s, _ := e[key].(string); return s }

func (e Event) Int(key string) int {
	switch v := e[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// Handler processes one event. Returning an error logs it; the offset is still
// committed so one bad event can't stall the group (handlers are idempotent and
// the state they act on can be re-derived, e.g. by running promotion again).
type Handler func(ctx context.Context, topic string, ev Event) error

// Consume runs a consumer group until ctx ends. Offsets are committed after each
// batch is handled, so delivery is at-least-once.
func Consume(ctx context.Context, brokers []string, group string, topics []string, h Handler) {
	for ctx.Err() == nil {
		cl, err := kgo.NewClient(
			kgo.SeedBrokers(brokers...),
			kgo.ConsumerGroup(group),
			kgo.ConsumeTopics(topics...),
			kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
			kgo.DisableAutoCommit(),
			kgo.AllowAutoTopicCreation(),
		)
		if err != nil {
			slog.Warn("consumer: connect", "group", group, "err", err)
			sleep(ctx, 3*time.Second)
			continue
		}
		run(ctx, cl, group, h)
		cl.Close()
	}
}

func run(ctx context.Context, cl *kgo.Client, group string, h Handler) {
	for {
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		fetches.EachError(func(topic string, p int32, err error) {
			slog.Warn("consumer: fetch", "group", group, "topic", topic, "err", err)
		})
		fetches.EachRecord(func(r *kgo.Record) {
			var ev Event
			if err := json.Unmarshal(r.Value, &ev); err != nil {
				slog.Warn("consumer: bad event", "group", group, "topic", r.Topic, "err", err)
				return
			}
			if err := h(ctx, r.Topic, ev); err != nil {
				slog.Warn("consumer: handler", "group", group, "type", ev.Type(), "err", err)
			}
		})
		if err := cl.CommitUncommittedOffsets(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("consumer: commit", "group", group, "err", err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
