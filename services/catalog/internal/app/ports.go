// Package app implements catalog use cases on top of the domain model and
// the ports below (implemented by adapters).
package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// ShopStore persists shops.
type ShopStore interface {
	CreateShop(ctx context.Context, shop domain.Shop, beforeCommit func(context.Context) error) (domain.Shop, error)
	ShopByOwner(ctx context.Context, owner uuid.UUID) (domain.Shop, error)
	ShopBySlug(ctx context.Context, slug string) (domain.Shop, error)
	UpdateShop(ctx context.Context, owner uuid.UUID, p domain.ShopPatch) (domain.Shop, error)
}

// RoleGranter grants roles in identity-svc.
type RoleGranter interface {
	GrantSeller(ctx context.Context, userID uuid.UUID) error
}

// ProductStore persists products.
type ProductStore interface {
	ShopByOwner(ctx context.Context, owner uuid.UUID) (domain.Shop, error)
	CreateProduct(ctx context.Context, shopID uuid.UUID, c domain.ProductChange) (uuid.UUID, error)
	UpdateProduct(ctx context.Context, shopID, productID uuid.UUID, c domain.ProductChange) error
	ArchiveProduct(ctx context.Context, shopID, productID uuid.UUID) error
	Products(ctx context.Context, ids []uuid.UUID) ([]domain.Product, error)
	ListShopProducts(ctx context.Context, shopID uuid.UUID, status *domain.ProductStatus, cursor string, limit int) (domain.Page[domain.Product], error)
}

// Presigner issues presigned image uploads.
type Presigner interface {
	PresignImageUpload(ctx context.Context, shopID uuid.UUID, contentType string) (domain.Upload, error)
}

// SearchStore serves the public catalog.
type SearchStore interface {
	Categories(ctx context.Context) ([]domain.Category, error)
	SearchProducts(ctx context.Context, p domain.SearchParams) (domain.Page[domain.ProductSummary], error)
}

// InventoryStore reserves and releases stock.
type InventoryStore interface {
	SkuInfos(ctx context.Context, ids []uuid.UUID) ([]domain.SkuInfo, error)
	ReserveStock(ctx context.Context, orderID uuid.UUID, items []domain.StockItem) error
	ReleaseStock(ctx context.Context, orderID uuid.UUID) error
	ReleaseStockForEvent(ctx context.Context, eventID, orderID uuid.UUID) error
}
