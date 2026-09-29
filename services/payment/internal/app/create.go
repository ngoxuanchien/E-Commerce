package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// CheckoutOrder is one order of a checkout as order-svc reports it.
type CheckoutOrder struct {
	OrderID, SellerUserID, Status     string
	Subtotal, Total, CommissionAmount int64
}

// Checkout is a checkout with its orders.
type Checkout struct {
	ID, BuyerID, PaymentMethod string
	Deadline                   *time.Time
	Orders                     []CheckoutOrder
}

// Orders is the port to order-svc's OrderInternal API.
type Orders interface {
	GetCheckout(ctx context.Context, checkoutID string) (Checkout, error) // ErrNotFound | ErrOrdersUnavailable
}

// Errors of the create flow. ErrKeyReused and ErrKeyInProgress are not stored as replays.
var (
	ErrOrdersUnavailable = errors.New("order service unavailable")
	ErrKeyReused         = errors.New("idempotency key reused with a different request")
	ErrKeyInProgress     = errors.New("a request with this idempotency key is in progress")
)

// StateError is a business rejection that is stored and replayed (HTTP 409).
type StateError struct{ Reason string } // COD_CHECKOUT | NOT_AWAITING_PAYMENT | DEADLINE_PASSED | ALREADY_PAID | BELOW_PROVIDER_MINIMUM

func (e *StateError) Error() string { return "invalid state: " + e.Reason }

const (
	idemScope       = "payment"
	claimStaleAfter = 2 * time.Minute
	statusAwaiting  = "AWAITING_PAYMENT"
	methodCOD       = "COD"
	methodStripe    = "STRIPE"
	reasonProvider  = "PROVIDER_ERROR"
)

// CreateConfig configures Creator.
type CreateConfig struct {
	PublicBaseURL   string
	StripeMinAmount int64
}

// CreateInput is one POST /api/payments request.
type CreateInput struct{ BuyerID, IdempotencyKey, CheckoutID, ClientIP string }

// Encoder renders a result or a storable error as the HTTP response that is stored for replays.
type Encoder func(domain.Payment, error) idemkey.Response

// Creator implements CreatePayment (spec §6).
type Creator struct {
	store     Store
	db        idemkey.DB
	orders    Orders
	providers Providers
	obs       *Observer
	cfg       CreateConfig
	now       func() time.Time
}

func NewCreator(store Store, db idemkey.DB, orders Orders, providers Providers, obs *Observer, cfg CreateConfig, now func() time.Time) *Creator {
	return &Creator{store: store, db: db, orders: orders, providers: providers, obs: obs, cfg: cfg, now: now}
}

// Create starts (or resumes) paying a checkout. The result is stored under the idempotency key
// and replayed verbatim; transient failures release the key and are returned as errors.
func (c *Creator) Create(ctx context.Context, in CreateInput, enc Encoder) (idemkey.Response, error) {
	now := c.now()
	sum := sha256.Sum256([]byte(in.CheckoutID))
	hash := sum[:]
	rec, created, err := idemkey.Claim(ctx, c.db, idemScope, in.BuyerID, in.IdempotencyKey, hash, now, now.Add(-claimStaleAfter))
	if err != nil {
		return idemkey.Response{}, err
	}
	if !created {
		if !bytes.Equal(rec.RequestHash, hash) {
			return idemkey.Response{}, ErrKeyReused
		}
		if rec.Response == nil {
			return idemkey.Response{}, ErrKeyInProgress
		}
		return *rec.Response, nil
	}

	// Finishing the claim must survive a client that gave up.
	fin := context.WithoutCancel(ctx)
	p, err := c.create(ctx, in, now)
	var st *StateError
	switch {
	case err == nil:
		return c.complete(fin, in, rec.ClaimedAt, enc(p, nil))
	case errors.As(err, &st), errors.Is(err, ErrNotFound):
		return c.complete(fin, in, rec.ClaimedAt, enc(domain.Payment{}, err))
	default:
		if rerr := idemkey.Release(fin, c.db, idemScope, in.BuyerID, in.IdempotencyKey, rec.ClaimedAt); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return idemkey.Response{}, err
	}
}

func (c *Creator) complete(ctx context.Context, in CreateInput, claim time.Time, resp idemkey.Response) (idemkey.Response, error) {
	if err := idemkey.Complete(ctx, c.db, idemScope, in.BuyerID, in.IdempotencyKey, claim, resp); err != nil {
		if errors.Is(err, idemkey.ErrClaimLost) {
			return idemkey.Response{}, ErrKeyInProgress
		}
		return idemkey.Response{}, err
	}
	return resp, nil
}

// create runs steps 2-10 of the flow.
func (c *Creator) create(ctx context.Context, in CreateInput, now time.Time) (domain.Payment, error) {
	co, err := c.orders.GetCheckout(ctx, in.CheckoutID)
	if err != nil {
		return domain.Payment{}, err
	}
	if co.BuyerID != in.BuyerID {
		return domain.Payment{}, ErrNotFound
	}
	if co.PaymentMethod == methodCOD {
		return domain.Payment{}, &StateError{Reason: "COD_CHECKOUT"}
	}
	var payable []CheckoutOrder
	var amount int64
	for _, o := range co.Orders {
		if o.Status == statusAwaiting {
			payable = append(payable, o)
			amount += o.Total
		}
	}
	if len(payable) == 0 {
		return domain.Payment{}, &StateError{Reason: "NOT_AWAITING_PAYMENT"}
	}
	if co.Deadline == nil || !now.Before(*co.Deadline) {
		return domain.Payment{}, &StateError{Reason: "DEADLINE_PASSED"}
	}
	prior, err := c.store.ListCheckoutPayments(ctx, co.ID)
	if err != nil {
		return domain.Payment{}, err
	}
	for _, p := range prior {
		if p.Captured {
			return domain.Payment{}, &StateError{Reason: "ALREADY_PAID"}
		}
	}
	if co.PaymentMethod == methodStripe && amount < c.cfg.StripeMinAmount {
		return domain.Payment{}, &StateError{Reason: "BELOW_PROVIDER_MINIMUM"}
	}
	provider, ok := c.providers.ForMethod(co.PaymentMethod)
	if !ok {
		return domain.Payment{}, fmt.Errorf("no provider for payment method %q", co.PaymentMethod)
	}
	for _, p := range prior {
		if p.Status == domain.StatusPending {
			if err := c.retire(ctx, p); err != nil {
				return domain.Payment{}, err
			}
		}
	}

	np := NewPayment{
		Payment: domain.Payment{
			ID: uuid.Must(uuid.NewV7()).String(), CheckoutID: co.ID, BuyerID: in.BuyerID,
			Method: co.PaymentMethod, Provider: provider.Name(), Status: domain.StatusPending,
			Amount: amount, ExpiresAt: *co.Deadline, CreatedAt: now, UpdatedAt: now,
		},
	}
	for _, o := range payable {
		np.Allocations = append(np.Allocations, Allocation{
			OrderID: o.OrderID, SellerUserID: o.SellerUserID, Amount: o.Total, CommissionAmount: o.CommissionAmount,
		})
	}
	if err := c.store.InTx(ctx, func(tx Tx) error { return tx.InsertPayment(ctx, np) }); err != nil {
		return domain.Payment{}, fmt.Errorf("insert payment: %w", err)
	}

	sess, err := provider.CreateSession(ctx, SessionInput{
		PaymentID: np.ID, CheckoutID: co.ID, Amount: amount, ExpiresAt: np.ExpiresAt,
		ReturnURL:   c.returnURL(provider.Name(), np.ID),
		Description: "Thanh toan don hang " + co.ID, ClientIP: in.ClientIP,
	})
	fin := context.WithoutCancel(ctx)
	if err != nil {
		failed := np.Payment
		failed.Status, failed.FailureReason, failed.UpdatedAt = domain.StatusFailed, reasonProvider, c.now()
		if uerr := c.store.InTx(fin, func(tx Tx) error { return tx.UpdatePayment(fin, failed) }); uerr != nil {
			err = errors.Join(err, uerr)
		}
		if errors.Is(err, ErrProviderUnavailable) {
			return domain.Payment{}, err
		}
		return domain.Payment{}, fmt.Errorf("%w: create session: %w", ErrProviderUnavailable, err)
	}
	if err := c.store.InTx(ctx, func(tx Tx) error {
		return tx.SetSession(ctx, np.ID, sess.ProviderRef, sess.RedirectURL)
	}); err != nil {
		return domain.Payment{}, fmt.Errorf("set session: %w", err)
	}
	return c.store.GetPayment(ctx, np.ID)
}

// retire cancels a checkout's open attempt: Cancel, then Query, then apply what the provider
// says. A success wins (ALREADY_PAID); an attempt still pending is expired here, because a
// provider that cannot cancel (VNPay) will not do it for us.
func (c *Creator) retire(ctx context.Context, p domain.Payment) error {
	prov, ok := c.providers.ByName(p.Provider)
	if !ok {
		return fmt.Errorf("no provider %q for payment %s", p.Provider, p.ID)
	}
	if _, err := prov.Cancel(ctx, p); err != nil {
		return err
	}
	obs, err := prov.Query(ctx, p)
	if err != nil {
		return err
	}
	if _, err := c.obs.ApplyObserved(ctx, p.Provider, "", p.ID, obs); err != nil {
		return err
	}
	cur, err := c.store.GetPayment(ctx, p.ID) // Outcome.Payment is stale when Unchanged
	if err != nil {
		return err
	}
	if cur.Captured || cur.Status == domain.StatusSucceeded {
		return &StateError{Reason: "ALREADY_PAID"}
	}
	_, err = c.obs.Expire(ctx, p.ID)
	return err
}

// returnURL is where the provider sends the buyer's browser back to (the gateway-facing URL).
func (c *Creator) returnURL(provider, paymentID string) string {
	base := strings.TrimRight(c.cfg.PublicBaseURL, "/")
	switch provider {
	case "vnpay":
		return base + "/api/payments/vnpay/return"
	case "stripe":
		return base + "/api/payments/stripe/return?session_id={CHECKOUT_SESSION_ID}"
	default:
		return base + "/api/payments/fake/return?payment_id=" + paymentID
	}
}

// Get returns the caller's own payment; anyone else's is ErrNotFound.
func (c *Creator) Get(ctx context.Context, buyerID, paymentID string) (domain.Payment, error) {
	p, err := c.store.GetPayment(ctx, paymentID)
	if err != nil {
		return domain.Payment{}, err
	}
	if p.BuyerID != buyerID {
		return domain.Payment{}, ErrNotFound
	}
	return p, nil
}
