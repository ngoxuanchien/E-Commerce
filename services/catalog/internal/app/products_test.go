package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

type stubPresigner struct{ shop uuid.UUID }

func (s *stubPresigner) PresignImageUpload(_ context.Context, shopID uuid.UUID, contentType string) (domain.Upload, error) {
	s.shop = shopID
	return domain.Upload{ObjectKey: domain.ImageKeyPrefix(shopID) + "x." + contentType}, nil
}

func ptr[T any](v T) *T { return &v }

func draftInput() domain.ProductInput {
	return domain.ProductInput{
		CategoryID: ptr(testsupport.CategoryAoThunNam.String()), Name: ptr("Áo thun nháp"), Status: ptr("draft"),
		Skus: []domain.SkuInput{
			{Name: "M", Price: ptr(int64(100_000)), Stock: ptr(int32(5))},
			{Name: "L", Price: ptr(int64(90_000)), Stock: ptr(int32(5)), Active: ptr(false)},
		},
	}
}

func TestProductVisibility(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	shops := app.NewShopService(store, allowAll{})
	products := app.NewProductService(store, &stubPresigner{})
	owner, stranger := testsupport.NewID(t), testsupport.NewID(t)
	_, err := shops.Open(ctx, owner, domain.ShopInput{Name: "Shop Áo", Slug: "shop-ao"})
	require.NoError(t, err)

	p, err := products.Create(ctx, owner, draftInput())
	require.NoError(t, err)

	_, err = products.Get(ctx, p.ID, uuid.Nil)
	assert.ErrorIs(t, err, domain.ErrNotFound, "draft hidden from anonymous")
	_, err = products.Get(ctx, p.ID, stranger)
	assert.ErrorIs(t, err, domain.ErrNotFound, "draft hidden from other users")
	own, err := products.Get(ctx, p.ID, owner)
	require.NoError(t, err)
	assert.Len(t, own.Skus, 2, "owner sees inactive SKUs")

	_, err = products.Update(ctx, owner, p.ID, domain.ProductInput{Status: ptr("active")})
	require.NoError(t, err)
	pub, err := products.Get(ctx, p.ID, uuid.Nil)
	require.NoError(t, err)
	assert.Len(t, pub.Skus, 1, "public view hides inactive SKUs")
}

func TestSellerOperationsWithoutShop(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	products := app.NewProductService(store, &stubPresigner{})
	ctx := context.Background()
	noShop := testsupport.NewID(t)

	_, err := products.Create(ctx, noShop, draftInput())
	assert.ErrorIs(t, err, domain.ErrNoShop)
	_, err = products.ListMine(ctx, noShop, nil, "", 0)
	assert.ErrorIs(t, err, domain.ErrNoShop)
	_, err = products.PresignUpload(ctx, noShop, "image/png")
	assert.ErrorIs(t, err, domain.ErrNoShop)
}

func TestPresignUploadUsesCallersShopAndChecksType(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	presigner := &stubPresigner{}
	products := app.NewProductService(store, presigner)
	owner := testsupport.NewID(t)
	shop, err := app.NewShopService(store, allowAll{}).Open(ctx, owner, domain.ShopInput{Name: "Shop Áo", Slug: "shop-ao"})
	require.NoError(t, err)

	_, err = products.PresignUpload(ctx, owner, "image/webp")
	require.NoError(t, err)
	assert.Equal(t, shop.ID, presigner.shop)

	_, err = products.PresignUpload(ctx, owner, "image/gif")
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, "content_type")
}

type allowAll struct{}

func (allowAll) GrantSeller(context.Context, uuid.UUID) error { return nil }
