package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// fields returns the field map of a *domain.ValidationError (failing the test otherwise).
func fields(t *testing.T, err error) map[string]string {
	t.Helper()
	var ve *domain.ValidationError
	require.True(t, errors.As(err, &ve), "want *ValidationError, got %v", err)
	return ve.Fields
}

func ptr[T any](v T) *T { return &v }

func TestShopInputValid(t *testing.T) {
	in, err := domain.ShopInput{Name: "  Shop Áo Đẹp ", Slug: "ao-dep-1", Description: " hi "}.Validate()
	require.NoError(t, err)
	assert.Equal(t, "Shop Áo Đẹp", in.Name)
	assert.Equal(t, "hi", in.Description)
}

func TestShopInputRules(t *testing.T) {
	cases := map[string]struct {
		in    domain.ShopInput
		field string
	}{
		"name too short (runes)": {domain.ShopInput{Name: "Áo", Slug: "abc"}, "name"},
		"name too long":          {domain.ShopInput{Name: strings.Repeat("a", 81), Slug: "abc"}, "name"},
		"slug upper case":        {domain.ShopInput{Name: "Shop", Slug: "Abc"}, "slug"},
		"slug underscore":        {domain.ShopInput{Name: "Shop", Slug: "a_bc"}, "slug"},
		"slug too short":         {domain.ShopInput{Name: "Shop", Slug: "ab"}, "slug"},
		"slug too long":          {domain.ShopInput{Name: "Shop", Slug: strings.Repeat("a", 51)}, "slug"},
		"slug reserved new":      {domain.ShopInput{Name: "Shop", Slug: "new"}, "slug"},
		"slug reserved admin":    {domain.ShopInput{Name: "Shop", Slug: "admin"}, "slug"},
		"slug reserved seller":   {domain.ShopInput{Name: "Shop", Slug: "seller"}, "slug"},
		"description too long":   {domain.ShopInput{Name: "Shop", Slug: "abc", Description: strings.Repeat("x", 2001)}, "description"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tc.in.Validate()
			assert.Contains(t, fields(t, err), tc.field)
		})
	}
}

func TestShopPatch(t *testing.T) {
	_, err := domain.ShopPatch{}.Validate()
	assert.Contains(t, fields(t, err), "body")

	p, err := domain.ShopPatch{Name: ptr("  New name ")}.Validate()
	require.NoError(t, err)
	assert.Equal(t, "New name", *p.Name)
	assert.Nil(t, p.Description)

	_, err = domain.ShopPatch{Name: ptr("ab")}.Validate()
	assert.Contains(t, fields(t, err), "name")
}
