// Package postgres implements catalog persistence on catalog_db using
// sqlc-generated queries (see queries/) plus a hand-built search query.
package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// Constraint names from migrations/000002_catalog_schema.up.sql.
const (
	constraintShopOwner       = "shops_owner_user_id_key"
	constraintShopSlug        = "shops_slug_key"
	constraintProductCategory = "products_category_id_fkey"
)

// Store is the catalog repository.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlcdb.Queries
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: sqlcdb.New(pool)}
}

// Ping checks database connectivity (readiness).
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// notFound maps pgx.ErrNoRows to domain.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// constraintOf returns the violated constraint name, or "".
func constraintOf(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.ConstraintName
	}
	return ""
}
