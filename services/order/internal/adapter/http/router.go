// Package httpadapter serves order-svc's REST API (api/openapi/order.yaml).
package httpadapter

import (
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
)

// Services are the use cases exposed over HTTP.
type Services struct {
	Cart     *app.CartService
	Checkout *app.CheckoutService
	Orders   *app.OrderService
}

type handler struct{ s Services }

// NewRouter returns the /api routes. authctx.Middleware must run before it.
func NewRouter(s Services) http.Handler {
	h := &handler{s: s}
	user := func(f http.HandlerFunc) http.Handler { return authctx.RequireUser(requireUUIDCaller(f)) }
	seller := func(f http.HandlerFunc) http.Handler {
		return authctx.RequireRole(authctx.RoleSeller)(requireUUIDCaller(f))
	}

	mux := http.NewServeMux()
	mux.Handle("GET /api/cart", user(h.getCart))
	mux.Handle("PUT /api/cart/items/{sku_id}", user(h.setCartItem))
	mux.Handle("DELETE /api/cart/items/{sku_id}", user(h.removeCartItem))
	mux.Handle("POST /api/checkout", user(h.checkout))
	mux.Handle("GET /api/orders", user(h.listMyOrders))
	mux.Handle("GET /api/orders/{id}", user(h.getOrder))
	mux.Handle("POST /api/orders/{id}/cancel", user(h.cancelMyOrder))
	mux.Handle("POST /api/orders/{id}/complete", user(h.completeMyOrder))
	mux.Handle("GET /api/seller/orders", seller(h.listSellerOrders))
	mux.Handle("POST /api/seller/orders/{id}/{action}", seller(h.sellerAction))
	mux.Handle("/", httpx.NotFound())
	return mux
}
