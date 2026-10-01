package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	pgrpc "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/grpc"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

type stub struct {
	out paymentv1.Outcome
	err error
}

func (s stub) CancelCheckoutPayments(context.Context, string) (paymentv1.Outcome, error) {
	return s.out, s.err
}

func client(t *testing.T, c pgrpc.Canceller) paymentv1.PaymentInternalClient {
	t.Helper()
	return clientLog(t, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func clientLog(t *testing.T, c pgrpc.Canceller, log *slog.Logger) paymentv1.PaymentInternalClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	paymentv1.RegisterPaymentInternalServer(srv, pgrpc.NewServer(c, log))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return paymentv1.NewPaymentInternalClient(conn)
}

func TestCancelCheckoutPayments(t *testing.T) {
	req := &paymentv1.CancelCheckoutPaymentsRequest{CheckoutId: "0198f0a4-7c1e-7b2a-8d3e-1f2a3b4c5d6e"}
	for _, tc := range []struct {
		name string
		s    stub
		req  *paymentv1.CancelCheckoutPaymentsRequest
		out  paymentv1.Outcome
		code codes.Code
	}{
		{"paid", stub{out: paymentv1.Outcome_OUTCOME_PAID}, req, paymentv1.Outcome_OUTCOME_PAID, codes.OK},
		{"not paid", stub{out: paymentv1.Outcome_OUTCOME_NOT_PAID}, req, paymentv1.Outcome_OUTCOME_NOT_PAID, codes.OK},
		{"provider unavailable", stub{err: fmt.Errorf("x: %w", app.ErrProviderUnavailable)}, req, 0, codes.Unavailable},
		{"still payable", stub{err: fmt.Errorf("x: %w", app.ErrStillPayable)}, req, 0, codes.Unavailable},
		{"other error", stub{err: errors.New("db down")}, req, 0, codes.Internal},
		{"malformed checkout id", stub{out: paymentv1.Outcome_OUTCOME_NOT_PAID}, &paymentv1.CancelCheckoutPaymentsRequest{CheckoutId: "not-a-uuid"}, 0, codes.InvalidArgument},
		{"missing checkout id", stub{}, &paymentv1.CancelCheckoutPaymentsRequest{}, 0, codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client(t, tc.s).CancelCheckoutPayments(context.Background(), tc.req)
			assert.Equal(t, tc.code, status.Code(err))
			if tc.code == codes.OK {
				assert.Equal(t, tc.out, resp.GetOutcome())
			}
		})
	}
}

func TestInternalErrorIsLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	id := "0198f0a4-7c1e-7b2a-8d3e-1f2a3b4c5d6e"
	_, err := clientLog(t, stub{err: errors.New("db down")}, log).CancelCheckoutPayments(context.Background(),
		&paymentv1.CancelCheckoutPaymentsRequest{CheckoutId: id})
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, buf.String(), "db down")
	assert.Contains(t, buf.String(), id)
	assert.NotContains(t, status.Convert(err).Message(), "db down")
}
