package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// sellerListSort names the cursor sort of ListShopProducts.
const sellerListSort = "newest"

// CreateProduct inserts a product of shopID from a validated create change.
func (s *Store) CreateProduct(ctx context.Context, shopID uuid.UUID, c domain.ProductChange) (uuid.UUID, error) {
	id := newID()
	err := pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		err := q.InsertProduct(ctx, sqlcdb.InsertProductParams{
			ID: id, ShopID: shopID, CategoryID: *c.CategoryID, Name: *c.Name,
			Description: deref(c.Description), Status: string(*c.Status),
		})
		if err != nil {
			return categoryError(err)
		}
		return applyChildren(ctx, q, id, c)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// UpdateProduct applies c to productID if it belongs to shopID.
func (s *Store) UpdateProduct(ctx context.Context, shopID, productID uuid.UUID, c domain.ProductChange) error {
	return pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockShopProduct(ctx, sqlcdb.LockShopProductParams{ID: productID, ShopID: shopID}); err != nil {
			return notFound(err)
		}
		err := q.UpdateProductFields(ctx, sqlcdb.UpdateProductFieldsParams{
			CategoryID: c.CategoryID, Name: c.Name, Description: c.Description,
			Status: (*string)(c.Status), ID: productID,
		})
		if err != nil {
			return categoryError(err)
		}
		return applyChildren(ctx, q, productID, c)
	})
}

// ArchiveProduct sets status=archived on productID if it belongs to shopID.
func (s *Store) ArchiveProduct(ctx context.Context, shopID, productID uuid.UUID) error {
	n, err := s.q.ArchiveProduct(ctx, sqlcdb.ArchiveProductParams{ID: productID, ShopID: shopID})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// applyChildren replaces images and applies SKU changes when set, then
// recomputes min_price, all inside the caller's transaction.
func applyChildren(ctx context.Context, q *sqlcdb.Queries, productID uuid.UUID, c domain.ProductChange) error {
	if c.SetImages {
		if err := q.DeleteProductImages(ctx, productID); err != nil {
			return err
		}
		for i, key := range c.ImageKeys {
			if err := q.InsertProductImage(ctx, sqlcdb.InsertProductImageParams{
				ID: newID(), ProductID: productID, ObjectKey: key, Position: int32(i),
			}); err != nil {
				return err
			}
		}
	}
	skus, err := q.ListProductSkus(ctx, productID)
	if err != nil {
		return err
	}
	if c.SetSkus {
		if err := applySkus(ctx, q, productID, skus, c.Skus); err != nil {
			return err
		}
		if skus, err = q.ListProductSkus(ctx, productID); err != nil {
			return err
		}
	}
	current := make([]domain.Sku, len(skus))
	for i, r := range skus {
		current[i] = domain.Sku{ID: r.ID, Name: r.Name, Price: r.Price, Stock: r.Stock, Active: r.Active}
	}
	return q.SetMinPrice(ctx, sqlcdb.SetMinPriceParams{ID: productID, MinPrice: domain.MinActivePrice(current)})
}

// applySkus updates SKUs with an id, creates those without, and deactivates
// existing SKUs missing from in (they are never deleted).
func applySkus(ctx context.Context, q *sqlcdb.Queries, productID uuid.UUID, existing []sqlcdb.ListProductSkusRow, in []domain.SkuChange) error {
	owned := make(map[uuid.UUID]bool, len(existing))
	for _, s := range existing {
		owned[s.ID] = true
	}
	keep := make(map[uuid.UUID]bool, len(in))
	for i, sku := range in {
		if sku.ID == nil {
			if err := q.InsertSku(ctx, sqlcdb.InsertSkuParams{
				ID: newID(), ProductID: productID, Name: sku.Name, Price: sku.Price, Stock: sku.Stock, Active: sku.Active,
			}); err != nil {
				return err
			}
			continue
		}
		if !owned[*sku.ID] {
			return &domain.ValidationError{Fields: map[string]string{
				fmt.Sprintf("skus[%d].id", i): "does not belong to this product",
			}}
		}
		keep[*sku.ID] = true
		if err := q.UpdateSku(ctx, sqlcdb.UpdateSkuParams{
			ID: *sku.ID, ProductID: productID, Name: sku.Name, Price: sku.Price, Stock: sku.Stock, Active: sku.Active,
		}); err != nil {
			return err
		}
	}
	for _, s := range existing {
		if !keep[s.ID] && s.Active {
			if err := q.DeactivateSku(ctx, s.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Products loads full products (with SKUs and images) in the order of ids,
// skipping unknown ids.
func (s *Store) Products(ctx context.Context, ids []uuid.UUID) ([]domain.Product, error) {
	if len(ids) == 0 {
		return []domain.Product{}, nil
	}
	rows, err := s.q.GetProducts(ctx, ids)
	if err != nil {
		return nil, err
	}
	skus, err := s.q.GetProductsSkus(ctx, ids)
	if err != nil {
		return nil, err
	}
	images, err := s.q.GetProductsImages(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*domain.Product, len(rows))
	for _, r := range rows {
		byID[r.ID] = &domain.Product{
			ID:          r.ID,
			Shop:        domain.ShopRef{ID: r.ShopID, Name: r.ShopName, Slug: r.ShopSlug},
			OwnerUserID: r.OwnerUserID,
			CategoryID:  r.CategoryID,
			Name:        r.Name,
			Description: r.Description,
			Status:      domain.ProductStatus(r.Status),
			ImageKeys:   []string{},
			Skus:        []domain.Sku{},
			MinPrice:    r.MinPrice,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		}
	}
	for _, r := range skus {
		if p := byID[r.ProductID]; p != nil {
			p.Skus = append(p.Skus, domain.Sku{ID: r.ID, Name: r.Name, Price: r.Price, Stock: r.Stock, Active: r.Active})
		}
	}
	for _, r := range images {
		if p := byID[r.ProductID]; p != nil {
			p.ImageKeys = append(p.ImageKeys, r.ObjectKey)
		}
	}
	out := make([]domain.Product, 0, len(ids))
	for _, id := range ids {
		if p := byID[id]; p != nil {
			out = append(out, *p)
		}
	}
	return out, nil
}

// ListShopProducts pages through shopID's products, newest first, optionally
// filtered by status.
func (s *Store) ListShopProducts(ctx context.Context, shopID uuid.UUID, status *domain.ProductStatus, cursor string, limit int) (domain.Page[domain.Product], error) {
	after, err := domain.DecodeCursor(cursor, sellerListSort)
	if err != nil {
		return domain.Page[domain.Product]{}, err
	}
	params := sqlcdb.ListShopProductIDsParams{ShopID: shopID, Status: (*string)(status), MaxRows: int32(limit + 1)}
	if after != nil {
		var ts time.Time
		if err := after.DecodeValue(&ts); err != nil {
			return domain.Page[domain.Product]{}, err
		}
		params.AfterCreatedAt, params.AfterID = &ts, &after.ID
	}
	rows, err := s.q.ListShopProductIDs(ctx, params)
	if err != nil {
		return domain.Page[domain.Product]{}, err
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		c, err := domain.EncodeCursor(sellerListSort, last.CreatedAt, last.ID)
		if err != nil {
			return domain.Page[domain.Product]{}, err
		}
		next = &c
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	items, err := s.Products(ctx, ids)
	if err != nil {
		return domain.Page[domain.Product]{}, err
	}
	return domain.Page[domain.Product]{Items: items, NextCursor: next}, nil
}

// categoryError maps a category foreign-key violation to a validation error.
func categoryError(err error) error {
	if constraintOf(err) == constraintProductCategory {
		return &domain.ValidationError{Fields: map[string]string{"category_id": "does not exist"}}
	}
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
