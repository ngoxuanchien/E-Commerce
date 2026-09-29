package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNulAndInvalidUTF8InputIsNeverA500(t *testing.T) {
	h := newHarness(t)
	u := newUser()
	for _, path := range []string{
		"/api/products?q=%00", "/api/products?q=%FF", "/api/products?shop=%00", "/api/products?category=%FF",
	} {
		res := h.raw(http.MethodGet, path, nil, "")
		assert.Equal(t, http.StatusBadRequest, res.Status, path)
		assert.Equal(t, "VALIDATION_ERROR", res.code(t), path)
	}
	for _, path := range []string{"/api/shops/%00", "/api/shops/%FF"} {
		assert.Equal(t, http.StatusNotFound, h.raw(http.MethodGet, path, nil, "").Status, path)
	}
	res := h.raw(http.MethodPost, "/api/shops", &u, `{"name":"Shop\u0000x","slug":"nul-shop"}`)
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Contains(t, res.fields(t), "name")
}
