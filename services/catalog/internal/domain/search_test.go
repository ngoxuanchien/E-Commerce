package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

func TestSearchParamsDefaults(t *testing.T) {
	p, err := domain.SearchParams{}.Normalize()
	require.NoError(t, err)
	assert.Equal(t, domain.SortNewest, p.Sort)
	assert.Equal(t, domain.DefaultPageLimit, p.Limit)

	p, err = domain.SearchParams{Query: "  áo thun "}.Normalize()
	require.NoError(t, err)
	assert.Equal(t, domain.SortRelevance, p.Sort)
	assert.Equal(t, "áo thun", p.Query)

	p, err = domain.SearchParams{Sort: domain.SortRelevance}.Normalize()
	require.NoError(t, err)
	assert.Equal(t, domain.SortNewest, p.Sort, "relevance without q falls back to newest")
}

func TestSearchParamsRules(t *testing.T) {
	cases := map[string]struct {
		p     domain.SearchParams
		field string
	}{
		"unknown sort":   {domain.SearchParams{Sort: "cheapest"}, "sort"},
		"min > max":      {domain.SearchParams{MinPrice: ptr(int64(5)), MaxPrice: ptr(int64(4))}, "min_price"},
		"negative min":   {domain.SearchParams{MinPrice: ptr(int64(-1))}, "min_price"},
		"negative max":   {domain.SearchParams{MaxPrice: ptr(int64(-1))}, "max_price"},
		"limit too big":  {domain.SearchParams{Limit: 101}, "limit"},
		"limit negative": {domain.SearchParams{Limit: -1}, "limit"},
		"q too long":     {domain.SearchParams{Query: strings.Repeat("á", 201)}, "q"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tc.p.Normalize()
			assert.Contains(t, fields(t, err), tc.field)
		})
	}
}
