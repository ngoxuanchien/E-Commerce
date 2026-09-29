package domain

import (
	"strings"
	"unicode/utf8"
)

// SearchSort orders search results.
type SearchSort string

const (
	SortRelevance SearchSort = "relevance"
	SortNewest    SearchSort = "newest"
	SortPriceAsc  SearchSort = "price_asc"
	SortPriceDesc SearchSort = "price_desc"
)

// Pagination limits (WS0 §3).
const (
	DefaultPageLimit = 20
	MaxPageLimit     = 100
	maxQueryChars    = 200
)

// Page is one keyset page; NextCursor is nil on the last page.
type Page[T any] struct {
	Items      []T
	NextCursor *string
}

// NormalizeLimit maps 0 to the default and rejects values outside 1..MaxPageLimit.
func NormalizeLimit(n int) (int, error) {
	switch {
	case n == 0:
		return DefaultPageLimit, nil
	case n < 1 || n > MaxPageLimit:
		return 0, &ValidationError{Fields: map[string]string{"limit": "must be between 1 and 100"}}
	}
	return n, nil
}

// SearchParams is a public product search. Cursor is the raw opaque cursor.
type SearchParams struct {
	Query        string
	CategorySlug string
	ShopSlug     string
	MinPrice     *int64
	MaxPrice     *int64
	Sort         SearchSort
	Limit        int
	Cursor       string
}

// Normalize trims q, applies the default sort/limit and validates the params.
func (p SearchParams) Normalize() (SearchParams, error) {
	f := fieldErrors{}
	p.Query = strings.TrimSpace(p.Query)
	for field, v := range map[string]string{"q": p.Query, "category": p.CategorySlug, "shop": p.ShopSlug} {
		if !ValidText(v) {
			f.add(field, msgText)
		}
	}
	if utf8.RuneCountInString(p.Query) > maxQueryChars {
		f.add("q", "must be at most 200 characters")
	}
	switch p.Sort {
	case "":
		p.Sort = SortNewest
		if p.Query != "" {
			p.Sort = SortRelevance
		}
	case SortRelevance:
		if p.Query == "" {
			p.Sort = SortNewest
		}
	case SortNewest, SortPriceAsc, SortPriceDesc:
	default:
		f.add("sort", "must be one of: relevance newest price_asc price_desc")
	}
	if p.MinPrice != nil && *p.MinPrice < 0 {
		f.add("min_price", "must be at least 0")
	}
	if p.MaxPrice != nil && *p.MaxPrice < 0 {
		f.add("max_price", "must be at least 0")
	}
	if p.MinPrice != nil && p.MaxPrice != nil && *p.MinPrice > *p.MaxPrice {
		f.add("min_price", "must not exceed max_price")
	}
	limit, err := NormalizeLimit(p.Limit)
	if err != nil {
		f.add("limit", "must be between 1 and 100")
	}
	p.Limit = limit
	return p, f.err()
}
