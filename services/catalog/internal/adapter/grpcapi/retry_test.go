package grpcapi_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi/grpctest"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

type failingInventory struct{ err error }

func (f failingInventory) GetSkus(context.Context, []uuid.UUID) ([]domain.SkuInfo, error) {
	return nil, f.err
}
func (f failingInventory) Reserve(context.Context, uuid.UUID, []domain.StockItem) error { return f.err }
func (f failingInventory) Release(context.Context, uuid.UUID) error                     { return f.err }

// Deadlocks / serialization failures are transient: callers (order-svc retries
// UNAVAILABLE) must be able to retry instead of failing the checkout.
func TestTransientStoreErrorsAreUnavailable(t *testing.T) {
	inv := failingInventory{err: fmt.Errorf("reserve: %w", domain.ErrRetryable)}
	conn := grpctest.Serve(t, func(s *grpc.Server) {
		catalogv1.RegisterCatalogInternalServer(s, grpcapi.NewCatalogServer(inv, assets))
	})
	c := catalogv1.NewCatalogInternalClient(conn)

	_, err := c.ReserveStock(context.Background(), &catalogv1.ReserveStockRequest{
		OrderId: uuid.NewString(), Items: []*catalogv1.StockItem{{SkuId: uuid.NewString(), Quantity: 1}},
	})
	assert.Equal(t, codes.Unavailable, status.Code(err))
	_, err = c.ReleaseStock(context.Background(), &catalogv1.ReleaseStockRequest{OrderId: uuid.NewString()})
	assert.Equal(t, codes.Unavailable, status.Code(err))
}
