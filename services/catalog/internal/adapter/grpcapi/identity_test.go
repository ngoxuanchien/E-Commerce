package grpcapi_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi/grpctest"
)

func TestIdentityClientGrantsSellerRole(t *testing.T) {
	fake, conn := grpctest.StartFakeIdentity(t)
	user := uuid.Must(uuid.NewV7())

	require.NoError(t, grpcapi.NewIdentityClient(conn).GrantSeller(context.Background(), user))

	calls := fake.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, user.String(), calls[0].GetUserId())
	assert.Equal(t, "seller", calls[0].GetRole())
}

func TestIdentityClientReturnsErrors(t *testing.T) {
	fake, conn := grpctest.StartFakeIdentity(t)
	fake.SetError(status.Error(codes.Unavailable, "down"))
	err := grpcapi.NewIdentityClient(conn).GrantSeller(context.Background(), uuid.Must(uuid.NewV7()))
	assert.Equal(t, codes.Unavailable, status.Code(err))
}
