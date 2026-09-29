// Package proxy builds the reverse proxy the gateway uses to forward a
// matched request to its upstream (identity, catalog, or order), mapping
// upstream connectivity and body-size failures to the gateway's standard
// error envelope. Upstream responses are otherwise passed through
// untouched: any status code, headers, and body.
package proxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
)

// Pooled idle connection limits for the transport built by NewTransport.
// These bound how many keep-alive connections to upstreams accumulate,
// independent of the per-request timeout.
const (
	maxIdleConns        = 256
	maxIdleConnsPerHost = 64
)

// keepAlive is the TCP keep-alive period for connections dialed by
// NewTransport's transport.
const keepAlive = 30 * time.Second

// ParseTarget parses raw as an upstream base URL. It must have an http or
// https scheme and a non-empty host, and nothing else: no path, query,
// fragment, or userinfo. The returned URL carries only scheme and host,
// ready to pass to New.
func ParseTarget(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse target %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("proxy: target %q must use http or https", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy: target %q must have a host", raw)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("proxy: target %q must be a bare scheme and host, with no path, query, fragment, or userinfo", raw)
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

// NewTransport builds the http.RoundTripper the gateway uses to reach a
// proxied upstream: a clone of the default transport with its dial and
// response-header timeouts bound by timeout, a pooled idle-connection
// cache, and OpenTelemetry instrumentation so traceparent propagates to
// the upstream service.
func NewTransport(timeout time.Duration) http.RoundTripper {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{
		Timeout:   timeout,
		KeepAlive: keepAlive,
	}).DialContext
	base.ResponseHeaderTimeout = timeout
	base.MaxIdleConns = maxIdleConns
	base.MaxIdleConnsPerHost = maxIdleConnsPerHost
	return otelx.HTTPTransport(base)
}

// New builds a reverse proxy that forwards every request to target over
// transport, keeping the request's path and raw query unchanged (target
// carries only scheme and host, as ParseTarget returns it).
//
// Upstream responses of any status are passed through untouched. A
// request body that exceeds the gateway's body limit (enforced by an
// http.MaxBytesReader wrapped around the request body upstream of the
// proxy) maps to 413 PAYLOAD_TOO_LARGE; a client that has already
// cancelled its request gets no response written; any other failure
// (connection refused, DNS failure, a response-header timeout, ...) maps
// to 503 UPSTREAM_UNAVAILABLE.
func New(target *url.URL, transport http.RoundTripper) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
		},
		Transport: transport,
		ErrorLog:  slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			var maxBytesErr *http.MaxBytesError
			switch {
			case errors.As(err, &maxBytesErr):
				httpx.WriteError(w, r, httpx.NewError(httpx.CodePayloadTooLarge, "request body too large").Wrap(err))
			case r.Context().Err() != nil:
				// The client is already gone; there is no one to write a
				// response to.
				slog.DebugContext(r.Context(), "proxy: client request cancelled", "error", err)
			default:
				httpx.WriteError(w, r, httpx.NewError(httpx.CodeUpstreamUnavailable, "upstream service unavailable").Wrap(redactURLError(err)))
			}
		},
	}
}

// redactURLError strips the query string, fragment and userinfo from the URL
// embedded in a *url.Error, which would otherwise end up in the logs (the
// forwarded query may carry tokens). The inner error stays unwrappable.
func redactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	redacted := ue.URL
	if u, perr := url.Parse(ue.URL); perr == nil {
		redacted = (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
	} else {
		redacted = "<unparsable url>"
	}
	return fmt.Errorf("%s %s: %w", ue.Op, redacted, ue.Err)
}
