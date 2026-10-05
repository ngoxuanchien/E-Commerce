// Package orderfake is an in-process fake of order-svc's OrderInternal gRPC API for tests.
// Checkouts are scripted with Put; failures and latency can be injected.
package orderfake

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
)

// StatusAwaitingPayment is the order status a payable order has.
const StatusAwaitingPayment = "AWAITING_PAYMENT"

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// NewCheckout returns a checkout of buyer with one AWAITING_PAYMENT order per total
// (subtotal = total - 30000, commission 5% of the subtotal). A zero deadline leaves it unset.
func NewCheckout(buyerID, method string, deadline time.Time, totals ...int64) *orderv1.GetCheckoutResponse {
	c := &orderv1.GetCheckoutResponse{CheckoutId: newID(), BuyerId: buyerID, PaymentMethod: method}
	if !deadline.IsZero() {
		c.PaymentDeadline = timestamppb.New(deadline)
	}
	for _, t := range totals {
		sub := max(t-30_000, 0)
		c.Orders = append(c.Orders, &orderv1.CheckoutOrder{
			OrderId: newID(), SellerUserId: newID(), Status: StatusAwaitingPayment,
			Subtotal: sub, Total: t, CommissionAmount: sub / 20,
		})
	}
	return c
}

// Fake implements orderv1.OrderInternalServer in memory. It is safe for concurrent use.
type Fake struct {
	orderv1.UnimplementedOrderInternalServer

	mu        sync.Mutex
	checkouts map[string]*orderv1.GetCheckoutResponse
	calls     int
	failNext  []codes.Code
	delay     time.Duration
}

// New returns an empty fake.
func New() *Fake { return &Fake{checkouts: map[string]*orderv1.GetCheckoutResponse{}} }

// Put adds or replaces a checkout (a copy is stored).
func (f *Fake) Put(c *orderv1.GetCheckoutResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkouts[c.GetCheckoutId()] = proto.Clone(c).(*orderv1.GetCheckoutResponse)
}

// SetOrderStatus changes the status of every order of a checkout.
func (f *Fake) SetOrderStatus(checkoutID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.checkouts[checkoutID].GetOrders() {
		o.Status = status
	}
}

// Calls returns how many times GetCheckout was invoked (failed calls included).
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// FailNext makes the next n calls fail with code.
func (f *Fake) FailNext(code codes.Code, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for range n {
		f.failNext = append(f.failNext, code)
	}
}

// SetDelay makes every call wait d (or until the caller's deadline).
func (f *Fake) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

func (f *Fake) GetCheckout(ctx context.Context, req *orderv1.GetCheckoutRequest) (*orderv1.GetCheckoutResponse, error) {
	f.mu.Lock()
	f.calls++
	d := f.delay
	code := codes.OK
	if len(f.failNext) > 0 {
		code, f.failNext = f.failNext[0], f.failNext[1:]
	}
	f.mu.Unlock()

	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-timer.C:
		}
	}
	if code != codes.OK {
		return nil, status.Errorf(code, "injected GetCheckout failure")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.checkouts[req.GetCheckoutId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "checkout %s not found", req.GetCheckoutId())
	}
	return proto.Clone(c).(*orderv1.GetCheckoutResponse), nil
}

// Dial serves the fake over an in-memory bufconn listener and returns a client
// connection; both are closed when t ends.
func (f *Fake) Dial(t testing.TB) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	orderv1.RegisterOrderInternalServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///orderfake",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial order fake: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return conn
}
