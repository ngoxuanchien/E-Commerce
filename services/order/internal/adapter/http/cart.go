package httpadapter

import (
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

type cartItemRequest struct {
	Quantity *int32 `json:"quantity" validate:"required,min=1,max=99"`
}

func (h *handler) getCart(w http.ResponseWriter, r *http.Request) {
	c, err := h.s.Cart.Get(r.Context(), callerID(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCartDTO(c))
}

func (h *handler) setCartItem(w http.ResponseWriter, r *http.Request) {
	skuID, ok := parseID(r.PathValue("sku_id"))
	if !ok {
		writeError(w, r, httpx.ValidationError(map[string]string{"sku_id": "must be a valid UUID"}))
		return
	}
	var req cartItemRequest
	if err := httpx.Decode(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	c, err := h.s.Cart.SetItem(r.Context(), callerID(r), skuID, *req.Quantity)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCartDTO(c))
}

func (h *handler) removeCartItem(w http.ResponseWriter, r *http.Request) {
	var (
		c   domain.Cart
		err error
	)
	if skuID, ok := parseID(r.PathValue("sku_id")); ok {
		c, err = h.s.Cart.RemoveItem(r.Context(), callerID(r), skuID)
	} else {
		c, err = h.s.Cart.Get(r.Context(), callerID(r)) // a non-UUID can never be in the cart
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCartDTO(c))
}
