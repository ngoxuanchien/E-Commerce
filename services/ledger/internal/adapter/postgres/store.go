// Package postgres implements app.Store on PostgreSQL with sqlc-generated
// queries (queries/ → sqlcdb/).
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

// Store implements app.Store.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlcdb.Queries
}

var _ app.Store = (*Store)(nil)

// NewStore uses pool, whose database must have the service migrations applied.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: sqlcdb.New(pool)}
}

func (s *Store) InTx(ctx context.Context, fn func(app.Tx) error) error {
	return pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(&txStore{tx: tx, q: s.q.WithTx(tx)})
	})
}
