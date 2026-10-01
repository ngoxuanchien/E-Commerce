package httpadapter

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, toAppError(err))
}

// toAppError maps use-case errors to API errors (WS0 §3 codes).
func toAppError(err error) *httpx.AppError {
	var (
		ae  *httpx.AppError
		sel *app.SelectionError
		lp  *domain.LineProblemsError
		oos *app.OutOfStockError
	)
	switch {
	case errors.As(err, &ae):
		if ae.Status == 0 {
			ae = httpx.NewError(ae.Code, ae.Message).WithDetails(ae.Details)
		}
		return ae
	case errors.Is(err, app.ErrNotFound):
		return httpx.NewError(httpx.CodeNotFound, "order not found")
	case errors.Is(err, app.ErrSkuNotFound):
		return httpx.NewError(httpx.CodeNotFound, "sku not found")
	case errors.Is(err, app.ErrSkuUnavailable):
		return httpx.NewError(httpx.CodeConflict, "sku is not available for sale")
	case errors.Is(err, app.ErrOwnShop):
		return httpx.NewError(httpx.CodeConflict, "cannot buy from your own shop")
	case errors.Is(err, app.ErrCartFull):
		return httpx.NewError(httpx.CodeConflict, fmt.Sprintf("cart cannot hold more than %d items", domain.MaxCartLines))
	case errors.Is(err, app.ErrInvalidQuantity):
		return httpx.ValidationError(map[string]string{"quantity": fmt.Sprintf("must be between %d and %d", domain.MinQuantity, domain.MaxQuantity)})
	case errors.Is(err, app.ErrEmptyCart):
		return httpx.ValidationError(map[string]string{"sku_ids": "no cart items to check out"})
	case errors.Is(err, app.ErrPaymentMethodNotEnabled):
		return httpx.ValidationError(map[string]string{"payment_method": "is not enabled"})
	case errors.As(err, &sel):
		return httpx.ValidationError(map[string]string{"sku_ids": "not in cart: " + strings.Join(sel.NotInCart, ", ")})
	case errors.As(err, &lp):
		items := make([]map[string]string, len(lp.Problems))
		for i, p := range lp.Problems {
			items[i] = map[string]string{"sku_id": p.SkuID, "reason": p.Reason}
		}
		return httpx.NewError(httpx.CodeConflict, "some items cannot be purchased").WithDetails(map[string]any{"items": items})
	case errors.As(err, &oos):
		shortages := make([]map[string]any, len(oos.Shortages))
		for i, s := range oos.Shortages {
			shortages[i] = map[string]any{"sku_id": s.SkuID, "requested": s.Requested, "available": s.Available}
		}
		return httpx.NewError(httpx.CodeOutOfStock, "insufficient stock").WithDetails(map[string]any{"shortages": shortages})
	case errors.Is(err, app.ErrCatalogRejected):
		return httpx.NewError(httpx.CodeConflict, "catalog rejected the request")
	case errors.Is(err, app.ErrCatalogUnavailable):
		return httpx.NewError(httpx.CodeUpstreamUnavailable, "catalog service is unavailable").Wrap(err)
	case errors.Is(err, app.ErrIdempotencyInProgress):
		return httpx.NewError(httpx.CodeConflict, "a request with this Idempotency-Key is still in progress")
	case errors.Is(err, app.ErrIdempotencyClaimLost):
		return httpx.NewError(httpx.CodeConflict, "a newer request took over this Idempotency-Key; retry to get its result")
	case errors.Is(err, app.ErrIdempotencyKeyReused):
		return httpx.NewError(httpx.CodeIdempotencyKeyReused, "Idempotency-Key was already used with a different request")
	case errors.Is(err, domain.ErrInvalidTransition):
		return httpx.NewError(httpx.CodeInvalidStateTransition, err.Error())
	case errors.Is(err, app.ErrConcurrentUpdate):
		return httpx.NewError(httpx.CodeConflict, "order was modified concurrently; retry")
	case errors.Is(err, app.ErrCheckoutExpired):
		return httpx.NewError(httpx.CodeConflict, "checkout took too long and was cancelled; retry")
	case errors.Is(err, app.ErrInvalidCursor):
		return httpx.ValidationError(map[string]string{"cursor": "is invalid"})
	default:
		return httpx.NewError(httpx.CodeInternal, "internal server error").Wrap(err)
	}
}
