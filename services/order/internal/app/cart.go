package app

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// CartService manages the buyer's cart (WS3 §3).
type CartService struct {
	carts   CartStore
	catalog Catalog
	logger  *slog.Logger
}

// NewCartService uses logger for best-effort failures (nil = slog.Default()).
func NewCartService(carts CartStore, catalog Catalog, logger *slog.Logger) *CartService {
	if logger == nil {
		logger = slog.Default()
	}
	return &CartService{carts: carts, catalog: catalog, logger: logger}
}

// Get returns the cart enriched with current catalog data. Lines whose SKU
// catalog no longer knows are left out and pruned from the store (best effort).
func (s *CartService) Get(ctx context.Context, userID string) (domain.Cart, error) {
	lines, err := s.carts.Lines(ctx, userID)
	if err != nil {
		return domain.Cart{}, err
	}
	if len(lines) == 0 {
		return domain.Cart{}, nil
	}
	skus, err := s.catalog.GetSkus(ctx, slices.Sorted(maps.Keys(lines)))
	if err != nil {
		return domain.Cart{}, err
	}
	cart, missing := domain.BuildCart(lines, skus)
	if len(missing) > 0 {
		if err := s.carts.Remove(ctx, userID, missing...); err != nil {
			s.logger.WarnContext(ctx, "prune unknown cart skus failed", "user_id", userID, "error", err)
		}
	}
	return cart, nil
}

// SetItem sets the quantity of a line, creating it if needed. The SKU must
// exist, be available and not belong to the caller's own shop; a quantity
// above current stock is allowed (checkout enforces stock).
func (s *CartService) SetItem(ctx context.Context, userID, skuID string, quantity int32) (domain.Cart, error) {
	if quantity < domain.MinQuantity || quantity > domain.MaxQuantity {
		return domain.Cart{}, ErrInvalidQuantity
	}
	skus, err := s.catalog.GetSkus(ctx, []string{skuID})
	if err != nil {
		return domain.Cart{}, err
	}
	sku, ok := skus[skuID]
	switch {
	case !ok:
		return domain.Cart{}, ErrSkuNotFound
	case sku.SellerUserID == userID:
		return domain.Cart{}, ErrOwnShop
	case !sku.Available:
		return domain.Cart{}, ErrSkuUnavailable
	}
	if err := s.carts.Set(ctx, userID, skuID, quantity); err != nil {
		return domain.Cart{}, err
	}
	return s.Get(ctx, userID)
}

// RemoveItem deletes a line (a no-op if absent) and returns the cart.
func (s *CartService) RemoveItem(ctx context.Context, userID, skuID string) (domain.Cart, error) {
	if err := s.carts.Remove(ctx, userID, skuID); err != nil {
		return domain.Cart{}, err
	}
	return s.Get(ctx, userID)
}
