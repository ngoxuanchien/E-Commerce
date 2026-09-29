package httpadapter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/http"
)

// vnpayStub stands in for vnpay.Callbacks; the adapter's own tests cover the logic.
type vnpayStub struct {
	code, msg, paymentID string
	gotIPN, gotReturn    url.Values
}

func (s *vnpayStub) IPN(_ context.Context, q url.Values) (string, string) {
	s.gotIPN = q
	return s.code, s.msg
}

func (s *vnpayStub) Return(_ context.Context, q url.Values) string {
	s.gotReturn = q
	return s.paymentID
}

func vnpayServer(t *testing.T, s *vnpayStub) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(authctx.Middleware(httpadapter.NewRouter(nil, httpadapter.NewVNPayCallbacks(s))))
	t.Cleanup(srv.Close)
	return srv, &http.Client{Transport: testutil.ContractTransport(t, spec, nil), CheckRedirect: noRedirect}
}

func TestVNPayIPNAnswers200JSON(t *testing.T) {
	for _, code := range []string{"00", "01", "02", "04", "97", "99"} {
		s := &vnpayStub{code: code, msg: "m" + code}
		srv, c := vnpayServer(t, s)
		resp, err := c.Get(srv.URL + "/api/payments/vnpay/ipn?vnp_TxnRef=abc&vnp_Amount=100&vnp_SecureHash=ff")
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode, code)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		assert.Equal(t, "abc", s.gotIPN.Get("vnp_TxnRef"))
	}
	s := &vnpayStub{code: "00", msg: "Confirm Success"}
	srv, _ := vnpayServer(t, s)
	resp, err := http.Get(srv.URL + "/api/payments/vnpay/ipn")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, map[string]string{"RspCode": "00", "Message": "Confirm Success"}, body)
}

func TestVNPayReturnRedirects(t *testing.T) {
	s := &vnpayStub{paymentID: "0192f0c1-a2b3-7c4d-8e9f-a0b1c2d3e4f5"}
	srv, c := vnpayServer(t, s)
	resp, err := c.Get(srv.URL + "/api/payments/vnpay/return?vnp_TxnRef=0192f0c1a2b37c4d8e9fa0b1c2d3e4f5")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 302, resp.StatusCode)
	assert.Equal(t, "/checkout/result?payment_id=0192f0c1-a2b3-7c4d-8e9f-a0b1c2d3e4f5", resp.Header.Get("Location"))
	assert.Equal(t, "0192f0c1a2b37c4d8e9fa0b1c2d3e4f5", s.gotReturn.Get("vnp_TxnRef"))

	s.paymentID = ""
	resp, err = c.Get(srv.URL + "/api/payments/vnpay/return?vnp_SecureHash=bad")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 302, resp.StatusCode)
	assert.Equal(t, "/checkout/result", resp.Header.Get("Location"))
}
