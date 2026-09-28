package testsupport

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/identity/migrations"
)

// Pool starts a throwaway Postgres, applies identity-svc's migrations and
// returns a pool. Call it once per top-level test and share it across subtests.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := testutil.Postgres(t)
	if err := pgxx.Migrate(dsn, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxx.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Email returns a unique address so subtests sharing a database never collide.
func Email() string {
	return "user-" + uuid.NewString() + "@example.com"
}
