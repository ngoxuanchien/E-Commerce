package httpadapter

import (
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

type addressRequest struct {
	FullName string `json:"full_name" validate:"required"`
	Phone    string `json:"phone" validate:"required"`
	Line1    string `json:"line1" validate:"required"`
	Ward     string `json:"ward" validate:"required"`
	District string `json:"district" validate:"required"`
	Province string `json:"province" validate:"required"`
}

type checkoutRequest struct {
	SkuIDs          *[]string       `json:"sku_ids" validate:"omitnil,min=1,dive,uuid"`
	ShippingAddress *addressRequest `json:"shipping_address" validate:"required"`
	PaymentMethod   string          `json:"payment_method" validate:"required,oneof=COD VNPAY STRIPE"`
}

func (h *handler) checkout(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get(headerIdempotencyKey)
	if n := utf8.RuneCountInString(key); n < 1 || n > maxIdempotencyKey {
		writeError(w, r, httpx.ValidationError(map[string]string{headerIdempotencyKey: "header is required (1-100 characters)"}))
		return
	}
	var req checkoutRequest
	if err := httpx.Decode(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	a := req.ShippingAddress
	in := app.CheckoutInput{
		BuyerID: callerID(r), IdempotencyKey: key, PaymentMethod: req.PaymentMethod,
		Address: domain.Address{FullName: a.FullName, Phone: a.Phone, Line1: a.Line1, Ward: a.Ward, District: a.District, Province: a.Province},
	}
	if req.SkuIDs != nil {
		in.SkuIDs = make([]string, 0, len(*req.SkuIDs))
		for _, id := range *req.SkuIDs {
			canonical, _ := parseID(id) // validated by the uuid tag
			in.SkuIDs = append(in.SkuIDs, canonical)
		}
	}
	resp, err := h.s.Checkout.Checkout(r.Context(), in, encodeCheckout)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// encodeCheckout is the app.Encoder for checkout: 201 with the orders, or the
// standard error body for a business rejection.
func encodeCheckout(res app.CheckoutResult, err error) app.Response {
	if err != nil {
		ae := toAppError(err)
		body, _ := json.Marshal(errorEnvelope{Error: errorPayload{Code: ae.Code, Message: ae.Message, Details: ae.Details}})
		return app.Response{Status: ae.Status, Body: body}
	}
	body, _ := json.Marshal(checkoutResponseDTO{CheckoutID: res.CheckoutID, Orders: toOrderDTOs(res.Orders)})
	return app.Response{Status: http.StatusCreated, Body: body}
}
