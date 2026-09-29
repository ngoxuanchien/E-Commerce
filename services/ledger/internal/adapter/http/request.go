package httpadapter

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

const (
	headerIdempotencyKey = "Idempotency-Key"
	maxIdempotencyKey    = 100
	defaultPageLimit     = 20
	maxPageLimit         = 100
	maxCursorLen         = 500
)

type callerKey struct{}

// requireUUIDCaller rejects callers whose X-User-Id is not a hyphenated UUID
// and keeps the parsed id in the context.
func requireUUIDCaller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := authctx.UserID(r.Context())
		id, err := uuid.Parse(raw)
		if err != nil || len(raw) != 36 {
			httpx.WriteError(w, r, httpx.NewError(httpx.CodeUnauthenticated, "caller identity is not a valid user id"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, id)))
	})
}

func callerID(r *http.Request) uuid.UUID { return r.Context().Value(callerKey{}).(uuid.UUID) }

// parseList reads limit and cursor, and status when withStatus.
func parseList(r *http.Request, withStatus bool) (app.ListInput, *domain.PayoutStatus, error) {
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
	if len(in.Cursor) > maxCursorLen {
		fields["cursor"] = "is invalid"
	}
	var status *domain.PayoutStatus
	if v := q.Get("status"); withStatus && v != "" {
		st, ok := domain.ParsePayoutStatus(v)
		if !ok {
			fields["status"] = "must be one of: REQUESTED, SETTLED, FAILED"
		} else {
			status = &st
		}
	}
	if len(fields) > 0 {
		return in, nil, httpx.ValidationError(fields)
	}
	return in, status, nil
}
