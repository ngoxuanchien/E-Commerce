// Package grpc exposes payment-svc's PaymentInternal service.
package grpc

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

// Canceller is the use case behind CancelCheckoutPayments.
type Canceller interface {
	CancelCheckoutPayments(ctx context.Context, checkoutID string) (paymentv1.Outcome, error)
}

// Server implements paymentv1.PaymentInternalServer.
type Server struct {
	paymentv1.UnimplementedPaymentInternalServer
	canceller Canceller
	logger    *slog.Logger
}

func NewServer(c Canceller, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{canceller: c, logger: logger}
}

var _ paymentv1.PaymentInternalServer = (*Server)(nil)

// CancelCheckoutPayments maps ErrProviderUnavailable and ErrStillPayable to UNAVAILABLE, which
// order-svc retries, and any other failure to INTERNAL.
func (s *Server) CancelCheckoutPayments(ctx context.Context, req *paymentv1.CancelCheckoutPaymentsRequest) (*paymentv1.CancelCheckoutPaymentsResponse, error) {
	// A malformed id must not reach the use case: an unknown checkout is NOT_PAID, which makes
	// order-svc cancel its orders.
	if _, err := uuid.Parse(req.GetCheckoutId()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "checkout_id must be a UUID")
	}
	out, err := s.canceller.CancelCheckoutPayments(ctx, req.GetCheckoutId())
	switch {
	case err == nil:
		return &paymentv1.CancelCheckoutPaymentsResponse{Outcome: out}, nil
	case errors.Is(err, app.ErrProviderUnavailable), errors.Is(err, app.ErrStillPayable):
		return nil, status.Error(codes.Unavailable, err.Error())
	case ctx.Err() != nil:
		return nil, status.FromContextError(ctx.Err()).Err()
	default:
		s.logger.ErrorContext(ctx, "cancel checkout payments failed", "checkout_id", req.GetCheckoutId(), "error", err)
		return nil, status.Error(codes.Internal, "cancel checkout payments failed")
	}
}
