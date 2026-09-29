package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

func noHook(context.Context) error { return nil }

func newShop(t *testing.T, slug string) domain.Shop {
	return domain.Shop{ID: testsupport.NewID(t), OwnerUserID: testsupport.NewID(t), Name: "Shop " + slug, Slug: slug, Description: "desc"}
}

func TestCreateShopCommitsAfterHook(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	in := newShop(t, "shop-one")
	hookCalled := false

	got, err := store.CreateShop(ctx, in, func(context.Context) error { hookCalled = true; return nil })
	require.NoError(t, err)
	assert.True(t, hookCalled)
	assert.Equal(t, in.ID, got.ID)
	assert.False(t, got.CreatedAt.IsZero())

	byOwner, err := store.ShopByOwner(ctx, in.OwnerUserID)
	require.NoError(t, err)
	assert.Equal(t, "shop-one", byOwner.Slug)
	bySlug, err := store.ShopBySlug(ctx, "shop-one")
	require.NoError(t, err)
	assert.Equal(t, in.OwnerUserID, bySlug.OwnerUserID)
}

func TestCreateShopRollsBackWhenHookFails(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	in := newShop(t, "shop-two")
	boom := errors.New("identity down")

	_, err := store.CreateShop(ctx, in, func(context.Context) error { return boom })
	require.ErrorIs(t, err, boom)

	_, err = store.ShopByOwner(ctx, in.OwnerUserID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	_, err = store.ShopBySlug(ctx, "shop-two")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCreateShopConflicts(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	first := newShop(t, "taken")
	_, err := store.CreateShop(ctx, first, noHook)
	require.NoError(t, err)

	sameSlug := newShop(t, "taken")
	_, err = store.CreateShop(ctx, sameSlug, noHook)
	var ce *domain.ConflictError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, "shop slug is already taken", ce.Message)

	sameOwner := newShop(t, "another")
	sameOwner.OwnerUserID = first.OwnerUserID
	_, err = store.CreateShop(ctx, sameOwner, noHook)
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, "you already have a shop", ce.Message)
}

func TestCreateShopConcurrentSameOwnerCreatesOne(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	owner := testsupport.NewID(t)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := newShop(t, "race-"+uuid.NewString()[:8])
			s.OwnerUserID = owner
			_, errs[i] = store.CreateShop(context.Background(), s, noHook)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		var ce *domain.ConflictError
		assert.ErrorAs(t, err, &ce)
	}
	assert.Equal(t, 1, ok)
}

func TestUpdateShop(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	in := newShop(t, "upd")
	_, err := store.CreateShop(ctx, in, noHook)
	require.NoError(t, err)

	name := "Renamed shop"
	got, err := store.UpdateShop(ctx, in.OwnerUserID, domain.ShopPatch{Name: &name})
	require.NoError(t, err)
	assert.Equal(t, name, got.Name)
	assert.Equal(t, "desc", got.Description, "unset fields keep their value")
	assert.Equal(t, "upd", got.Slug)

	_, err = store.UpdateShop(ctx, testsupport.NewID(t), domain.ShopPatch{Name: &name})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCategoriesTree(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	tree, err := store.Categories(context.Background())
	require.NoError(t, err)
	require.Len(t, tree, 10)
	assert.Equal(t, "thoi-trang-nam", tree[0].Slug)
	require.Len(t, tree[0].Children, 3)
	assert.Equal(t, "ao-thun-nam", tree[0].Children[0].Slug)
	assert.NotNil(t, tree[0].Children[0].Children)
	assert.Empty(t, tree[0].Children[0].Children)
}
