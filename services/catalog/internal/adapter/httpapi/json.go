package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

type shopRefJSON struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

type shopJSON struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	OwnerUserID uuid.UUID `json:"owner_user_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type categoryJSON struct {
	ID       uuid.UUID      `json:"id"`
	Name     string         `json:"name"`
	Slug     string         `json:"slug"`
	Children []categoryJSON `json:"children"`
}

type skuJSON struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Price  int64     `json:"price"`
	Stock  int32     `json:"stock"`
	Active bool      `json:"active"`
}

type imageJSON struct {
	ObjectKey string `json:"object_key"`
	URL       string `json:"url"`
}

type productJSON struct {
	ID          uuid.UUID            `json:"id"`
	Shop        shopRefJSON          `json:"shop"`
	CategoryID  uuid.UUID            `json:"category_id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Status      domain.ProductStatus `json:"status"`
	Images      []imageJSON          `json:"images"`
	Skus        []skuJSON            `json:"skus"`
	MinPrice    *int64               `json:"min_price"` // null when no SKU is active
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

type productSummaryJSON struct {
	ID       uuid.UUID   `json:"id"`
	Name     string      `json:"name"`
	ImageURL *string     `json:"image_url"`
	MinPrice int64       `json:"min_price"`
	Shop     shopRefJSON `json:"shop"`
}

type pageJSON[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type uploadJSON struct {
	UploadURL string            `json:"upload_url"`
	Fields    map[string]string `json:"fields"`
	ObjectKey string            `json:"object_key"`
	PublicURL string            `json:"public_url"`
}

func toShopRefJSON(s domain.ShopRef) shopRefJSON {
	return shopRefJSON{ID: s.ID, Name: s.Name, Slug: s.Slug}
}

func toShopJSON(s domain.Shop) shopJSON {
	return shopJSON{
		ID: s.ID, Name: s.Name, Slug: s.Slug, Description: s.Description,
		OwnerUserID: s.OwnerUserID, CreatedAt: s.CreatedAt.UTC(),
	}
}

func toCategoriesJSON(cs []domain.Category) []categoryJSON {
	out := make([]categoryJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, categoryJSON{ID: c.ID, Name: c.Name, Slug: c.Slug, Children: toCategoriesJSON(c.Children)})
	}
	return out
}

func (h *handlers) toProductJSON(p domain.Product) productJSON {
	images := make([]imageJSON, 0, len(p.ImageKeys))
	for _, k := range p.ImageKeys {
		images = append(images, imageJSON{ObjectKey: k, URL: h.Assets.URL(k)})
	}
	skus := make([]skuJSON, 0, len(p.Skus))
	for _, s := range p.Skus {
		skus = append(skus, skuJSON{ID: s.ID, Name: s.Name, Price: s.Price, Stock: s.Stock, Active: s.Active})
	}
	return productJSON{
		ID: p.ID, Shop: toShopRefJSON(p.Shop), CategoryID: p.CategoryID, Name: p.Name,
		Description: p.Description, Status: p.Status, Images: images, Skus: skus, MinPrice: p.MinPrice,
		CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
	}
}

func (h *handlers) toSummaryJSON(s domain.ProductSummary) productSummaryJSON {
	out := productSummaryJSON{ID: s.ID, Name: s.Name, MinPrice: s.MinPrice, Shop: toShopRefJSON(s.Shop)}
	if s.ImageKey != "" {
		u := h.Assets.URL(s.ImageKey)
		out.ImageURL = &u
	}
	return out
}

func toPage[T, J any](p domain.Page[T], conv func(T) J) pageJSON[J] {
	items := make([]J, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, conv(it))
	}
	return pageJSON[J]{Items: items, NextCursor: p.NextCursor}
}
