package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	ShippingFee int64            // flat fee per order (SHIPPING_FEE_FLAT)
	Now         func() time.Time // nil = UTC wall clock
	Logger      *slog.Logger     // nil = slog.Default()
}

// CheckoutInput is a validated checkout request.
type CheckoutInput struct {
	BuyerID        string
	IdempotencyKey string
	SkuIDs         []string // nil = the whole cart
	Address        domain.Address
	PaymentMethod  string
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
//   - same key and request, still running → ErrIdempotencyInProgress;
//   - same key, different request → ErrIdempotencyKeyReused.
//
// Successes and deterministic rejections are rendered by enc, stored and
// returned as a Response. Transient failures are returned as errors and free
// the key so the client can retry with it. Cancellation of ctx is ignored:
// once orders exist the checkout must finish or compensate.
func (s *CheckoutService) Checkout(ctx context.Context, in CheckoutInput, enc Encoder) (Response, error) {
	ctx = context.WithoutCancel(ctx)
	in.SkuIDs = normalizeIDs(in.SkuIDs)
	hash := RequestHash(in)
	rec, created, err := s.store.BeginIdempotency(ctx, in.BuyerID, in.IdempotencyKey, hash, s.cfg.Now())
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
	resp, err := s.run(ctx, in, enc)
	if err != nil {
		if derr := s.store.DeleteIdempotencyKey(ctx, in.BuyerID, in.IdempotencyKey); derr != nil {
			s.cfg.Logger.ErrorContext(ctx, "release idempotency key failed", "buyer_id", in.BuyerID, "error", derr)
		}
		return Response{}, err
	}
	return resp, nil
}

func (s *CheckoutService) run(ctx context.Context, in CheckoutInput, enc Encoder) (Response, error) {
	lines, err := s.selectLines(ctx, in)
	if err != nil {
		return s.reject(ctx, in, enc, err)
	}
	skus, err := s.catalog.GetSkus(ctx, skuIDsOf(lines))
	if err != nil {
		return Response{}, err
	}
	checkoutID := newID()
	orders, err := domain.BuildOrders(domain.CheckoutParams{
		CheckoutID: checkoutID, BuyerID: in.BuyerID, Lines: lines, Skus: skus,
		ShippingFee: s.cfg.ShippingFee, Address: in.Address, PaymentMethod: in.PaymentMethod,
		Now: s.cfg.Now(), NewID: newID,
	})
	if err != nil {
		return s.reject(ctx, in, enc, err)
	}
	// Tx A: the orders exist (PENDING) before any stock is reserved, so the
	// sweeper can always find and compensate a checkout that dies midway.
	err = s.store.InTx(ctx, func(tx Tx) error {
		for _, o := range orders {
			if err := tx.InsertOrder(ctx, o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if err := s.reserveAll(ctx, orders); err != nil {
		return Response{}, err // compensation: Task 11
	}
	return s.commit(ctx, in, enc, CheckoutResult{CheckoutID: checkoutID, Orders: orders}, lines)
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
func (s *CheckoutService) reject(ctx context.Context, in CheckoutInput, enc Encoder, cause error) (Response, error) {
	if !storable(cause) {
		return Response{}, cause
	}
	resp := enc(CheckoutResult{}, cause)
	err := s.store.InTx(ctx, func(tx Tx) error {
		return tx.CompleteIdempotencyKey(ctx, in.BuyerID, in.IdempotencyKey, resp)
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

// commit is Tx C: every order moves to PENDING_CONFIRMATION with its events
// and the response is stored, atomically.
func (s *CheckoutService) commit(ctx context.Context, in CheckoutInput, enc Encoder, res CheckoutResult, lines []domain.Line) (Response, error) {
	now := s.cfg.Now()
	var resp Response
	err := s.store.InTx(ctx, func(tx Tx) error {
		for i := range res.Orders {
			o := &res.Orders[i]
			if err := tx.Publish(ctx, o.ID, orderCreated(*o), now); err != nil {
				return err
			}
			if err := applyTransition(ctx, tx, o, domain.ActionSubmit, domain.SystemActor(), "", now); err != nil {
				return err
			}
		}
		resp = enc(res, nil)
		return tx.CompleteIdempotencyKey(ctx, in.BuyerID, in.IdempotencyKey, resp)
	})
	if errors.Is(err, ErrConcurrentUpdate) {
		// The sweeper cancelled these orders first (the checkout outlived
		// PENDING_TIMEOUT): give back whatever we reserved after it released.
		s.releaseAll(ctx, res.Orders)
		return Response{}, ErrCheckoutExpired
	}
	if err != nil {
		return Response{}, err
	}
	if err := s.carts.Remove(ctx, in.BuyerID, skuIDsOf(lines)...); err != nil {
		s.cfg.Logger.WarnContext(ctx, "clear purchased cart lines failed", "buyer_id", in.BuyerID, "error", err)
	}
	return resp, nil
}

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
