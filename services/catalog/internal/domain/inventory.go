package domain

import (
	"bytes"
	"fmt"
	"math"
	"slices"

	"github.com/google/uuid"
)

const (
	// MaxStockItems caps the distinct items of one ReserveStock call.
	MaxStockItems = 100
	// MaxSkuLookup caps the ids of one GetSkus call (WS2 §3).
	MaxSkuLookup = 100
)

// StockItem is a quantity of one SKU.
type StockItem struct {
	SkuID    uuid.UUID
	Quantity int32
}

// StockShortage describes one SKU that could not be reserved.
type StockShortage struct {
	SkuID     uuid.UUID
	Requested int32
	Available int32
}

// InsufficientStockError lists every shortage of a failed reservation.
type InsufficientStockError struct {
	Shortages []StockShortage
}

func (e *InsufficientStockError) Error() string {
	return fmt.Sprintf("insufficient stock for %d sku(s)", len(e.Shortages))
}

// NormalizeStockItems merges duplicate SKUs and sorts by SKU id, the lock
// order every stock transaction uses to avoid deadlocks.
func NormalizeStockItems(items []StockItem) ([]StockItem, error) {
	f := fieldErrors{}
	switch {
	case len(items) == 0:
		f.add("items", msgRequired)
		return nil, f.err()
	case len(items) > MaxStockItems:
		f.add("items", fmt.Sprintf("must have at most %d items", MaxStockItems))
		return nil, f.err()
	}
	sums := make(map[uuid.UUID]int64, len(items))
	for i, it := range items {
		if it.Quantity <= 0 {
			f.add(fmt.Sprintf("items[%d].quantity", i), "must be at least 1")
			continue
		}
		sums[it.SkuID] += int64(it.Quantity)
	}
	out := make([]StockItem, 0, len(sums))
	for id, q := range sums {
		if q > math.MaxInt32 {
			f.add("items", "total quantity of sku "+id.String()+" is too large")
			continue
		}
		out = append(out, StockItem{SkuID: id, Quantity: int32(q)})
	}
	if err := f.err(); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b StockItem) int { return bytes.Compare(a.SkuID[:], b.SkuID[:]) })
	return out, nil
}

// SkuInfo is what order-svc needs to price and display a SKU (GetSkus).
type SkuInfo struct {
	SkuID        uuid.UUID
	ProductID    uuid.UUID
	ProductName  string
	VariantName  string
	ImageKey     string
	Price        int64
	Stock        int32
	Available    bool // sku.active && product.status == active
	ShopID       uuid.UUID
	ShopName     string
	ShopSlug     string
	SellerUserID uuid.UUID
}
