// Package grpcadapter serves order-svc's internal gRPC API (OrderInternal) for
// payment-svc. It does no authorization: the port is only reachable inside the
// compose network and payment-svc compares buyer_id with its caller (WS3' §6).
package grpcadapter

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
)

// Server implements orderv1.OrderInternalServer.
type Server struct {
	orderv1.UnimplementedOrderInternalServer
	query  *app.CheckoutQuery
	logger *slog.Logger
}

var _ orderv1.OrderInternalServer = (*Server)(nil)

// NewServer answers from q and logs failures to logger (nil = slog.Default()).
func NewServer(q *app.CheckoutQuery, logger *slog.Logger) orderv1.OrderInternalServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{query: q, logger: logger}
}

// GetCheckout returns a checkout's orders for payment-svc. A checkout_id that is
// not a UUID is answered as NOT_FOUND without a query: no such checkout
// exists, and the database is never asked to parse it. A valid one is
// canonicalized (lower case) before the query, so the response and the logs
// always carry the canonical id.
func (s *Server) GetCheckout(ctx context.Context, req *orderv1.GetCheckoutRequest) (*orderv1.GetCheckoutResponse, error) {
	u, err := uuid.Parse(req.GetCheckoutId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "checkout not found")
	}
	id := u.String()
	view, err := s.query.GetCheckout(ctx, id)
	if err != nil {
		if errors.Is(err, app.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "checkout not found")
		}
		s.logger.ErrorContext(ctx, "get checkout failed", "checkout_id", id, "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}

	resp := &orderv1.GetCheckoutResponse{
		CheckoutId:    view.CheckoutID,
		BuyerId:       view.BuyerID,
		PaymentMethod: view.PaymentMethod,
		Orders:        make([]*orderv1.CheckoutOrder, 0, len(view.Orders)),
	}
	if view.PaymentDeadline != nil {
		resp.PaymentDeadline = timestamppb.New(*view.PaymentDeadline)
	}
	for _, o := range view.Orders {
		resp.Orders = append(resp.Orders, &orderv1.CheckoutOrder{
			OrderId:          o.ID,
			SellerUserId:     o.SellerUserID,
			Status:           string(o.Status),
			Subtotal:         o.Subtotal,
			Total:            o.Total,
			CommissionAmount: o.CommissionAmount,
		})
	}
	return resp, nil
}
