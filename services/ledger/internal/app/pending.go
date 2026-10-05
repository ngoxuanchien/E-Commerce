package app

import (
	"context"
	"log/slog"
	"time"
)

// PendingConfig tunes PendingJob. Zero StaleAfter = 1h, zero BatchSize = 50.
type PendingConfig struct {
	Interval   time.Duration // PENDING_POSTING_INTERVAL
	StaleAfter time.Duration
	BatchSize  int
	Now        func() time.Time
	Logger     *slog.Logger
}

// PendingJob retries OrderCompleted events parked before their capture
// (WS2' §6). Rows are claimed with SKIP LOCKED, so replicas don't collide.
type PendingJob struct {
	store  Store
	events *EventHandler
	cfg    PendingConfig
}

func NewPendingJob(store Store, events *EventHandler, cfg PendingConfig) *PendingJob {
	if cfg.StaleAfter == 0 {
		cfg.StaleAfter = time.Hour
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 50
	}
	return &PendingJob{store: store, events: events, cfg: cfg}
}

// Run calls RunOnce every Interval until ctx is cancelled.
func (j *PendingJob) Run(ctx context.Context) error {
	for {
		if _, err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
			j.cfg.Logger.ErrorContext(ctx, "pending postings run failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(j.cfg.Interval):
		}
	}
}

// RunOnce releases every claimed row whose allocation now exists and counts
// an attempt on the rest. It returns how many orders were released.
func (j *PendingJob) RunOnce(ctx context.Context) (int, error) {
	var released int
	err := j.store.InTx(ctx, func(tx Tx) error {
		released = 0
		rows, err := tx.ClaimPending(ctx, j.cfg.BatchSize)
		if err != nil {
			return err
		}
		now := j.cfg.Now()
		for _, r := range rows {
			src, d, err := decodePending(r)
			if err != nil {
				j.cfg.Logger.ErrorContext(ctx, "dropping an undecodable pending posting", "order_id", r.OrderID, "error", err)
				if err := tx.DeletePending(ctx, r.OrderID); err != nil {
					return err
				}
				continue
			}
			a, found, err := tx.GetAllocation(ctx, r.OrderID)
			if err != nil {
				return err
			}
			if !found {
				if now.Sub(r.CreatedAt) > j.cfg.StaleAfter {
					j.cfg.Logger.ErrorContext(ctx, "pending posting still waiting for its capture",
						"order_id", r.OrderID, "since", r.CreatedAt, "attempts", r.Attempts+1)
				}
				if err := tx.TouchPending(ctx, r.OrderID, now); err != nil {
					return err
				}
				continue
			}
			if err := j.events.release(ctx, tx, src, a, d); err != nil {
				if !permanent(err) {
					return err
				}
				j.cfg.Logger.ErrorContext(ctx, "pending release rejected", "alert", true, "order_id", r.OrderID, "error", err)
				if err := tx.TouchPending(ctx, r.OrderID, now); err != nil {
					return err
				}
				continue
			}
			if err := tx.DeletePending(ctx, r.OrderID); err != nil {
				return err
			}
			released++
		}
		return nil
	})
	return released, err
}
