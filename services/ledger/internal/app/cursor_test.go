package app_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

func TestCursorRoundTrip(t *testing.T) {
	c := app.Cursor{At: time.Now().UTC().Truncate(time.Microsecond), ID: newID()}
	got, err := app.DecodeCursor(app.EncodeCursor(c))
	require.NoError(t, err)
	assert.Equal(t, c, got)
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	badCursors := []string{"", "!!!", "bm9waXBl", "MTIzfG5vdC1hLXV1aWQ",
		enc("-1|" + newID().String()), enc("9223372036854775807|" + newID().String())}
	for _, bad := range badCursors {
		_, err := app.DecodeCursor(bad)
		assert.ErrorIs(t, err, app.ErrInvalidCursor, bad)
	}
}
