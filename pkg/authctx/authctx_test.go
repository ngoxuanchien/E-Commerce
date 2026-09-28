package authctx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
)

func TestParseRoles(t *testing.T) {
	assert.Equal(t, []string{"buyer", "seller"}, authctx.ParseRoles(" buyer, seller,,"))
	assert.Equal(t, []string{"buyer"}, authctx.ParseRoles("buyer,buyer"))
	assert.Empty(t, authctx.ParseRoles(""))
	assert.Empty(t, authctx.ParseRoles(" , "))
}

func serve(h http.Handler, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestMiddlewarePopulatesIdentity(t *testing.T) {
	var got authctx.Identity
	var ok bool
	h := authctx.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = authctx.FromContext(r.Context())
	}))
	serve(h, map[string]string{authctx.HeaderUserID: "u1", authctx.HeaderUserRoles: "buyer, seller"})

	assert.True(t, ok)
	assert.Equal(t, authctx.Identity{UserID: "u1", Roles: []string{"buyer", "seller"}}, got)
}

func TestMiddlewareIgnoresRolesWithoutUser(t *testing.T) {
	var ok bool
	h := authctx.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok = authctx.FromContext(r.Context())
	}))
	serve(h, map[string]string{authctx.HeaderUserID: "  ", authctx.HeaderUserRoles: "seller"})
	assert.False(t, ok)
}

func TestHelpersWithoutIdentity(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", authctx.UserID(ctx))
	assert.False(t, authctx.HasRole(ctx, authctx.RoleSeller))
}

func TestHasRole(t *testing.T) {
	ctx := authctx.WithIdentity(context.Background(), authctx.Identity{UserID: "u1", Roles: []string{"buyer"}})
	assert.Equal(t, "u1", authctx.UserID(ctx))
	assert.True(t, authctx.HasRole(ctx, authctx.RoleBuyer))
	assert.False(t, authctx.HasRole(ctx, authctx.RoleSeller))
}

var ok200 = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

func TestRequireUser(t *testing.T) {
	h := authctx.Middleware(authctx.RequireUser(ok200))

	rec := serve(h, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"UNAUTHENTICATED","message":"authentication required"}}`, rec.Body.String())

	assert.Equal(t, http.StatusOK, serve(h, map[string]string{authctx.HeaderUserID: "u1"}).Code)
}

func TestRequireRole(t *testing.T) {
	h := authctx.Middleware(authctx.RequireRole(authctx.RoleSeller)(ok200))

	assert.Equal(t, http.StatusUnauthorized, serve(h, nil).Code)

	rec := serve(h, map[string]string{authctx.HeaderUserID: "u1", authctx.HeaderUserRoles: "buyer"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"FORBIDDEN","message":"requires role seller"}}`, rec.Body.String())

	assert.Equal(t, http.StatusOK,
		serve(h, map[string]string{authctx.HeaderUserID: "u1", authctx.HeaderUserRoles: "buyer,seller"}).Code)
}
