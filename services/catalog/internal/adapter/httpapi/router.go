// Package httpapi serves catalog's REST API (api/openapi/catalog.yaml).
package httpapi

import (
	"net/http"

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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/categories", h.listCategories)
	mux.HandleFunc("GET /api/products", h.searchProducts)
	mux.HandleFunc("GET /api/products/{id}", h.getProduct)
	mux.HandleFunc("GET /api/shops/{slug}", h.getShop)
	mux.Handle("/", httpx.NotFound())
	return mux
}
