package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func TestCommission(t *testing.T) {
	for _, tc := range []struct {
		subtotal int64
		bps      int32
		want     int64
	}{
		{99_999, 500, 4_999}, {100_000, 500, 5_000}, {240_000, 500, 12_000}, {45_000, 500, 2_250},
		{1, 500, 0}, {0, 500, 0}, {123_456, 0, 0}, {123_456, 10_000, 123_456},
	} {
		assert.Equal(t, tc.want, domain.Commission(tc.subtotal, tc.bps), "%d@%d", tc.subtotal, tc.bps)
	}
}
