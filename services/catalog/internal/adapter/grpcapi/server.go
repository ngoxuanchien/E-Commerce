package grpcapi

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// Inventory is the use-case port behind CatalogInternal.
type Inventory interface {
	GetSkus(ctx context.Context, ids []uuid.UUID) ([]domain.SkuInfo, error)
	Reserve(ctx context.Context, orderID uuid.UUID, items []domain.StockItem) error
	Release(ctx context.Context, orderID uuid.UUID) error
}

// CatalogServer implements catalogv1.CatalogInternalServer.
type CatalogServer struct {
	catalogv1.UnimplementedCatalogInternalServer
	inv    Inventory
	assets domain.AssetBase
}

// NewCatalogServer returns a CatalogServer; assets builds image URLs.
func NewCatalogServer(inv Inventory, assets domain.AssetBase) *CatalogServer {
	return &CatalogServer{inv: inv, assets: assets}
}

// GetSkus returns SkuInfo for known SKUs; unknown or malformed ids are omitted.
func (s *CatalogServer) GetSkus(ctx context.Context, req *catalogv1.GetSkusRequest) (*catalogv1.GetSkusResponse, error) {
	if n := len(req.GetSkuIds()); n > domain.MaxSkuLookup {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d sku_ids per call, got %d", domain.MaxSkuLookup, n)
	}
	ids := make([]uuid.UUID, 0, len(req.GetSkuIds()))
	for _, raw := range req.GetSkuIds() {
		if id, err := uuid.Parse(raw); err == nil {
			ids = append(ids, id)
		}
	}
	infos, err := s.inv.GetSkus(ctx, ids)
	if err != nil {
		return nil, toStatus(ctx, err)
	}
	resp := &catalogv1.GetSkusResponse{Skus: make([]*catalogv1.SkuInfo, 0, len(infos))}
	for _, in := range infos {
		resp.Skus = append(resp.Skus, &catalogv1.SkuInfo{
			SkuId: in.SkuID.String(), ProductId: in.ProductID.String(), ProductName: in.ProductName,
			VariantName: in.VariantName, ImageUrl: s.assets.URL(in.ImageKey), Price: in.Price, Stock: in.Stock,
			Available: in.Available, ShopId: in.ShopID.String(), ShopName: in.ShopName,
			SellerUserId: in.SellerUserID.String(), ShopSlug: in.ShopSlug,
		})
	}
	return resp, nil
}

// ReserveStock reserves stock for an order (idempotent per order_id).
func (s *CatalogServer) ReserveStock(ctx context.Context, req *catalogv1.ReserveStockRequest) (*catalogv1.ReserveStockResponse, error) {
	orderID, err := uuid.Parse(req.GetOrderId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "order_id must be a UUID")
	}
	items := make([]domain.StockItem, len(req.GetItems()))
	for i, it := range req.GetItems() {
		sku, err := uuid.Parse(it.GetSkuId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "items[%d].sku_id must be a UUID", i)
		}
		items[i] = domain.StockItem{SkuID: sku, Quantity: it.GetQuantity()}
	}
	if err := s.inv.Reserve(ctx, orderID, items); err != nil {
		return nil, toStatus(ctx, err)
	}
	return &catalogv1.ReserveStockResponse{}, nil
}

// ReleaseStock releases an order's reservation (idempotent; OK when nothing is reserved).
func (s *CatalogServer) ReleaseStock(ctx context.Context, req *catalogv1.ReleaseStockRequest) (*catalogv1.ReleaseStockResponse, error) {
	orderID, err := uuid.Parse(req.GetOrderId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "order_id must be a UUID")
	}
	if err := s.inv.Release(ctx, orderID); err != nil {
		return nil, toStatus(ctx, err)
	}
	return &catalogv1.ReleaseStockResponse{}, nil
}

// toStatus maps domain errors to gRPC statuses (WS0 §5).
func toStatus(ctx context.Context, err error) error {
	var (
		ise *domain.InsufficientStockError
		ve  *domain.ValidationError
	)
	switch {
	case errors.As(err, &ise):
		detail := &catalogv1.InsufficientStock{}
		for _, sh := range ise.Shortages {
			detail.Shortages = append(detail.Shortages, &catalogv1.StockShortage{
				SkuId: sh.SkuID.String(), Requested: sh.Requested, Available: sh.Available,
			})
		}
		st := status.New(codes.FailedPrecondition, "insufficient stock")
		if withDetail, derr := st.WithDetails(detail); derr == nil {
			st = withDetail
		}
		return st.Err()
	case errors.As(err, &ve):
		return status.Error(codes.InvalidArgument, ve.Error())
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, "not found")
	case errors.Is(err, domain.ErrRetryable):
		return status.Error(codes.Unavailable, "concurrent update, retry")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		slog.ErrorContext(ctx, "catalog rpc failed", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}
