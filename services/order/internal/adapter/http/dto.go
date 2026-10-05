package httpadapter

import (
	"time"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// JSON shapes of api/openapi/order.yaml. Slices are always non-nil so empty
// collections encode as [] (the contract requires arrays).

type shopRefDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type cartItemDTO struct {
	SkuID       string  `json:"sku_id"`
	ProductID   string  `json:"product_id"`
	ProductName string  `json:"product_name"`
	VariantName string  `json:"variant_name"`
	ImageURL    *string `json:"image_url"`
	UnitPrice   int64   `json:"unit_price"`
	Quantity    int32   `json:"quantity"`
	Stock       int32   `json:"stock"`
	Available   bool    `json:"available"`
}

type cartShopDTO struct {
	Shop        shopRefDTO    `json:"shop"`
	Items       []cartItemDTO `json:"items"`
	ShippingFee int64         `json:"shipping_fee"`
}

type cartDTO struct {
	Shops    []cartShopDTO `json:"shops"`
	Subtotal int64         `json:"subtotal"`
}

type orderShopDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type orderItemDTO struct {
	SkuID       string  `json:"sku_id"`
	ProductID   string  `json:"product_id"`
	ProductName string  `json:"product_name"`
	VariantName string  `json:"variant_name"`
	ImageURL    *string `json:"image_url"`
	UnitPrice   int64   `json:"unit_price"`
	Quantity    int32   `json:"quantity"`
	LineTotal   int64   `json:"line_total"`
}

type historyDTO struct {
	From      *domain.Status `json:"from"`
	To        domain.Status  `json:"to"`
	ActorRole domain.Role    `json:"actor_role"`
	Note      *string        `json:"note"`
	CreatedAt time.Time      `json:"created_at"`
}

type orderDTO struct {
	ID                string               `json:"id"`
	CheckoutID        string               `json:"checkout_id"`
	Status            domain.Status        `json:"status"`
	BuyerID           string               `json:"buyer_id"`
	Shop              orderShopDTO         `json:"shop"`
	Items             []orderItemDTO       `json:"items"`
	Subtotal          int64                `json:"subtotal"`
	ShippingFee       int64                `json:"shipping_fee"`
	Total             int64                `json:"total"`
	ShippingAddress   domain.Address       `json:"shipping_address"`
	PaymentMethod     string               `json:"payment_method"`
	PaymentStatus     domain.PaymentStatus `json:"payment_status"`
	PaymentDeadline   *time.Time           `json:"payment_deadline"`
	CommissionRateBps int32                `json:"commission_rate_bps"`
	CommissionAmount  int64                `json:"commission_amount"`
	DeliveredAt       *time.Time           `json:"delivered_at"`
	CancelReason      *string              `json:"cancel_reason"`
	History           []historyDTO         `json:"history"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

type pageDTO struct {
	Items      []orderDTO `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

type checkoutResponseDTO struct {
	CheckoutID string     `json:"checkout_id"`
	Orders     []orderDTO `json:"orders"`
}

func toCartDTO(c domain.Cart) cartDTO {
	out := cartDTO{Shops: make([]cartShopDTO, 0, len(c.Shops)), Subtotal: c.Subtotal}
	for _, s := range c.Shops {
		shop := cartShopDTO{Shop: shopRefDTO{ID: s.Shop.ID, Name: s.Shop.Name, Slug: s.Shop.Slug}, Items: make([]cartItemDTO, 0, len(s.Items)), ShippingFee: s.ShippingFee}
		for _, it := range s.Items {
			shop.Items = append(shop.Items, cartItemDTO{
				SkuID: it.SkuID, ProductID: it.ProductID, ProductName: it.ProductName, VariantName: it.VariantName,
				ImageURL: it.ImageURL, UnitPrice: it.UnitPrice, Quantity: it.Quantity, Stock: it.Stock, Available: it.Available,
			})
		}
		out.Shops = append(out.Shops, shop)
	}
	return out
}

func toOrderDTO(o domain.Order) orderDTO {
	out := orderDTO{
		ID: o.ID, CheckoutID: o.CheckoutID, Status: o.Status, BuyerID: o.BuyerID,
		Shop: orderShopDTO{ID: o.ShopID, Name: o.ShopName, Slug: o.ShopSlug}, Items: make([]orderItemDTO, 0, len(o.Items)),
		Subtotal: o.Subtotal, ShippingFee: o.ShippingFee, Total: o.Total, ShippingAddress: o.ShippingAddress,
		PaymentMethod: o.PaymentMethod, PaymentStatus: o.PaymentStatus, PaymentDeadline: o.PaymentDeadline,
		CommissionRateBps: o.CommissionRateBps, CommissionAmount: o.CommissionAmount, DeliveredAt: o.DeliveredAt,
		CancelReason: o.CancelReason, History: make([]historyDTO, 0, len(o.History)),
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
	for _, it := range o.Items {
		out.Items = append(out.Items, orderItemDTO{
			SkuID: it.SkuID, ProductID: it.ProductID, ProductName: it.ProductName, VariantName: it.VariantName,
			ImageURL: it.ImageURL, UnitPrice: it.UnitPrice, Quantity: it.Quantity, LineTotal: it.LineTotal,
		})
	}
	for _, h := range o.History {
		out.History = append(out.History, historyDTO{From: h.From, To: h.To, ActorRole: h.ActorRole, Note: h.Note, CreatedAt: h.At})
	}
	return out
}

func toOrderDTOs(orders []domain.Order) []orderDTO {
	out := make([]orderDTO, 0, len(orders))
	for _, o := range orders {
		out = append(out, toOrderDTO(o))
	}
	return out
}

func toPageDTO(p app.Page) pageDTO {
	return pageDTO{Items: toOrderDTOs(p.Orders), NextCursor: p.NextCursor}
}
