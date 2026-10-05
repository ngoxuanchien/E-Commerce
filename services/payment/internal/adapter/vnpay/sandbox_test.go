//go:build sandbox

package vnpay_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/vnpay"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// TestSandboxVNPay is an opt-in smoke test against the real VNPay sandbox. It never runs in CI
// (build tag `sandbox`) and skips unless PAYMENT_SANDBOX=1 and the VNPAY_* credentials are set:
//
//	PAYMENT_SANDBOX=1 VNPAY_TMN_CODE=... VNPAY_HASH_SECRET=... \
//	VNPAY_PAY_URL=https://sandbox.vnpayment.vn/paymentv2/vpcpay.html \
//	VNPAY_API_URL=https://sandbox.vnpayment.vn/merchant_webapi/api/transaction \
//	go test -tags sandbox -run Sandbox -v ./internal/adapter/vnpay/
func TestSandboxVNPay(t *testing.T) {
	if os.Getenv("PAYMENT_SANDBOX") != "1" {
		t.Skip("set PAYMENT_SANDBOX=1 to run the real-sandbox smoke test")
	}
	cfg := vnpay.Config{
		TmnCode: os.Getenv("VNPAY_TMN_CODE"), HashSecret: os.Getenv("VNPAY_HASH_SECRET"),
		PayURL: os.Getenv("VNPAY_PAY_URL"), APIURL: os.Getenv("VNPAY_API_URL"),
		PublicBaseURL: "http://localhost:8080",
	}
	if cfg.TmnCode == "" || cfg.HashSecret == "" || cfg.PayURL == "" || cfg.APIURL == "" {
		t.Skip("VNPAY_TMN_CODE, VNPAY_HASH_SECRET, VNPAY_PAY_URL and VNPAY_API_URL are required")
	}
	p := vnpay.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	now := time.Now().Truncate(time.Second)
	pay := domain.Payment{
		ID: uuid.NewString(), CheckoutID: uuid.NewString(), Method: "VNPAY", Provider: "vnpay",
		Status: domain.StatusPending, Amount: 20000, CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	}
	sess, err := p.CreateSession(ctx, app.SessionInput{
		PaymentID: pay.ID, CheckoutID: pay.CheckoutID, Amount: pay.Amount, ExpiresAt: pay.ExpiresAt,
		CreatedAt: pay.CreatedAt, Description: "Sandbox smoke test", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	pay.ProviderRef = sess.ProviderRef

	// 1. The pay URL is accepted: 200 or a redirect to a VNPay page, and no "invalid signature" (RC 70).
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sess.RedirectURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET pay url: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Fatalf("pay url answered %d, want 200 or 302 (Location %q)", resp.StatusCode, loc)
	}
	if strings.Contains(loc, "code=70") || strings.Contains(loc, "ResponseCode=70") || strings.Contains(string(body), "code=70") {
		t.Fatalf("VNPay rejected the signature (response code 70): Location %q", loc)
	}
	t.Logf("pay url answered %d, Location %q", resp.StatusCode, loc)

	// 2. querydr on the fresh TxnRef: a response whose hash verifies (Provider.call checks it) → Pending.
	obs, err := p.Query(ctx, pay)
	if err != nil {
		t.Fatalf("Query (querydr): %v", err)
	}
	if obs.State != domain.ObservedPending {
		t.Fatalf("Query state = %v, want Pending", obs.State)
	}
}
