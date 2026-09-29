package httpadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func TestToAppError(t *testing.T) {
	fields := func(f map[string]string) map[string]any { return map[string]any{"fields": f} }
	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		details map[string]any
	}{
		{"order not found", app.ErrNotFound, 404, httpx.CodeNotFound, nil},
		{"sku not found", fmt.Errorf("wrapped: %w", app.ErrSkuNotFound), 404, httpx.CodeNotFound, nil},
		{"sku unavailable", app.ErrSkuUnavailable, 409, httpx.CodeConflict, nil},
		{"own shop", app.ErrOwnShop, 409, httpx.CodeConflict, nil},
		{"cart full", app.ErrCartFull, 409, httpx.CodeConflict, nil},
		{"quantity", app.ErrInvalidQuantity, 400, httpx.CodeValidation, fields(map[string]string{"quantity": "must be between 1 and 99"})},
		{"empty cart", app.ErrEmptyCart, 400, httpx.CodeValidation, fields(map[string]string{"sku_ids": "no cart items to check out"})},
		{"selection", &app.SelectionError{NotInCart: []string{"s1", "s2"}}, 400, httpx.CodeValidation, fields(map[string]string{"sku_ids": "not in cart: s1, s2"})},
		{"line problems", &domain.LineProblemsError{Problems: []domain.LineProblem{{SkuID: "s1", Reason: domain.ProblemOwnShop}}}, 409, httpx.CodeConflict,
			map[string]any{"items": []map[string]string{{"sku_id": "s1", "reason": "OWN_SHOP"}}}},
		{"out of stock", &app.OutOfStockError{Shortages: []domain.Shortage{{SkuID: "s1", Requested: 3, Available: 1}}}, 409, httpx.CodeOutOfStock,
			map[string]any{"shortages": []map[string]any{{"sku_id": "s1", "requested": int32(3), "available": int32(1)}}}},
		{"catalog rejected", app.ErrCatalogRejected, 409, httpx.CodeConflict, nil},
		{"catalog down", fmt.Errorf("%w: boom", app.ErrCatalogUnavailable), 503, httpx.CodeUpstreamUnavailable, nil},
		{"key in progress", app.ErrIdempotencyInProgress, 409, httpx.CodeConflict, nil},
		{"key reused", app.ErrIdempotencyKeyReused, 422, httpx.CodeIdempotencyKeyReused, nil},
		{"key reclaimed", app.ErrIdempotencyClaimLost, 409, httpx.CodeConflict, nil},
		{"invalid transition", fmt.Errorf("%w: x", domain.ErrInvalidTransition), 409, httpx.CodeInvalidStateTransition, nil},
		{"concurrent update", app.ErrConcurrentUpdate, 409, httpx.CodeConflict, nil},
		{"checkout expired", app.ErrCheckoutExpired, 409, httpx.CodeConflict, nil},
		{"cursor", app.ErrInvalidCursor, 400, httpx.CodeValidation, fields(map[string]string{"cursor": "is invalid"})},
		{"app error passes through", httpx.NewError(httpx.CodeForbidden, "no"), 403, httpx.CodeForbidden, nil},
		{"anything else", errors.New("db down"), 500, httpx.CodeInternal, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := toAppError(tc.err)
			assert.Equal(t, tc.status, ae.Status)
			assert.Equal(t, tc.code, ae.Code)
			if tc.details != nil {
				assert.Equal(t, tc.details, ae.Details)
			}
		})
	}
}

func TestEncodeCheckoutError(t *testing.T) {
	resp := encodeCheckout(app.CheckoutResult{}, &app.OutOfStockError{Shortages: []domain.Shortage{{SkuID: "s1", Requested: 2, Available: 0}}})
	assert.Equal(t, http.StatusConflict, resp.Status)
	var body map[string]map[string]any
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	assert.Equal(t, "OUT_OF_STOCK", body["error"]["code"])
	assert.NotEmpty(t, body["error"]["message"])
	assert.Equal(t, []any{map[string]any{"sku_id": "s1", "requested": 2.0, "available": 0.0}}, body["error"]["details"].(map[string]any)["shortages"])
}
