// Package grpcapi serves identity-svc's internal gRPC API (proto/identity/v1).
package grpcapi

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// Server implements identityv1.IdentityInternalServer.
type Server struct {
	identityv1.UnimplementedIdentityInternalServer
	roles  *app.RoleService
	logger *slog.Logger
}

// NewServer builds the gRPC server implementation.
func NewServer(roles *app.RoleService, logger *slog.Logger) *Server {
	return &Server{roles: roles, logger: logger}
}

// GrantRole adds a role to a user (idempotent).
func (s *Server) GrantRole(ctx context.Context, req *identityv1.GrantRoleRequest) (*identityv1.GrantRoleResponse, error) {
	err := s.roles.Grant(ctx, req.GetUserId(), req.GetRole())
	var ve *domain.ValidationError
	switch {
	case err == nil:
		return &identityv1.GrantRoleResponse{}, nil
	case errors.Is(err, domain.ErrRoleNotGrantable):
		return nil, status.Errorf(codes.InvalidArgument, "role %q cannot be granted; only %q is allowed", req.GetRole(), domain.RoleSeller)
	case errors.As(err, &ve):
		return nil, status.Error(codes.InvalidArgument, "user_id must be a valid UUID")
	case errors.Is(err, domain.ErrNotFound):
		return nil, status.Error(codes.NotFound, "user not found")
	default:
		s.logger.ErrorContext(ctx, "grant role failed", "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
}
