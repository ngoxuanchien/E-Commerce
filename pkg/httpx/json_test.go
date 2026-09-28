package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

type skuIn struct {
	Name  string `json:"name" validate:"required"`
	Price int64  `json:"price" validate:"min=1000"`
}

type productIn struct {
	FullName string  `json:"full_name" validate:"required,max=10"`
	Skus     []skuIn `json:"skus" validate:"required,min=1,dive"`
}

func req(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var ae *httpx.AppError
	require.True(t, errors.As(err, &ae), "want *AppError, got %v", err)
	require.Equal(t, httpx.CodeValidation, ae.Code)
	fields, _ := ae.Details["fields"].(map[string]string)
	return fields
}

func TestDecodeValid(t *testing.T) {
	var in productIn
	require.NoError(t, httpx.Decode(req(`{"full_name":"An","skus":[{"name":"M","price":1000}]}`), &in))
	assert.Equal(t, "An", in.FullName)
	assert.Len(t, in.Skus, 1)
}

func TestDecodeReportsJSONFieldPaths(t *testing.T) {
	var in productIn
	err := httpx.Decode(req(`{"full_name":"","skus":[{"name":"","price":5}]}`), &in)
	fields := fieldsOf(t, err)
	assert.Equal(t, "is required", fields["full_name"])
	assert.Equal(t, "is required", fields["skus[0].name"])
	assert.Equal(t, "must be at least 1000", fields["skus[0].price"])
}

func TestDecodeRejectsMalformedBodies(t *testing.T) {
	cases := map[string]string{
		"unknown field": `{"full_name":"a","skus":[{"name":"x","price":1000}],"extra":1}`,
		"trailing data": `{"full_name":"a","skus":[{"name":"x","price":1000}]} {}`,
		"empty":         ``,
		"not json":      `full_name=a`,
		"wrong type":    `{"full_name":5}`,
		"too large":     `{"full_name":"` + strings.Repeat("a", httpx.MaxBodyBytes) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var in productIn
			err := httpx.Decode(req(body), &in)
			require.Error(t, err)
			var ae *httpx.AppError
			require.True(t, errors.As(err, &ae))
			assert.Equal(t, http.StatusBadRequest, ae.Status)
			assert.Contains(t, ae.Details["fields"], "body")
		})
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteJSON(rec, http.StatusCreated, map[string]string{"id": "1"})
	assert.Equal(t, 201, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id":"1"}`, rec.Body.String())
}

func TestWriteJSONNilBody(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteJSON(rec, http.StatusNoContent, nil)
	assert.Equal(t, 204, rec.Code)
	assert.Empty(t, rec.Body.String())
}
