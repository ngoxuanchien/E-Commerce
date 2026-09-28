package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// callerID returns the authenticated user id; a non-UUID id is unauthenticated.
func callerID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(authctx.UserID(r.Context()))
	if err != nil {
		return uuid.Nil, httpx.NewError(httpx.CodeUnauthenticated, "authentication required")
	}
	return id, nil
}

// viewerID returns the caller's id, or uuid.Nil for anonymous callers.
func viewerID(r *http.Request) uuid.UUID {
	id, _ := uuid.Parse(authctx.UserID(r.Context()))
	return id
}

// parseLimit reads ?limit= (absent → 0, meaning the default); errors go to f.
func parseLimit(q url.Values, f map[string]string) int {
	raw := q.Get("limit")
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > domain.MaxPageLimit {
		f["limit"] = "must be an integer between 1 and 100"
		return 0
	}
	return n
}

// parseMoney reads an optional non-negative integer amount; errors go to f.
func parseMoney(q url.Values, name string, f map[string]string) *int64 {
	raw := q.Get(name)
	if raw == "" {
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		f[name] = "must be a non-negative integer"
		return nil
	}
	return &n
}
