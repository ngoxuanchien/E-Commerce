package domain_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

var shopID = uuid.MustParse("01a0e700-0000-7000-8000-000000000001")

func imageKey(shop uuid.UUID) string {
	return domain.ImageKeyPrefix(shop) + uuid.Must(uuid.NewV7()).String() + ".jpg"
}

func validCreate() domain.ProductInput {
	return domain.ProductInput{
		CategoryID: ptr(uuid.Must(uuid.NewV7()).String()),
		Name:       ptr("  Áo thun cotton  "),
		Status:     ptr("active"),
		ImageKeys:  []string{imageKey(shopID)},
		Skus: []domain.SkuInput{
			{Name: " Size M ", Price: ptr(int64(150_000)), Stock: ptr(int32(10))},
			{Name: "Size L", Price: ptr(int64(120_000)), Stock: ptr(int32(0)), Active: ptr(false)},
		},
	}
}

func TestProductCreateValid(t *testing.T) {
	c, err := validCreate().Validate(shopID, true)
	require.NoError(t, err)
	assert.Equal(t, "Áo thun cotton", *c.Name)
	assert.Equal(t, domain.StatusActive, *c.Status)
	assert.True(t, c.SetImages)
	assert.True(t, c.SetSkus)
	require.Len(t, c.Skus, 2)
	assert.Equal(t, "Size M", c.Skus[0].Name)
	assert.True(t, c.Skus[0].Active, "active defaults to true")
	assert.False(t, c.Skus[1].Active)
}

func TestProductCreateRequiresFields(t *testing.T) {
	_, err := domain.ProductInput{}.Validate(shopID, true)
	f := fields(t, err)
	for _, k := range []string{"category_id", "name", "status", "skus"} {
		assert.Equal(t, "is required", f[k], k)
	}
}

func TestProductUpdateNeedsAtLeastOneField(t *testing.T) {
	_, err := domain.ProductInput{}.Validate(shopID, false)
	assert.Contains(t, fields(t, err), "body")

	c, err := domain.ProductInput{Name: ptr("Tên mới dài")}.Validate(shopID, false)
	require.NoError(t, err)
	assert.False(t, c.SetSkus)
	assert.False(t, c.SetImages)
}

func TestProductUpdateEmptyImageListClearsImages(t *testing.T) {
	c, err := domain.ProductInput{ImageKeys: []string{}}.Validate(shopID, false)
	require.NoError(t, err)
	assert.True(t, c.SetImages)
	assert.Empty(t, c.ImageKeys)
}

func TestProductFieldRules(t *testing.T) {
	cases := map[string]struct {
		mutate func(*domain.ProductInput)
		field  string
	}{
		"bad category id":  {func(in *domain.ProductInput) { in.CategoryID = ptr("nope") }, "category_id"},
		"name 4 runes":     {func(in *domain.ProductInput) { in.Name = ptr("Đầm ") }, "name"},
		"name 201 chars":   {func(in *domain.ProductInput) { in.Name = ptr(strings.Repeat("a", 201)) }, "name"},
		"bad status":       {func(in *domain.ProductInput) { in.Status = ptr("sold") }, "status"},
		"description long": {func(in *domain.ProductInput) { in.Description = ptr(strings.Repeat("a", 5001)) }, "description"},
		"no skus":          {func(in *domain.ProductInput) { in.Skus = []domain.SkuInput{} }, "skus"},
		"51 skus": {func(in *domain.ProductInput) {
			in.Skus = make([]domain.SkuInput, 51)
			for i := range in.Skus {
				in.Skus[i] = domain.SkuInput{Name: "x", Price: ptr(int64(1000)), Stock: ptr(int32(1))}
			}
		}, "skus"},
		"price below min":  {func(in *domain.ProductInput) { in.Skus[0].Price = ptr(int64(999)) }, "skus[0].price"},
		"price above max":  {func(in *domain.ProductInput) { in.Skus[0].Price = ptr(int64(1_000_000_001)) }, "skus[0].price"},
		"price missing":    {func(in *domain.ProductInput) { in.Skus[0].Price = nil }, "skus[0].price"},
		"stock negative":   {func(in *domain.ProductInput) { in.Skus[1].Stock = ptr(int32(-1)) }, "skus[1].stock"},
		"stock above max":  {func(in *domain.ProductInput) { in.Skus[1].Stock = ptr(int32(100_001)) }, "skus[1].stock"},
		"stock missing":    {func(in *domain.ProductInput) { in.Skus[1].Stock = nil }, "skus[1].stock"},
		"sku name blank":   {func(in *domain.ProductInput) { in.Skus[0].Name = "   " }, "skus[0].name"},
		"sku name 101":     {func(in *domain.ProductInput) { in.Skus[0].Name = strings.Repeat("a", 101) }, "skus[0].name"},
		"sku id on create": {func(in *domain.ProductInput) { in.Skus[0].ID = ptr(uuid.Must(uuid.NewV7()).String()) }, "skus[0].id"},
		"10 images": {func(in *domain.ProductInput) {
			in.ImageKeys = nil
			for range 10 {
				in.ImageKeys = append(in.ImageKeys, imageKey(shopID))
			}
		}, "image_keys"},
		"other shop image": {func(in *domain.ProductInput) { in.ImageKeys = []string{imageKey(uuid.Must(uuid.NewV7()))} }, "image_keys[0]"},
		"path traversal": {func(in *domain.ProductInput) {
			in.ImageKeys = []string{domain.ImageKeyPrefix(shopID) + "../x/" + uuid.NewString() + ".jpg"}
		}, "image_keys[0]"},
		"bad extension": {func(in *domain.ProductInput) {
			in.ImageKeys = []string{domain.ImageKeyPrefix(shopID) + uuid.NewString() + ".gif"}
		}, "image_keys[0]"},
		"duplicate image": {func(in *domain.ProductInput) {
			k := imageKey(shopID)
			in.ImageKeys = []string{k, k}
		}, "image_keys[1]"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := validCreate()
			tc.mutate(&in)
			_, err := in.Validate(shopID, true)
			assert.Contains(t, fields(t, err), tc.field)
		})
	}
}

func TestProductUpdateSkuIDs(t *testing.T) {
	id := uuid.Must(uuid.NewV7()).String()
	sku := func(id *string) domain.SkuInput {
		return domain.SkuInput{ID: id, Name: "M", Price: ptr(int64(1000)), Stock: ptr(int32(1))}
	}

	c, err := domain.ProductInput{Skus: []domain.SkuInput{sku(&id), sku(nil)}}.Validate(shopID, false)
	require.NoError(t, err)
	require.NotNil(t, c.Skus[0].ID)
	assert.Equal(t, id, c.Skus[0].ID.String())
	assert.Nil(t, c.Skus[1].ID)

	_, err = domain.ProductInput{Skus: []domain.SkuInput{sku(&id), sku(&id)}}.Validate(shopID, false)
	assert.Equal(t, "is duplicated", fields(t, err)["skus[1].id"])

	_, err = domain.ProductInput{Skus: []domain.SkuInput{sku(ptr("x"))}}.Validate(shopID, false)
	assert.Contains(t, fields(t, err), "skus[0].id")
}

func TestMinActivePrice(t *testing.T) {
	assert.Nil(t, domain.MinActivePrice(nil))
	assert.Nil(t, domain.MinActivePrice([]domain.Sku{{Price: 5000, Active: false}}))
	got := domain.MinActivePrice([]domain.Sku{
		{Price: 9000, Active: true}, {Price: 1000, Active: false}, {Price: 3000, Active: true},
	})
	require.NotNil(t, got)
	assert.Equal(t, int64(3000), *got)
}

func TestPublicViewHidesInactiveSkus(t *testing.T) {
	p := domain.Product{Skus: []domain.Sku{{Name: "a", Active: true}, {Name: "b", Active: false}}}
	pub := p.PublicView()
	require.Len(t, pub.Skus, 1)
	assert.Equal(t, "a", pub.Skus[0].Name)
	assert.Len(t, p.Skus, 2, "original is not modified")
}
