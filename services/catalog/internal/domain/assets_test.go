package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

func TestAssetBaseURL(t *testing.T) {
	assert.Equal(t, "http://cdn.test/b/shops/x/y.jpg", domain.AssetBase("http://cdn.test/b/").URL("shops/x/y.jpg"))
	assert.Equal(t, "http://cdn.test/b/k", domain.AssetBase("http://cdn.test/b").URL("k"))
	assert.Empty(t, domain.AssetBase("http://cdn.test/b").URL(""))
}

func TestImageExtension(t *testing.T) {
	for ct, want := range map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"} {
		ext, ok := domain.ImageExtension(ct)
		assert.True(t, ok)
		assert.Equal(t, want, ext)
	}
	_, ok := domain.ImageExtension("image/gif")
	assert.False(t, ok)
}
