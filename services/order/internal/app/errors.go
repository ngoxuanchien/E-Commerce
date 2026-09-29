package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// Errors returned by the use cases; adapter/http maps them to API errors.
var (
	ErrNotFound              = errors.New("order not found")
	ErrSkuNotFound           = errors.New("sku not found")
	ErrSkuUnavailable        = errors.New("sku is not available")
	ErrOwnShop               = errors.New("cannot buy from your own shop")
	ErrCartFull              = errors.New("cart is full")
	ErrInvalidQuantity       = errors.New("invalid quantity")
	ErrEmptyCart             = errors.New("nothing to check out")
	ErrCatalogUnavailable    = errors.New("catalog unavailable")
	ErrCatalogRejected       = errors.New("catalog rejected the request")
	ErrIdempotencyInProgress = errors.New("idempotency key is in progress")
	ErrIdempotencyKeyReused  = errors.New("idempotency key reused with a different request")
	ErrIdempotencyClaimLost  = errors.New("idempotency key was reclaimed by a newer request")
	ErrConcurrentUpdate      = errors.New("order was modified concurrently")
	ErrCheckoutExpired       = errors.New("checkout expired before it completed")
	ErrInvalidCursor         = errors.New("invalid cursor")
)

// SelectionError lists requested sku_ids that are not in the cart.
type SelectionError struct {
	NotInCart []string
}

func (e *SelectionError) Error() string {
	return "sku_ids not in cart: " + strings.Join(e.NotInCart, ", ")
}

// OutOfStockError is a failed reservation with the SKUs that are short.
type OutOfStockError struct {
	Shortages []domain.Shortage
}

func (e *OutOfStockError) Error() string {
	return fmt.Sprintf("insufficient stock for %d sku(s)", len(e.Shortages))
}
