package domain

import (
	"cmp"
	"slices"
)

// Cart limits (WS3 §2).
const (
	MaxCartLines = 50
	MinQuantity  = 1
	MaxQuantity  = 99
)

// ShopRef identifies a shop in the cart.
type ShopRef struct {
	ID   string
	Name string
	Slug string
}

// CartItem is a cart line enriched with current catalog data. Quantity may
// exceed Stock: the cart allows it, checkout enforces stock.
type CartItem struct {
	SkuID       string
	ProductID   string
	ProductName string
	VariantName string
	ImageURL    *string
	UnitPrice   int64
	Quantity    int32
	Stock       int32
	Available   bool
}

// CartShop groups the cart lines of one shop. ShippingFee is the flat fee
// the shop's order will be charged at checkout.
type CartShop struct {
	Shop        ShopRef
	Items       []CartItem
	ShippingFee int64
}

// Cart is the buyer's cart grouped by shop.
type Cart struct {
	Shops    []CartShop
	Subtotal int64 // available lines only
}

// BuildCart enriches cart lines (SKU ID → quantity) with catalog data and
// groups them by shop, each carrying shippingFee. SKUs unknown to the catalog have no shop, so they are
// left out and returned as missing (sorted). Shops are sorted by name then ID,
// items by product name, variant name, then SKU ID.
func BuildCart(lines map[string]int32, skus map[string]Sku, shippingFee int64) (Cart, []string) {
	var cart Cart
	var missing []string
	byShop := map[string]*CartShop{}
	for skuID, qty := range lines {
		s, ok := skus[skuID]
		if !ok {
			missing = append(missing, skuID)
			continue
		}
		cs := byShop[s.ShopID]
		if cs == nil {
			cs = &CartShop{Shop: ShopRef{ID: s.ShopID, Name: s.ShopName, Slug: s.ShopSlug}, ShippingFee: shippingFee}
			byShop[s.ShopID] = cs
		}
		cs.Items = append(cs.Items, CartItem{
			SkuID: skuID, ProductID: s.ProductID, ProductName: s.ProductName, VariantName: s.VariantName,
			ImageURL: optional(s.ImageURL), UnitPrice: s.Price, Quantity: qty,
			Stock: max(s.Stock, 0), Available: s.Available,
		})
		if s.Available {
			cart.Subtotal += s.Price * int64(qty)
		}
	}
	for _, cs := range byShop {
		slices.SortFunc(cs.Items, func(a, b CartItem) int {
			return cmp.Or(cmp.Compare(a.ProductName, b.ProductName), cmp.Compare(a.VariantName, b.VariantName), cmp.Compare(a.SkuID, b.SkuID))
		})
		cart.Shops = append(cart.Shops, *cs)
	}
	slices.SortFunc(cart.Shops, func(a, b CartShop) int {
		return cmp.Or(cmp.Compare(a.Shop.Name, b.Shop.Name), cmp.Compare(a.Shop.ID, b.Shop.ID))
	})
	slices.Sort(missing)
	return cart, missing
}
