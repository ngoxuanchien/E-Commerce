// Package grpcapi exposes the CatalogInternal gRPC service and wraps the
// IdentityInternal client.
package grpcapi

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
)

// IdentityClient calls identity-svc's internal API.
type IdentityClient struct {
	client identityv1.IdentityInternalClient
}

// NewIdentityClient wraps cc.
func NewIdentityClient(cc grpc.ClientConnInterface) *IdentityClient {
	return &IdentityClient{client: identityv1.NewIdentityInternalClient(cc)}
}

// GrantSeller grants userID the seller role (idempotent on the server).
func (c *IdentityClient) GrantSeller(ctx context.Context, userID uuid.UUID) error {
	_, err := c.client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: userID.String(), Role: authctx.RoleSeller})
	return err
}
