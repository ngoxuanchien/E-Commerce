package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// CheckoutConfig configures CheckoutService.
type CheckoutConfig struct {
	ShippingFee int64 // flat fee per order (SHIPPING_FEE_FLAT)
	// ClaimTimeout is how long an idempotency key may stay without a
	// response before a retry of the same request takes it over (0 = never).
	// bootstrap uses PENDING_TIMEOUT + SWEEPER_INTERVAL + 1 min: by then the
	// sweeper has cancelled whatever the crashed run left PENDING.
	ClaimTimeout time.Duration
	// OnlineMethods are the accepted online payment methods
	// (ONLINE_PAYMENT_METHODS); an empty list means COD only.
	OnlineMethods []string
	// CommissionRateBps is the platform commission snapshot in basis points,
	// applied to online orders at checkout.
	CommissionRateBps int32
	// PaymentTimeout is how long an online order may stay AWAITING_PAYMENT
	// before the sweeper cancels it (PAYMENT_TIMEOUT).
	PaymentTimeout time.Duration
	// Deadline bounds the whole forward path — from claiming the idempotency
	// key to Tx C (0 = unbounded). bootstrap sets it to PENDING_TIMEOUT, so a
	// checkout gives up before the sweeper would cancel its orders.
	Deadline time.Duration
	// CleanupTimeout bounds the compensation of a failed checkout — stock
	// release, Tx B, Tx C recovery and releasing the idempotency key
	// (0 = defaultCleanupTimeout). It gets a fresh context, so compensation
	// still works after Deadline fired.
	CleanupTimeout time.Duration
	Now            func() time.Time // nil = UTC wall clock
	Logger         *slog.Logger     // nil = slog.Default()
}

// CheckoutInput is a validated checkout request.
type CheckoutInput struct {
	BuyerID        string
	IdempotencyKey string
	SkuIDs         []string // nil = the whole cart
	Address        domain.Address
	PaymentMethod  string

	claimedAt time.Time // this run's idempotency claim, set by Checkout
}

// CheckoutResult is a successful checkout.
type CheckoutResult struct {
	CheckoutID string
	Orders     []domain.Order
}

// Encoder renders a checkout outcome — a result, or a business error — as the
// response that is stored for idempotent replays and sent to the client.
type Encoder func(res CheckoutResult, err error) Response

// CheckoutService turns a cart into one order per shop (WS3 §4).
type CheckoutService struct {
	store   Store
	carts   CartStore
	catalog Catalog
	cfg     CheckoutConfig
}

func NewCheckoutService(store Store, carts CartStore, catalog Catalog, cfg CheckoutConfig) *CheckoutService {
	if cfg.Now == nil {
		cfg.Now = defaultNow
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &CheckoutService{store: store, carts: carts, catalog: catalog, cfg: cfg}
}

// defaultCleanupTimeout bounds a checkout's compensation when the config leaves
// CleanupTimeout unset.
const defaultCleanupTimeout = 30 * time.Second

// forwardCtx bounds the forward path — the key claim, the cart read, GetSkus,
// Tx A, the reservations and Tx C. A zero Deadline leaves it unbounded.
// Cancellation of the caller's ctx is still ignored (README decision 10).
func (s *CheckoutService) forwardCtx(base context.Context) (context.Context, context.CancelFunc) {
	if s.cfg.Deadline <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, s.cfg.Deadline)
}

// cleanupCtx gives the compensation of a failed checkout its own budget, so it
// still runs when the forward deadline already fired, but never forever.
func (s *CheckoutService) cleanupCtx(base context.Context) (context.Context, context.CancelFunc) {
	d := s.cfg.CleanupTimeout
	if d <= 0 {
		d = defaultCleanupTimeout
	}
	return context.WithTimeout(base, d)
}

// RequestHash fingerprints a checkout request for idempotency. Equivalent
// requests (same SKUs in any order, duplicates ignored) hash the same.
func RequestHash(in CheckoutInput) []byte {
	b, _ := json.Marshal(struct { // strings only: cannot fail
		SkuIDs        []string       `json:"sku_ids"`
		Address       domain.Address `json:"shipping_address"`
		PaymentMethod string         `json:"payment_method"`
	}{normalizeIDs(in.SkuIDs), in.Address, in.PaymentMethod})
	sum := sha256.Sum256(b)
	return sum[:]
}

// Checkout runs a checkout idempotently per (buyer, key):
//   - same key and request, completed → the stored response;
//   - same key and request, still running → ErrIdempotencyInProgress, unless
//     the key has had no response for ClaimTimeout: its run is presumed dead
//     and this request takes the key over;
//   - same key, different request → ErrIdempotencyKeyReused.
//
// Successes and deterministic rejections are rendered by enc, stored and
// returned as a Response. Transient failures are returned as errors and free
// the key so the client can retry with it. Cancellation of ctx is ignored:
// once orders exist the checkout must finish or compensate — cfg.Deadline is
// what bounds the run.
func (s *CheckoutService) Checkout(ctx context.Context, in CheckoutInput, enc Encoder) (Response, error) {
	base := context.WithoutCancel(ctx)
	fwd, stopForward := s.forwardCtx(base)
	defer stopForward()
	if domain.IsOnline(in.PaymentMethod) && !slices.Contains(s.cfg.OnlineMethods, in.PaymentMethod) {
		// Before BeginIdempotency: a method this deployment does not accept is a
		// bad request, so it must not consume the caller's idempotency key.
		return Response{}, ErrPaymentMethodNotEnabled
	}
	in.SkuIDs = normalizeIDs(in.SkuIDs)
	hash := RequestHash(in)
	now := s.cfg.Now()
	var staleBefore time.Time
	if s.cfg.ClaimTimeout > 0 {
		staleBefore = now.Add(-s.cfg.ClaimTimeout)
	}
	rec, created, err := s.store.BeginIdempotency(fwd, in.BuyerID, in.IdempotencyKey, hash, now, staleBefore)
	if err != nil {
		return Response{}, err
	}
	if !created {
		switch {
		case !bytes.Equal(rec.RequestHash, hash):
			return Response{}, ErrIdempotencyKeyReused
		case rec.Response == nil:
			return Response{}, ErrIdempotencyInProgress
		default:
			return *rec.Response, nil
		}
	}
	in.claimedAt = rec.ClaimedAt
	resp, err := s.run(base, fwd, in, enc)
	if err != nil {
		var keep *keepKeyError
		if errors.As(err, &keep) {
			s.cfg.Logger.ErrorContext(ctx, "checkout failed before compensation; key stays claimed", "buyer_id", in.BuyerID, "error", err)
			return Response{}, err
		}
		cctx, stopCleanup := s.cleanupCtx(base)
		defer stopCleanup()
		if derr := s.store.DeleteIdempotencyKey(cctx, in.BuyerID, in.IdempotencyKey, in.claimedAt); derr != nil {
			s.cfg.Logger.ErrorContext(ctx, "release idempotency key failed", "buyer_id", in.BuyerID, "error", derr)
		}
		return Response{}, err
	}
	return resp, nil
}

// run drives the forward path under fwd — the key claim through Tx C — and
// hands base to the compensation, which gets its own budget.
func (s *CheckoutService) run(base, fwd context.Context, in CheckoutInput, enc Encoder) (Response, error) {
	lines, err := s.selectLines(fwd, in)
	if err != nil {
		return s.reject(base, in, enc, err)
	}
	skus, err := s.catalog.GetSkus(fwd, skuIDsOf(lines))
	if err != nil {
		return Response{}, err
	}
	checkoutID := newID()
	orders, err := domain.BuildOrders(domain.CheckoutParams{
		CheckoutID: checkoutID, BuyerID: in.BuyerID, Lines: lines, Skus: skus,
		ShippingFee: s.cfg.ShippingFee, Address: in.Address, PaymentMethod: in.PaymentMethod,
		CommissionRateBps: s.cfg.CommissionRateBps,
		Now:               s.cfg.Now(), NewID: newID,
	})
	if err != nil {
		return s.reject(base, in, enc, err)
	}
	// Tx A: the orders exist (PENDING) before any stock is reserved, so the
	// sweeper can always find and compensate a checkout that dies midway.
	err = s.store.InTx(fwd, func(tx Tx) error {
		for _, o := range orders {
			if err := tx.InsertOrder(fwd, o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if err := s.reserveAll(fwd, orders); err != nil {
		return s.abort(base, in, enc, orders, err)
	}
	return s.commit(base, fwd, in, enc, CheckoutResult{CheckoutID: checkoutID, Orders: orders}, lines)
}

// selectLines returns the cart lines to buy, sorted by SKU ID.
func (s *CheckoutService) selectLines(ctx context.Context, in CheckoutInput) ([]domain.Line, error) {
	cart, err := s.carts.Lines(ctx, in.BuyerID)
	if err != nil {
		return nil, err
	}
	ids := in.SkuIDs
	if ids == nil {
		ids = slices.Sorted(maps.Keys(cart))
	} else {
		var missing []string
		for _, id := range ids {
			if _, ok := cart[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return nil, &SelectionError{NotInCart: missing}
		}
	}
	if len(ids) == 0 {
		return nil, ErrEmptyCart
	}
	lines := make([]domain.Line, len(ids))
	for i, id := range ids {
		lines[i] = domain.Line{SkuID: id, Quantity: cart[id]}
	}
	return lines, nil
}

// reject ends a checkout that failed before any order existed.
func (s *CheckoutService) reject(base context.Context, in CheckoutInput, enc Encoder, cause error) (Response, error) {
	if !storable(cause) {
		return Response{}, cause
	}
	resp := enc(CheckoutResult{}, cause)
	// The cleanup budget, like abort and recoverCommit: base alone is unbounded.
	cctx, stop := s.cleanupCtx(base)
	defer stop()
	err := s.store.InTx(cctx, func(tx Tx) error {
		return tx.CompleteIdempotencyKey(cctx, in.BuyerID, in.IdempotencyKey, in.claimedAt, resp)
	})
	if err != nil {
		return Response{}, err
	}
	return resp, nil
}

// reserveAll reserves stock for every order concurrently; nil only if all
// succeeded.
func (s *CheckoutService) reserveAll(ctx context.Context, orders []domain.Order) error {
	errs := make([]error, len(orders))
	var wg sync.WaitGroup
	for i, o := range orders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.catalog.ReserveStock(ctx, o.ID, o.StockLines())
		}()
	}
	wg.Wait()
	return mergeReserveErrors(errs)
}

// abort compensates a failed reservation (WS3 §4 step 7): release every order
// of the checkout, then Tx B cancels them all (system, OUT_OF_STOCK or
// RESERVE_FAILED). Release failures are only logged: the OrderCancelled events
// written in Tx B make catalog release the stock asynchronously.
//
// Tx B can lose the optimistic lock when the sweeper cancelled an order of the
// checkout first — a checkout that outlived PENDING_TIMEOUT. That is not the
// buyer's fault, so it must not be answered as a conflict: the orders are
// re-read and those still PENDING cancelled, and the real cause is stored.
func (s *CheckoutService) abort(base context.Context, in CheckoutInput, enc Encoder, orders []domain.Order, cause error) (Response, error) {
	cctx, stop := s.cleanupCtx(base)
	defer stop()
	s.releaseAll(cctx, orders)
	reason := domain.CancelReserveFailed
	var oos *OutOfStockError
	if errors.As(cause, &oos) {
		reason = domain.CancelOutOfStock
	}
	store := storable(cause)
	now := s.cfg.Now()
	// The second pass cancels only what the re-read shows still PENDING, so it
	// never tries to cancel an order the sweeper already cancelled. A re-read
	// that fails leaves the orders to the sweeper and frees the key.
	for attempt := range 2 {
		fresh := orders
		if attempt > 0 {
			fresh = make([]domain.Order, 0, len(orders))
			for _, o := range orders {
				stored, err := s.store.GetOrder(cctx, o.ID)
				if err != nil {
					return Response{}, fmt.Errorf("re-read orders after losing the reservation race: %w", err)
				}
				if stored.Status == domain.StatusPending {
					fresh = append(fresh, stored)
				}
			}
		}
		var resp Response
		claimLost := false
		err := s.store.InTx(cctx, func(tx Tx) error {
			for i := range fresh {
				if err := applyTransition(cctx, tx, &fresh[i], domain.ActionCancel, domain.SystemActor(), reason, now); err != nil {
					return err
				}
			}
			if !store {
				return nil
			}
			resp = enc(CheckoutResult{}, cause)
			err := tx.CompleteIdempotencyKey(cctx, in.BuyerID, in.IdempotencyKey, in.claimedAt, resp)
			if errors.Is(err, ErrIdempotencyClaimLost) {
				// A retry owns the key now: keep the cancellations, store nothing.
				claimLost = true
				return nil
			}
			return err
		})
		switch {
		case err == nil:
			if claimLost {
				return Response{}, ErrIdempotencyClaimLost
			}
			if !store {
				return Response{}, cause
			}
			return resp, nil
		case errors.Is(err, ErrConcurrentUpdate):
			if attempt == 0 {
				continue // the sweeper won Tx B; re-read and try once more
			}
			// It won twice: answer the real cause without storing it, so the
			// client may retry the same request.
			return Response{}, cause
		default:
			return Response{}, fmt.Errorf("cancel orders after failed reservation: %w", err)
		}
	}
	return Response{}, cause
}

// mergeReserveErrors prefers OUT_OF_STOCK (with every shop's shortages) over
// other failures.
func mergeReserveErrors(errs []error) error {
	var merged *OutOfStockError
	var first error
	for _, err := range errs {
		var oos *OutOfStockError
		switch {
		case err == nil:
		case errors.As(err, &oos):
			if merged == nil {
				merged = &OutOfStockError{}
			}
			merged.Shortages = append(merged.Shortages, oos.Shortages...)
		case first == nil:
			first = err
		}
	}
	if merged != nil {
		slices.SortFunc(merged.Shortages, func(a, b domain.Shortage) int { return strings.Compare(a.SkuID, b.SkuID) })
		return merged
	}
	return first
}

// commit is Tx C: every order moves to PENDING_CONFIRMATION — or, for an
// online method, AWAITING_PAYMENT with a payment deadline — with its events
// and the response is stored, atomically. Tx C runs on fwd; its compensation
// (releasing stock, recovering the commit) runs on a fresh context derived from
// base, so it still works when the deadline fired mid-transaction.
func (s *CheckoutService) commit(base, fwd context.Context, in CheckoutInput, enc Encoder, res CheckoutResult, lines []domain.Line) (Response, error) {
	now := s.cfg.Now()
	var resp Response
	err := s.store.InTx(fwd, func(tx Tx) error {
		for i := range res.Orders {
			o := &res.Orders[i]
			action := domain.ActionSubmit
			if domain.IsOnline(o.PaymentMethod) {
				// One deadline for the whole checkout: every order is paid by
				// the same payment.
				deadline := now.Add(s.cfg.PaymentTimeout)
				o.PaymentDeadline = &deadline
				action = domain.ActionAwaitPayment
			}
			if err := tx.Publish(fwd, o.ID, orderCreated(*o), now); err != nil {
				return err
			}
			if err := applyTransition(fwd, tx, o, action, domain.SystemActor(), "", now); err != nil {
				return err
			}
		}
		resp = enc(res, nil)
		return tx.CompleteIdempotencyKey(fwd, in.BuyerID, in.IdempotencyKey, in.claimedAt, resp)
	})
	if errors.Is(err, ErrConcurrentUpdate) {
		// The sweeper cancelled these orders first (the checkout outlived
		// PENDING_TIMEOUT): give back whatever we reserved after it released.
		cctx, stop := s.cleanupCtx(base)
		defer stop()
		s.releaseAll(cctx, res.Orders)
		return Response{}, ErrCheckoutExpired
	}
	if err != nil {
		return s.recoverCommit(base, in, res.Orders, lines, err)
	}
	// The commit may have landed just before the forward deadline; the cart
	// cleanup gets its own budget so the purchased lines still leave the cart.
	cctx, stop := s.cleanupCtx(base)
	defer stop()
	s.clearCart(cctx, in.BuyerID, lines)
	return resp, nil
}

// recoverCommit handles a Tx C failure other than a lost race. The commit may
// have succeeded with only its acknowledgement lost, so a response stored
// under this run's own claim wins. Otherwise — including when a retry
// reclaimed the key (ErrIdempotencyClaimLost), whose response is never ours —
// the reservations are released and the orders cancelled now rather than left
// to the sweeper, so the client's retry does not run into its own held stock.
// If the cancellation cannot be written, the key stays claimed until the
// sweeper has cleaned up.
func (s *CheckoutService) recoverCommit(base context.Context, in CheckoutInput, orders []domain.Order, lines []domain.Line, cause error) (Response, error) {
	cctx, stop := s.cleanupCtx(base)
	defer stop()
	reason := domain.CancelReserveFailed
	if errors.Is(cause, ErrIdempotencyClaimLost) {
		reason = domain.CancelCheckoutTimeout
	} else {
		rec, found, err := s.store.IdempotencyKey(cctx, in.BuyerID, in.IdempotencyKey) // a read: never claims
		if err == nil && found && rec.ClaimedAt.Equal(in.claimedAt) && rec.Response != nil {
			s.clearCart(cctx, in.BuyerID, lines)
			return *rec.Response, nil
		}
	}
	s.releaseAll(cctx, orders)
	fresh := make([]domain.Order, 0, len(orders))
	for _, o := range orders {
		stored, err := s.store.GetOrder(cctx, o.ID) // the in-memory copy reflects the rolled-back Tx C
		if err != nil {
			return Response{}, &keepKeyError{err: errors.Join(cause, err)}
		}
		if stored.Status == domain.StatusPending {
			fresh = append(fresh, stored)
		}
	}
	now := s.cfg.Now()
	err := s.store.InTx(cctx, func(tx Tx) error {
		for i := range fresh {
			if err := applyTransition(cctx, tx, &fresh[i], domain.ActionCancel, domain.SystemActor(), reason, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, &keepKeyError{err: errors.Join(cause, err)}
	}
	return Response{}, cause
}

// clearCart removes purchased lines from the cart, best effort.
func (s *CheckoutService) clearCart(ctx context.Context, buyerID string, lines []domain.Line) {
	if err := s.carts.Remove(ctx, buyerID, skuIDsOf(lines)...); err != nil {
		s.cfg.Logger.WarnContext(ctx, "clear purchased cart lines failed", "buyer_id", buyerID, "error", err)
	}
}

// keepKeyError marks a failure after which the idempotency key must stay
// claimed: the checkout's outcome is not yet compensated.
type keepKeyError struct{ err error }

func (e *keepKeyError) Error() string { return e.err.Error() }
func (e *keepKeyError) Unwrap() error { return e.err }

// releaseAll releases every order's stock, best effort.
func (s *CheckoutService) releaseAll(ctx context.Context, orders []domain.Order) {
	for _, o := range orders {
		if err := s.catalog.ReleaseStock(ctx, o.ID); err != nil {
			s.cfg.Logger.WarnContext(ctx, "release stock failed", "order_id", o.ID, "error", err)
		}
	}
}

// storable reports whether a failure is a final answer for this request
// (stored and replayed) rather than a transient one.
func storable(err error) bool {
	var (
		sel *SelectionError
		lp  *domain.LineProblemsError
		oos *OutOfStockError
	)
	return errors.Is(err, ErrEmptyCart) || errors.As(err, &sel) || errors.As(err, &lp) ||
		errors.As(err, &oos) || errors.Is(err, ErrCatalogRejected)
}

func normalizeIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

func skuIDsOf(lines []domain.Line) []string {
	ids := make([]string, len(lines))
	for i, l := range lines {
		ids[i] = l.SkuID
	}
	return ids
}
