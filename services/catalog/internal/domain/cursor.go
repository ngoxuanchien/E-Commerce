package domain

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Cursor is a keyset position: the sort key and id of the last item returned.
// It is encoded as base64url JSON {"sort", "sort_value", "id"}.
type Cursor struct {
	Sort  string          `json:"sort"`
	Value json.RawMessage `json:"sort_value"`
	ID    uuid.UUID       `json:"id"`
}

// EncodeCursor builds the opaque cursor for the item (value, id) under sort.
func EncodeCursor(sort string, value any, id uuid.UUID) (string, error) {
	v, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode cursor value: %w", err)
	}
	b, err := json.Marshal(Cursor{Sort: sort, Value: v, ID: id})
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor parses raw; "" yields (nil, nil). A malformed cursor or one
// produced for another sort is a validation error on "cursor".
func DecodeCursor(raw, sort string) (*Cursor, error) {
	if raw == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalidCursor()
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil || c.Sort != sort || c.ID == uuid.Nil || len(c.Value) == 0 {
		return nil, invalidCursor()
	}
	return &c, nil
}

// DecodeValue unmarshals the sort value into dst.
func (c *Cursor) DecodeValue(dst any) error {
	if err := json.Unmarshal(c.Value, dst); err != nil {
		return invalidCursor()
	}
	return nil
}

func invalidCursor() error {
	return &ValidationError{Fields: map[string]string{"cursor": "is invalid"}}
}
