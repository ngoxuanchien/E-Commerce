//go:build sandbox

package stripe_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/stripe"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// TestSandboxStripe is an opt-in smoke test against Stripe test mode. It never runs in CI (build tag
// `sandbox`) and skips unless PAYMENT_SANDBOX=1 and STRIPE_SECRET_KEY (sk_test_...) is set:
//
//	PAYMENT_SANDBOX=1 STRIPE_SECRET_KEY=sk_test_... go test -tags sandbox -run Sandbox -v ./internal/adapter/stripe/
func TestSandboxStripe(t *testing.T) {
	if os.Getenv("PAYMENT_SANDBOX") != "1" {
		t.Skip("set PAYMENT_SANDBOX=1 to run the real-sandbox smoke test")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if key == "" {
		t.Skip("STRIPE_SECRET_KEY is required")
	}
	if !strings.HasPrefix(key, "sk_test_") {
		t.Fatal("STRIPE_SECRET_KEY must be a test-mode key (sk_test_...)")
	}
	p := stripe.New(stripe.Config{SecretKey: key, PublicBaseURL: "http://localhost:8080"})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	now := time.Now().Truncate(time.Second)
	pay := domain.Payment{
		ID: uuid.NewString(), CheckoutID: uuid.NewString(), Method: "STRIPE", Provider: "stripe",
		Status: domain.StatusPending, Amount: 20000, CreatedAt: now, ExpiresAt: now.Add(31 * time.Minute),
	}

	// 1. CreateSession for 20,000 VND with ExpiresAt = now+31m.
	sess, err := p.CreateSession(ctx, app.SessionInput{
		PaymentID: pay.ID, CheckoutID: pay.CheckoutID, Amount: pay.Amount, ExpiresAt: pay.ExpiresAt, CreatedAt: pay.CreatedAt,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if !strings.HasPrefix(sess.RedirectURL, "https://checkout.stripe.com/") {
		t.Fatalf("redirect url = %q, want a https://checkout.stripe.com/ URL", sess.RedirectURL)
	}
	pay.ProviderRef = sess.ProviderRef

	// 2. Query → Pending (the session is open).
	obs, err := p.Query(ctx, pay)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if obs.State != domain.ObservedPending {
		t.Fatalf("Query state = %v, want Pending", obs.State)
	}

	// 3. Cancel → true (sessions.expire).
	ok, err := p.Cancel(ctx, pay)
	if err != nil || !ok {
		t.Fatalf("Cancel = %v, %v; want true, nil", ok, err)
	}

	// 4. Query → Expired.
	obs, err = p.Query(ctx, pay)
	if err != nil {
		t.Fatalf("Query after cancel: %v", err)
	}
	if obs.State != domain.ObservedExpired {
		t.Fatalf("Query state after cancel = %v, want Expired", obs.State)
	}
}
