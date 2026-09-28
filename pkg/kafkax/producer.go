// Package kafkax wraps franz-go with the project conventions: synchronous
// publishing with trace propagation, and consumer groups that commit offsets
// only after successful processing, retrying failures with backoff.
package kafkax

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Message is a Kafka record in transport-neutral form.
type Message struct {
	Topic   string
	Key     []byte
	Value   []byte
	Headers map[string]string
}

// Producer publishes messages synchronously (acks=all).
type Producer struct {
	client *kgo.Client
}

// NewProducer connects to brokers. Extra kgo options are appended.
func NewProducer(brokers []string, opts ...kgo.Opt) (*Producer, error) {
	base := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(5 * time.Millisecond),
	}
	cl, err := kgo.NewClient(append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	return &Producer{client: cl}, nil
}

// Publish sends msgs and waits for every ack. A traceparent from ctx is added
// to messages that do not already carry one.
func (p *Producer) Publish(ctx context.Context, msgs ...Message) error {
	recs := make([]*kgo.Record, 0, len(msgs))
	for _, m := range msgs {
		h := maps.Clone(m.Headers)
		if h == nil {
			h = map[string]string{}
		}
		if _, ok := h["traceparent"]; !ok {
			otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(h))
		}
		recs = append(recs, &kgo.Record{Topic: m.Topic, Key: m.Key, Value: m.Value, Headers: toKgoHeaders(h)})
	}
	if err := p.client.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		return fmt.Errorf("kafka publish: %w", err)
	}
	return nil
}

// Ping checks broker connectivity (for /readyz).
func (p *Producer) Ping(ctx context.Context) error { return p.client.Ping(ctx) }

// Close flushes and closes the client.
func (p *Producer) Close() { p.client.Close() }

func toKgoHeaders(h map[string]string) []kgo.RecordHeader {
	out := make([]kgo.RecordHeader, 0, len(h))
	for k, v := range h {
		out = append(out, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	return out
}

func fromKgoHeaders(hs []kgo.RecordHeader) map[string]string {
	out := make(map[string]string, len(hs))
	for _, h := range hs {
		out[h.Key] = string(h.Value)
	}
	return out
}
