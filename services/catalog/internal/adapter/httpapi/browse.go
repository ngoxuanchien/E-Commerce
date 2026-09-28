package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

func (h *handlers) listCategories(w http.ResponseWriter, r *http.Request) {
	cs, err := h.Search.Categories(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCategoriesJSON(cs))
}

func (h *handlers) searchProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := map[string]string{}
	p := domain.SearchParams{
		Query:        q.Get("q"),
		CategorySlug: q.Get("category"),
		ShopSlug:     q.Get("shop"),
		MinPrice:     parseMoney(q, "min_price", f),
		MaxPrice:     parseMoney(q, "max_price", f),
		Sort:         domain.SearchSort(q.Get("sort")),
		Limit:        parseLimit(q, f),
		Cursor:       q.Get("cursor"),
	}
	if len(f) > 0 {
		writeError(w, r, &domain.ValidationError{Fields: f})
		return
	}
	page, err := h.Search.Search(r.Context(), p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPage(page, h.toSummaryJSON))
}

func (h *handlers) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, r, domain.ErrNotFound)
		return
	}
	p, err := h.Products.Get(r.Context(), id, viewerID(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.toProductJSON(p))
}

func (h *handlers) getShop(w http.ResponseWriter, r *http.Request) {
	shop, err := h.Shops.BySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toShopJSON(shop))
}
