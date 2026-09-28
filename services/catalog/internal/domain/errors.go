// Package domain holds catalog entities and business rules. It performs no I/O.
package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	// ErrNotFound means the resource does not exist or is not visible to the caller.
	ErrNotFound = errors.New("not found")
	// ErrNoShop means a seller operation was attempted by a user without a shop.
	ErrNoShop = errors.New("caller has no shop")
	// ErrUpstreamUnavailable means a dependency (identity-svc) failed or timed out.
	ErrUpstreamUnavailable = errors.New("upstream unavailable")
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

// checkChars validates the rune length of s against [minLen, maxLen].
func checkChars(f fieldErrors, field, s string, minLen, maxLen int) {
	switch n := utf8.RuneCountInString(s); {
	case n == 0 && minLen > 0:
		f.add(field, msgRequired)
	case n < minLen:
		f.add(field, fmt.Sprintf("must be at least %d characters", minLen))
	case n > maxLen:
		f.add(field, fmt.Sprintf("must be at most %d characters", maxLen))
	}
}
