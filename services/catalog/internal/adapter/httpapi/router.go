// Package httpapi serves catalog's REST API (api/openapi/catalog.yaml).
package httpapi

import (
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// Deps are the use cases behind the REST API.
type Deps struct {
	Shops    *app.ShopService
	Products *app.ProductService
	Search   *app.SearchService
	Assets   domain.AssetBase
}

type handlers struct {
	Deps
}

// NewRouter returns the /api handler. The caller applies authctx.Middleware
// so X-User-Id / X-User-Roles are available in the request context.
func NewRouter(d Deps) http.Handler {
	h := &handlers{Deps: d}
	user := authctx.RequireUser
	seller := authctx.RequireRole(authctx.RoleSeller)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/categories", h.listCategories)
	mux.HandleFunc("GET /api/products", h.searchProducts)
	mux.HandleFunc("GET /api/products/{id}", h.getProduct)
	mux.HandleFunc("GET /api/shops/{slug}", h.getShop)

	mux.Handle("POST /api/shops", user(http.HandlerFunc(h.createShop)))
	mux.Handle("GET /api/shops/me", seller(http.HandlerFunc(h.getMyShop))) // more specific than {slug}
	mux.Handle("PATCH /api/shops/me", seller(http.HandlerFunc(h.updateMyShop)))

	mux.Handle("GET /api/seller/products", seller(http.HandlerFunc(h.listSellerProducts)))
	mux.Handle("POST /api/seller/products", seller(http.HandlerFunc(h.createSellerProduct)))
	mux.Handle("PATCH /api/seller/products/{id}", seller(http.HandlerFunc(h.updateSellerProduct)))
	mux.Handle("DELETE /api/seller/products/{id}", seller(http.HandlerFunc(h.archiveSellerProduct)))
	mux.Handle("POST /api/seller/uploads", seller(http.HandlerFunc(h.createUpload)))

	mux.Handle("/", httpx.NotFound())
	return mux
}
