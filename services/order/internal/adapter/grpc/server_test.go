package grpcadapter_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
	grpcadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/grpc"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

var ctx = context.Background()

func uid() string { return uuid.Must(uuid.NewV7()).String() }

// stubStore answers only ListCheckoutOrders; the embedded nil Store makes any
// other call panic, so a server reaching for more than the query is caught.
type stubStore struct {
	app.Store
	orders []domain.Order
	err    error
	gotID  string
}

func (s *stubStore) ListCheckoutOrders(_ context.Context, checkoutID string) ([]domain.Order, error) {
	s.gotID = checkoutID
	if s.err != nil {
		return nil, s.err
	}
	return s.orders, nil
}

var testDeadline = time.Date(2026, 9, 30, 10, 30, 0, 0, time.UTC)

// serve runs the OrderInternal server over an in-memory bufconn listener and
// returns a client to it, like catalogfake.Dial does for its own service.
func serve(t *testing.T, store app.Store) orderv1.OrderInternalClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	orderv1.RegisterOrderInternalServer(srv, grpcadapter.NewServer(app.NewCheckoutQuery(store), slog.Default()))
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return orderv1.NewOrderInternalClient(conn)
}

func TestGetCheckout(t *testing.T) {
	checkoutID := uid()
	onlineOrders := []domain.Order{
		{ID: uid(), CheckoutID: checkoutID, BuyerID: "buyer-1", SellerUserID: "seller-1",
			Status: domain.StatusAwaitingPayment, PaymentMethod: domain.PaymentVNPay,
			PaymentDeadline: &testDeadline, Subtotal: 100_000, ShippingFee: 30_000,
			Total: 130_000, CommissionAmount: 5_000},
		{ID: uid(), CheckoutID: checkoutID, BuyerID: "buyer-1", SellerUserID: "seller-2",
			Status: domain.StatusCancelled, PaymentMethod: domain.PaymentVNPay,
			PaymentDeadline: &testDeadline, Subtotal: 200_000, ShippingFee: 30_000,
			Total: 230_000, CommissionAmount: 10_000},
	}

	t.Run("an online checkout answers every field", func(t *testing.T) {
		store := &stubStore{orders: onlineOrders}
		resp, err := serve(t, store).GetCheckout(ctx, &orderv1.GetCheckoutRequest{CheckoutId: checkoutID})
		require.NoError(t, err)

		assert.Equal(t, checkoutID, resp.GetCheckoutId())
		assert.Equal(t, checkoutID, store.gotID, "the id reaches the query verbatim")
		assert.Equal(t, "buyer-1", resp.GetBuyerId())
		assert.Equal(t, domain.PaymentVNPay, resp.GetPaymentMethod())
		require.NotNil(t, resp.GetPaymentDeadline())
		assert.True(t, testDeadline.Equal(resp.GetPaymentDeadline().AsTime()), "the deadline is carried as-is")

		orders := resp.GetOrders()
		require.Len(t, orders, 2)
		assert.Equal(t, []*orderv1.CheckoutOrder{
			{OrderId: onlineOrders[0].ID, SellerUserId: "seller-1", Status: string(domain.StatusAwaitingPayment),
				Subtotal: 100_000, Total: 130_000, CommissionAmount: 5_000},
			{OrderId: onlineOrders[1].ID, SellerUserId: "seller-2", Status: string(domain.StatusCancelled),
				Subtotal: 200_000, Total: 230_000, CommissionAmount: 10_000},
		}, orders)
	})

	t.Run("a COD checkout has no deadline", func(t *testing.T) {
		store := &stubStore{orders: []domain.Order{{
			ID: uid(), CheckoutID: uid(), BuyerID: "buyer-cod", SellerUserID: "seller-cod",
			Status: domain.StatusPendingConfirmation, PaymentMethod: domain.PaymentCOD,
			Subtotal: 50_000, Total: 80_000,
		}}}
		resp, err := serve(t, store).GetCheckout(ctx, &orderv1.GetCheckoutRequest{
			CheckoutId: store.orders[0].CheckoutID,
		})
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentCOD, resp.GetPaymentMethod())
		assert.Nil(t, resp.GetPaymentDeadline(), "an unset deadline stays unset")
		require.Len(t, resp.GetOrders(), 1)
		assert.Zero(t, resp.GetOrders()[0].GetCommissionAmount(), "COD orders carry no commission")
	})

	t.Run("an unknown checkout is NotFound", func(t *testing.T) {
		_, err := serve(t, &stubStore{}).GetCheckout(ctx, &orderv1.GetCheckoutRequest{CheckoutId: uid()})
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Equal(t, "checkout not found", status.Convert(err).Message())
	})

	t.Run("a malformed checkout id is NotFound without asking the store", func(t *testing.T) {
		store := &stubStore{orders: onlineOrders}
		_, err := serve(t, store).GetCheckout(ctx, &orderv1.GetCheckoutRequest{CheckoutId: "not-a-uuid"})
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Empty(t, store.gotID, "a malformed id never reaches the database")
	})

	t.Run("a store failure is Internal", func(t *testing.T) {
		_, err := serve(t, &stubStore{err: errors.New("connection reset by peer")}).
			GetCheckout(ctx, &orderv1.GetCheckoutRequest{CheckoutId: uid()})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}
