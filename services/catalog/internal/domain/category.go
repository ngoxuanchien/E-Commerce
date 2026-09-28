package domain

import "github.com/google/uuid"

// Category is a node of the two-level category tree.
type Category struct {
	ID       uuid.UUID
	Name     string
	Slug     string
	Children []Category // empty (never nil) for second-level categories
}
