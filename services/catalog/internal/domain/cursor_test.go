package domain_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 28, 10, 0, 0, 123456000, time.UTC)

	cases := map[string]struct {
		value any
		dst   func() any
		want  any
	}{
		"time":    {now, func() any { return new(time.Time) }, now},
		"int64":   {int64(150_000), func() any { return new(int64) }, int64(150_000)},
		"float32": {float32(0.0607927), func() any { return new(float32) }, float32(0.0607927)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := domain.EncodeCursor("s", tc.value, id)
			require.NoError(t, err)
			c, err := domain.DecodeCursor(raw, "s")
			require.NoError(t, err)
			require.NotNil(t, c)
			assert.Equal(t, id, c.ID)
			dst := tc.dst()
			require.NoError(t, c.DecodeValue(dst))
			switch v := dst.(type) {
			case *time.Time:
				assert.True(t, v.Equal(tc.want.(time.Time)))
			case *int64:
				assert.Equal(t, tc.want, *v)
			case *float32:
				assert.Equal(t, tc.want, *v, "float32 must round-trip bit-exactly")
			}
		})
	}
}

func TestDecodeCursorEmptyIsNil(t *testing.T) {
	c, err := domain.DecodeCursor("", "newest")
	require.NoError(t, err)
	assert.Nil(t, c)
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	other, err := domain.EncodeCursor("price_asc", int64(1), uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	for name, raw := range map[string]string{
		"not base64":     "%%%",
		"not json":       base64.RawURLEncoding.EncodeToString([]byte("nope")),
		"missing id":     base64.RawURLEncoding.EncodeToString([]byte(`{"sort":"newest","sort_value":1}`)),
		"missing value":  base64.RawURLEncoding.EncodeToString([]byte(`{"sort":"newest","id":"` + uuid.NewString() + `"}`)),
		"different sort": other,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := domain.DecodeCursor(raw, "newest")
			assert.Equal(t, "is invalid", fields(t, err)["cursor"])
		})
	}
}

func TestCursorDecodeValueTypeMismatch(t *testing.T) {
	raw, err := domain.EncodeCursor("newest", "not-a-time", uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	c, err := domain.DecodeCursor(raw, "newest")
	require.NoError(t, err)
	var ts time.Time
	assert.Contains(t, fields(t, c.DecodeValue(&ts)), "cursor")
}
