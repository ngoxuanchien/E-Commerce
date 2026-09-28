package domain

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Shop is a seller's storefront. P1: at most one per user.
type Shop struct {
	ID          uuid.UUID
	OwnerUserID uuid.UUID
	Name        string
	Slug        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ShopRef is the short form of a shop embedded in products.
type ShopRef struct {
	ID   uuid.UUID
	Name string
	Slug string
}

const (
	minShopName        = 3
	maxShopName        = 80
	maxShopDescription = 2000
)

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9-]{3,50}$`)
	// Reserved so /api/shops/me etc. never collide with /api/shops/{slug}.
	reservedSlugs = map[string]bool{"me": true, "new": true, "admin": true, "seller": true}
)

// ShopInput is the payload for opening a shop.
type ShopInput struct {
	Name        string
	Slug        string
	Description string
}

// Validate trims the input and checks WS2 §3 rules.
func (in ShopInput) Validate() (ShopInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	f := fieldErrors{}
	checkChars(f, "name", in.Name, minShopName, maxShopName)
	switch {
	case !slugPattern.MatchString(in.Slug):
		f.add("slug", "must match ^[a-z0-9-]{3,50}$")
	case reservedSlugs[in.Slug]:
		f.add("slug", "is reserved")
	}
	checkChars(f, "description", in.Description, 0, maxShopDescription)
	return in, f.err()
}

// ShopPatch updates name and/or description; the slug is immutable.
type ShopPatch struct {
	Name        *string
	Description *string
}

// Validate trims the patch and requires at least one field.
func (p ShopPatch) Validate() (ShopPatch, error) {
	f := fieldErrors{}
	if p.Name == nil && p.Description == nil {
		f.add("body", "must contain at least one field")
	}
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		checkChars(f, "name", name, minShopName, maxShopName)
		p.Name = &name
	}
	if p.Description != nil {
		d := strings.TrimSpace(*p.Description)
		checkChars(f, "description", d, 0, maxShopDescription)
		p.Description = &d
	}
	return p, f.err()
}
