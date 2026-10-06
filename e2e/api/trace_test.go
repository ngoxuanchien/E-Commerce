package api_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

type jaegerTrace struct {
	Data []struct {
		Spans []struct {
			OperationName string `json:"operationName"`
			ProcessID     string `json:"processID"`
		} `json:"spans"`
		Processes map[string]struct {
			ServiceName string `json:"serviceName"`
		} `json:"processes"`
	} `json:"data"`
}

// Success criterion 6: a checkout started with a known traceparent produces
// one Jaeger trace with spans from gateway, order and catalog, including the
// ReserveStock RPC on catalog.
func TestCheckoutTraceCrossesGatewayOrderCatalog(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[4]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	p := newProduct(t, w.sellerA, w.categoryID, "Quạt nan tre "+w.run, 5)
	_, err := buyer.SetCartItem(ctx(t), p.Skus[0].ID, 1)
	require.NoError(t, err)

	traceID, spanID := randHex(16), randHex(8)
	_, err = buyer.Checkout(ctx(t), newKey(),
		client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 4"), PaymentMethod: "COD"},
		http.Header{"Traceparent": {"00-" + traceID + "-" + spanID + "-01"}})
	require.NoError(t, err)

	var services map[string]bool
	var reserveOnCatalog bool
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		services, _, reserveOnCatalog = fetchTrace(t, traceID)
		if services["gateway"] && services["order"] && services["catalog"] && reserveOnCatalog {
			return
		}
		time.Sleep(time.Second)
	}
	names := make([]string, 0, len(services))
	for s := range services {
		names = append(names, s)
	}
	sort.Strings(names)
	t.Fatalf("trace %s: services %v, catalog ReserveStock span found: %v (want gateway, order, catalog + ReserveStock)", traceID, names, reserveOnCatalog)
}

// TestPaymentTraceCrossesGatewayPaymentOrder verifies that a payment create trace
// spans from gateway through payment to order (GetCheckout).
func TestPaymentTraceCrossesGatewayPaymentOrder(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[2]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	p := newProduct(t, w.sellerA, w.categoryID, "Thanh toán VNPAY "+w.run, 3)
	_, err := buyer.SetCartItem(ctx(t), p.Skus[0].ID, 1)
	require.NoError(t, err)

	// Checkout VNPAY
	res, err := buyer.Checkout(ctx(t), newKey(),
		client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 2"), PaymentMethod: "VNPAY"},
		nil)
	require.NoError(t, err)
	checkoutID := res.CheckoutID

	// Create payment with traceparent header
	traceID, spanID := randHex(16), randHex(8)
	_, err = buyer.CreatePayment(ctx(t), newKey(), checkoutID,
		http.Header{"Traceparent": {"00-" + traceID + "-" + spanID + "-01"}})
	require.NoError(t, err)

	// Compute the order before polling (orderFor uses require internally)
	o := orderFor(t, res, w.shopA.ID)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := buyer.CancelOrder(cctx, o.ID, ""); err != nil {
			t.Logf("cleanup: cancel order %s: %v", o.ID, err)
		}
	})

	// Poll for trace with gateway, payment, and order services + GetCheckout operation on order
	deadline := time.Now().Add(30 * time.Second)
	var services map[string]bool
	var ops map[string][]string
	var foundGetCheckout bool
	for time.Now().Before(deadline) {
		services, ops, _ = fetchTrace(t, traceID)
		orderOps := ops["order"]
		for _, op := range orderOps {
			if strings.Contains(op, "GetCheckout") {
				foundGetCheckout = true
				break
			}
		}
		if services["gateway"] && services["payment"] && services["order"] && foundGetCheckout {
			return
		}
		time.Sleep(time.Second)
	}
	names := make([]string, 0, len(services))
	for s := range services {
		names = append(names, s)
	}
	sort.Strings(names)

	// Include order operations in failure message
	opDesc := []string{}
	for svc, opList := range ops {
		opDesc = append(opDesc, svc+": "+strings.Join(opList, ", "))
	}
	sort.Strings(opDesc)
	t.Logf("trace %s operations:\n  %s", traceID, strings.Join(opDesc, "\n  "))

	t.Fatalf("trace %s: services %v, order GetCheckout found: %v (want gateway, payment, order + GetCheckout)", traceID, names, foundGetCheckout)
}

// TestFakeWebhookTraceReachesConsumers verifies that a FAKE webhook trace propagates
// through Kafka consumers into order and ledger services.
func TestFakeWebhookTraceReachesConsumers(t *testing.T) {
	w := getWorld(t)
	buyer := w.payer
	require.NoError(t, buyer.ClearCart(ctx(t)))

	// Create product and record baseline wallet
	p := newProduct(t, w.sellerA, w.categoryID, "Webhook Trace "+w.run, 2)
	baseWallet := walletOf(t, w.sellerA)

	// Checkout VNPAY
	lines := map[string]int{p.Skus[0].ID: 1}
	res := checkoutOnline(t, buyer, "VNPAY", lines)
	checkoutID := res.CheckoutID
	o := orderFor(t, res, w.shopA.ID)

	// Pay with FAKE but don't send webhook (hook_webhook=drop)
	// This creates the outbox row with traceparent before the reconciler picks it up
	paymentData := payFake(t, buyer, checkoutID, "succeed", url.Values{"hook_webhook": {"drop"}})

	// Immediately post the webhook with traceparent header BEFORE the reconciler processes the outbox
	// (reconciler processes rows older than reconcileMinAge, so webhook must arrive sooner)
	traceID, spanID := randHex(16), randHex(8)
	fakeSecret := envOr("FAKE_WEBHOOK_SECRET", "dev-fake-secret")
	eventID := "e2e-" + newKey()
	err := buyer.FakeWebhook(ctx(t), fakeSecret, client.FakeEvent{
		EventID:   eventID,
		PaymentID: paymentData.ID,
		Outcome:   "succeed",
	}, http.Header{"Traceparent": {"00-" + traceID + "-" + spanID + "-01"}})
	require.NoError(t, err, "webhook send failed")

	// Now wait for payment to reach SUCCEEDED status
	paymentData = waitPayment(t, buyer, paymentData.ID, "SUCCEEDED", 5*time.Second)
	require.Equal(t, "SUCCEEDED", paymentData.Status, "payment should be SUCCEEDED")

	// Wait for order to reach PAID status (webhook processing + consumer)
	o = waitOrder(t, buyer, o.ID, 15*time.Second,
		func(order client.Order) bool {
			return order.PaymentStatus == "PAID"
		},
		"order should be PAID after webhook processing")

	// Verify trace has gateway, payment, order, and ledger services
	deadline := time.Now().Add(30 * time.Second)
	var services map[string]bool
	var ops map[string][]string
	var hasAllServices bool
	for time.Now().Before(deadline) {
		services, ops, _ = fetchTrace(t, traceID)
		if services["gateway"] && services["payment"] && services["order"] && services["ledger"] {
			hasAllServices = true
			break
		}
		time.Sleep(time.Second)
	}
	names := make([]string, 0, len(services))
	for s := range services {
		names = append(names, s)
	}
	sort.Strings(names)

	// Dump services and operations for failure message
	opDesc := []string{}
	for svc, opList := range ops {
		opDesc = append(opDesc, svc+": "+strings.Join(opList, ", "))
	}
	sort.Strings(opDesc)

	if !hasAllServices {
		t.Logf("trace %s services and operations seen:\n  %s", traceID, strings.Join(opDesc, "\n  "))
	}
	require.True(t, hasAllServices, "trace %s: services %v (want gateway, payment, order, ledger)", traceID, names)

	// Seller cancels to transition to CANCELLED and REFUNDED
	_, err = w.sellerA.SellerOrderAction(ctx(t), o.ID, "cancel", "Test refund")
	require.NoError(t, err)

	// Wait for order to reach CANCELLED status (seller action)
	o = waitOrder(t, buyer, o.ID, 10*time.Second,
		func(order client.Order) bool {
			return order.Status == "CANCELLED"
		},
		"order should be CANCELLED after seller cancels")

	// Wait for payment to transition to REFUNDED status
	o = waitOrder(t, buyer, o.ID, 3*refundInterval+5*time.Second,
		func(order client.Order) bool {
			return order.PaymentStatus == "REFUNDED"
		},
		"order should be REFUNDED after seller cancels")

	// Verify wallet escrow was released
	waitWallet(t, w.sellerA, 10*time.Second,
		func(wl client.Wallet) bool {
			// Escrow should be back to baseline after refund
			return wl.InEscrow == baseWallet.InEscrow
		},
		"sellerA wallet: InEscrow should return to baseline after refund")
}

// fetchTrace queries Jaeger for a trace and returns services, operations by service,
// and whether catalog has a ReserveStock operation.
func fetchTrace(t *testing.T, traceID string) (map[string]bool, map[string][]string, bool) {
	t.Helper()
	resp, err := http.Get(jaegerURL + "/api/traces/" + traceID)
	if err != nil {
		return map[string]bool{}, map[string][]string{}, false
	}
	defer func() { _ = resp.Body.Close() }()

	services := map[string]bool{}
	ops := map[string][]string{}
	if resp.StatusCode != http.StatusOK {
		return services, ops, false
	}
	var tr jaegerTrace
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return services, ops, false
	}

	reserve := false
	for _, d := range tr.Data {
		for _, pr := range d.Processes {
			services[pr.ServiceName] = true
			// Initialize operation list for this service if not present
			if _, exists := ops[pr.ServiceName]; !exists {
				ops[pr.ServiceName] = []string{}
			}
		}
		for _, s := range d.Spans {
			svc := d.Processes[s.ProcessID].ServiceName
			ops[svc] = append(ops[svc], s.OperationName)
			if svc == "catalog" && strings.Contains(s.OperationName, "ReserveStock") {
				reserve = true
			}
		}
	}
	return services, ops, reserve
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
