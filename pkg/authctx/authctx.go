// Package authctx carries the caller identity injected by the gateway
// (X-User-Id / X-User-Roles) through request contexts. Services trust these
// headers because only the gateway is reachable from outside.
package authctx

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

const (
	HeaderUserID    = "X-User-Id"
	HeaderUserRoles = "X-User-Roles"

	RoleBuyer  = "buyer"
	RoleSeller = "seller"
	RoleAdmin  = "admin"
)

// Identity is the authenticated caller.
type Identity struct {
	UserID string
	Roles  []string
}

type identityKey struct{}

// WithIdentity stores id in ctx.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// FromContext returns the caller identity, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// UserID returns the caller's user ID, or "" for anonymous requests.
func UserID(ctx context.Context) string {
	id, _ := FromContext(ctx)
	return id.UserID
}

// HasRole reports whether the caller has role.
func HasRole(ctx context.Context, role string) bool {
	id, _ := FromContext(ctx)
	return slices.Contains(id.Roles, role)
}

// ParseRoles splits a comma-separated role list, trimming blanks and duplicates.
func ParseRoles(s string) []string {
	var roles []string
	for _, r := range strings.Split(s, ",") {
		r = strings.TrimSpace(r)
		if r != "" && !slices.Contains(roles, r) {
			roles = append(roles, r)
		}
	}
	return roles
}

// Middleware reads the identity headers into the request context.
// A blank X-User-Id means an anonymous request, regardless of X-User-Roles.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uid := strings.TrimSpace(r.Header.Get(HeaderUserID)); uid != "" {
			id := Identity{UserID: uid, Roles: ParseRoles(r.Header.Get(HeaderUserRoles))}
			r = r.WithContext(WithIdentity(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}

// RequireUser rejects anonymous requests with 401 UNAUTHENTICATED.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserID(r.Context()) == "" {
			httpx.WriteError(w, r, httpx.NewError(httpx.CodeUnauthenticated, "authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole rejects anonymous requests with 401 and callers lacking role with 403.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !HasRole(r.Context(), role) {
				httpx.WriteError(w, r, httpx.NewError(httpx.CodeForbidden, "requires role "+role))
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}
