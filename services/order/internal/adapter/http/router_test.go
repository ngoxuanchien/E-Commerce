package httpadapter_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/http"
)

const validCheckout = `{"shipping_address":{"full_name":"A","phone":"1","line1":"l","ward":"w","district":"d","province":"p"},"payment_method":"COD"}`

func TestRequestsRejectedBeforeReachingServices(t *testing.T) {
	// nil services: every case must be answered by routing/validation alone.
	h := authctx.Middleware(httpadapter.NewRouter(httpadapter.Services{}))
	user := uuid.Must(uuid.NewV7()).String()
	id := uuid.Must(uuid.NewV7()).String()
	buyer := map[string]string{authctx.HeaderUserID: user, authctx.HeaderUserRoles: "buyer"}
	seller := map[string]string{authctx.HeaderUserID: user, authctx.HeaderUserRoles: "buyer,seller"}
	withKey := func(h map[string]string, key string) map[string]string {
		out := map[string]string{"Idempotency-Key": key}
		for k, v := range h {
			out[k] = v
		}
		return out
	}
	long := strings.Repeat("a", 201)

	cases := []struct {
		name, method, path, body string
		headers                  map[string]string
		status                   int
		code, field              string
	}{
		{"anonymous", "GET", "/api/cart", "", nil, 401, "UNAUTHENTICATED", ""},
		{"caller id not a uuid", "GET", "/api/cart", "", map[string]string{authctx.HeaderUserID: "admin"}, 401, "UNAUTHENTICATED", ""},
		{"caller id in urn form", "GET", "/api/cart", "", map[string]string{authctx.HeaderUserID: "urn:uuid:" + user}, 401, "UNAUTHENTICATED", ""},
		{"seller route without role", "GET", "/api/seller/orders", "", buyer, 403, "FORBIDDEN", ""},
		{"sku id not a uuid", "PUT", "/api/cart/items/abc", `{"quantity":1}`, buyer, 400, "VALIDATION_ERROR", "sku_id"},
		{"quantity zero", "PUT", "/api/cart/items/" + id, `{"quantity":0}`, buyer, 400, "VALIDATION_ERROR", "quantity"},
		{"quantity 100", "PUT", "/api/cart/items/" + id, `{"quantity":100}`, buyer, 400, "VALIDATION_ERROR", "quantity"},
		{"quantity missing", "PUT", "/api/cart/items/" + id, `{}`, buyer, 400, "VALIDATION_ERROR", "quantity"},
		{"unknown field", "PUT", "/api/cart/items/" + id, `{"quantity":1,"x":1}`, buyer, 400, "VALIDATION_ERROR", "body"},
		{"checkout without key", "POST", "/api/checkout", validCheckout, buyer, 400, "VALIDATION_ERROR", "Idempotency-Key"},
		{"checkout key too long", "POST", "/api/checkout", validCheckout, withKey(buyer, strings.Repeat("k", 101)), 400, "VALIDATION_ERROR", "Idempotency-Key"},
		{"checkout empty sku_ids", "POST", "/api/checkout", `{"sku_ids":[],` + validCheckout[1:], withKey(buyer, "k"), 400, "VALIDATION_ERROR", "sku_ids"},
		{"checkout bad sku id", "POST", "/api/checkout", `{"sku_ids":["x"],` + validCheckout[1:], withKey(buyer, "k"), 400, "VALIDATION_ERROR", "sku_ids[0]"},
		{"checkout bad payment", "POST", "/api/checkout", strings.Replace(validCheckout, "COD", "CARD", 1), withKey(buyer, "k"), 400, "VALIDATION_ERROR", "payment_method"},
		{"checkout missing ward", "POST", "/api/checkout", strings.Replace(validCheckout, `"ward":"w",`, "", 1), withKey(buyer, "k"), 400, "VALIDATION_ERROR", "shipping_address.ward"},
		{"list limit zero", "GET", "/api/orders?limit=0", "", buyer, 400, "VALIDATION_ERROR", "limit"},
		{"list limit text", "GET", "/api/orders?limit=abc", "", buyer, 400, "VALIDATION_ERROR", "limit"},
		{"list limit 101", "GET", "/api/orders?limit=101", "", buyer, 400, "VALIDATION_ERROR", "limit"},
		{"list bad status", "GET", "/api/seller/orders?status=LOST", "", seller, 400, "VALIDATION_ERROR", "status"},
		{"get order bad id", "GET", "/api/orders/nope", "", buyer, 404, "NOT_FOUND", ""},
		{"complete bad id", "POST", "/api/orders/nope/complete", "", buyer, 404, "NOT_FOUND", ""},
		{"cancel bad id", "POST", "/api/orders/nope/cancel", `{"reason":"x"}`, buyer, 404, "NOT_FOUND", ""},
		{"cancel blank reason", "POST", "/api/orders/" + id + "/cancel", `{"reason":"   "}`, buyer, 400, "VALIDATION_ERROR", "reason"},
		{"cancel reason too long", "POST", "/api/orders/" + id + "/cancel", `{"reason":"` + long + `"}`, buyer, 400, "VALIDATION_ERROR", "reason"},
		{"cancel without body", "POST", "/api/orders/" + id + "/cancel", "", buyer, 400, "VALIDATION_ERROR", "body"},
		{"seller unknown action", "POST", "/api/seller/orders/" + id + "/refund", "", seller, 404, "NOT_FOUND", ""},
		{"seller cancel blank reason", "POST", "/api/seller/orders/" + id + "/cancel", `{"reason":""}`, seller, 400, "VALIDATION_ERROR", "reason"},
		{"seller note too long", "POST", "/api/seller/orders/" + id + "/ship", `{"reason":"` + long + `"}`, seller, 400, "VALIDATION_ERROR", "reason"},
		{"unknown route", "GET", "/api/nope", "", buyer, 404, "NOT_FOUND", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			var body struct {
				Error struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tc.code, body.Error.Code)
			if tc.field != "" {
				assert.Contains(t, body.Error.Details["fields"], tc.field)
			}
		})
	}
}
