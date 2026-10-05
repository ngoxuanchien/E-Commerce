package client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// CreatePayment creates a payment for a checkout with an idempotency key.
// POST /api/payments {checkout_id}, Idempotency-Key: key
func (c *Client) CreatePayment(ctx context.Context, key, checkoutID string, hdr http.Header) (Payment, error) {
	h := http.Header{"Idempotency-Key": {key}}
	for k, vs := range hdr {
		h[k] = vs
	}
	var p Payment
	return p, c.Do(ctx, http.MethodPost, "/api/payments", map[string]string{"checkout_id": checkoutID}, &p, h)
}

// Payment retrieves a payment by ID.
// GET /api/payments/{id}
func (c *Client) Payment(ctx context.Context, id string) (Payment, error) {
	var p Payment
	return p, c.Do(ctx, http.MethodGet, "/api/payments/"+url.PathEscape(id), nil, &p, nil)
}

// FakeArm loads the FAKE provider checkout page (HTML form).
// GET /api/payments/fake/checkout/{id}?<hooks>
// Returns the HTML response (discarded) on 200.
func (c *Client) FakeArm(ctx context.Context, paymentID string, hooks url.Values) error {
	path := "/api/payments/fake/checkout/" + url.PathEscape(paymentID)
	if len(hooks) > 0 {
		path += "?" + hooks.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: expected 2xx, got %d", path, resp.StatusCode)
	}
	return nil
}

// FakeSubmit submits the FAKE provider form with outcome and hooks.
// Form POST, does NOT follow 302 redirects, returns Location header.
// POST /api/payments/fake/checkout/{id} with form data (outcome + hooks values)
// Expects 302, returns Location; any other status becomes APIError.
func (c *Client) FakeSubmit(ctx context.Context, paymentID, outcome string, hooks url.Values, hdr http.Header) (string, error) {
	// Build form data
	formData := url.Values{}
	formData.Set("outcome", outcome)
	for k, vs := range hooks {
		for _, v := range vs {
			formData.Add(k, v)
		}
	}

	// Create HTTP client that doesn't follow redirects
	httpCopy := *c.HTTP
	httpCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	path := "/api/payments/fake/checkout/" + url.PathEscape(paymentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader([]byte(formData.Encode())))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := httpCopy.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("POST %s: read body: %w", path, err)
	}

	if resp.StatusCode != http.StatusFound {
		return "", apiError(resp.StatusCode, data)
	}

	return resp.Header.Get("Location"), nil
}

// FakeReturn navigates back from the FAKE provider.
// GET /api/payments/fake/return?payment_id=…
// Expects 302, returns Location.
func (c *Client) FakeReturn(ctx context.Context, paymentID string) (string, error) {
	// Create HTTP client that doesn't follow redirects
	httpCopy := *c.HTTP
	httpCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	path := "/api/payments/fake/return?payment_id=" + url.QueryEscape(paymentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return "", err
	}

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := httpCopy.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusFound {
		return "", fmt.Errorf("GET %s: expected 302, got %d", path, resp.StatusCode)
	}

	return resp.Header.Get("Location"), nil
}

// SignFake computes the signature for a FAKE webhook event.
// Format: "t=<unix>,v1=<hex HMAC-SHA256(secret, "<unix>." + body)>"
func SignFake(secret string, ts time.Time, body []byte) string {
	unix := strconv.FormatInt(ts.Unix(), 10)
	msg := unix + "." + string(body)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(msg))
	sig := hex.EncodeToString(h.Sum(nil))
	return "t=" + unix + ",v1=" + sig
}

// FakeWebhook sends a webhook event for the FAKE payment provider.
// POST /api/payments/fake/webhook, X-Fake-Signature: SignFake(secret, now, body)
func (c *Client) FakeWebhook(ctx context.Context, secret string, ev FakeEvent, hdr http.Header) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	h := http.Header{"X-Fake-Signature": {SignFake(secret, time.Now(), body)}}
	for k, vs := range hdr {
		h[k] = vs
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/payments/fake/webhook", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, vs := range h {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, data)
	}
	return nil
}

// Wallet retrieves the seller's wallet balance.
// GET /api/seller/wallet
func (c *Client) Wallet(ctx context.Context) (Wallet, error) {
	var w Wallet
	return w, c.Do(ctx, http.MethodGet, "/api/seller/wallet", nil, &w, nil)
}

// WalletEntries retrieves the seller's wallet entries (ledger).
// GET /api/seller/wallet/entries?limit=100
func (c *Client) WalletEntries(ctx context.Context) (Page[WalletEntry], error) {
	q := url.Values{"limit": {"100"}}
	var p Page[WalletEntry]
	return p, c.Do(ctx, http.MethodGet, "/api/seller/wallet/entries?"+q.Encode(), nil, &p, nil)
}

// CreatePayout creates a payout request with an idempotency key.
// POST /api/seller/payouts {amount}, Idempotency-Key: key
func (c *Client) CreatePayout(ctx context.Context, key string, amount int64) (Payout, error) {
	h := http.Header{"Idempotency-Key": {key}}
	var p Payout
	return p, c.Do(ctx, http.MethodPost, "/api/seller/payouts", map[string]int64{"amount": amount}, &p, h)
}

// Payouts retrieves the seller's payouts.
// GET /api/seller/payouts?limit=100[&status=]
func (c *Client) Payouts(ctx context.Context, status string) (Page[Payout], error) {
	q := url.Values{"limit": {"100"}}
	if status != "" {
		q.Set("status", status)
	}
	var p Page[Payout]
	return p, c.Do(ctx, http.MethodGet, "/api/seller/payouts?"+q.Encode(), nil, &p, nil)
}
