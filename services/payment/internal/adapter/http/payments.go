package httpadapter

import (
	"net"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

const (
	headerIdempotencyKey = "Idempotency-Key"
	maxIdempotencyKey    = 100
)

// parseID returns the canonical (lower-case) form of a hyphenated UUID.
func parseID(s string) (string, bool) {
	if len(s) != 36 {
		return "", false
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return "", false
	}
	return u.String(), true
}

// requireUUIDCaller rejects callers whose X-User-Id is not a UUID and stores its canonical
// form so ownership checks compare equal strings.
func requireUUIDCaller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := authctx.FromContext(r.Context())
		canonical, ok := parseID(id.UserID)
		if !ok {
			httpx.WriteError(w, r, httpx.NewError(httpx.CodeUnauthenticated, "caller identity is not a valid user id"))
			return
		}
		id.UserID = canonical
		next.ServeHTTP(w, r.WithContext(authctx.WithIdentity(r.Context(), id)))
	})
}

func callerID(r *http.Request) string { return authctx.UserID(r.Context()) }

// clientIP is the buyer's address as the gateway forwards it (first X-Forwarded-For hop), else the peer.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type paymentRoutes struct{ creator *app.Creator }

func (h paymentRoutes) create(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get(headerIdempotencyKey)
	if n := utf8.RuneCountInString(key); n < 1 || n > maxIdempotencyKey {
		writeError(w, r, httpx.ValidationError(map[string]string{headerIdempotencyKey: "header is required (1-100 characters)"}))
		return
	}
	var req createPaymentRequest
	if err := httpx.Decode(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	checkoutID, _ := parseID(req.CheckoutID) // validated by the uuid tag
	resp, err := h.creator.Create(r.Context(), app.CreateInput{
		BuyerID: callerID(r), IdempotencyKey: key, CheckoutID: checkoutID, ClientIP: clientIP(r),
	}, encodePayment)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

func (h paymentRoutes) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		writeError(w, r, app.ErrNotFound)
		return
	}
	p, err := h.creator.Get(r.Context(), callerID(r), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPaymentDTO(p))
}
