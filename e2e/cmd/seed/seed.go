package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

type seeder struct {
	gateway  string
	fixtures string
	rt       http.RoundTripper
	upload   *http.Client
	out      io.Writer

	categories map[string]string // slug -> id
}

type sellerState struct {
	c        *client.Client
	shop     client.Shop
	products map[string]client.Product // by name
}

func (s *seeder) run(ctx context.Context) error {
	if err := waitReady(ctx, s.gateway+"/readyz", 90*time.Second); err != nil {
		return err
	}
	if err := s.loadCategories(ctx); err != nil {
		return err
	}
	shops := make([]sellerState, len(sellers))
	for i, sd := range sellers {
		st, err := s.seller(ctx, sd)
		if err != nil {
			return fmt.Errorf("seller %s: %w", sd.Account.Email, err)
		}
		shops[i] = st
	}
	bs := make([]*client.Client, len(buyers))
	for i, a := range buyers {
		c, err := s.account(ctx, a)
		if err != nil {
			return err
		}
		bs[i] = c
	}
	if err := s.orders(ctx, shops, bs); err != nil {
		return err
	}
	if err := s.onlineOrders(ctx, shops, bs); err != nil {
		return err
	}
	if err := s.payout(ctx, shops[0].c); err != nil {
		return err
	}
	s.summary(ctx, shops, bs)
	return nil
}

func (s *seeder) loadCategories(ctx context.Context) error {
	cats, err := client.New(s.gateway, s.rt).Categories(ctx)
	if err != nil {
		return fmt.Errorf("categories: %w", err)
	}
	s.categories = map[string]string{}
	for _, top := range cats {
		s.categories[top.Slug] = top.ID
		for _, ch := range top.Children {
			s.categories[ch.Slug] = ch.ID
		}
	}
	return nil
}

// account registers a (registering an existing email is a 409 we accept)
// and logs in.
func (s *seeder) account(ctx context.Context, a account) (*client.Client, error) {
	c := client.New(s.gateway, s.rt)
	if _, err := c.Register(ctx, a.Email, password, a.FullName); err != nil && !client.IsCode(err, "CONFLICT") {
		return nil, fmt.Errorf("register %s: %w", a.Email, err)
	}
	if _, err := c.Login(ctx, a.Email, password); err != nil {
		return nil, fmt.Errorf("login %s: %w", a.Email, err)
	}
	return c, nil
}

// seller ensures the account, its shop and every product exist. It resumes a
// partial earlier run: an existing shop is kept and only missing products
// (matched by name) are created.
func (s *seeder) seller(ctx context.Context, sd sellerData) (sellerState, error) {
	c, err := s.account(ctx, sd.Account)
	if err != nil {
		return sellerState{}, err
	}
	shop, err := c.MyShop(ctx)
	switch st := client.StatusOf(err); {
	case err == nil:
		_, _ = fmt.Fprintf(s.out, "shop %s: exists\n", shop.Slug)
	case st == http.StatusForbidden || st == http.StatusNotFound: // no seller role yet / no shop
		if shop, err = c.OpenShop(ctx, sd.Shop.Name, sd.Shop.Slug, sd.Shop.Description); err != nil {
			return sellerState{}, fmt.Errorf("open shop: %w", err)
		}
		if _, err := c.Refresh(ctx); err != nil { // the new token carries the seller role
			return sellerState{}, fmt.Errorf("refresh after opening shop: %w", err)
		}
		_, _ = fmt.Fprintf(s.out, "shop %s: created\n", shop.Slug)
	default:
		return sellerState{}, fmt.Errorf("my shop: %w", err)
	}

	existing, err := c.AllSellerProducts(ctx)
	if err != nil {
		return sellerState{}, fmt.Errorf("list products: %w", err)
	}
	st := sellerState{c: c, shop: shop, products: map[string]client.Product{}}
	for _, p := range existing {
		st.products[p.Name] = p
	}
	created := 0
	for _, pd := range sd.Products {
		if _, ok := st.products[pd.Name]; ok {
			continue
		}
		p, err := s.createProduct(ctx, c, pd)
		if err != nil {
			return sellerState{}, fmt.Errorf("product %q: %w", pd.Name, err)
		}
		st.products[p.Name] = p
		created++
	}
	_, _ = fmt.Fprintf(s.out, "shop %s: %d products created, %d already present\n", shop.Slug, created, len(sd.Products)-created)
	return st, nil
}

func (s *seeder) createProduct(ctx context.Context, c *client.Client, pd productData) (client.Product, error) {
	catID, ok := s.categories[pd.Category]
	if !ok {
		return client.Product{}, fmt.Errorf("unknown category %q", pd.Category)
	}
	up, err := c.RequestUpload(ctx, "image/jpeg")
	if err != nil {
		return client.Product{}, fmt.Errorf("request upload: %w", err)
	}
	if err := client.UploadFile(ctx, s.upload, up, filepath.Join(s.fixtures, pd.Image), "image/jpeg"); err != nil {
		return client.Product{}, err
	}
	return c.CreateProduct(ctx, client.ProductInput{
		CategoryID: catID, Name: pd.Name, Description: pd.Description, Status: "active",
		ImageKeys: []string{up.ObjectKey}, Skus: pd.Skus,
	})
}

// orders creates the demo orders once: if buyer1 already has any order, the
// whole phase is skipped so re-runs never pile up duplicates.
func (s *seeder) orders(ctx context.Context, shops []sellerState, bs []*client.Client) error {
	page, err := bs[0].Orders(ctx, "")
	if err != nil {
		return fmt.Errorf("list orders: %w", err)
	}
	if len(page.Items) > 0 {
		_, _ = fmt.Fprintln(s.out, "orders: exist, skipped")
		return nil
	}
	for _, op := range orderPlans {
		b, seller := bs[op.Buyer], shops[op.Shop]
		p := seller.products[sellers[op.Shop].Products[op.Product].Name]
		skuID := p.Skus[0].ID
		if err := b.ClearCart(ctx); err != nil {
			return err
		}
		if _, err := b.SetCartItem(ctx, skuID, 1); err != nil {
			return fmt.Errorf("cart %q: %w", p.Name, err)
		}
		res, err := b.Checkout(ctx, newKey(), client.CheckoutRequest{
			SkuIDs: []string{skuID}, ShippingAddress: client.DemoAddress(buyers[op.Buyer].FullName), PaymentMethod: "COD",
		}, nil)
		if err != nil {
			return fmt.Errorf("checkout %q: %w", p.Name, err)
		}
		o := res.Orders[0]
		for _, step := range op.Steps {
			switch step {
			case "complete":
				o, err = b.CompleteOrder(ctx, o.ID)
			case "cancel":
				o, err = b.CancelOrder(ctx, o.ID, "Đặt nhầm phân loại")
			default:
				o, err = seller.c.SellerOrderAction(ctx, o.ID, step, "")
			}
			if err != nil {
				return fmt.Errorf("order %s %s: %w", o.ID, step, err)
			}
		}
		if o.Status != op.Want {
			return fmt.Errorf("order %s: status %s, want %s", o.ID, o.Status, op.Want)
		}
		_, _ = fmt.Fprintf(s.out, "order %s (%s): %s\n", o.ID, p.Name, o.Status)
	}
	return nil
}

func (s *seeder) summary(ctx context.Context, shops []sellerState, bs []*client.Client) {
	_, _ = fmt.Fprintln(s.out, "\nDemo accounts (password: "+password+")")
	w := tabwriter.NewWriter(s.out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ROLE\tEMAIL\tNAME\tSHOP")
	for _, sd := range sellers {
		_, _ = fmt.Fprintf(w, "seller\t%s\t%s\t/shops/%s\n", sd.Account.Email, sd.Account.FullName, sd.Shop.Slug)
	}
	for _, a := range buyers {
		_, _ = fmt.Fprintf(w, "buyer\t%s\t%s\t-\n", a.Email, a.FullName)
	}
	_ = w.Flush()

	// Wallet table (log in again to refresh token if needed)
	_, _ = fmt.Fprintln(s.out, "\nWALLET")
	w = tabwriter.NewWriter(s.out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SELLER\tAVAILABLE\tIN_ESCROW\tPENDING_PAYOUT")
	for i, sd := range sellers {
		c := shops[i].c
		wallet, err := c.Wallet(ctx)
		if err != nil {
			// Try refreshing token in case it expired
			if _, refreshErr := c.Refresh(ctx); refreshErr == nil {
				wallet, err = c.Wallet(ctx)
			}
		}
		if err != nil {
			_, _ = fmt.Fprintf(w, "%s\tn/a\tn/a\tn/a\n", sd.Account.FullName)
		} else {
			_, _ = fmt.Fprintf(w, "%s\t%d ₫\t%d ₫\t%d ₫\n", sd.Account.FullName, wallet.Available, wallet.InEscrow, wallet.PendingPayout)
		}
	}
	_ = w.Flush()

	_, _ = fmt.Fprintln(s.out, "\nWeb: http://localhost:3000  Gateway: http://localhost:8080  Jaeger: http://localhost:16686  MinIO console: http://localhost:9001")
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return "seed-" + hex.EncodeToString(b)
}

func waitReady(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway not ready at %s (run `make up-app` first)", url)
		case <-time.After(time.Second):
		}
	}
}
