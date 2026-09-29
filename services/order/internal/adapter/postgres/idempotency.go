package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
)

func (s *Store) BeginIdempotency(ctx context.Context, userID, key string, requestHash []byte, at, staleBefore time.Time) (app.IdempotencyRecord, bool, error) {
	uid, err := mustParseUUID(userID)
	if err != nil {
		return app.IdempotencyRecord{}, false, err
	}
	at = at.Truncate(time.Microsecond) // the claim is compared with created_at, which Postgres stores in µs
	claimed := app.IdempotencyRecord{RequestHash: requestHash, ClaimedAt: at}
	n, err := s.q.InsertIdempotencyKey(ctx, sqlcdb.InsertIdempotencyKeyParams{UserID: uid, Key: key, RequestHash: requestHash, CreatedAt: at})
	if err != nil {
		return app.IdempotencyRecord{}, false, fmt.Errorf("insert idempotency key: %w", err)
	}
	if n == 1 {
		return claimed, true, nil
	}
	n, err = s.q.ReclaimIdempotencyKey(ctx, sqlcdb.ReclaimIdempotencyKeyParams{
		ClaimedAt: at, UserID: uid, Key: key, RequestHash: requestHash, StaleBefore: staleBefore,
	})
	if err != nil {
		return app.IdempotencyRecord{}, false, fmt.Errorf("reclaim idempotency key: %w", err)
	}
	if n == 1 {
		return claimed, true, nil
	}
	rec, found, err := s.IdempotencyKey(ctx, userID, key)
	if err != nil {
		return app.IdempotencyRecord{}, false, err
	}
	if !found {
		// The request that owned the key failed and released it between our
		// insert and this read; report "in progress" so the client retries.
		return app.IdempotencyRecord{RequestHash: requestHash}, false, nil
	}
	return rec, false, nil
}

func (s *Store) IdempotencyKey(ctx context.Context, userID, key string) (app.IdempotencyRecord, bool, error) {
	uid, err := mustParseUUID(userID)
	if err != nil {
		return app.IdempotencyRecord{}, false, err
	}
	row, err := s.q.GetIdempotencyKey(ctx, sqlcdb.GetIdempotencyKeyParams{UserID: uid, Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return app.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return app.IdempotencyRecord{}, false, fmt.Errorf("get idempotency key: %w", err)
	}
	rec := app.IdempotencyRecord{RequestHash: row.RequestHash, ClaimedAt: row.CreatedAt}
	if row.StatusCode != nil {
		rec.Response = &app.Response{Status: int(*row.StatusCode), Body: row.Response}
	}
	return rec, true, nil
}

func (s *Store) DeleteIdempotencyKey(ctx context.Context, userID, key string, claimedAt time.Time) error {
	uid, err := mustParseUUID(userID)
	if err != nil {
		return err
	}
	if err := s.q.DeleteIdempotencyKey(ctx, sqlcdb.DeleteIdempotencyKeyParams{UserID: uid, Key: key, CreatedAt: claimedAt}); err != nil {
		return fmt.Errorf("delete idempotency key: %w", err)
	}
	return nil
}

func (s *Store) DeleteIdempotencyKeysBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.q.DeleteIdempotencyKeysBefore(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("delete expired idempotency keys: %w", err)
	}
	return n, nil
}

func (t *txStore) CompleteIdempotencyKey(ctx context.Context, userID, key string, claimedAt time.Time, resp app.Response) error {
	uid, err := mustParseUUID(userID)
	if err != nil {
		return err
	}
	code := int32(resp.Status)
	n, err := t.q.CompleteIdempotencyKey(ctx, sqlcdb.CompleteIdempotencyKeyParams{
		Response: resp.Body, StatusCode: &code, UserID: uid, Key: key, ClaimedAt: claimedAt,
	})
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	if n == 0 {
		return app.ErrIdempotencyClaimLost
	}
	return nil
}
