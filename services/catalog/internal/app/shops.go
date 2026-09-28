package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// GrantRoleTimeout bounds the GrantRole call made while opening a shop (WS2 §3).
const GrantRoleTimeout = 3 * time.Second

// ShopService opens and manages shops.
type ShopService struct {
	store        ShopStore
	roles        RoleGranter
	grantTimeout time.Duration
}

// NewShopService returns a ShopService using GrantRoleTimeout.
func NewShopService(store ShopStore, roles RoleGranter) *ShopService {
	return &ShopService{store: store, roles: roles, grantTimeout: GrantRoleTimeout}
}

// WithGrantTimeout returns a copy using d as the GrantRole timeout.
func (s *ShopService) WithGrantTimeout(d time.Duration) *ShopService {
	c := *s
	c.grantTimeout = d
	return &c
}

// Open creates owner's shop and grants them the seller role in one
// transaction: if GrantRole fails or times out, no shop is created.
func (s *ShopService) Open(ctx context.Context, owner uuid.UUID, in domain.ShopInput) (domain.Shop, error) {
	in, err := in.Validate()
	if err != nil {
		return domain.Shop{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.Shop{}, err
	}
	shop := domain.Shop{ID: id, OwnerUserID: owner, Name: in.Name, Slug: in.Slug, Description: in.Description}
	return s.store.CreateShop(ctx, shop, func(ctx context.Context) error {
		gctx, cancel := context.WithTimeout(ctx, s.grantTimeout)
		defer cancel()
		if err := s.roles.GrantSeller(gctx, owner); err != nil {
			return fmt.Errorf("%w: grant seller role: %w", domain.ErrUpstreamUnavailable, err)
		}
		return nil
	})
}

// Mine returns owner's shop (domain.ErrNotFound when they have none).
func (s *ShopService) Mine(ctx context.Context, owner uuid.UUID) (domain.Shop, error) {
	return s.store.ShopByOwner(ctx, owner)
}

// BySlug returns the shop with slug.
func (s *ShopService) BySlug(ctx context.Context, slug string) (domain.Shop, error) {
	return s.store.ShopBySlug(ctx, slug)
}

// Update changes the name/description of owner's shop.
func (s *ShopService) Update(ctx context.Context, owner uuid.UUID, p domain.ShopPatch) (domain.Shop, error) {
	p, err := p.Validate()
	if err != nil {
		return domain.Shop{}, err
	}
	return s.store.UpdateShop(ctx, owner, p)
}
