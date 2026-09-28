package kafkax

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Handler processes one message. Returning an error retries the same message.
type Handler func(ctx context.Context, m Message) error

// ConsumerConfig configures a consumer group member.
type ConsumerConfig struct {
	Brokers    []string
	Group      string
	Topics     []string
	MinBackoff time.Duration // default 200ms
	MaxBackoff time.Duration // default 10s
	Logger     *slog.Logger
}

// Consumer processes records one at a time, in partition order.
type Consumer struct {
	client  *kgo.Client
	handler Handler
	cfg     ConsumerConfig
}

// NewConsumer joins cfg.Group. New groups start from the earliest offset.
func NewConsumer(cfg ConsumerConfig, h Handler, opts ...kgo.Opt) (*Consumer, error) {
	if cfg.MinBackoff == 0 {
		cfg.MinBackoff = 200 * time.Millisecond
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	base := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topics...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}
	cl, err := kgo.NewClient(append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: %w", err)
	}
	return &Consumer{client: cl, handler: h, cfg: cfg}, nil
}

// Run polls until ctx is cancelled. Each record is retried with exponential
// backoff until it succeeds; offsets of a polled batch are committed only
// after every record in it was handled (at-least-once delivery).
func (c *Consumer) Run(ctx context.Context) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if ctx.Err() != nil {
			c.client.AllowRebalance()
			return nil
		}
		if fetches.IsClientClosed() {
			// Close() raced Run() without ctx being cancelled first: the
			// client is already gone, so stop instead of hot-looping on the
			// same injected ErrClientClosed fetch forever.
			return nil
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			c.cfg.Logger.ErrorContext(ctx, "kafka fetch error", "topic", topic, "partition", partition, "error", err)
		})

		aborted := false
		fetches.EachRecord(func(r *kgo.Record) {
			if aborted {
				return
			}
			if err := c.handle(ctx, r); err != nil {
				aborted = true // ctx cancelled mid-retry: leave the batch uncommitted
			}
		})
		if aborted {
			c.client.AllowRebalance()
			return nil
		}
		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			c.cfg.Logger.ErrorContext(ctx, "kafka commit failed", "error", err)
		}
		c.client.AllowRebalance()
	}
}

func (c *Consumer) handle(ctx context.Context, r *kgo.Record) error {
	msg := Message{Topic: r.Topic, Key: r.Key, Value: r.Value, Headers: fromKgoHeaders(r.Headers)}
	mctx := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(msg.Headers))
	mctx, span := otel.Tracer("kafkax").Start(mctx, "consume "+r.Topic, trace.WithSpanKind(trace.SpanKindConsumer))
	defer span.End()

	backoff := c.cfg.MinBackoff
	for {
		err := c.handler(mctx, msg)
		if err == nil {
			return nil
		}
		c.cfg.Logger.WarnContext(mctx, "kafka handler failed, retrying",
			"topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "backoff", backoff, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, c.cfg.MaxBackoff)
	}
}

// Close leaves the group and closes the client.
func (c *Consumer) Close() { c.client.Close() }
