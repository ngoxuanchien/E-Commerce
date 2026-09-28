package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// RoleService grants roles on behalf of other services (catalog-svc grants seller).
type RoleService struct {
	store RoleStore
}

// NewRoleService builds a RoleService.
func NewRoleService(store RoleStore) *RoleService {
	return &RoleService{store: store}
}

// Grant adds role to the user. Only seller can be granted; granting a role
// the user already has succeeds.
func (s *RoleService) Grant(ctx context.Context, userID, role string) error {
	if domain.Role(role) != domain.RoleSeller {
		return domain.ErrRoleNotGrantable
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return &domain.ValidationError{Fields: map[string]string{"user_id": "must be a valid UUID"}}
	}
	return s.store.GrantRole(ctx, id, domain.RoleSeller)
}
