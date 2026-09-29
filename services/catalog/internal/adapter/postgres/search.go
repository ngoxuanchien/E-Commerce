package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// sortSpec describes how one SearchSort maps to SQL.
type sortSpec struct {
	expr string // sort key expression; "%[1]s" is replaced by the tsquery placeholder
	cast string // SQL type of the cursor value
	desc bool
	zero func() any // new pointer for decoding the cursor value
}

var sortSpecs = map[domain.SearchSort]sortSpec{
	domain.SortNewest:    {expr: "p.created_at", cast: "timestamptz", desc: true, zero: func() any { return new(time.Time) }},
	domain.SortPriceAsc:  {expr: "p.min_price", cast: "bigint", zero: func() any { return new(int64) }},
	domain.SortPriceDesc: {expr: "p.min_price", cast: "bigint", desc: true, zero: func() any { return new(int64) }},
	// ts_rank returns real; the cursor stores it as float32 so comparisons are exact.
	domain.SortRelevance: {expr: "ts_rank(p.search_vector, %[1]s)", cast: "real", desc: true, zero: func() any { return new(float32) }},
}

// SearchProducts runs a public search. p must be normalized (Sort and Limit set).
func (s *Store) SearchProducts(ctx context.Context, p domain.SearchParams) (domain.Page[domain.ProductSummary], error) {
	spec, ok := sortSpecs[p.Sort]
	if !ok {
		return domain.Page[domain.ProductSummary]{}, fmt.Errorf("search: unsupported sort %q", p.Sort)
	}
	after, err := domain.DecodeCursor(p.Cursor, string(p.Sort))
	if err != nil {
		return domain.Page[domain.ProductSummary]{}, err
	}

	var args []any
	bind := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	where := []string{"p.status = 'active'", "p.min_price IS NOT NULL"}
	tsquery := ""
	if p.Query != "" {
		tsquery = "websearch_to_tsquery('simple', vn_unaccent(" + bind(p.Query) + "))"
		where = append(where, "p.search_vector @@ "+tsquery)
	}
	sortExpr := spec.expr
	if strings.Contains(sortExpr, "%[1]s") {
		sortExpr = fmt.Sprintf(sortExpr, tsquery)
	}
	if p.CategorySlug != "" {
		c := bind(p.CategorySlug)
		where = append(where, "p.category_id IN (SELECT c.id FROM categories c WHERE c.slug = "+c+
			" OR c.parent_id IN (SELECT id FROM categories WHERE slug = "+c+"))")
	}
	if p.ShopSlug != "" {
		where = append(where, "s.slug = "+bind(p.ShopSlug))
	}
	if p.MinPrice != nil {
		where = append(where, "p.min_price >= "+bind(*p.MinPrice))
	}
	if p.MaxPrice != nil {
		where = append(where, "p.min_price <= "+bind(*p.MaxPrice))
	}
	dir, cmp := "ASC", ">"
	if spec.desc {
		dir, cmp = "DESC", "<"
	}
	if after != nil {
		v := spec.zero()
		if err := after.DecodeValue(v); err != nil {
			return domain.Page[domain.ProductSummary]{}, err
		}
		where = append(where, fmt.Sprintf("(%s, p.id) %s (%s::%s, %s::uuid)", sortExpr, cmp, bind(v), spec.cast, bind(after.ID)))
	}

	sql := `SELECT p.id, p.name, p.min_price, s.id, s.name, s.slug,
       coalesce((SELECT i.object_key FROM product_images i WHERE i.product_id = p.id ORDER BY i.position LIMIT 1), ''),
       ` + sortExpr + `
FROM products p JOIN shops s ON s.id = p.shop_id
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY ` + sortExpr + ` ` + dir + `, p.id ` + dir + `
LIMIT ` + bind(p.Limit+1)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return domain.Page[domain.ProductSummary]{}, fmt.Errorf("search products: %w", err)
	}
	defer rows.Close()
	var (
		items []domain.ProductSummary
		keys  []any
	)
	for rows.Next() {
		var (
			it      domain.ProductSummary
			sortKey any
		)
		if err := rows.Scan(&it.ID, &it.Name, &it.MinPrice, &it.Shop.ID, &it.Shop.Name, &it.Shop.Slug, &it.ImageKey, &sortKey); err != nil {
			return domain.Page[domain.ProductSummary]{}, fmt.Errorf("scan search row: %w", err)
		}
		items = append(items, it)
		keys = append(keys, sortKey)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.ProductSummary]{}, fmt.Errorf("search products: %w", err)
	}

	page := domain.Page[domain.ProductSummary]{Items: items}
	if page.Items == nil {
		page.Items = []domain.ProductSummary{}
	}
	if len(items) > p.Limit {
		page.Items = items[:p.Limit]
		c, err := domain.EncodeCursor(string(p.Sort), keys[p.Limit-1], items[p.Limit-1].ID)
		if err != nil {
			return domain.Page[domain.ProductSummary]{}, err
		}
		page.NextCursor = &c
	}
	return page, nil
}
