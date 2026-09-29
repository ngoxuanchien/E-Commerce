package httpadapter

import (
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

type payoutRequest struct {
	Amount *int64 `json:"amount" validate:"required,gte=0"`
}

// encodePayout renders PayoutService results; it is what the idempotency store keeps.
func encodePayout(p domain.Payout, err error) app.Response {
	if err != nil {
		return renderError(err)
	}
	body, _ := json.Marshal(toPayoutDTO(p))
	return app.Response{Status: http.StatusCreated, Body: body}
}

func (h *handler) createPayout(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get(headerIdempotencyKey)
	if n := utf8.RuneCountInString(key); n < 1 || n > maxIdempotencyKey {
		writeError(w, r, httpx.ValidationError(map[string]string{headerIdempotencyKey: "header is required (1-100 characters)"}))
		return
	}
	var req payoutRequest
	if err := httpx.Decode(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := h.s.Payouts.Request(r.Context(),
		app.PayoutRequestInput{SellerID: callerID(r), IdempotencyKey: key, Amount: *req.Amount}, encodePayout)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

func (h *handler) listPayouts(w http.ResponseWriter, r *http.Request) {
	in, status, err := parseList(r, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := h.s.Payouts.List(r.Context(), callerID(r), status, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPage(page, toPayoutDTO))
}
