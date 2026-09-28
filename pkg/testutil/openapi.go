package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// LoadOpenAPI loads and validates an OpenAPI document (external $refs allowed).
func LoadOpenAPI(t testing.TB, path string) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	doc, err := loader.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("invalid %s: %v", path, err)
	}
	return doc
}

type contractTransport struct {
	base   http.RoundTripper
	router routers.Router
}

// ContractTransport returns a RoundTripper that validates every request and
// response against the spec at specPath. Violations are returned as errors
// from RoundTrip. Security requirements are not checked (the gateway owns auth).
func ContractTransport(t testing.TB, specPath string, base http.RoundTripper) http.RoundTripper {
	t.Helper()
	doc := LoadOpenAPI(t, specPath)
	doc.Servers = nil // match any host, e.g. httptest servers
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("openapi router: %v", err)
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &contractTransport{base: base, router: router}
}

func (c *contractTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	route, params, err := c.router.FindRoute(req)
	if err != nil {
		return nil, fmt.Errorf("request violates contract: %s %s: %w", req.Method, req.URL.Path, err)
	}

	var reqBody []byte
	if req.Body != nil {
		if reqBody, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	in := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
		return nil, fmt.Errorf("request violates contract: %s %s: %w", req.Method, req.URL.Path, err)
	}
	req.Body = io.NopCloser(bytes.NewReader(reqBody))

	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	respBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in, Status: resp.StatusCode, Header: resp.Header,
		Body: io.NopCloser(bytes.NewReader(respBody)),
	}
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		return nil, fmt.Errorf("response violates contract: %s %s -> %d: %w", req.Method, req.URL.Path, resp.StatusCode, err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	return resp, nil
}
