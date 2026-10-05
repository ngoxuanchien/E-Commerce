package app

import (
	"context"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// CheckoutView is a checkout as payment-svc sees it (OrderInternal.GetCheckout).
type CheckoutView struct {
	CheckoutID, BuyerID, PaymentMethod string
	PaymentDeadline                    *time.Time     // nil for COD
	Orders                             []domain.Order // every status, ordered by order ID
}

// CheckoutQuery reads a checkout for payment-svc, which needs the whole
// checkout to allocate one payment across its orders.
type CheckoutQuery struct {
	store Store
}

// NewCheckoutQuery reads checkouts from store.
func NewCheckoutQuery(store Store) *CheckoutQuery { return &CheckoutQuery{store: store} }

// GetCheckout returns every order of the checkout, whatever its status, or
// ErrNotFound when the checkout has none. The buyer, the payment method and
// the deadline are checkout-wide, so they are read off the orders rather than
// stored again: the first order names the buyer and the method, and the first
// order with a deadline names the deadline (every order of an online checkout
// shares it, while COD has none).
func (q *CheckoutQuery) GetCheckout(ctx context.Context, checkoutID string) (CheckoutView, error) {
	orders, err := q.store.ListCheckoutOrders(ctx, checkoutID)
	if err != nil {
		return CheckoutView{}, err
	}
	if len(orders) == 0 {
		return CheckoutView{}, ErrNotFound
	}
	view := CheckoutView{
		CheckoutID:    orders[0].CheckoutID,
		BuyerID:       orders[0].BuyerID,
		PaymentMethod: orders[0].PaymentMethod,
		Orders:        orders,
	}
	for _, o := range orders {
		if o.PaymentDeadline != nil {
			view.PaymentDeadline = o.PaymentDeadline
			break
		}
	}
	return view, nil
}
