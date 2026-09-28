// Package catalog adapts catalog-svc's CatalogInternal gRPC API to
// app.Catalog: a timeout per attempt, retries on UNAVAILABLE and
// DEADLINE_EXCEEDED (every RPC is idempotent), and gRPC → app error mapping.
package catalog

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// Config tunes calls to catalog.
type Config struct {
	Timeout time.Duration // per attempt
	Retries int           // extra attempts after the first
	Backoff time.Duration // wait before retry n is Backoff × n
}

// DefaultConfig is WS3 §4 step 6: 3 s per attempt, up to 2 retries.
func DefaultConfig() Config {
	return Config{Timeout: 3 * time.Second, Retries: 2, Backoff: 100 * time.Millisecond}
}

// Client implements app.Catalog.
type Client struct {
	rpc catalogv1.CatalogInternalClient
	cfg Config
}

var _ app.Catalog = (*Client)(nil)

// New wraps a connection to catalog-svc.
func New(cc grpc.ClientConnInterface, cfg Config) *Client {
	return &Client{rpc: catalogv1.NewCatalogInternalClient(cc), cfg: cfg}
}

func (c *Client) GetSkus(ctx context.Context, ids []string) (map[string]domain.Sku, error) {
	out := make(map[string]domain.Sku, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var resp *catalogv1.GetSkusResponse
	err := c.call(ctx, func(ctx context.Context) (err error) {
		resp, err = c.rpc.GetSkus(ctx, &catalogv1.GetSkusRequest{SkuIds: ids})
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, s := range resp.GetSkus() {
		out[s.GetSkuId()] = domain.Sku{
			ID: s.GetSkuId(), ProductID: s.GetProductId(), ProductName: s.GetProductName(),
			VariantName: s.GetVariantName(), ImageURL: s.GetImageUrl(), Price: s.GetPrice(),
			Stock: s.GetStock(), Available: s.GetAvailable(), ShopID: s.GetShopId(),
			ShopName: s.GetShopName(), ShopSlug: s.GetShopSlug(), SellerUserID: s.GetSellerUserId(),
		}
	}
	return out, nil
}

func (c *Client) ReserveStock(ctx context.Context, orderID string, lines []domain.Line) error {
	items := make([]*catalogv1.StockItem, len(lines))
	for i, l := range lines {
		items[i] = &catalogv1.StockItem{SkuId: l.SkuID, Quantity: l.Quantity}
	}
	return c.call(ctx, func(ctx context.Context) error {
		_, err := c.rpc.ReserveStock(ctx, &catalogv1.ReserveStockRequest{OrderId: orderID, Items: items})
		return err
	})
}

func (c *Client) ReleaseStock(ctx context.Context, orderID string) error {
	return c.call(ctx, func(ctx context.Context) error {
		_, err := c.rpc.ReleaseStock(ctx, &catalogv1.ReleaseStockRequest{OrderId: orderID})
		return err
	})
}

// call runs fn with a per-attempt timeout, retrying transient failures.
func (c *Client) call(ctx context.Context, fn func(context.Context) error) error {
	var err error
	for attempt := 0; ; attempt++ {
		actx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
		err = fn(actx)
		cancel()
		if err == nil {
			return nil
		}
		if attempt >= c.cfg.Retries || !retryable(err) || ctx.Err() != nil {
			return mapError(err)
		}
		timer := time.NewTimer(c.cfg.Backoff * time.Duration(attempt+1))
		select {
		case <-ctx.Done():
			timer.Stop()
			return mapError(err)
		case <-timer.C:
		}
	}
}

func retryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

func mapError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%w: %w", app.ErrCatalogUnavailable, err)
	}
	switch st.Code() {
	case codes.FailedPrecondition:
		oos := &app.OutOfStockError{}
		for _, d := range st.Details() {
			if is, ok := d.(*catalogv1.InsufficientStock); ok {
				for _, s := range is.GetShortages() {
					oos.Shortages = append(oos.Shortages, domain.Shortage{SkuID: s.GetSkuId(), Requested: s.GetRequested(), Available: s.GetAvailable()})
				}
			}
		}
		return oos
	case codes.NotFound, codes.InvalidArgument:
		return fmt.Errorf("%w: %s", app.ErrCatalogRejected, st.Message())
	default:
		return fmt.Errorf("%w: %w", app.ErrCatalogUnavailable, err)
	}
}
