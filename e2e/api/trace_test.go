package api_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
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
		services, reserveOnCatalog = fetchTrace(t, traceID)
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

func fetchTrace(t *testing.T, traceID string) (map[string]bool, bool) {
	t.Helper()
	resp, err := http.Get(jaegerURL + "/api/traces/" + traceID)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	services := map[string]bool{}
	if resp.StatusCode != http.StatusOK {
		return services, false
	}
	var tr jaegerTrace
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tr))
	reserve := false
	for _, d := range tr.Data {
		for _, pr := range d.Processes {
			services[pr.ServiceName] = true
		}
		for _, s := range d.Spans {
			if d.Processes[s.ProcessID].ServiceName == "catalog" && strings.Contains(s.OperationName, "ReserveStock") {
				reserve = true
			}
		}
	}
	return services, reserve
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
