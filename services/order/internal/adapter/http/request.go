package httpadapter

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

const (
	headerIdempotencyKey = "Idempotency-Key"
	maxIdempotencyKey    = 100
	defaultPageLimit     = 20
	maxPageLimit         = 100
	maxCursorLen         = 500
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

// requireUUIDCaller rejects callers whose X-User-Id is not a UUID and stores
// its canonical form so ownership checks compare equal strings.
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

type reasonRequest struct {
	Reason string `json:"reason" validate:"required,max=200"`
}

type noteRequest struct {
	Reason string `json:"reason" validate:"max=200"`
}

// decodeReason reads a required cancel reason (1–200 characters, not blank).
func decodeReason(r *http.Request) (string, error) {
	var req reasonRequest
	if err := httpx.Decode(r, &req); err != nil {
		return "", err
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return "", httpx.ValidationError(map[string]string{"reason": "is required"})
	}
	return reason, nil
}

// decodeOptional decodes the body into dst unless the body is empty.
func decodeOptional(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, httpx.MaxBodyBytes+1))
	if err != nil {
		return httpx.ValidationError(map[string]string{"body": "could not be read"})
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return httpx.Decode(r, dst)
}

// parseListInput reads status, limit and cursor query parameters.
func parseListInput(r *http.Request) (app.ListInput, error) {
	q := r.URL.Query()
	in := app.ListInput{Limit: defaultPageLimit, Cursor: q.Get("cursor")}
	fields := map[string]string{}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageLimit {
			fields["limit"] = "must be an integer between 1 and 100"
		} else {
			in.Limit = n
		}
	}
	if v := q.Get("status"); v != "" {
		st, ok := domain.ParseStatus(v)
		if !ok {
			fields["status"] = "must be a valid order status"
		} else {
			in.Status = &st
		}
	}
	if len(in.Cursor) > maxCursorLen {
		fields["cursor"] = "is invalid"
	}
	if len(fields) > 0 {
		return in, httpx.ValidationError(fields)
	}
	return in, nil
}
