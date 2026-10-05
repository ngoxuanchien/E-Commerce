package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// onlineOrders creates the online-paid orders once: skipped (prints
// "online orders: exist, skipped") if buyer1 already has any non-COD order.
// For each plan: clear cart, set line, checkout, optionally pay and poll,
// then run steps and poll until Want status and WantPayment.
func (s *seeder) onlineOrders(ctx context.Context, shops []sellerState, bs []*client.Client) error {
	page, err := bs[1].Orders(ctx, "")
	if err != nil {
		return fmt.Errorf("list orders for buyer1: %w", err)
	}
	for _, o := range page.Items {
		if o.PaymentMethod != "COD" {
			_, _ = fmt.Fprintln(s.out, "online orders: exist, skipped")
			return nil
		}
	}

	for _, op := range onlinePlans {
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
			SkuIDs: []string{skuID}, ShippingAddress: client.DemoAddress(buyers[op.Buyer].FullName), PaymentMethod: op.Method,
		}, nil)
		if err != nil {
			return fmt.Errorf("checkout %q: %w", p.Name, err)
		}
		o := res.Orders[0]
		checkoutID := res.CheckoutID

		if op.Pay {
			pmt, err := b.CreatePayment(ctx, newKey(), checkoutID, nil)
			if err != nil {
				return fmt.Errorf("create payment for %q: %w", p.Name, err)
			}
			if _, err := b.FakeSubmit(ctx, pmt.ID, "succeed", nil, nil); err != nil {
				return fmt.Errorf("submit payment for %q: %w", p.Name, err)
			}

			// Poll until PAID (deadline 30s)
			ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			for {
				o, err = b.Order(ctx2, o.ID)
				if err != nil {
					return fmt.Errorf("fetch order %s: %w", o.ID, err)
				}
				if o.PaymentStatus == "PAID" {
					break
				}
				select {
				case <-ctx2.Done():
					return fmt.Errorf("order %s: payment status %s, want PAID (timeout)", o.ID, o.PaymentStatus)
				case <-time.After(time.Second):
				}
			}
		}

		// Run steps and poll until Want status and WantPayment
		for _, step := range op.Steps {
			switch step {
			case "complete":
				o, err = b.CompleteOrder(ctx, o.ID)
			case "cancel":
				o, err = seller.c.SellerOrderAction(ctx, o.ID, "cancel", "Hết hàng")
			default:
				o, err = seller.c.SellerOrderAction(ctx, o.ID, step, "")
			}
			if err != nil {
				return fmt.Errorf("order %s %s: %w", o.ID, step, err)
			}
		}

		// Poll until Want status and WantPayment (deadline 60s)
		ctx3, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		for {
			o, err = b.Order(ctx3, o.ID)
			if err != nil {
				return fmt.Errorf("fetch order %s: %w", o.ID, err)
			}
			if o.Status == op.Want && o.PaymentStatus == op.WantPayment {
				break
			}
			select {
			case <-ctx3.Done():
				return fmt.Errorf("order %s: status %s (want %s), payment %s (want %s) (timeout)",
					o.ID, o.Status, op.Want, o.PaymentStatus, op.WantPayment)
			case <-time.After(time.Second):
			}
		}

		_, _ = fmt.Fprintf(s.out, "order %s (%s): %s / %s\n", o.ID, p.Name, o.Status, o.PaymentStatus)
	}
	return nil
}

// payout waits for seller1's wallet to accumulate a balance, then requests
// half (rounded down to 1000) and polls until SETTLED. Skipped if payouts
// already exist.
func (s *seeder) payout(ctx context.Context, seller1 *client.Client) error {
	payouts, err := seller1.Payouts(ctx, "")
	if err != nil {
		return fmt.Errorf("list payouts: %w", err)
	}
	if len(payouts.Items) > 0 {
		_, _ = fmt.Fprintln(s.out, "payout: exists, skipped")
		return nil
	}

	// Poll until available > 0 (deadline 60s)
	ctx1, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var wallet client.Wallet
	for {
		w, err := seller1.Wallet(ctx1)
		if err != nil {
			return fmt.Errorf("fetch wallet: %w", err)
		}
		if w.Available > 0 {
			wallet = w
			break
		}
		select {
		case <-ctx1.Done():
			return fmt.Errorf("wallet: available still 0 (timeout)")
		case <-time.After(time.Second):
		}
	}

	// Request available/2 rounded down to multiple of 1000
	amount := (wallet.Available / 2) / 1000 * 1000
	if amount < 50_000 {
		amount = 50_000
	}
	if amount > wallet.Available {
		amount = wallet.Available
	}

	payout, err := seller1.CreatePayout(ctx, newKey(), amount)
	if err != nil {
		return fmt.Errorf("create payout: %w", err)
	}

	// Poll until SETTLED (deadline 3 min, default PAYOUT_SETTLE_DELAY is 1 min)
	ctx2, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for {
		p, err := seller1.Payouts(ctx2, "")
		if err != nil {
			return fmt.Errorf("fetch payouts: %w", err)
		}
		if len(p.Items) > 0 && p.Items[0].Status == "SETTLED" {
			payout = p.Items[0]
			break
		}
		select {
		case <-ctx2.Done():
			return fmt.Errorf("payout: status still %s (timeout)", payout.Status)
		case <-time.After(time.Second):
			if len(p.Items) > 0 && p.Items[0].Status != payout.Status {
				_, _ = fmt.Fprintln(s.out, "payout: waiting to settle…")
				payout = p.Items[0]
			}
		}
	}

	_, _ = fmt.Fprintf(s.out, "payout %s: %d ₫ / %s\n", payout.ID, payout.Amount, payout.Status)
	return nil
}
