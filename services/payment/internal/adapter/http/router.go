// Package httpadapter serves payment-svc's REST API (api/openapi/payment.yaml): the buyer-facing
// payment routes and the provider callback routes.
package httpadapter

import (
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

// Routes registers a group of public callback routes (one per provider) on the router's mux.
// Callback routes are authenticated by signature, not by X-User-Id.
type Routes interface {
	Register(mux *http.ServeMux)
}

// NewRouter returns the /api routes: the buyer's payment routes plus every callback group given
// (FakeCallbacks today; VNPay and Stripe register theirs the same way). Nil groups are skipped.
// authctx.Middleware must run before it.
func NewRouter(creator *app.Creator, callbacks ...Routes) http.Handler {
	h := paymentRoutes{creator: creator}
	user := func(f http.HandlerFunc) http.Handler { return authctx.RequireUser(requireUUIDCaller(f)) }

	mux := http.NewServeMux()
	mux.Handle("POST /api/payments", user(h.create))
	mux.Handle("GET /api/payments/{id}", user(h.get))
	for _, c := range callbacks {
		if c != nil {
			c.Register(mux)
		}
	}
	mux.Handle("/", httpx.NotFound())
	return mux
}
