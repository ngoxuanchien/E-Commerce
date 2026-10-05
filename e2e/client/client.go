// Package client is a small typed REST client for the marketplace gateway,
// used by the seed program and the API smoke tests.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// Client calls the gateway as one user: it keeps that user's refresh cookie
// in its jar and sends the current access token as a bearer token.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	token   string
}

// New returns a Client for baseURL. rt is the transport (nil means
// http.DefaultTransport); wrap it in RetryRateLimited to survive 429s.
func New(baseURL string, rt http.RoundTripper) *Client {
	jar, _ := cookiejar.New(nil) // cookiejar.New never fails with nil options
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Transport: rt, Jar: jar, Timeout: 30 * time.Second},
	}
}

// Token returns the current access token ("" before login).
func (c *Client) Token() string { return c.token }

// SetToken replaces the access token sent with every request.
func (c *Client) SetToken(t string) { c.token = t }

// APIError is a non-2xx gateway response in the standard error format.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details json.RawMessage
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message)
}

// IsCode reports whether err is an APIError with the given error code.
func IsCode(err error, code string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == code
}

// StatusOf returns the HTTP status of an APIError, or 0 for other errors.
func StatusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// Do sends a JSON request and decodes a JSON response into out (if non-nil).
// Non-2xx responses become *APIError.
func (c *Client) Do(ctx context.Context, method, path string, body, out any, hdr http.Header) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read body: %w", method, path, err)
	}

	if resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

// apiError decodes a non-2xx response as an APIError.
func apiError(status int, data []byte) *APIError {
	var env struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &env) // a non-JSON body still yields an APIError with the status
	return &APIError{Status: status, Code: env.Error.Code, Message: env.Error.Message, Details: env.Error.Details}
}
