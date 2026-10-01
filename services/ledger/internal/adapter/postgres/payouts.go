package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// errPayoutVersion: the payout changed since it was claimed (cannot happen under the row lock).
var errPayoutVersion = errors.New("payout was modified concurrently")

func (s *Store) ClaimKey(ctx context.Context, scope string, userID uuid.UUID, key string, hash []byte, at, staleBefore time.Time) (idemkey.Record, bool, error) {
	return idemkey.Claim(ctx, s.pool, scope, userID.String(), key, hash, at, staleBefore)
}

func (s *Store) ReleaseKey(ctx context.Context, scope string, userID uuid.UUID, key string, claimedAt time.Time) error {
	return idemkey.Release(ctx, s.pool, scope, userID.String(), key, claimedAt)
}

func (t *txStore) CompleteKey(ctx context.Context, scope string, userID uuid.UUID, key string, claimedAt time.Time, resp app.Response) error {
	return idemkey.Complete(ctx, t.tx, scope, userID.String(), key, claimedAt, resp)
}

func (t *txStore) InsertPayout(ctx context.Context, p domain.Payout) error {
	if err := t.q.InsertPayout(ctx, sqlcdb.InsertPayoutParams{
		ID: p.ID, SellerUserID: p.SellerUserID, Amount: p.Amount, Status: string(p.Status),
		RequestedAt: p.RequestedAt, Version: int32(p.Version),
	}); err != nil {
		return fmt.Errorf("insert payout: %w", err)
	}
	return nil
}

func toDomainPayout(r sqlcdb.Payout) domain.Payout {
	return domain.Payout{ID: r.ID, SellerUserID: r.SellerUserID, Amount: r.Amount, Status: domain.PayoutStatus(r.Status),
		FailureReason: textPtr(r.FailureReason), RequestedAt: r.RequestedAt, SettledAt: r.SettledAt, Version: int(r.Version)}
}

func (t *txStore) ClaimDuePayout(ctx context.Context, due time.Time) (domain.Payout, bool, error) {
	r, err := t.q.ClaimDuePayout(ctx, due)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Payout{}, false, nil
	}
	if err != nil {
		return domain.Payout{}, false, fmt.Errorf("claim due payout: %w", err)
	}
	return toDomainPayout(r), true, nil
}

func (t *txStore) FinishPayout(ctx context.Context, p domain.Payout, prevVersion int) error {
	n, err := t.q.FinishPayout(ctx, sqlcdb.FinishPayoutParams{
		Status: string(p.Status), FailureReason: pgTextPtr(p.FailureReason), SettledAt: p.SettledAt,
		Version: int32(p.Version), ID: p.ID, PrevVersion: int32(prevVersion),
	})
	if err != nil {
		return fmt.Errorf("finish payout: %w", err)
	}
	if n == 0 {
		return errPayoutVersion
	}
	return nil
}

func (t *txStore) PublishPayoutEvent(ctx context.Context, sellerID uuid.UUID, p event.Payload, at time.Time) error {
	b, err := event.Encode(p, at)
	if err != nil {
		return err
	}
	return outbox.Write(ctx, t.tx, outbox.Message{Topic: event.TopicPayoutEvents, Key: sellerID.String(), Payload: b})
}

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func pgTextPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
