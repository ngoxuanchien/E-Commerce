package domain

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"
)

// Sku is catalog's current view of a SKU (CatalogInternal.GetSkus).
type Sku struct {
	ID           string
	ProductID    string
	ProductName  string
	VariantName  string
	ImageURL     string
	Price        int64
	Stock        int32
	Available    bool
	ShopID       string
	ShopName     string
	ShopSlug     string
	SellerUserID string
}

// Line is a SKU and a quantity: a cart line or a stock reservation item.
type Line struct {
	SkuID    string
	Quantity int32
}

// Shortage is a SKU whose stock cannot cover the requested quantity.
type Shortage struct {
	SkuID     string
	Requested int32
	Available int32
}

// Reasons a cart line cannot be purchased.
const (
	ProblemNotFound    = "NOT_FOUND"
	ProblemUnavailable = "UNAVAILABLE"
	ProblemOwnShop     = "OWN_SHOP"
)

// LineProblem is a cart line that cannot be purchased.
type LineProblem struct {
	SkuID  string
	Reason string
}

// LineProblemsError lists every line that blocks a checkout.
type LineProblemsError struct {
	Problems []LineProblem
}

func (e *LineProblemsError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.SkuID + ": " + p.Reason
	}
	return "items cannot be purchased: " + strings.Join(parts, ", ")
}

// CheckoutParams is everything BuildOrders needs.
type CheckoutParams struct {
	CheckoutID    string
	BuyerID       string
	Lines         []Line
	Skus          map[string]Sku // current catalog data, keyed by SKU ID
	ShippingFee   int64          // flat fee per order
	Address       Address
	PaymentMethod string
	// CommissionRateBps is the platform commission snapshot; used for online methods only.
	CommissionRateBps int32
	Now               time.Time
	NewID             func() string
}

// BuildOrders splits a checkout into one PENDING order per shop, pricing every
// line at the current catalog price and snapshotting product data. If any line
// is unknown, unavailable or sold by the buyer it builds nothing and returns a
// *LineProblemsError listing all of them (sorted by SKU ID). Orders are sorted
// by shop ID and items by SKU ID.
func BuildOrders(p CheckoutParams) ([]Order, error) {
	var problems []LineProblem
	byShop := map[string][]Line{}
	for _, l := range p.Lines {
		s, ok := p.Skus[l.SkuID]
		switch {
		case !ok:
			problems = append(problems, LineProblem{SkuID: l.SkuID, Reason: ProblemNotFound})
		case s.SellerUserID == p.BuyerID:
			problems = append(problems, LineProblem{SkuID: l.SkuID, Reason: ProblemOwnShop})
		case !s.Available:
			problems = append(problems, LineProblem{SkuID: l.SkuID, Reason: ProblemUnavailable})
		default:
			byShop[s.ShopID] = append(byShop[s.ShopID], l)
		}
	}
	if len(problems) > 0 {
		slices.SortFunc(problems, func(a, b LineProblem) int { return cmp.Compare(a.SkuID, b.SkuID) })
		return nil, &LineProblemsError{Problems: problems}
	}

	orders := make([]Order, 0, len(byShop))
	for _, shopID := range slices.Sorted(maps.Keys(byShop)) {
		lines := byShop[shopID]
		slices.SortFunc(lines, func(a, b Line) int { return cmp.Compare(a.SkuID, b.SkuID) })
		first := p.Skus[lines[0].SkuID]
		o := Order{
			ID: p.NewID(), CheckoutID: p.CheckoutID, BuyerID: p.BuyerID,
			ShopID: shopID, ShopName: first.ShopName, SellerUserID: first.SellerUserID,
			Status: StatusPending, ShippingFee: p.ShippingFee, ShippingAddress: p.Address,
			PaymentMethod: p.PaymentMethod, PaymentStatus: PaymentNone, CreatedAt: p.Now, UpdatedAt: p.Now,
			History: []HistoryEntry{{To: StatusPending, ActorRole: RoleSystem, At: p.Now}},
		}
		o.ShopSlug = first.ShopSlug
		if IsOnline(p.PaymentMethod) {
			o.PaymentStatus = PaymentUnpaid
			o.CommissionRateBps = p.CommissionRateBps
		}
		for _, l := range lines {
			s := p.Skus[l.SkuID]
			it := Item{
				SkuID: l.SkuID, ProductID: s.ProductID, ProductName: s.ProductName, VariantName: s.VariantName,
				ImageURL: optional(s.ImageURL), UnitPrice: s.Price, Quantity: l.Quantity,
				LineTotal: s.Price * int64(l.Quantity),
			}
			o.Items = append(o.Items, it)
			o.Subtotal += it.LineTotal
		}
		if IsOnline(p.PaymentMethod) {
			o.CommissionAmount = Commission(o.Subtotal, p.CommissionRateBps)
		}
		o.Total = o.Subtotal + o.ShippingFee
		orders = append(orders, o)
	}
	return orders, nil
}

// StockLines returns the reservation items of o.
func (o Order) StockLines() []Line {
	lines := make([]Line, len(o.Items))
	for i, it := range o.Items {
		lines[i] = Line{SkuID: it.SkuID, Quantity: it.Quantity}
	}
	return lines
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
