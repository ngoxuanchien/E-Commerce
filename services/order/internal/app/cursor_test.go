package app_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
)

func TestCursorRoundTrip(t *testing.T) {
	c := app.Cursor{CreatedAt: time.Date(2026, 9, 28, 10, 0, 0, 123456000, time.UTC), ID: "01928c3a-0000-7000-8000-000000000001"}
	got, err := app.DecodeCursor(app.EncodeCursor(c))
	require.NoError(t, err)
	assert.Equal(t, c, got)
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, s := range []string{
		"!!!", enc("no-separator"), enc("abc|01928c3a-0000-7000-8000-000000000001"), enc("123|not-a-uuid"),
	} {
		_, err := app.DecodeCursor(s)
		assert.ErrorIs(t, err, app.ErrInvalidCursor, s)
	}
}
