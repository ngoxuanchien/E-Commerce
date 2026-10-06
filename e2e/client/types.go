package client

// DTOs for the subset of api/openapi/*.yaml that seed and e2e use.

type User struct {
	ID       string   `json:"id"`
	Email    string   `json:"email"`
	FullName string   `json:"full_name"`
	Roles    []string `json:"roles"`
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	User        User   `json:"user"`
}

type ShopRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Shop struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	OwnerUserID string `json:"owner_user_id"`
}

type Category struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Slug     string     `json:"slug"`
	Children []Category `json:"children"`
}

type Sku struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Price  int64  `json:"price"`
	Stock  int    `json:"stock"`
	Active bool   `json:"active"`
}

type Image struct {
	ObjectKey string `json:"object_key"`
	URL       string `json:"url"`
}

type Product struct {
	ID          string  `json:"id"`
	Shop        ShopRef `json:"shop"`
	CategoryID  string  `json:"category_id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Images      []Image `json:"images"`
	Skus        []Sku   `json:"skus"`
	MinPrice    *int64  `json:"min_price"`
}

type ProductSummary struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	ImageURL *string `json:"image_url"`
	MinPrice *int64  `json:"min_price"`
	Shop     ShopRef `json:"shop"`
}

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type SkuInput struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Price  int64  `json:"price"`
	Stock  int    `json:"stock"`
	Active bool   `json:"active"`
}

type ProductInput struct {
	CategoryID  string     `json:"category_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	ImageKeys   []string   `json:"image_keys"`
	Skus        []SkuInput `json:"skus"`
}

type Upload struct {
	UploadURL string            `json:"upload_url"`
	Fields    map[string]string `json:"fields"`
	ObjectKey string            `json:"object_key"`
	PublicURL string            `json:"public_url"`
}

type CartItem struct {
	SkuID       string `json:"sku_id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	VariantName string `json:"variant_name"`
	UnitPrice   int64  `json:"unit_price"`
	Quantity    int    `json:"quantity"`
	Stock       int    `json:"stock"`
	Available   bool   `json:"available"`
}

type CartShop struct {
	Shop        ShopRef    `json:"shop"`
	Items       []CartItem `json:"items"`
	ShippingFee int64      `json:"shipping_fee"`
}

type Cart struct {
	Shops    []CartShop `json:"shops"`
	Subtotal int64      `json:"subtotal"`
}

type Address struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
	Line1    string `json:"line1"`
	Ward     string `json:"ward"`
	District string `json:"district"`
	Province string `json:"province"`
}

// DemoAddress is a valid Vietnamese shipping address for fullName.
func DemoAddress(fullName string) Address {
	return Address{FullName: fullName, Phone: "0901234567", Line1: "12 Lý Tự Trọng", Ward: "Bến Nghé", District: "Quận 1", Province: "TP. Hồ Chí Minh"}
}

type CheckoutRequest struct {
	SkuIDs          []string `json:"sku_ids,omitempty"`
	ShippingAddress Address  `json:"shipping_address"`
	PaymentMethod   string   `json:"payment_method"`
}

type CheckoutResponse struct {
	CheckoutID string  `json:"checkout_id"`
	Orders     []Order `json:"orders"`
}

type OrderItem struct {
	SkuID       string `json:"sku_id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	VariantName string `json:"variant_name"`
	UnitPrice   int64  `json:"unit_price"`
	Quantity    int    `json:"quantity"`
	LineTotal   int64  `json:"line_total"`
}

type HistoryEntry struct {
	From      *string `json:"from"`
	To        string  `json:"to"`
	ActorRole string  `json:"actor_role"`
	Note      *string `json:"note"`
	CreatedAt string  `json:"created_at"`
}

type OrderShop struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Order struct {
	ID                string         `json:"id"`
	CheckoutID        string         `json:"checkout_id"`
	Status            string         `json:"status"`
	BuyerID           string         `json:"buyer_id"`
	Shop              OrderShop      `json:"shop"`
	Items             []OrderItem    `json:"items"`
	Subtotal          int64          `json:"subtotal"`
	ShippingFee       int64          `json:"shipping_fee"`
	Total             int64          `json:"total"`
	CancelReason      *string        `json:"cancel_reason"`
	History           []HistoryEntry `json:"history"`
	PaymentMethod     string         `json:"payment_method"`
	PaymentStatus     string         `json:"payment_status"`
	PaymentDeadline   *string        `json:"payment_deadline"`
	CommissionRateBps int            `json:"commission_rate_bps"`
	CommissionAmount  int64          `json:"commission_amount"`
	DeliveredAt       *string        `json:"delivered_at"`
}

// Payment represents a payment for a checkout.
type Payment struct {
	ID            string  `json:"id"`
	CheckoutID    string  `json:"checkout_id"`
	Method        string  `json:"method"`   // VNPAY | STRIPE
	Provider      string  `json:"provider"` // vnpay | stripe | fake
	Status        string  `json:"status"`   // PENDING | SUCCEEDED | FAILED | EXPIRED | DUPLICATE
	Amount        int64   `json:"amount"`
	RedirectURL   *string `json:"redirect_url"`
	ExpiresAt     string  `json:"expires_at"`
	FailureReason *string `json:"failure_reason"`
}

// Wallet represents a seller's wallet balance.
type Wallet struct {
	Available     int64  `json:"available"`
	PendingPayout int64  `json:"pending_payout"`
	InEscrow      int64  `json:"in_escrow"`
	Currency      string `json:"currency"`
}

// WalletEntry represents a ledger entry in the seller's wallet.
type WalletEntry struct {
	ID            string  `json:"id"`
	TransactionID string  `json:"transaction_id"`
	Kind          string  `json:"kind"` // RELEASE | PAYOUT_REQUEST | PAYOUT_FAIL
	Amount        int64   `json:"amount"`
	OrderID       *string `json:"order_id"`
	PayoutID      *string `json:"payout_id"`
	CreatedAt     string  `json:"created_at"`
}

// Payout represents a seller's payout request.
type Payout struct {
	ID            string  `json:"id"`
	Amount        int64   `json:"amount"`
	Status        string  `json:"status"` // REQUESTED | SETTLED | FAILED
	FailureReason *string `json:"failure_reason"`
	RequestedAt   string  `json:"requested_at"`
	SettledAt     *string `json:"settled_at"`
}

// FakeEvent is a webhook event for the FAKE payment provider.
type FakeEvent struct {
	EventID   string `json:"event_id"`
	PaymentID string `json:"payment_id"`
	Outcome   string `json:"outcome"`
}
