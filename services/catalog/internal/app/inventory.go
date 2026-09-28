package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// InventoryService is the single source of truth for stock.
type InventoryService struct {
	store InventoryStore
}

// NewInventoryService returns an InventoryService.
func NewInventoryService(store InventoryStore) *InventoryService { return &InventoryService{store: store} }

// GetSkus returns SKU data for up to domain.MaxSkuLookup ids (duplicates ignored).
func (s *InventoryService) GetSkus(ctx context.Context, ids []uuid.UUID) ([]domain.SkuInfo, error) {
	if len(ids) > domain.MaxSkuLookup {
		return nil, &domain.ValidationError{Fields: map[string]string{
			"sku_ids": fmt.Sprintf("must have at most %d items", domain.MaxSkuLookup),
		}}
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	unique := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return s.store.SkuInfos(ctx, unique)
}

// Reserve reserves items for orderID (idempotent per order, all-or-nothing).
func (s *InventoryService) Reserve(ctx context.Context, orderID uuid.UUID, items []domain.StockItem) error {
	norm, err := domain.NormalizeStockItems(items)
	if err != nil {
		return err
	}
	return s.store.ReserveStock(ctx, orderID, norm)
}

// Release returns orderID's reserved stock (idempotent).
func (s *InventoryService) Release(ctx context.Context, orderID uuid.UUID) error {
	return s.store.ReleaseStock(ctx, orderID)
}

// ReleaseForEvent releases orderID at most once per eventID.
func (s *InventoryService) ReleaseForEvent(ctx context.Context, eventID, orderID uuid.UUID) error {
	return s.store.ReleaseStockForEvent(ctx, eventID, orderID)
}
