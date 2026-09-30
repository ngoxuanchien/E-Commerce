package httpadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/http"
)

// stripeStub stands in for stripe.Callbacks; the adapter's own tests cover the logic.
type stripeStub struct {
	paymentID       string
	code            int
	gotBody         []byte
	gotSig, gotSess string
	calls           int
}

func (s *stripeStub) Webhook(_ context.Context, raw []byte, sig string) int {
	s.calls++
	s.gotBody, s.gotSig = raw, sig
	return s.code
}

func (s *stripeStub) Return(_ context.Context, sessionID string) string {
	s.gotSess = sessionID
	return s.paymentID
}

func stripeServer(t *testing.T, s *stripeStub) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(authctx.Middleware(httpadapter.NewRouter(nil, httpadapter.NewStripeCallbacks(s))))
	t.Cleanup(srv.Close)
	return srv, &http.Client{Transport: testutil.ContractTransport(t, spec, nil), CheckRedirect: noRedirect}
}

func postWebhook(t *testing.T, c *http.Client, url string, body []byte, sig string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/api/payments/stripe/webhook", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("Stripe-Signature", sig)
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	return resp
}

func TestStripeWebhookPassesTheRawBody(t *testing.T) {
	s := &stripeStub{code: http.StatusOK}
	srv, c := stripeServer(t, s)
	body := []byte(`{"id": "evt_1",  "object":"event", "type":"checkout.session.completed"}`)
	resp := postWebhook(t, c, srv.URL, body, "t=1,v1=ab")
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, body, s.gotBody) // byte for byte, whitespace included
	assert.Equal(t, "t=1,v1=ab", s.gotSig)
}

func TestStripeWebhookStatuses(t *testing.T) {
	s := &stripeStub{code: http.StatusBadRequest}
	srv, c := stripeServer(t, s)
	resp := postWebhook(t, c, srv.URL, []byte(`{}`), "t=1,v1=00")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	assert.Equal(t, "VALIDATION_ERROR", e.Error.Code)

	// 500 asks Stripe to retry; payment.yaml documents only 200/400, so use a plain client.
	s.code = http.StatusInternalServerError
	resp2 := postWebhook(t, http.DefaultClient, srv.URL, []byte(`{}`), "t=1,v1=00")
	_ = resp2.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp2.StatusCode)
}

func TestStripeWebhookBodyLimit(t *testing.T) {
	s := &stripeStub{code: http.StatusOK}
	srv, _ := stripeServer(t, s)

	ok := postWebhook(t, http.DefaultClient, srv.URL, bytes.Repeat([]byte(" "), 256<<10), "t=1,v1=00")
	_ = ok.Body.Close()
	assert.Equal(t, http.StatusOK, ok.StatusCode)
	assert.Equal(t, 1, s.calls)

	big := postWebhook(t, http.DefaultClient, srv.URL, bytes.Repeat([]byte(" "), 256<<10+1), "t=1,v1=00")
	_ = big.Body.Close()
	assert.Equal(t, http.StatusBadRequest, big.StatusCode)
	assert.Equal(t, 1, s.calls) // never reached the handler
}

func TestStripeReturnRedirects(t *testing.T) {
	s := &stripeStub{paymentID: "0192f0c1-a2b3-7c4d-8e9f-a0b1c2d3e4f5"}
	srv, c := stripeServer(t, s)
	resp, err := c.Get(srv.URL + "/api/payments/stripe/return?session_id=cs_test_a1B2")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 302, resp.StatusCode)
	assert.Equal(t, "/checkout/result?payment_id=0192f0c1-a2b3-7c4d-8e9f-a0b1c2d3e4f5", resp.Header.Get("Location"))
	assert.Equal(t, "cs_test_a1B2", s.gotSess)

	s.paymentID = ""
	resp, err = c.Get(srv.URL + "/api/payments/stripe/return?session_id=cs_test_unknown")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 302, resp.StatusCode)
	assert.Equal(t, "/checkout/result", resp.Header.Get("Location"))

	// Even without the (required) session_id the browser lands on the result page.
	resp, err = (&http.Client{CheckRedirect: noRedirect}).Get(srv.URL + "/api/payments/stripe/return")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 302, resp.StatusCode)
	assert.Equal(t, "/checkout/result", resp.Header.Get("Location"))
}
