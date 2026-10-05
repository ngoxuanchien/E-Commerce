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
		orderID := o.ID
		checkoutID := res.CheckoutID

		if op.Pay {
			pmt, err := b.CreatePayment(ctx, newKey(), checkoutID, nil)
			if err != nil {
				return fmt.Errorf("create payment for order %s: %w", orderID, err)
			}
			if _, err := b.FakeSubmit(ctx, pmt.ID, "succeed", nil, nil); err != nil {
				return fmt.Errorf("submit payment for order %s: %w", orderID, err)
			}

			// Poll until PAID (deadline 30s)
			ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
			for {
				o, err = b.Order(ctx2, orderID)
				if err != nil {
					cancel()
					return fmt.Errorf("fetch order %s: %w", orderID, err)
				}
				if o.PaymentStatus == "PAID" {
					cancel()
					break
				}
				select {
				case <-ctx2.Done():
					cancel()
					return fmt.Errorf("order %s: payment status %s, want PAID (timeout)", orderID, o.PaymentStatus)
				case <-time.After(time.Second):
				}
			}
		}

		// Run steps and poll until Want status and WantPayment
		for _, step := range op.Steps {
			switch step {
			case "complete":
				o, err = b.CompleteOrder(ctx, orderID)
			case "cancel":
				o, err = seller.c.SellerOrderAction(ctx, orderID, "cancel", "Hết hàng")
			default:
				o, err = seller.c.SellerOrderAction(ctx, orderID, step, "")
			}
			if err != nil {
				return fmt.Errorf("order %s %s: %w", orderID, step, err)
			}
		}

		// Poll until Want status and WantPayment (deadline 60s)
		ctx3, cancel := context.WithTimeout(ctx, 60*time.Second)
		for {
			o, err = b.Order(ctx3, orderID)
			if err != nil {
				cancel()
				return fmt.Errorf("fetch order %s: %w", orderID, err)
			}
			if o.Status == op.Want && o.PaymentStatus == op.WantPayment {
				cancel()
				break
			}
			select {
			case <-ctx3.Done():
				cancel()
				return fmt.Errorf("order %s: status %s (want %s), payment %s (want %s) (timeout)",
					orderID, o.Status, op.Want, o.PaymentStatus, op.WantPayment)
			case <-time.After(time.Second):
			}
		}

		_, _ = fmt.Fprintf(s.out, "order %s (%s): %s / %s\n", orderID, p.Name, o.Status, o.PaymentStatus)
	}
	return nil
}

// payout waits for seller1's wallet to accumulate credits for COMPLETED shop-0 orders,
// then requests half (rounded down to 1000) and polls until SETTLED. Skipped if payouts
// already exist. Skips if calculated amount < 50,000.
func (s *seeder) payout(ctx context.Context, seller1 *client.Client) error {
	payouts, err := seller1.Payouts(ctx, "")
	if err != nil {
		return fmt.Errorf("list payouts: %w", err)
	}
	if len(payouts.Items) > 0 {
		_, _ = fmt.Fprintln(s.out, "payout: exists, skipped")
		return nil
	}

	// Count COMPLETED orders on shop 0 in onlinePlans
	completedShop0 := 0
	for _, op := range onlinePlans {
		if op.Shop == 0 && op.Want == "COMPLETED" {
			completedShop0++
		}
	}

	// Poll until wallet entries have at least completedShop0 RELEASE entries (deadline 60s)
	ctx1, cancel := context.WithTimeout(ctx, 60*time.Second)
	for {
		entries, err := seller1.WalletEntries(ctx1)
		if err != nil {
			cancel()
			return fmt.Errorf("fetch wallet entries: %w", err)
		}
		releaseCount := 0
		for _, e := range entries.Items {
			if e.Kind == "RELEASE" {
				releaseCount++
			}
		}
		if releaseCount >= completedShop0 {
			cancel()
			break
		}
		select {
		case <-ctx1.Done():
			cancel()
			return fmt.Errorf("wallet entries: RELEASE count %d, want %d (timeout)", releaseCount, completedShop0)
		case <-time.After(time.Second):
		}
	}

	// Fetch final wallet balance
	wallet, err := seller1.Wallet(ctx)
	if err != nil {
		return fmt.Errorf("fetch wallet: %w", err)
	}

	// Calculate amount: available/2 rounded down to multiple of 1000
	amount := (wallet.Available / 2) / 1000 * 1000
	if amount < 50_000 {
		_, _ = fmt.Fprintf(s.out, "payout: amount %d ₫ < 50,000 ₫, skipped\n", amount)
		return nil
	}

	payout, err := seller1.CreatePayout(ctx, newKey(), amount)
	if err != nil {
		return fmt.Errorf("create payout: %w", err)
	}

	// Poll until SETTLED (deadline 3 min, default PAYOUT_SETTLE_DELAY is 1 min)
	ctx2, cancel := context.WithTimeout(ctx, 3*time.Minute)
	var lastStatus string
	printed := false
	for {
		p, err := seller1.Payouts(ctx2, "")
		if err != nil {
			cancel()
			return fmt.Errorf("fetch payouts: %w", err)
		}
		if len(p.Items) > 0 {
			lastStatus = p.Items[0].Status
			if lastStatus == "SETTLED" {
				payout = p.Items[0]
				cancel()
				break
			}
			if !printed && lastStatus != "REQUESTED" {
				_, _ = fmt.Fprintln(s.out, "payout: waiting to settle…")
				printed = true
			}
		}
		select {
		case <-ctx2.Done():
			cancel()
			return fmt.Errorf("payout: status %s (timeout)", lastStatus)
		case <-time.After(time.Second):
		}
	}

	_, _ = fmt.Fprintf(s.out, "payout %s: %d ₫ / %s\n", payout.ID, payout.Amount, payout.Status)
	return nil
}
