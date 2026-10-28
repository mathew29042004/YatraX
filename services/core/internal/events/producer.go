// Package events publishes domain events to Redpanda (Kafka API).
package events

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/yatrax/yatrax/services/core/internal/metrics"
)

type Producer struct{ client *kgo.Client }

func NewProducer(brokers []string) (*Producer, error) {
	c, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.AllowAutoTopicCreation())
	if err != nil {
		return nil, err
	}
	return &Producer{client: c}, nil
}

// Publish sends asynchronously. Phase 0 events are informational, so a failure is
// logged and counted rather than failing the request.
func (p *Producer) Publish(ctx context.Context, topic, key string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("marshal event", "topic", topic, "err", err)
		return
	}
	p.client.Produce(context.WithoutCancel(ctx), &kgo.Record{Topic: topic, Key: []byte(key), Value: b},
		func(_ *kgo.Record, err error) {
			if err != nil {
				metrics.EventsPublished.WithLabelValues(topic, "error").Inc()
				slog.Warn("publish event", "topic", topic, "err", err)
				return
			}
			metrics.EventsPublished.WithLabelValues(topic, "ok").Inc()
		})
}

func (p *Producer) Ping(ctx context.Context) error { return p.client.Ping(ctx) }

func (p *Producer) Close() {
	p.client.Flush(context.Background())
	p.client.Close()
}
