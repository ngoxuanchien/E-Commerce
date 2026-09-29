// Package httpadapter serves ledger-svc's REST API (api/openapi/ledger.yaml).
package httpadapter

import (
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

// Services are the use cases exposed over HTTP.
type Services struct {
	Wallet  *app.WalletService
	Payouts *app.PayoutService
}

type handler struct{ s Services }

// NewRouter returns the /api routes. authctx.Middleware must run before it.
// Every route requires the seller role; the seller is X-User-Id.
func NewRouter(s Services) http.Handler {
	h := &handler{s: s}
	seller := func(f http.HandlerFunc) http.Handler {
		return authctx.RequireRole(authctx.RoleSeller)(requireUUIDCaller(f))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/seller/wallet", seller(h.getWallet))
	mux.Handle("GET /api/seller/wallet/entries", seller(h.listWalletEntries))
	mux.Handle("POST /api/seller/payouts", seller(h.createPayout))
	mux.Handle("GET /api/seller/payouts", seller(h.listPayouts))
	mux.Handle("/", httpx.NotFound())
	return mux
}
