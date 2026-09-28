package grpcapi_test

import (
	"context"
	"log/slog"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	grpcapi "github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/grpc"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
)

func TestGrantRole(t *testing.T) {
	store := postgres.NewStore(testsupport.Pool(t))
	client := startServer(t, store)
	ctx := context.Background()

	newUser := func(t *testing.T) domain.User {
		t.Helper()
		u, err := store.CreateUser(ctx, domain.User{
			ID: uuid.Must(uuid.NewV7()), Email: testsupport.Email(), FullName: "U",
			Roles: []domain.Role{domain.RoleBuyer},
		}, "h")
		require.NoError(t, err)
		return u
	}

	t.Run("granting seller twice is idempotent", func(t *testing.T) {
		u := newUser(t)
		for range 2 {
			_, err := client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: u.ID.String(), Role: "seller"})
			require.NoError(t, err)
		}
		got, err := store.UserByID(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, []domain.Role{domain.RoleBuyer, domain.RoleSeller}, got.Roles)
	})

	t.Run("unknown user is NOT_FOUND", func(t *testing.T) {
		_, err := client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: uuid.Must(uuid.NewV7()).String(), Role: "seller"})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("roles other than seller are INVALID_ARGUMENT", func(t *testing.T) {
		u := newUser(t)
		for _, role := range []string{"admin", "buyer", "", "SELLER"} {
			_, err := client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: u.ID.String(), Role: role})
			assert.Equal(t, codes.InvalidArgument, status.Code(err), "role %q", role)
		}
	})

	t.Run("malformed user id is INVALID_ARGUMENT", func(t *testing.T) {
		_, err := client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: "not-a-uuid", Role: "seller"})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func startServer(t *testing.T, store *postgres.Store) identityv1.IdentityInternalClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv, _ := grpcx.NewServer()
	identityv1.RegisterIdentityInternalServer(srv, grpcapi.NewServer(app.NewRoleService(store), slog.New(slog.DiscardHandler)))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpcx.Dial(lis.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return identityv1.NewIdentityInternalClient(conn)
}
