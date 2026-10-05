package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// Response is a rendered HTTP response, stored for Idempotency-Key replays.
type Response = idemkey.Response

// PayoutEncoder renders a payout result: the created payout (err nil) or a
// deterministic rejection (*InsufficientFundsError). The HTTP adapter owns it.
type PayoutEncoder func(p domain.Payout, err error) Response

const payoutScope = "payout"

var (
	ErrIdempotencyKeyReused  = errors.New("ledger: idempotency key reused with a different request")
	ErrIdempotencyInProgress = errors.New("ledger: a request with this idempotency key is in progress")
)

// BelowMinimumError: the amount is below PAYOUT_MIN_AMOUNT.
type BelowMinimumError struct{ Min int64 }

func (e *BelowMinimumError) Error() string {
	return fmt.Sprintf("payout amount must be at least %d", e.Min)
}

// InsufficientFundsError: the wallet's available balance is below the amount.
type InsufficientFundsError struct{ Available int64 }

func (e *InsufficientFundsError) Error() string {
	return fmt.Sprintf("insufficient funds: %d available", e.Available)
}

// PayoutConfig tunes PayoutService. Zero ClaimTimeout = 1 minute.
type PayoutConfig struct {
	MinAmount    int64 // PAYOUT_MIN_AMOUNT
	ClaimTimeout time.Duration
	Now          func() time.Time
	Logger       *slog.Logger
}

// PayoutService handles payout requests (WS2' §7).
type PayoutService struct {
	store Store
	cfg   PayoutConfig
}

func NewPayoutService(store Store, cfg PayoutConfig) *PayoutService {
	if cfg.ClaimTimeout == 0 {
		cfg.ClaimTimeout = time.Minute
	}
	return &PayoutService{store: store, cfg: cfg}
}

// PayoutRequestInput is one POST /api/seller/payouts.
type PayoutRequestInput struct {
	SellerID       uuid.UUID
	IdempotencyKey string
	Amount         int64
}

// Request moves Amount from the seller's wallet to payout-pending, or answers
// INSUFFICIENT_FUNDS. Both outcomes are stored under the Idempotency-Key and
// replayed verbatim; transient failures free the key and return an error.
func (s *PayoutService) Request(ctx context.Context, in PayoutRequestInput, enc PayoutEncoder) (Response, error) {
	if in.Amount <= 0 || in.Amount < s.cfg.MinAmount {
		return Response{}, &BelowMinimumError{Min: max(1, s.cfg.MinAmount)}
	}
	hash := sha256.Sum256([]byte(strconv.FormatInt(in.Amount, 10)))
	now := s.cfg.Now()
	rec, claimed, err := s.store.ClaimKey(ctx, payoutScope, in.SellerID, in.IdempotencyKey, hash[:], now, now.Add(-s.cfg.ClaimTimeout))
	if err != nil {
		return Response{}, err
	}
	if !claimed {
		switch {
		case !bytes.Equal(rec.RequestHash, hash[:]):
			return Response{}, ErrIdempotencyKeyReused
		case rec.Response == nil:
			return Response{}, ErrIdempotencyInProgress
		default:
			return *rec.Response, nil
		}
	}
	resp, err := s.request(ctx, in, rec.ClaimedAt, enc)
	if err == nil {
		return resp, nil
	}
	if errors.Is(err, idemkey.ErrClaimLost) {
		return Response{}, ErrIdempotencyInProgress
	}
	if rerr := s.store.ReleaseKey(context.WithoutCancel(ctx), payoutScope, in.SellerID, in.IdempotencyKey, rec.ClaimedAt); rerr != nil {
		s.cfg.Logger.ErrorContext(ctx, "release payout idempotency key", "error", rerr)
	}
	return Response{}, err
}

func (s *PayoutService) request(ctx context.Context, in PayoutRequestInput, claimedAt time.Time, enc PayoutEncoder) (Response, error) {
	now := s.cfg.Now()
	p := domain.Payout{ID: uuid.Must(uuid.NewV7()), SellerUserID: in.SellerID, Amount: in.Amount,
		Status: domain.PayoutRequested, RequestedAt: now, Version: 1}
	j, err := domain.PayoutRequest(p)
	if err != nil {
		return Response{}, err
	}
	j.OccurredAt = now
	var resp Response
	err = s.store.InTx(ctx, func(tx Tx) error {
		_, err := Post(ctx, tx, j) // locks the wallet and checks available ≥ amount
		var oe *OverdrawError
		switch {
		case errors.As(err, &oe):
			resp = enc(domain.Payout{}, &InsufficientFundsError{Available: domain.Shown(oe.Balance)})
		case err != nil:
			return err
		default:
			if err := tx.InsertPayout(ctx, p); err != nil {
				return err
			}
			ev := event.PayoutRequested{PayoutID: p.ID.String(), SellerUserID: p.SellerUserID.String(), Amount: p.Amount}
			if err := tx.PublishPayoutEvent(ctx, p.SellerUserID, ev, now); err != nil {
				return err
			}
			resp = enc(p, nil)
		}
		return tx.CompleteKey(ctx, payoutScope, in.SellerID, in.IdempotencyKey, claimedAt, resp)
	})
	return resp, err
}

// List returns the seller's payouts, newest first, optionally by status.
func (s *PayoutService) List(ctx context.Context, seller uuid.UUID, status *domain.PayoutStatus, in ListInput) (Page[domain.Payout], error) {
	before, err := decodeOptionalCursor(in.Cursor)
	if err != nil {
		return Page[domain.Payout]{}, err
	}
	rows, err := s.store.ListPayouts(ctx, seller, status, before, in.Limit+1)
	if err != nil {
		return Page[domain.Payout]{}, err
	}
	return pageOf(rows, in.Limit, func(p domain.Payout) Cursor { return Cursor{At: p.RequestedAt, ID: p.ID} }), nil
}
