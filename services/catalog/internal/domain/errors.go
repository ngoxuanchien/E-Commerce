// Package domain holds catalog entities and business rules. It performs no I/O.
package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	// ErrNotFound means the resource does not exist or is not visible to the caller.
	ErrNotFound = errors.New("not found")
	// ErrNoShop means a seller operation was attempted by a user without a shop.
	ErrNoShop = errors.New("caller has no shop")
	// ErrUpstreamUnavailable means a dependency (identity-svc) failed or timed out.
	ErrUpstreamUnavailable = errors.New("upstream unavailable")
	// ErrRetryable means the operation lost a transient race (deadlock or
	// serialization failure) and was rolled back; retrying it is safe.
	ErrRetryable = errors.New("transient conflict, retry")
)

// ValidationError maps JSON field paths (e.g. "skus[0].price") to messages.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + " " + e.Fields[k]
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// ConflictError is a uniqueness conflict whose message is safe to show to clients.
type ConflictError struct {
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

// StockChange is a SKU whose stock moved since the seller read it.
type StockChange struct {
	SkuID    uuid.UUID
	Expected int32
	Actual   int32
}

// StockChangedError rejects a seller update whose expected_stock no longer
// matches (orders reserved or released stock meanwhile); nothing was written.
type StockChangedError struct {
	Skus []StockChange
}

func (e *StockChangedError) Error() string {
	parts := make([]string, len(e.Skus))
	for i, s := range e.Skus {
		parts[i] = fmt.Sprintf("%s expected %d, now %d", s.SkuID, s.Expected, s.Actual)
	}
	return "stock changed since it was read: " + strings.Join(parts, "; ")
}

const (
	msgRequired = "is required"
	msgUUID     = "must be a valid UUID"
)

// fieldErrors collects the first message per field.
type fieldErrors map[string]string

func (f fieldErrors) add(field, msg string) {
	if _, ok := f[field]; !ok {
		f[field] = msg
	}
}

func (f fieldErrors) err() error {
	if len(f) == 0 {
		return nil
	}
	return &ValidationError{Fields: f}
}

const msgText = "must be valid UTF-8 text without NUL characters"

// ValidText reports whether s can be stored in Postgres text: valid UTF-8
// without NUL bytes (anything else is rejected by the database with an error).
func ValidText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// checkChars validates that s is storable text of rune length [minLen, maxLen].
func checkChars(f fieldErrors, field, s string, minLen, maxLen int) {
	switch n := utf8.RuneCountInString(s); {
	case !ValidText(s):
		f.add(field, msgText)
	case n == 0 && minLen > 0:
		f.add(field, msgRequired)
	case n < minLen:
		f.add(field, fmt.Sprintf("must be at least %d characters", minLen))
	case n > maxLen:
		f.add(field, fmt.Sprintf("must be at most %d characters", maxLen))
	}
}
