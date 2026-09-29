// Package app holds order-svc's use cases: cart, checkout, the order
// lifecycle and the PENDING sweeper. It depends on the domain and on the port
// interfaces below; adapters implement them.
package app

import (
	"context"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// Catalog is catalog-svc's internal API (adapter/catalog).
type Catalog interface {
	// GetSkus returns current data for the SKUs among ids that catalog knows.
	GetSkus(ctx context.Context, ids []string) (map[string]domain.Sku, error)
	// ReserveStock reserves stock for an order. Idempotent per orderID.
	// Shortages are reported as *OutOfStockError.
	ReserveStock(ctx context.Context, orderID string, lines []domain.Line) error
	// ReleaseStock returns an order's reserved stock. Idempotent.
	ReleaseStock(ctx context.Context, orderID string) error
}

// CartStore persists carts as SKU ID → quantity (adapter/redis).
type CartStore interface {
	Lines(ctx context.Context, userID string) (map[string]int32, error)
	// Set creates or updates a line. ErrCartFull when a new line would exceed
	// domain.MaxCartLines.
	Set(ctx context.Context, userID, skuID string, quantity int32) error
	Remove(ctx context.Context, userID string, skuIDs ...string) error
}

// Store is the order database (adapter/postgres).
type Store interface {
	InTx(ctx context.Context, fn func(Tx) error) error
	// GetOrder returns an order with items and history; ErrNotFound if absent.
	GetOrder(ctx context.Context, id string) (domain.Order, error)
	// ListOrders returns up to q.Limit orders, newest first, with items and
	// history, strictly after q.After when set.
	ListOrders(ctx context.Context, q ListQuery) ([]domain.Order, error)
	// BeginIdempotency claims (userID, key) at `at`. created reports whether
	// this call holds the claim now: it inserted the row, or took over an
	// orphaned one (same requestHash, no response, claimed before
	// staleBefore; the zero time never reclaims). Otherwise rec describes the
	// existing row.
	BeginIdempotency(ctx context.Context, userID, key string, requestHash []byte, at, staleBefore time.Time) (rec IdempotencyRecord, created bool, err error)
	// IdempotencyKey reads (userID, key) without claiming it; found is false
	// if there is no row.
	IdempotencyKey(ctx context.Context, userID, key string) (rec IdempotencyRecord, found bool, err error)
	// DeleteIdempotencyKey releases the key if claimedAt is still its claim.
	DeleteIdempotencyKey(ctx context.Context, userID, key string, claimedAt time.Time) error
	DeleteIdempotencyKeysBefore(ctx context.Context, before time.Time) (int64, error)
}

// Tx is the set of writes that must commit atomically.
type Tx interface {
	// InsertOrder stores a new order with its items and history entries.
	InsertOrder(ctx context.Context, o domain.Order) error
	// SaveTransition persists o's status, cancel reason and updated_at if the
	// stored version is still prevVersion (and bumps it), then appends o's
	// last history entry. ErrConcurrentUpdate when the version moved on.
	SaveTransition(ctx context.Context, o domain.Order, prevVersion int) error
	// Publish writes p to the outbox for topic order.events, keyed by orderID.
	Publish(ctx context.Context, orderID string, p event.Payload, at time.Time) error
	// ClaimStalePending locks up to limit PENDING orders created before
	// `before` (FOR UPDATE SKIP LOCKED). Items and history are not loaded.
	ClaimStalePending(ctx context.Context, before time.Time, limit int) ([]domain.Order, error)
	// CompleteIdempotencyKey stores the final response for (userID, key).
	// ErrIdempotencyClaimLost when claimedAt is no longer the key's claim (a
	// newer request reclaimed it), which rolls the transaction back.
	CompleteIdempotencyKey(ctx context.Context, userID, key string, claimedAt time.Time, resp Response) error
}

// Party selects whose orders a list returns.
type Party int

const (
	PartyBuyer Party = iota + 1
	PartySeller
)

// ListQuery selects a page of orders.
type ListQuery struct {
	Party  Party
	UserID string
	Status *domain.Status
	Limit  int
	After  *Cursor
}

// Cursor is a keyset position in a newest-first order list.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Response is a rendered HTTP response, stored for idempotent replays.
type Response struct {
	Status int
	Body   []byte
}

// IdempotencyRecord is an existing idempotency key. Response is nil while the
// original request is still in progress.
type IdempotencyRecord struct {
	RequestHash []byte
	Response    *Response
	ClaimedAt   time.Time // when the owning request claimed the key
}
