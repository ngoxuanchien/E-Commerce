// Package postgres implements identity-svc's storage ports with sqlc-generated queries.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

var (
	_ app.Store     = (*Store)(nil)
	_ app.RoleStore = (*Store)(nil)
	_ app.Tx        = repo{}
)

// Store implements app.Store and app.RoleStore on a connection pool.
type Store struct {
	repo
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{repo: repo{q: sqlcdb.New(pool)}, pool: pool}
}

// CreateUser inserts the user and its roles in one transaction.
func (s *Store) CreateUser(ctx context.Context, u domain.User, passwordHash string) (domain.User, error) {
	err := pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		createdAt, err := q.CreateUser(ctx, sqlcdb.CreateUserParams{
			ID: u.ID, Email: u.Email, PasswordHash: passwordHash, FullName: u.FullName,
		})
		if isUniqueViolation(err, "users_email_key") {
			return domain.ErrEmailTaken
		}
		if err != nil {
			return fmt.Errorf("insert user: %w", err)
		}
		u.CreatedAt = createdAt.UTC()
		for _, r := range u.Roles {
			if err := q.AddUserRole(ctx, sqlcdb.AddUserRoleParams{UserID: u.ID, Role: string(r)}); err != nil {
				return fmt.Errorf("insert user role: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return domain.User{}, err
	}
	return u, nil
}

// InTx runs fn in a transaction.
func (s *Store) InTx(ctx context.Context, fn func(app.Tx) error) error {
	return pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(repo{q: s.q.WithTx(tx)})
	})
}

// repo implements single-statement operations on the pool or inside a transaction.
type repo struct {
	q *sqlcdb.Queries
}

func (r repo) UserByEmail(ctx context.Context, email string) (domain.User, string, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, "", domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, "", fmt.Errorf("get user by email: %w", err)
	}
	roles, err := r.roles(ctx, row.ID)
	if err != nil {
		return domain.User{}, "", err
	}
	u := domain.User{ID: row.ID, Email: row.Email, FullName: row.FullName, Roles: roles, CreatedAt: row.CreatedAt.UTC()}
	return u, row.PasswordHash, nil
}

func (r repo) UserByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user by id: %w", err)
	}
	roles, err := r.roles(ctx, row.ID)
	if err != nil {
		return domain.User{}, err
	}
	return domain.User{ID: row.ID, Email: row.Email, FullName: row.FullName, Roles: roles, CreatedAt: row.CreatedAt.UTC()}, nil
}

func (r repo) roles(ctx context.Context, userID uuid.UUID) ([]domain.Role, error) {
	names, err := r.q.ListUserRoles(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list user roles: %w", err)
	}
	roles := make([]domain.Role, len(names))
	for i, n := range names {
		roles[i] = domain.Role(n)
	}
	return roles, nil
}

func (r repo) GrantRole(ctx context.Context, userID uuid.UUID, role domain.Role) error {
	err := r.q.AddUserRole(ctx, sqlcdb.AddUserRoleParams{UserID: userID, Role: string(role)})
	if isForeignKeyViolation(err) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("grant role: %w", err)
	}
	return nil
}

func (r repo) CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error {
	err := r.q.CreateRefreshToken(ctx, sqlcdb.CreateRefreshTokenParams{
		ID: t.ID, UserID: t.UserID, FamilyID: t.FamilyID, TokenHash: t.Hash, ExpiresAt: t.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (r repo) RefreshTokenForUpdate(ctx context.Context, hash []byte) (domain.RefreshToken, error) {
	row, err := r.q.GetRefreshTokenForUpdate(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshToken{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RefreshToken{}, fmt.Errorf("get refresh token: %w", err)
	}
	return domain.RefreshToken{
		ID: row.ID, UserID: row.UserID, FamilyID: row.FamilyID, Hash: row.TokenHash,
		ExpiresAt: row.ExpiresAt.UTC(), RevokedAt: row.RevokedAt,
	}, nil
}

func (r repo) ReplaceRefreshToken(ctx context.Context, oldID, newID uuid.UUID, at time.Time) error {
	err := r.q.ReplaceRefreshToken(ctx, sqlcdb.ReplaceRefreshTokenParams{ID: oldID, ReplacedBy: &newID, RevokedAt: &at})
	if err != nil {
		return fmt.Errorf("replace refresh token: %w", err)
	}
	return nil
}

func (r repo) RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error {
	err := r.q.RevokeRefreshTokenFamily(ctx, sqlcdb.RevokeRefreshTokenFamilyParams{FamilyID: familyID, RevokedAt: &at})
	if err != nil {
		return fmt.Errorf("revoke refresh token family: %w", err)
	}
	return nil
}

func (r repo) RevokeRefreshToken(ctx context.Context, hash []byte, at time.Time) error {
	err := r.q.RevokeRefreshToken(ctx, sqlcdb.RevokeRefreshTokenParams{TokenHash: hash, RevokedAt: &at})
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}
