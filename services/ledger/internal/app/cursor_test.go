package app_test

import (
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
	for _, bad := range []string{"", "!!!", "bm9waXBl", "MTIzfG5vdC1hLXV1aWQ"} {
		_, err := app.DecodeCursor(bad)
		assert.ErrorIs(t, err, app.ErrInvalidCursor, bad)
	}
}
