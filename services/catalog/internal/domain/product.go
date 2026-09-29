package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProductStatus is the lifecycle state of a product.
type ProductStatus string

const (
	StatusDraft    ProductStatus = "draft"
	StatusActive   ProductStatus = "active"
	StatusArchived ProductStatus = "archived"
)

// ParseProductStatus reports whether s is a known status.
func ParseProductStatus(s string) (ProductStatus, bool) {
	switch st := ProductStatus(s); st {
	case StatusDraft, StatusActive, StatusArchived:
		return st, true
	}
	return "", false
}

// Limits from WS2 §3, plus the description and SKU-name caps chosen by this service.
const (
	MinProductName = 5
	MaxProductName = 200
	MaxDescription = 5000
	MinSkus        = 1
	MaxSkus        = 50
	MaxImages      = 9
	MaxSkuName     = 100
	MinSkuPrice    = int64(1_000)
	MaxSkuPrice    = int64(1_000_000_000)
	MaxSkuStock    = int32(100_000)
)

// Sku is a purchasable variant. SKUs are never deleted, only deactivated.
type Sku struct {
	ID     uuid.UUID
	Name   string
	Price  int64
	Stock  int32
	Active bool
}

// Product is a listing with its SKUs and ordered image keys.
type Product struct {
	ID          uuid.UUID
	Shop        ShopRef
	OwnerUserID uuid.UUID
	CategoryID  uuid.UUID
	Name        string
	Description string
	Status      ProductStatus
	ImageKeys   []string
	Skus        []Sku
	MinPrice    *int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PublicView returns a copy without inactive SKUs (what buyers see).
func (p Product) PublicView() Product {
	p.Skus = slices.DeleteFunc(slices.Clone(p.Skus), func(s Sku) bool { return !s.Active })
	return p
}

// ProductSummary is a search/browse result.
type ProductSummary struct {
	ID       uuid.UUID
	Name     string
	ImageKey string // first image, "" when the product has none
	MinPrice int64
	Shop     ShopRef
}

// MinActivePrice is the lowest price among active SKUs, or nil when none is active.
func MinActivePrice(skus []Sku) *int64 {
	var lowest *int64
	for _, s := range skus {
		if s.Active && (lowest == nil || s.Price < *lowest) {
			p := s.Price
			lowest = &p
		}
	}
	return lowest
}

// ImageKeyPrefix is the object-key prefix every image of shopID must use.
func ImageKeyPrefix(shopID uuid.UUID) string { return "shops/" + shopID.String() + "/" }

var imageKeyPattern = regexp.MustCompile(`^shops/([0-9a-f-]{36})/[0-9a-f-]{36}\.(jpg|png|webp)$`)

// SkuInput is one raw SKU of a seller request.
type SkuInput struct {
	ID            *string
	Name          string
	Price         *int64
	Stock         *int32
	ExpectedStock *int32 // the stock the seller last saw; required with ID
	Active        *bool
}

// ProductInput is a raw seller request (create or partial update). Nil
// pointers/slices mean "not provided"; an empty ImageKeys slice clears images.
type ProductInput struct {
	CategoryID  *string
	Name        *string
	Description *string
	Status      *string
	ImageKeys   []string
	Skus        []SkuInput
}

// SkuChange is a validated SKU: ID nil → create, set → update. An update
// applies only if the SKU's stock still equals ExpectedStock (nil = no check;
// seller input always sets it, see Validate).
type SkuChange struct {
	ID            *uuid.UUID
	Name          string
	Price         int64
	Stock         int32
	ExpectedStock *int32
	Active        bool
}

// ProductChange is a validated ProductInput. Nil fields are left unchanged;
// SetImages/SetSkus say whether the lists replace the current ones.
type ProductChange struct {
	CategoryID  *uuid.UUID
	Name        *string
	Description *string
	Status      *ProductStatus
	SetImages   bool
	ImageKeys   []string
	SetSkus     bool
	Skus        []SkuChange
}

// Validate checks in for a product of shopID. create requires category_id,
// name, status and skus and forbids SKU ids; an update needs at least one field.
func (in ProductInput) Validate(shopID uuid.UUID, create bool) (ProductChange, error) {
	f := fieldErrors{}
	var c ProductChange
	if !create && in.CategoryID == nil && in.Name == nil && in.Description == nil &&
		in.Status == nil && in.ImageKeys == nil && in.Skus == nil {
		f.add("body", "must contain at least one field")
	}
	if create {
		for field, missing := range map[string]bool{
			"category_id": in.CategoryID == nil, "name": in.Name == nil,
			"status": in.Status == nil, "skus": in.Skus == nil,
		} {
			if missing {
				f.add(field, msgRequired)
			}
		}
	}
	if in.CategoryID != nil {
		if id, err := uuid.Parse(*in.CategoryID); err != nil {
			f.add("category_id", msgUUID)
		} else {
			c.CategoryID = &id
		}
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		checkChars(f, "name", name, MinProductName, MaxProductName)
		c.Name = &name
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		checkChars(f, "description", d, 0, MaxDescription)
		c.Description = &d
	}
	if in.Status != nil {
		if st, ok := ParseProductStatus(*in.Status); ok {
			c.Status = &st
		} else {
			f.add("status", "must be one of: draft active archived")
		}
	}
	if in.ImageKeys != nil {
		c.SetImages = true
		c.ImageKeys = validateImageKeys(f, shopID, in.ImageKeys)
	}
	if in.Skus != nil {
		c.SetSkus = true
		c.Skus = validateSkus(f, in.Skus, create)
	}
	return c, f.err()
}

func validateImageKeys(f fieldErrors, shopID uuid.UUID, keys []string) []string {
	if len(keys) > MaxImages {
		f.add("image_keys", fmt.Sprintf("must have at most %d items", MaxImages))
		return nil
	}
	seen := make(map[string]bool, len(keys))
	for i, k := range keys {
		field := fmt.Sprintf("image_keys[%d]", i)
		m := imageKeyPattern.FindStringSubmatch(k)
		switch {
		case m == nil || m[1] != shopID.String():
			f.add(field, "must be an image uploaded by this shop")
		case seen[k]:
			f.add(field, "is duplicated")
		}
		seen[k] = true
	}
	return append([]string{}, keys...)
}

func validateSkus(f fieldErrors, in []SkuInput, create bool) []SkuChange {
	switch {
	case len(in) < MinSkus:
		f.add("skus", fmt.Sprintf("must have at least %d item", MinSkus))
		return nil
	case len(in) > MaxSkus:
		f.add("skus", fmt.Sprintf("must have at most %d items", MaxSkus))
		return nil
	}
	out := make([]SkuChange, len(in))
	seen := make(map[uuid.UUID]bool, len(in))
	for i, s := range in {
		p := fmt.Sprintf("skus[%d]", i)
		c := SkuChange{Name: strings.TrimSpace(s.Name), Active: true}
		if s.ID != nil {
			switch id, err := uuid.Parse(*s.ID); {
			case create:
				f.add(p+".id", "must not be set when creating a product")
			case err != nil:
				f.add(p+".id", msgUUID)
			case seen[id]:
				f.add(p+".id", "is duplicated")
			default:
				seen[id] = true
				c.ID = &id
			}
		}
		checkChars(f, p+".name", c.Name, 1, MaxSkuName)
		switch {
		case s.Price == nil:
			f.add(p+".price", msgRequired)
		case *s.Price < MinSkuPrice:
			f.add(p+".price", fmt.Sprintf("must be at least %d", MinSkuPrice))
		case *s.Price > MaxSkuPrice:
			f.add(p+".price", fmt.Sprintf("must be at most %d", MaxSkuPrice))
		default:
			c.Price = *s.Price
		}
		switch {
		case s.Stock == nil:
			f.add(p+".stock", msgRequired)
		case *s.Stock < 0:
			f.add(p+".stock", "must be at least 0")
		case *s.Stock > MaxSkuStock:
			f.add(p+".stock", fmt.Sprintf("must be at most %d", MaxSkuStock))
		default:
			c.Stock = *s.Stock
		}
		switch {
		case s.ExpectedStock == nil && s.ID != nil && !create:
			// Stock is always written, so a stale form would silently undo
			// reservations made since it was loaded.
			f.add(p+".expected_stock", "is required when id is set")
		case s.ExpectedStock == nil:
		case s.ID == nil || create:
			f.add(p+".expected_stock", "must not be set for a new SKU")
		case *s.ExpectedStock < 0:
			f.add(p+".expected_stock", "must be at least 0")
		default:
			c.ExpectedStock = s.ExpectedStock
		}
		if s.Active != nil {
			c.Active = *s.Active
		}
		out[i] = c
	}
	return out
}
