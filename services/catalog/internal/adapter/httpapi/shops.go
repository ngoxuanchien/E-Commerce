package httpapi

import (
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

type shopCreateJSON struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

type shopUpdateJSON struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (h *handlers) createShop(w http.ResponseWriter, r *http.Request) {
	uid, err := callerID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in shopCreateJSON
	if err := httpx.Decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	shop, err := h.Shops.Open(r.Context(), uid, domain.ShopInput{Name: in.Name, Slug: in.Slug, Description: in.Description})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toShopJSON(shop))
}

func (h *handlers) getMyShop(w http.ResponseWriter, r *http.Request) {
	uid, err := callerID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	shop, err := h.Shops.Mine(r.Context(), uid)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toShopJSON(shop))
}

func (h *handlers) updateMyShop(w http.ResponseWriter, r *http.Request) {
	uid, err := callerID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in shopUpdateJSON
	if err := httpx.Decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	shop, err := h.Shops.Update(r.Context(), uid, domain.ShopPatch{Name: in.Name, Description: in.Description})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toShopJSON(shop))
}
