package api_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// Success criterion 5: five buyers race for two units; exactly two orders
// are placed, the rest get OUT_OF_STOCK, and stock ends at zero.
func TestConcurrentCheckoutsNeverOversell(t *testing.T) {
	w := getWorld(t)
	p := newProduct(t, w.sellerB, w.categoryID, "Ấm tử sa pha trà "+w.run, 2)
	sku := p.Skus[0]
	for _, b := range w.buyers {
		require.NoError(t, b.ClearCart(ctx(t)))
		_, err := b.SetCartItem(ctx(t), sku.ID, 1)
		require.NoError(t, err)
	}

	start := make(chan struct{})
	errs := make([]error, len(w.buyers))
	var wg sync.WaitGroup
	for i, b := range w.buyers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = b.Checkout(ctx(t), newKey(), client.CheckoutRequest{SkuIDs: []string{sku.ID}, ShippingAddress: client.DemoAddress("Người mua"), PaymentMethod: "COD"}, nil)
		}()
	}
	close(start)
	wg.Wait()

	ok, outOfStock := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case client.IsCode(err, "OUT_OF_STOCK"):
			outOfStock++
		default:
			t.Errorf("unexpected checkout error: %v", err)
		}
	}
	assert.Equal(t, 2, ok)
	assert.Equal(t, 3, outOfStock)
	assert.Equal(t, 0, stockOf(t, p.ID, sku.ID))

	// Releasing the failed checkouts' (non-existent) reservations must not add stock back.
	time.Sleep(3 * time.Second)
	assert.Equal(t, 0, stockOf(t, p.ID, sku.ID))
}
