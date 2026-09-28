package domain

import (
	"strings"
	"time"
)

// Upload limits (WS2 §3).
const (
	MaxImageBytes = 5 << 20
	UploadTTL     = 10 * time.Minute
)

var imageExtensions = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}

// ImageExtension returns the object-key extension for an accepted image content type.
func ImageExtension(contentType string) (string, bool) {
	ext, ok := imageExtensions[contentType]
	return ext, ok
}

// AssetBase is the public base URL of the image bucket (PUBLIC_ASSET_BASE_URL).
type AssetBase string

// URL returns the public URL of key, or "" when key is empty.
func (b AssetBase) URL(key string) string {
	if key == "" {
		return ""
	}
	return strings.TrimRight(string(b), "/") + "/" + key
}

// Upload is a presigned POST form for one image.
type Upload struct {
	URL       string
	Fields    map[string]string
	ObjectKey string
	PublicURL string
}
