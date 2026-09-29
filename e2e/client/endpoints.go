package client

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) Register(ctx context.Context, email, password, fullName string) (User, error) {
	var u User
	err := c.Do(ctx, http.MethodPost, "/api/auth/register",
		map[string]string{"email": email, "password": password, "full_name": fullName}, &u, nil)
	return u, err
}

// Login stores the access token; the refresh cookie lands in the jar.
func (c *Client) Login(ctx context.Context, email, password string) (TokenResponse, error) {
	var tr TokenResponse
	err := c.Do(ctx, http.MethodPost, "/api/auth/login", map[string]string{"email": email, "password": password}, &tr, nil)
	if err == nil {
		c.token = tr.AccessToken
	}
	return tr, err
}

// Refresh rotates the refresh cookie and stores the new access token.
func (c *Client) Refresh(ctx context.Context) (TokenResponse, error) {
	var tr TokenResponse
	err := c.Do(ctx, http.MethodPost, "/api/auth/refresh", nil, &tr, nil)
	if err == nil {
		c.token = tr.AccessToken
	}
	return tr, err
}

func (c *Client) Logout(ctx context.Context) error {
	err := c.Do(ctx, http.MethodPost, "/api/auth/logout", nil, nil, nil)
	c.token = ""
	return err
}

func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	return u, c.Do(ctx, http.MethodGet, "/api/users/me", nil, &u, nil)
}

func (c *Client) Categories(ctx context.Context) ([]Category, error) {
	var cs []Category
	return cs, c.Do(ctx, http.MethodGet, "/api/categories", nil, &cs, nil)
}

func (c *Client) SearchProducts(ctx context.Context, q url.Values) (Page[ProductSummary], error) {
	var p Page[ProductSummary]
	return p, c.Do(ctx, http.MethodGet, "/api/products?"+q.Encode(), nil, &p, nil)
}

func (c *Client) Product(ctx context.Context, id string) (Product, error) {
	var p Product
	return p, c.Do(ctx, http.MethodGet, "/api/products/"+url.PathEscape(id), nil, &p, nil)
}

func (c *Client) OpenShop(ctx context.Context, name, slug, description string) (Shop, error) {
	var s Shop
	err := c.Do(ctx, http.MethodPost, "/api/shops",
		map[string]string{"name": name, "slug": slug, "description": description}, &s, nil)
	return s, err
}

func (c *Client) MyShop(ctx context.Context) (Shop, error) {
	var s Shop
	return s, c.Do(ctx, http.MethodGet, "/api/shops/me", nil, &s, nil)
}

func (c *Client) SellerProducts(ctx context.Context, cursor string) (Page[Product], error) {
	q := url.Values{"limit": {"100"}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var p Page[Product]
	return p, c.Do(ctx, http.MethodGet, "/api/seller/products?"+q.Encode(), nil, &p, nil)
}

// AllSellerProducts follows next_cursor until the last page.
func (c *Client) AllSellerProducts(ctx context.Context) ([]Product, error) {
	var all []Product
	cursor := ""
	for {
		p, err := c.SellerProducts(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, p.Items...)
		if p.NextCursor == nil {
			return all, nil
		}
		cursor = *p.NextCursor
	}
}

func (c *Client) CreateProduct(ctx context.Context, in ProductInput) (Product, error) {
	var p Product
	return p, c.Do(ctx, http.MethodPost, "/api/seller/products", in, &p, nil)
}

func (c *Client) RequestUpload(ctx context.Context, contentType string) (Upload, error) {
	var u Upload
	return u, c.Do(ctx, http.MethodPost, "/api/seller/uploads", map[string]string{"content_type": contentType}, &u, nil)
}

func (c *Client) Cart(ctx context.Context) (Cart, error) {
	var cart Cart
	return cart, c.Do(ctx, http.MethodGet, "/api/cart", nil, &cart, nil)
}

func (c *Client) SetCartItem(ctx context.Context, skuID string, quantity int) (Cart, error) {
	var cart Cart
	return cart, c.Do(ctx, http.MethodPut, "/api/cart/items/"+url.PathEscape(skuID), map[string]int{"quantity": quantity}, &cart, nil)
}

func (c *Client) RemoveCartItem(ctx context.Context, skuID string) (Cart, error) {
	var cart Cart
	return cart, c.Do(ctx, http.MethodDelete, "/api/cart/items/"+url.PathEscape(skuID), nil, &cart, nil)
}

// ClearCart removes every line, so a test or seed step starts from an empty cart.
func (c *Client) ClearCart(ctx context.Context) error {
	cart, err := c.Cart(ctx)
	if err != nil {
		return err
	}
	for _, s := range cart.Shops {
		for _, it := range s.Items {
			if _, err := c.RemoveCartItem(ctx, it.SkuID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Checkout places the order with the given Idempotency-Key; hdr adds extra
// headers (e.g. traceparent).
func (c *Client) Checkout(ctx context.Context, key string, req CheckoutRequest, hdr http.Header) (CheckoutResponse, error) {
	h := http.Header{"Idempotency-Key": {key}}
	for k, vs := range hdr {
		h[k] = vs
	}
	var res CheckoutResponse
	return res, c.Do(ctx, http.MethodPost, "/api/checkout", req, &res, h)
}

func (c *Client) Orders(ctx context.Context, status string) (Page[Order], error) {
	q := url.Values{"limit": {"100"}}
	if status != "" {
		q.Set("status", status)
	}
	var p Page[Order]
	return p, c.Do(ctx, http.MethodGet, "/api/orders?"+q.Encode(), nil, &p, nil)
}

func (c *Client) Order(ctx context.Context, id string) (Order, error) {
	var o Order
	return o, c.Do(ctx, http.MethodGet, "/api/orders/"+url.PathEscape(id), nil, &o, nil)
}

func (c *Client) CancelOrder(ctx context.Context, id, reason string) (Order, error) {
	var o Order
	return o, c.Do(ctx, http.MethodPost, "/api/orders/"+url.PathEscape(id)+"/cancel", map[string]string{"reason": reason}, &o, nil)
}

func (c *Client) CompleteOrder(ctx context.Context, id string) (Order, error) {
	var o Order
	return o, c.Do(ctx, http.MethodPost, "/api/orders/"+url.PathEscape(id)+"/complete", nil, &o, nil)
}

func (c *Client) SellerOrders(ctx context.Context, status string) (Page[Order], error) {
	q := url.Values{"limit": {"100"}}
	if status != "" {
		q.Set("status", status)
	}
	var p Page[Order]
	return p, c.Do(ctx, http.MethodGet, "/api/seller/orders?"+q.Encode(), nil, &p, nil)
}

// SellerOrderAction runs confirm, ship, deliver or cancel. reason is
// required for cancel and optional otherwise.
func (c *Client) SellerOrderAction(ctx context.Context, id, action, reason string) (Order, error) {
	body := map[string]string{}
	if reason != "" {
		body["reason"] = reason
	}
	var o Order
	return o, c.Do(ctx, http.MethodPost, "/api/seller/orders/"+url.PathEscape(id)+"/"+action, body, &o, nil)
}
