package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// Postgres rejects NUL bytes and invalid UTF-8 in text; such input must be a
// validation error, never reach the database.
var badTexts = map[string]string{"nul": "Shop\x00name", "invalid utf8": "Shop\xffname"}

func TestTextFieldsRejectNulAndInvalidUTF8(t *testing.T) {
	for name, bad := range badTexts {
		t.Run(name, func(t *testing.T) {
			_, err := domain.ShopInput{Name: bad, Slug: "abc"}.Validate()
			assert.Contains(t, fields(t, err), "name")

			_, err = domain.ShopPatch{Description: &bad}.Validate()
			assert.Contains(t, fields(t, err), "description")

			in := validCreate()
			in.Name = &bad
			in.Skus[0].Name = bad
			_, err = in.Validate(shopID, true)
			f := fields(t, err)
			assert.Contains(t, f, "name")
			assert.Contains(t, f, "skus[0].name")

			for _, p := range []domain.SearchParams{{Query: bad}, {CategorySlug: bad}, {ShopSlug: bad}} {
				_, err = p.Normalize()
				assert.Error(t, err)
			}
		})
	}
}
