// Package paymentfake is an in-process fake of payment-svc's PaymentInternal
// gRPC API for tests. It answers OUTCOME_PAID by default and can pin an
// outcome or an error code per checkout, plus a global delay, so the sweeper's
// payment-timeout step can be exercised without the real service.
package paymentfake

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
)

// Fake implements paymentv1.PaymentInternalServer in memory. It is safe for
// concurrent use.
type Fake struct {
	paymentv1.UnimplementedPaymentInternalServer

	mu       sync.Mutex
	def      paymentv1.Outcome
	outcomes map[string]paymentv1.Outcome
	errs     map[string]codes.Code
	delay    time.Duration
	calls    map[string]int
}

// New returns a fake that answers OUTCOME_PAID for every checkout, so an
// unrelated test never has an order cancelled by the fake.
func New() *Fake {
	return &Fake{
		def:      paymentv1.Outcome_OUTCOME_PAID,
		outcomes: map[string]paymentv1.Outcome{},
		errs:     map[string]codes.Code{},
		calls:    map[string]int{},
	}
}

// SetDefault sets the answer for checkouts without their own outcome.
func (f *Fake) SetDefault(o paymentv1.Outcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.def = o
}

// SetOutcome pins the outcome of checkoutID; it also clears an error set for
// that checkout.
func (f *Fake) SetOutcome(checkoutID string, o paymentv1.Outcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[checkoutID] = o
	delete(f.errs, checkoutID)
}

// SetError makes every call for checkoutID fail with code until an outcome is
// set for it.
func (f *Fake) SetError(checkoutID string, c codes.Code) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[checkoutID] = c
}

// SetDelay makes every call wait d (or until the caller's deadline).
func (f *Fake) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

// Calls returns how many times CancelCheckoutPayments reached the handler for
// checkoutID, including calls that failed or were cut short.
func (f *Fake) Calls(checkoutID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[checkoutID]
}

// Dial serves the fake over an in-memory bufconn listener and returns a
// client connection; both are closed when t ends.
func (f *Fake) Dial(t testing.TB) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	paymentv1.RegisterPaymentInternalServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///paymentfake",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial payment fake: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return conn
}

// begin counts the call and applies the configured delay, then reports an
// injected failure, if any.
func (f *Fake) begin(ctx context.Context, checkoutID string) error {
	f.mu.Lock()
	f.calls[checkoutID]++
	d := f.delay
	code := codes.OK
	if c, ok := f.errs[checkoutID]; ok {
		code = c
	}
	f.mu.Unlock()

	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-timer.C:
		}
	}
	if code != codes.OK {
		return status.Errorf(code, "injected payment failure for checkout %s", checkoutID)
	}
	return nil
}

func (f *Fake) CancelCheckoutPayments(ctx context.Context, req *paymentv1.CancelCheckoutPaymentsRequest) (*paymentv1.CancelCheckoutPaymentsResponse, error) {
	id := req.GetCheckoutId()
	if err := f.begin(ctx, id); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.outcomes[id]
	if !ok {
		o = f.def
	}
	return &paymentv1.CancelCheckoutPaymentsResponse{Outcome: o}, nil
}
