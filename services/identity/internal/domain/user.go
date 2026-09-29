// Package domain holds identity-svc's core types, validation rules and errors.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role is an authorization role carried in access tokens.
type Role string

// Roles known to the marketplace.
const (
	RoleBuyer  Role = "buyer"
	RoleSeller Role = "seller"
	RoleAdmin  Role = "admin"
)

// User is an account as seen by the application. It never carries the password hash.
type User struct {
	ID        uuid.UUID
	Email     string
	FullName  string
	Roles     []Role
	CreatedAt time.Time
}

// RefreshToken is a stored refresh token. Only the SHA-256 hash of the raw
// token is kept; tokens rotated from the same login share a FamilyID.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	Hash      []byte
	ExpiresAt time.Time
	RevokedAt *time.Time
}
