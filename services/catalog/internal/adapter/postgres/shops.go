package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// CreateShop inserts shop and runs beforeCommit in the same transaction; an
// error from beforeCommit rolls the insert back and is returned unchanged.
func (s *Store) CreateShop(ctx context.Context, shop domain.Shop, beforeCommit func(context.Context) error) (domain.Shop, error) {
	var out domain.Shop
	err := pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		row, err := s.q.WithTx(tx).InsertShop(ctx, sqlcdb.InsertShopParams{
			ID: shop.ID, OwnerUserID: shop.OwnerUserID, Name: shop.Name, Slug: shop.Slug, Description: shop.Description,
		})
		switch constraintOf(err) {
		case constraintShopOwner:
			return &domain.ConflictError{Message: "you already have a shop"}
		case constraintShopSlug:
			return &domain.ConflictError{Message: "shop slug is already taken"}
		}
		if err != nil {
			return err
		}
		out = toShop(row)
		return beforeCommit(ctx)
	})
	if err != nil {
		return domain.Shop{}, err
	}
	return out, nil
}

// ShopByOwner returns the shop owned by owner.
func (s *Store) ShopByOwner(ctx context.Context, owner uuid.UUID) (domain.Shop, error) {
	row, err := s.q.GetShopByOwner(ctx, owner)
	if err != nil {
		return domain.Shop{}, notFound(err)
	}
	return toShop(row), nil
}

// ShopBySlug returns the shop with slug.
func (s *Store) ShopBySlug(ctx context.Context, slug string) (domain.Shop, error) {
	row, err := s.q.GetShopBySlug(ctx, slug)
	if err != nil {
		return domain.Shop{}, notFound(err)
	}
	return toShop(row), nil
}

// UpdateShop applies p to the shop owned by owner.
func (s *Store) UpdateShop(ctx context.Context, owner uuid.UUID, p domain.ShopPatch) (domain.Shop, error) {
	row, err := s.q.UpdateShop(ctx, sqlcdb.UpdateShopParams{Name: p.Name, Description: p.Description, OwnerUserID: owner})
	if err != nil {
		return domain.Shop{}, notFound(err)
	}
	return toShop(row), nil
}

func toShop(r sqlcdb.Shop) domain.Shop {
	return domain.Shop{
		ID: r.ID, OwnerUserID: r.OwnerUserID, Name: r.Name, Slug: r.Slug, Description: r.Description,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
