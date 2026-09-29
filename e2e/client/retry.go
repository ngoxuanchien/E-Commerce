package client

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// RetryRateLimited resends requests the gateway answered with 429, after the
// Retry-After delay. The gateway rate-limits before proxying, so the
// upstream never saw the rejected request and resending is safe for every
// method. A 429 is returned as-is once waiting would exceed MaxWait in total.
type RetryRateLimited struct {
	Base    http.RoundTripper
	MaxWait time.Duration
}

func (t RetryRateLimited) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	deadline := time.Now().Add(t.MaxWait)
	for {
		resp, err := base.RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests {
			return resp, err
		}
		wait := retryAfter(resp.Header.Get("Retry-After"))
		if time.Now().Add(wait).After(deadline) {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait):
		}

		if req.Body != nil && req.Body != http.NoBody {
			if req.GetBody == nil {
				return nil, fmt.Errorf("retry %s %s: request body cannot be replayed", req.Method, req.URL.Path)
			}
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}

func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return time.Second
}
