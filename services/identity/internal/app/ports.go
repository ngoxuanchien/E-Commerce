// Package app holds identity-svc's use cases and the storage ports they need.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// Store is the persistence port of AuthService.
type Store interface {
	// CreateUser inserts u with its roles and returns it with CreatedAt set.
	// A duplicate email (case-insensitive) returns domain.ErrEmailTaken.
	CreateUser(ctx context.Context, u domain.User, passwordHash string) (domain.User, error)
	// UserByEmail returns the user and password hash, or domain.ErrNotFound.
	UserByEmail(ctx context.Context, email string) (domain.User, string, error)
	// UserByID returns the user with current roles, or domain.ErrNotFound.
	UserByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error
	// RevokeRefreshToken revokes the token with hash if it is still active; unknown hashes are ignored.
	RevokeRefreshToken(ctx context.Context, hash []byte, at time.Time) error
	// InTx runs fn in one transaction, committing when fn returns nil.
	InTx(ctx context.Context, fn func(Tx) error) error
}

// Tx is the transactional view used by refresh-token rotation.
type Tx interface {
	// RefreshTokenForUpdate locks and returns the token with hash, or domain.ErrNotFound.
	RefreshTokenForUpdate(ctx context.Context, hash []byte) (domain.RefreshToken, error)
	RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error
	CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error
	// ReplaceRefreshToken marks oldID revoked at at and points it to newID.
	ReplaceRefreshToken(ctx context.Context, oldID, newID uuid.UUID, at time.Time) error
	UserByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// RoleStore is the persistence port of RoleService.
type RoleStore interface {
	// GrantRole adds role idempotently; an unknown user returns domain.ErrNotFound.
	GrantRole(ctx context.Context, userID uuid.UUID, role domain.Role) error
}
