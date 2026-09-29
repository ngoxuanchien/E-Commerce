package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi/grpctest"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

func newShopService(t *testing.T) (*app.ShopService, *grpctest.FakeIdentity, *postgres.Store) {
	t.Helper()
	store := postgres.New(testsupport.DB(t))
	fake, conn := grpctest.StartFakeIdentity(t)
	return app.NewShopService(store, grpcapi.NewIdentityClient(conn)), fake, store
}

func TestOpenShopGrantsSellerRole(t *testing.T) {
	svc, fake, store := newShopService(t)
	owner := testsupport.NewID(t)

	shop, err := svc.Open(context.Background(), owner, domain.ShopInput{Name: " Shop Áo ", Slug: "shop-ao"})
	require.NoError(t, err)
	assert.Equal(t, "Shop Áo", shop.Name)
	assert.Equal(t, owner, shop.OwnerUserID)

	calls := fake.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, owner.String(), calls[0].GetUserId())
	assert.Equal(t, "seller", calls[0].GetRole())

	stored, err := store.ShopByOwner(context.Background(), owner)
	require.NoError(t, err)
	assert.Equal(t, shop.ID, stored.ID)
}

func TestOpenShopRollsBackWhenIdentityFails(t *testing.T) {
	svc, fake, store := newShopService(t)
	ctx := context.Background()
	owner := testsupport.NewID(t)
	fake.SetError(status.Error(codes.Unavailable, "identity down"))

	_, err := svc.Open(ctx, owner, domain.ShopInput{Name: "Shop Áo", Slug: "shop-ao"})
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	_, err = store.ShopByOwner(ctx, owner)
	assert.ErrorIs(t, err, domain.ErrNotFound, "no shop is created")

	fake.SetError(nil)
	_, err = svc.Open(ctx, owner, domain.ShopInput{Name: "Shop Áo", Slug: "shop-ao"})
	require.NoError(t, err, "slug and owner are free again after the rollback")
}

func TestOpenShopTimesOutSlowIdentity(t *testing.T) {
	svc, fake, store := newShopService(t)
	owner := testsupport.NewID(t)
	fake.SetDelay(5 * time.Second)

	start := time.Now()
	_, err := svc.WithGrantTimeout(100*time.Millisecond).Open(context.Background(), owner, domain.ShopInput{Name: "Shop Áo", Slug: "shop-ao"})
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.Less(t, time.Since(start), 2*time.Second)
	_, err = store.ShopByOwner(context.Background(), owner)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestOpenShopValidationAndConflictsSkipIdentity(t *testing.T) {
	svc, fake, _ := newShopService(t)
	ctx := context.Background()
	owner := testsupport.NewID(t)

	_, err := svc.Open(ctx, owner, domain.ShopInput{Name: "Shop Áo", Slug: "admin"})
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Empty(t, fake.Calls())

	_, err = svc.Open(ctx, owner, domain.ShopInput{Name: "Shop Áo", Slug: "first"})
	require.NoError(t, err)
	_, err = svc.Open(ctx, owner, domain.ShopInput{Name: "Shop Hai", Slug: "second"})
	var ce *domain.ConflictError
	require.ErrorAs(t, err, &ce)
	assert.Len(t, fake.Calls(), 1, "GrantRole only for the successful insert")
}

func TestUpdateShopValidates(t *testing.T) {
	svc, _, _ := newShopService(t)
	_, err := svc.Update(context.Background(), testsupport.NewID(t), domain.ShopPatch{})
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
}
