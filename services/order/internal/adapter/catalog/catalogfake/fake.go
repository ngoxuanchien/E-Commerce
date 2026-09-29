// Package catalogfake is an in-process fake of catalog-svc's CatalogInternal
// gRPC API for tests. It keeps SKUs and stock in memory, reserves stock
// idempotently per order like the real service, reports shortages with the
// InsufficientStock detail, and can inject error codes and latency per method.
package catalogfake

import (
	"context"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
)

// Method names a CatalogInternal RPC for fault injection and call counting.
type Method string

const (
	GetSkus      Method = "GetSkus"
	ReserveStock Method = "ReserveStock"
	ReleaseStock Method = "ReleaseStock"
)

// Shop identifies a fake shop and its owner.
type Shop struct {
	ID           string
	Name         string
	Slug         string
	SellerUserID string
}

// NewShop returns a shop with fresh UUIDv7 IDs; the slug is derived from name.
func NewShop(name string) Shop {
	return Shop{
		ID: newID(), Name: name, SellerUserID: newID(),
		Slug: strings.ToLower(strings.Join(strings.Fields(name), "-")),
	}
}

// NewSku returns an active SKU of shop with fresh IDs.
func NewSku(shop Shop, price int64, stock int32) *catalogv1.SkuInfo {
	id := newID()
	return &catalogv1.SkuInfo{
		SkuId: id, ProductId: newID(), ProductName: "Product " + id[len(id)-6:], VariantName: "Default",
		ImageUrl: "https://img.example.com/" + id + ".jpg", Price: price, Stock: stock, Available: true,
		ShopId: shop.ID, ShopName: shop.Name, ShopSlug: shop.Slug, SellerUserId: shop.SellerUserID,
	}
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// Fake implements catalogv1.CatalogInternalServer in memory. It is safe for
// concurrent use.
type Fake struct {
	catalogv1.UnimplementedCatalogInternalServer

	mu           sync.Mutex
	skus         map[string]*catalogv1.SkuInfo
	reservations map[string][]*catalogv1.StockItem // order ID → reserved items
	released     []string
	calls        map[Method]int
	failNext     map[Method][]codes.Code
	failSkus     map[string]codes.Code
	delay        map[Method]time.Duration
}

// New returns an empty fake.
func New() *Fake {
	return &Fake{
		skus:         map[string]*catalogv1.SkuInfo{},
		reservations: map[string][]*catalogv1.StockItem{},
		calls:        map[Method]int{},
		failNext:     map[Method][]codes.Code{},
		failSkus:     map[string]codes.Code{},
		delay:        map[Method]time.Duration{},
	}
}

// PutSku adds or replaces a SKU (a copy is stored).
func (f *Fake) PutSku(s *catalogv1.SkuInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skus[s.GetSkuId()] = proto.Clone(s).(*catalogv1.SkuInfo)
}

// AddSku creates an active SKU of shop (see NewSku), stores and returns it.
func (f *Fake) AddSku(shop Shop, price int64, stock int32) *catalogv1.SkuInfo {
	s := NewSku(shop, price, stock)
	f.PutSku(s)
	return s
}

// SetStock sets the stock of a known SKU.
func (f *Fake) SetStock(skuID string, stock int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.skus[skuID]; ok {
		s.Stock = stock
	}
}

// SetAvailable marks a known SKU (in)active.
func (f *Fake) SetAvailable(skuID string, available bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.skus[skuID]; ok {
		s.Available = available
	}
}

// Stock returns the current stock of a SKU (0 if unknown).
func (f *Fake) Stock(skuID string) int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.skus[skuID].GetStock()
}

// Reserved reports whether orderID currently holds a reservation.
func (f *Fake) Reserved(orderID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.reservations[orderID]
	return ok
}

// ReleasedOrders returns the order ID of every ReleaseStock call that reached
// the handler, in call order (duplicates included).
func (f *Fake) ReleasedOrders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.released)
}

// Calls returns how many times m was invoked (including failed calls).
func (f *Fake) Calls(m Method) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[m]
}

// FailNext makes the next n calls of m fail with code.
func (f *Fake) FailNext(m Method, code codes.Code, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for range n {
		f.failNext[m] = append(f.failNext[m], code)
	}
}

// FailReserveForSkus makes every ReserveStock touching one of skuIDs fail with
// code until cleared with codes.OK.
func (f *Fake) FailReserveForSkus(code codes.Code, skuIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range skuIDs {
		if code == codes.OK {
			delete(f.failSkus, id)
		} else {
			f.failSkus[id] = code
		}
	}
}

// SetDelay makes every call of m wait d (or until the caller's deadline).
func (f *Fake) SetDelay(m Method, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay[m] = d
}

// Dial serves the fake over an in-memory bufconn listener and returns a
// client connection; both are closed when t ends.
func (f *Fake) Dial(t testing.TB) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	catalogv1.RegisterCatalogInternalServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///catalogfake",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial catalog fake: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
	})
	return conn
}

// begin counts the call, applies the configured delay and returns an injected
// failure, if any.
func (f *Fake) begin(ctx context.Context, m Method) error {
	f.mu.Lock()
	f.calls[m]++
	d := f.delay[m]
	code := codes.OK
	if q := f.failNext[m]; len(q) > 0 {
		code, f.failNext[m] = q[0], q[1:]
	}
	f.mu.Unlock()

	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-timer.C:
		}
	}
	if code != codes.OK {
		return status.Errorf(code, "injected %s failure", m)
	}
	return nil
}

func (f *Fake) GetSkus(ctx context.Context, req *catalogv1.GetSkusRequest) (*catalogv1.GetSkusResponse, error) {
	if err := f.begin(ctx, GetSkus); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := &catalogv1.GetSkusResponse{}
	for _, id := range req.GetSkuIds() {
		if s, ok := f.skus[id]; ok {
			resp.Skus = append(resp.Skus, proto.Clone(s).(*catalogv1.SkuInfo))
		}
	}
	return resp, nil
}

func (f *Fake) ReserveStock(ctx context.Context, req *catalogv1.ReserveStockRequest) (*catalogv1.ReserveStockResponse, error) {
	if err := f.begin(ctx, ReserveStock); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, done := f.reservations[req.GetOrderId()]; done {
		return &catalogv1.ReserveStockResponse{}, nil // idempotent per order
	}
	merged := map[string]int32{}
	for _, it := range req.GetItems() {
		if code, ok := f.failSkus[it.GetSkuId()]; ok {
			return nil, status.Errorf(code, "injected ReserveStock failure for sku %s", it.GetSkuId())
		}
		merged[it.GetSkuId()] += it.GetQuantity()
	}
	ids := slices.Sorted(maps.Keys(merged))
	var shortages []*catalogv1.StockShortage
	for _, id := range ids {
		s, ok := f.skus[id]
		if !ok {
			return nil, status.Errorf(codes.NotFound, "sku %s not found", id)
		}
		available := s.GetStock()
		if !s.GetAvailable() {
			available = 0
		}
		if available < merged[id] {
			shortages = append(shortages, &catalogv1.StockShortage{SkuId: id, Requested: merged[id], Available: available})
		}
	}
	if len(shortages) > 0 {
		st, err := status.New(codes.FailedPrecondition, "insufficient stock").
			WithDetails(&catalogv1.InsufficientStock{Shortages: shortages})
		if err != nil {
			return nil, err
		}
		return nil, st.Err()
	}
	items := make([]*catalogv1.StockItem, 0, len(ids))
	for _, id := range ids {
		f.skus[id].Stock -= merged[id]
		items = append(items, &catalogv1.StockItem{SkuId: id, Quantity: merged[id]})
	}
	f.reservations[req.GetOrderId()] = items
	return &catalogv1.ReserveStockResponse{}, nil
}

func (f *Fake) ReleaseStock(ctx context.Context, req *catalogv1.ReleaseStockRequest) (*catalogv1.ReleaseStockResponse, error) {
	if err := f.begin(ctx, ReleaseStock); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, req.GetOrderId())
	for _, it := range f.reservations[req.GetOrderId()] {
		if s, ok := f.skus[it.GetSkuId()]; ok {
			s.Stock += it.GetQuantity()
		}
	}
	delete(f.reservations, req.GetOrderId())
	return &catalogv1.ReleaseStockResponse{}, nil
}
