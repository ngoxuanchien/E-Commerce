// Package routes holds the gateway's static route table: which upstream
// each request goes to, and what auth level it requires, before any
// forwarding or authentication happens.
package routes

import "strings"

// Level is the minimum authentication a route requires.
type Level int

const (
	// Public routes need no authentication.
	Public Level = iota
	// User routes need a valid access token for any authenticated user.
	User
	// Seller routes need a valid access token whose roles include "seller".
	Seller
	// Callback is a public provider-callback route; auth.Middleware handles it
	// with an early return before checking the level, so no token is verified and
	// no identity is injected. Invalid Authorization headers are ignored rather
	// than rejected. It is appended last so that without this early return, the
	// level >= User check would demand a token (failing closed).
	Callback
)

// String returns the lower-case name of l, or "unknown" for an unrecognized value.
func (l Level) String() string {
	switch l {
	case Public:
		return "public"
	case User:
		return "user"
	case Seller:
		return "seller"
	case Callback:
		return "callback"
	default:
		return "unknown"
	}
}

// Upstream service names, used as Route.Upstream and as keys into the
// dispatcher's upstream URL configuration.
const (
	Identity = "identity"
	Catalog  = "catalog"
	Order    = "order"
	Payment  = "payment"
	Ledger   = "ledger"
)

// Route is one row of the gateway's route table. Pattern is a slash-
// separated path made of literal segments and {name} placeholders; Subtree
// makes it also match any deeper path under Pattern. Methods lists the HTTP
// methods it applies to; an empty Methods matches any method.
type Route struct {
	Pattern  string
	Subtree  bool
	Methods  []string
	Upstream string
	Auth     Level
}

// Table is an ordered list of routes. The first row whose method and path
// match wins, so more specific rows must precede the prefix rows that would
// otherwise shadow them.
type Table []Route

// Default returns the gateway's route table for the WS0 §4 REST contract.
func Default() Table {
	return Table{
		// identity.yaml
		{Pattern: "/api/auth/register", Methods: []string{"POST"}, Upstream: Identity, Auth: Public},
		{Pattern: "/api/auth/login", Methods: []string{"POST"}, Upstream: Identity, Auth: Public},
		{Pattern: "/api/auth/refresh", Methods: []string{"POST"}, Upstream: Identity, Auth: Public},
		{Pattern: "/api/auth/logout", Methods: []string{"POST"}, Upstream: Identity, Auth: Public},
		{Pattern: "/api/auth", Subtree: true, Upstream: Identity, Auth: User},
		{Pattern: "/api/users", Subtree: true, Upstream: Identity, Auth: User},

		// catalog.yaml
		{Pattern: "/api/categories", Methods: []string{"GET"}, Upstream: Catalog, Auth: Public},
		{Pattern: "/api/categories", Subtree: true, Upstream: Catalog, Auth: User},
		{Pattern: "/api/products", Subtree: true, Methods: []string{"GET"}, Upstream: Catalog, Auth: Public},
		{Pattern: "/api/products", Subtree: true, Upstream: Catalog, Auth: User},
		// /api/shops/me must precede /api/shops/{slug} so "me" is never
		// treated as a shop slug, and is stricter than GET|PATCH so any
		// other method under it (e.g. a future DELETE) stays Seller too.
		{Pattern: "/api/shops/me", Subtree: true, Upstream: Catalog, Auth: Seller},
		{Pattern: "/api/shops/{slug}", Methods: []string{"GET"}, Upstream: Catalog, Auth: Public},
		{Pattern: "/api/shops", Subtree: true, Upstream: Catalog, Auth: User},
		{Pattern: "/api/seller/products", Subtree: true, Upstream: Catalog, Auth: Seller},
		{Pattern: "/api/seller/uploads", Subtree: true, Upstream: Catalog, Auth: Seller},

		// order.yaml
		{Pattern: "/api/cart", Subtree: true, Upstream: Order, Auth: User},
		{Pattern: "/api/checkout", Subtree: true, Upstream: Order, Auth: User},
		{Pattern: "/api/orders", Subtree: true, Upstream: Order, Auth: User},
		{Pattern: "/api/seller/orders", Subtree: true, Upstream: Order, Auth: Seller},

		// payment.yaml
		// Callback routes must precede the User routes so they match first.
		{Pattern: "/api/payments/vnpay", Subtree: true, Upstream: Payment, Auth: Callback},
		{Pattern: "/api/payments/stripe", Subtree: true, Upstream: Payment, Auth: Callback},
		{Pattern: "/api/payments/fake", Subtree: true, Upstream: Payment, Auth: Callback},
		{Pattern: "/api/payments", Subtree: true, Upstream: Payment, Auth: User},

		// ledger.yaml
		{Pattern: "/api/seller/wallet", Subtree: true, Upstream: Ledger, Auth: Seller},
		{Pattern: "/api/seller/payouts", Subtree: true, Upstream: Ledger, Auth: Seller},
	}
}

// Match returns the index of the first row in t whose method and path
// match, and true. It returns (0, false) if no row matches. path is the
// decoded request path (r.URL.Path); a single trailing slash is ignored.
func (t Table) Match(method, path string) (int, bool) {
	segments := splitPath(path)
	for i, r := range t {
		if len(r.Methods) > 0 && !hasMethod(r.Methods, method) {
			continue
		}
		if matchSegments(splitPath(r.Pattern), segments, r.Subtree) {
			return i, true
		}
	}
	return 0, false
}

// splitPath splits an absolute path into its segments, dropping a single
// trailing slash first.
func splitPath(path string) []string {
	return strings.Split(strings.TrimSuffix(path, "/"), "/")
}

// matchSegments checks whether pattern segments match path segments: exact
// length for a leaf route, at-least length for a subtree route, {name}
// segments requiring a non-empty value, everything else compared literally.
func matchSegments(pattern, path []string, subtree bool) bool {
	if subtree {
		if len(path) < len(pattern) {
			return false
		}
	} else if len(path) != len(pattern) {
		return false
	}
	for i, seg := range pattern {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			if path[i] == "" {
				return false
			}
			continue
		}
		if path[i] != seg {
			return false
		}
	}
	return true
}

// hasMethod reports whether method is present in methods.
func hasMethod(methods []string, method string) bool {
	for _, m := range methods {
		if m == method {
			return true
		}
	}
	return false
}
