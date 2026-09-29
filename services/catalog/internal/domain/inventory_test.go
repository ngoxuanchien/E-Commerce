package domain_test

import (
	"bytes"
	"math"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

func TestNormalizeStockItemsMergesAndSorts(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	out, err := domain.NormalizeStockItems([]domain.StockItem{{SkuID: b, Quantity: 1}, {SkuID: a, Quantity: 2}, {SkuID: b, Quantity: 3}})
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.True(t, slices.IsSortedFunc(out, func(x, y domain.StockItem) int { return bytes.Compare(x.SkuID[:], y.SkuID[:]) }))
	got := map[uuid.UUID]int32{}
	for _, it := range out {
		got[it.SkuID] = it.Quantity
	}
	assert.Equal(t, map[uuid.UUID]int32{a: 2, b: 4}, got)
}

func TestNormalizeStockItemsRejectsBadInput(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	tooMany := make([]domain.StockItem, domain.MaxStockItems+1)
	for i := range tooMany {
		tooMany[i] = domain.StockItem{SkuID: uuid.Must(uuid.NewV7()), Quantity: 1}
	}
	cases := map[string]struct {
		items []domain.StockItem
		field string
	}{
		"empty":         {nil, "items"},
		"zero quantity": {[]domain.StockItem{{SkuID: id, Quantity: 0}}, "items[0].quantity"},
		"negative":      {[]domain.StockItem{{SkuID: id, Quantity: -1}}, "items[0].quantity"},
		"too many":      {tooMany, "items"},
		"overflow":      {[]domain.StockItem{{SkuID: id, Quantity: math.MaxInt32}, {SkuID: id, Quantity: 1}}, "items"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NormalizeStockItems(tc.items)
			assert.Contains(t, fields(t, err), tc.field)
		})
	}
}
