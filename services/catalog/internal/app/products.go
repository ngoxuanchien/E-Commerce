package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// ProductService manages a seller's products and serves product detail.
type ProductService struct {
	store     ProductStore
	presigner Presigner
}

// NewProductService returns a ProductService.
func NewProductService(store ProductStore, presigner Presigner) *ProductService {
	return &ProductService{store: store, presigner: presigner}
}

// Get returns product id as seen by viewer (uuid.Nil = anonymous). Non-active
// products are visible only to their shop owner, who also sees inactive SKUs.
func (s *ProductService) Get(ctx context.Context, id, viewer uuid.UUID) (domain.Product, error) {
	p, err := s.one(ctx, id)
	if err != nil {
		return domain.Product{}, err
	}
	if viewer != uuid.Nil && viewer == p.OwnerUserID {
		return p, nil
	}
	if p.Status != domain.StatusActive {
		return domain.Product{}, domain.ErrNotFound
	}
	return p.PublicView(), nil
}

// Create adds a product to owner's shop.
func (s *ProductService) Create(ctx context.Context, owner uuid.UUID, in domain.ProductInput) (domain.Product, error) {
	shop, err := s.shopOf(ctx, owner)
	if err != nil {
		return domain.Product{}, err
	}
	change, err := in.Validate(shop.ID, true)
	if err != nil {
		return domain.Product{}, err
	}
	id, err := s.store.CreateProduct(ctx, shop.ID, change)
	if err != nil {
		return domain.Product{}, err
	}
	return s.one(ctx, id)
}

// Update applies a partial update to one of owner's products.
func (s *ProductService) Update(ctx context.Context, owner, id uuid.UUID, in domain.ProductInput) (domain.Product, error) {
	shop, err := s.shopOf(ctx, owner)
	if err != nil {
		return domain.Product{}, err
	}
	change, err := in.Validate(shop.ID, false)
	if err != nil {
		return domain.Product{}, err
	}
	if err := s.store.UpdateProduct(ctx, shop.ID, id, change); err != nil {
		return domain.Product{}, err
	}
	return s.one(ctx, id)
}

// Archive archives one of owner's products.
func (s *ProductService) Archive(ctx context.Context, owner, id uuid.UUID) error {
	shop, err := s.shopOf(ctx, owner)
	if err != nil {
		return err
	}
	return s.store.ArchiveProduct(ctx, shop.ID, id)
}

// ListMine pages through owner's products (all statuses unless filtered).
func (s *ProductService) ListMine(ctx context.Context, owner uuid.UUID, status *domain.ProductStatus, cursor string, limit int) (domain.Page[domain.Product], error) {
	shop, err := s.shopOf(ctx, owner)
	if err != nil {
		return domain.Page[domain.Product]{}, err
	}
	if limit, err = domain.NormalizeLimit(limit); err != nil {
		return domain.Page[domain.Product]{}, err
	}
	return s.store.ListShopProducts(ctx, shop.ID, status, cursor, limit)
}

// PresignUpload issues a presigned POST for one image of owner's shop.
func (s *ProductService) PresignUpload(ctx context.Context, owner uuid.UUID, contentType string) (domain.Upload, error) {
	shop, err := s.shopOf(ctx, owner)
	if err != nil {
		return domain.Upload{}, err
	}
	if _, ok := domain.ImageExtension(contentType); !ok {
		return domain.Upload{}, &domain.ValidationError{Fields: map[string]string{
			"content_type": "must be one of: image/jpeg image/png image/webp",
		}}
	}
	return s.presigner.PresignImageUpload(ctx, shop.ID, contentType)
}

func (s *ProductService) shopOf(ctx context.Context, owner uuid.UUID) (domain.Shop, error) {
	shop, err := s.store.ShopByOwner(ctx, owner)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Shop{}, domain.ErrNoShop
	}
	return shop, err
}

func (s *ProductService) one(ctx context.Context, id uuid.UUID) (domain.Product, error) {
	ps, err := s.store.Products(ctx, []uuid.UUID{id})
	if err != nil {
		return domain.Product{}, err
	}
	if len(ps) == 0 {
		return domain.Product{}, domain.ErrNotFound
	}
	return ps[0], nil
}
