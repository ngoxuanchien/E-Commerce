package httpadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// VNPayHandler is the VNPay callback logic (vnpay.Callbacks).
type VNPayHandler interface {
	IPN(ctx context.Context, q url.Values) (rspCode, message string)
	Return(ctx context.Context, q url.Values) (paymentID string)
}

// VNPayCallbacks serves VNPay's IPN and browser return (spec §5.1).
type VNPayCallbacks struct{ h VNPayHandler }

var _ Routes = (*VNPayCallbacks)(nil)

func NewVNPayCallbacks(h VNPayHandler) *VNPayCallbacks { return &VNPayCallbacks{h: h} }

func (c *VNPayCallbacks) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/payments/vnpay/ipn", c.ipn)
	mux.HandleFunc("GET /api/payments/vnpay/return", c.returnURL)
}

type vnpayAck struct {
	RspCode string `json:"RspCode"`
	Message string `json:"Message"`
}

// ipn always answers 200: VNPay reads the outcome from RspCode.
func (c *VNPayCallbacks) ipn(w http.ResponseWriter, r *http.Request) {
	code, msg := c.h.IPN(r.Context(), r.URL.Query())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(vnpayAck{RspCode: code, Message: msg})
}

// returnURL always redirects to the result page, with the payment id only when the return was
// signed and names a known payment.
func (c *VNPayCallbacks) returnURL(w http.ResponseWriter, r *http.Request) {
	id := c.h.Return(r.Context(), r.URL.Query())
	if id == "" {
		http.Redirect(w, r, "/checkout/result", http.StatusFound)
		return
	}
	http.Redirect(w, r, resultURL(id), http.StatusFound)
}
