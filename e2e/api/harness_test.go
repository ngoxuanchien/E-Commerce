package api_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

const (
	password    = "matkhau123"
	shippingFee = int64(30_000)
)

// contractRouter validates each call against the OpenAPI document of the
// service that owns its path (WS4 §2 routing table).
type contractRouter struct{ identity, catalog, order, payment, ledger http.RoundTripper }

func (c contractRouter) RoundTrip(r *http.Request) (*http.Response, error) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/payments"):
		return c.payment.RoundTrip(r)
	case strings.HasPrefix(p, "/api/seller/wallet"), strings.HasPrefix(p, "/api/seller/payouts"):
		return c.ledger.RoundTrip(r)
	case strings.HasPrefix(p, "/api/auth/"), strings.HasPrefix(p, "/api/users/"):
		return c.identity.RoundTrip(r)
	case strings.HasPrefix(p, "/api/cart"), p == "/api/checkout",
		strings.HasPrefix(p, "/api/orders"), strings.HasPrefix(p, "/api/seller/orders"):
		return c.order.RoundTrip(r)
	default:
		return c.catalog.RoundTrip(r)
	}
}

// newTransport validates the contract above a 429-retrying transport, so the
// contract check only sees final responses.
func newTransport(t *testing.T) http.RoundTripper {
	t.Helper()
	base := client.RetryRateLimited{MaxWait: 3 * time.Minute}
	spec := func(name string) http.RoundTripper {
		return testutil.ContractTransport(t, filepath.Join("..", "..", "api", "openapi", name), base)
	}
	return contractRouter{
		identity: spec("identity.yaml"),
		catalog:  spec("catalog.yaml"),
		order:    spec("order.yaml"),
		payment:  spec("payment.yaml"),
		ledger:   spec("ledger.yaml"),
	}
}

type actor struct {
	*client.Client
	User  client.User
	Email string
}

func ctx(t *testing.T) context.Context { return t.Context() }

func newActor(t *testing.T, email, fullName string) actor {
	t.Helper()
	c := client.New(gatewayURL, newTransport(t))
	_, err := c.Register(ctx(t), email, password, fullName)
	require.NoError(t, err, "register %s", email)
	tr, err := c.Login(ctx(t), email, password)
	require.NoError(t, err, "login %s", email)
	return actor{Client: c, User: tr.User, Email: email}
}

func newSeller(t *testing.T, run, tag string) (actor, client.Shop) {
	t.Helper()
	a := newActor(t, fmt.Sprintf("e2e-%s-seller-%s@example.test", run, tag), "Người bán "+strings.ToUpper(tag))
	shop, err := a.OpenShop(ctx(t), "Shop E2E "+run+" "+tag, "e2e-"+run+"-"+tag, "Shop tạo bởi e2e.")
	require.NoError(t, err)
	tr, err := a.Refresh(ctx(t))
	require.NoError(t, err)
	require.Contains(t, tr.User.Roles, "seller")
	a.User = tr.User
	return a, shop
}

func newProduct(t *testing.T, seller actor, categoryID, name string, stocks ...int) client.Product {
	t.Helper()
	skus := make([]client.SkuInput, len(stocks))
	for i, s := range stocks {
		skus[i] = client.SkuInput{Name: fmt.Sprintf("Loại %d", i+1), Price: 100_000, Stock: s, Active: true}
	}
	p, err := seller.CreateProduct(ctx(t), client.ProductInput{
		CategoryID: categoryID, Name: name, Description: name + " — sản phẩm kiểm thử.",
		Status: "active", ImageKeys: []string{}, Skus: skus,
	})
	require.NoError(t, err)
	return p
}

// stockOf reads on-hand stock through the public product endpoint.
func stockOf(t *testing.T, productID, skuID string) int {
	t.Helper()
	p, err := client.New(gatewayURL, newTransport(t)).Product(ctx(t), productID)
	require.NoError(t, err)
	for _, s := range p.Skus {
		if s.ID == skuID {
			return s.Stock
		}
	}
	t.Fatalf("sku %s not in product %s", skuID, productID)
	return 0
}

// runID is 8 lowercase letters: one search token, valid in slugs and emails.
func runID() string {
	b := make([]byte, 8)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(26))
		b[i] = byte('a' + n.Int64())
	}
	return string(b)
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// world is shared by every test in the package. Registering and logging in
// share a 10/min login bucket per client IP, so users are created once per
// run; each test uses its own products and clears its buyers' carts.
type world struct {
	run          string
	sellerA      actor
	sellerB      actor
	sellerC      actor
	shopA, shopB client.Shop
	shopC        client.Shop
	payer        actor
	buyers       []actor
	categoryID   string
}

var (
	worldOnce sync.Once
	theWorld  *world
)

func getWorld(t *testing.T) *world {
	t.Helper()
	worldOnce.Do(func() { theWorld = buildWorld(t) })
	if theWorld == nil {
		t.Fatal("shared world could not be built (see the first failing test)")
	}
	t.Cleanup(func() { assertTrialBalance(t) })
	return theWorld
}

func buildWorld(t *testing.T) *world {
	w := &world{run: runID()}
	w.sellerA, w.shopA = newSeller(t, w.run, "a")
	w.sellerB, w.shopB = newSeller(t, w.run, "b")
	w.sellerC, w.shopC = newSeller(t, w.run, "c")
	w.payer = newActor(t, fmt.Sprintf("e2e-%s-payer@example.test", w.run), "Người thanh toán")
	for i := range 5 {
		w.buyers = append(w.buyers, newActor(t, fmt.Sprintf("e2e-%s-buyer%d@example.test", w.run, i), fmt.Sprintf("Người mua %d", i)))
	}
	cats, err := w.sellerA.Categories(ctx(t))
	require.NoError(t, err)
	for _, c := range cats {
		for _, ch := range c.Children {
			if ch.Slug == "do-dung-nha-bep" {
				w.categoryID = ch.ID
			}
		}
	}
	require.NotEmpty(t, w.categoryID)
	return w
}
