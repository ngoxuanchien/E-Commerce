package httpadapter

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestEmptyCollectionsAreArrays(t *testing.T) {
	assert.JSONEq(t, `{"shops":[],"subtotal":0}`, marshal(t, toCartDTO(domain.Cart{})))
	assert.JSONEq(t, `{"items":[],"next_cursor":null}`, marshal(t, toPageDTO(app.Page{})))
}

func TestCartDTOShopsCarryShippingFee(t *testing.T) {
	cart := domain.Cart{Shops: []domain.CartShop{{Shop: domain.ShopRef{ID: "s1", Name: "Shop", Slug: "shop"}, ShippingFee: 30_000}}}
	assert.JSONEq(t, `{"shops":[{"shop":{"id":"s1","name":"Shop","slug":"shop"},"items":[],"shipping_fee":30000}],"subtotal":0}`,
		marshal(t, toCartDTO(cart)))
}

func TestOrderDTONullsAndShape(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	pending := domain.StatusPending
	o := domain.Order{
		ID: "o1", CheckoutID: "c1", Status: domain.StatusPendingConfirmation, BuyerID: "b1", ShopID: "s1", ShopName: "Shop", ShopSlug: "shop",
		Items:    []domain.Item{{SkuID: "k1", ProductID: "p1", ProductName: "Áo", VariantName: "M", UnitPrice: 10, Quantity: 2, LineTotal: 20}},
		Subtotal: 20, ShippingFee: 30_000, Total: 30_020, ShippingAddress: domain.Address{FullName: "A"}, PaymentMethod: "COD",
		History: []domain.HistoryEntry{
			{To: domain.StatusPending, ActorRole: domain.RoleSystem, At: at},
			{From: &pending, To: domain.StatusPendingConfirmation, ActorRole: domain.RoleSystem, At: at},
		},
		CreatedAt: at, UpdatedAt: at,
	}
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(marshal(t, toOrderDTO(o))), &got))
	assert.Nil(t, got["cancel_reason"])
	assert.Contains(t, got, "cancel_reason")
	assert.Equal(t, map[string]any{"id": "s1", "name": "Shop", "slug": "shop"}, got["shop"])
	item := got["items"].([]any)[0].(map[string]any)
	assert.Contains(t, item, "image_url")
	assert.Nil(t, item["image_url"])
	h := got["history"].([]any)
	assert.Nil(t, h[0].(map[string]any)["from"])
	assert.Equal(t, "PENDING", h[1].(map[string]any)["from"])
	assert.Contains(t, h[0].(map[string]any), "note")
	assert.Equal(t, "2026-09-28T10:00:00Z", got["created_at"])
}

func TestEncodeCheckoutSuccess(t *testing.T) {
	resp := encodeCheckout(app.CheckoutResult{CheckoutID: "c1", Orders: nil}, nil)
	assert.Equal(t, http.StatusCreated, resp.Status)
	assert.JSONEq(t, `{"checkout_id":"c1","orders":[]}`, string(resp.Body))
}
