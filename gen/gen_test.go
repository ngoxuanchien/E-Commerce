package gen_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
)

type catalogStub struct {
	catalogv1.UnimplementedCatalogInternalServer
}

func (catalogStub) ReserveStock(context.Context, *catalogv1.ReserveStockRequest) (*catalogv1.ReserveStockResponse, error) {
	st, _ := status.New(codes.FailedPrecondition, "insufficient stock").WithDetails(&catalogv1.InsufficientStock{
		Shortages: []*catalogv1.StockShortage{{SkuId: "s1", Requested: 2, Available: 1}},
	})
	return nil, st.Err()
}

// TestGeneratedStubsRoundTrip proves the generated code compiles and that the
// InsufficientStock detail survives the wire.
func TestGeneratedStubsRoundTrip(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	catalogv1.RegisterCatalogInternalServer(srv, catalogStub{})
	identityv1.RegisterIdentityInternalServer(srv, identityv1.UnimplementedIdentityInternalServer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	_, err = catalogv1.NewCatalogInternalClient(conn).ReserveStock(context.Background(), &catalogv1.ReserveStockRequest{
		OrderId: "o1", Items: []*catalogv1.StockItem{{SkuId: "s1", Quantity: 2}},
	})
	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition || len(st.Details()) != 1 {
		t.Fatalf("got %v", st)
	}
	d, ok := st.Details()[0].(*catalogv1.InsufficientStock)
	if !ok || d.GetShortages()[0].GetAvailable() != 1 {
		t.Fatalf("bad detail %#v", st.Details()[0])
	}

	_, err = identityv1.NewIdentityInternalClient(conn).GrantRole(context.Background(),
		&identityv1.GrantRoleRequest{UserId: "u1", Role: "seller"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("want Unimplemented, got %v", err)
	}
}
