package app

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidCursor: a cursor that EncodeCursor did not produce.
var ErrInvalidCursor = errors.New("ledger: invalid cursor")

// Cursor timestamps must fit Postgres timestamptz: years 1970 through 9999.
var (
	minCursorMicros = int64(0)
	maxCursorMicros = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC).UnixMicro()
)

// Cursor is a keyset position: (created_at | requested_at, id), newest first.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// EncodeCursor renders c as an opaque URL-safe string.
func EncodeCursor(c Cursor) string {
	raw := strconv.FormatInt(c.At.UnixMicro(), 10) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses a string made by EncodeCursor.
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
	if micros < minCursorMicros || micros > maxCursorMicros {
		return Cursor{}, ErrInvalidCursor
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{At: time.UnixMicro(micros).UTC(), ID: u}, nil
}

// ListInput is a page request; Limit is already validated (1–100).
type ListInput struct {
	Limit  int
	Cursor string
}

// Page is one page of a newest-first list. Items is never nil.
type Page[T any] struct {
	Items      []T
	NextCursor *string
}

func decodeOptionalCursor(s string) (*Cursor, error) {
	if s == "" {
		return nil, nil
	}
	c, err := DecodeCursor(s)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// pageOf trims rows fetched with limit+1 and sets NextCursor when more exist.
func pageOf[T any](rows []T, limit int, key func(T) Cursor) Page[T] {
	if rows == nil {
		rows = []T{}
	}
	if len(rows) <= limit {
		return Page[T]{Items: rows}
	}
	rows = rows[:limit]
	next := EncodeCursor(key(rows[len(rows)-1]))
	return Page[T]{Items: rows, NextCursor: &next}
}
