package app

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// EncodeCursor renders a keyset position as an opaque URL-safe string.
func EncodeCursor(c Cursor) string {
	raw := strconv.FormatInt(c.CreatedAt.UnixMicro(), 10) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses a string made by EncodeCursor; anything else is
// ErrInvalidCursor.
func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return Cursor{}, ErrInvalidCursor
	}
	micros, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{CreatedAt: time.UnixMicro(micros).UTC(), ID: u.String()}, nil
}
