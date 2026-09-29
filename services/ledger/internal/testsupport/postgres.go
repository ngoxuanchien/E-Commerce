// Package testsupport holds ledger integration-test helpers: one Postgres
// container per test binary and a fresh, fully migrated database per test.
package testsupport

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/ledger/migrations"
)

const templateDB = "ledger_template"

var (
	templateURL *url.URL   // DSN of the migrated template database; nil when Postgres is not running
	createMu    sync.Mutex // serializes CREATE DATABASE ... TEMPLATE
)

// RunWithPostgres is a TestMain body: unless -short, it starts Postgres,
// migrates a template database, runs the tests and removes the container.
//
//	func TestMain(m *testing.M) { os.Exit(testsupport.RunWithPostgres(m)) }
func RunWithPostgres(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, testutil.PostgresImage,
		tcpostgres.WithDatabase(templateDB), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies())
	defer func() { _ = testcontainers.TerminateContainer(c) }()
	if err != nil {
		fmt.Fprintln(os.Stderr, "start postgres:", err)
		return 1
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "postgres dsn:", err)
		return 1
	}
	if err := pgxx.Migrate(dsn, migrations.FS); err != nil {
		fmt.Fprintln(os.Stderr, "migrate template:", err)
		return 1
	}
	if templateURL, err = url.Parse(dsn); err != nil {
		fmt.Fprintln(os.Stderr, "parse dsn:", err)
		return 1
	}
	return m.Run()
}

// DB returns a pool on a new database cloned from the migrated template; the
// database is dropped when the test ends. Skips when Postgres is not running.
func DB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, _ := DBWithDSN(t)
	return pool
}

// DBWithDSN is DB plus the database's DSN, for code that opens its own
// connection (the trial-balance command). The DSN's user is a superuser.
func DBWithDSN(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	if templateURL == nil {
		t.Skip("postgres is not running (-short, or the package lacks TestMain calling RunWithPostgres)")
	}
	ctx := context.Background()
	name := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	createMu.Lock()
	err := adminExec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+templateDB)
	createMu.Unlock()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	pool, err := pgxx.NewPool(ctx, dsnFor(name))
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_ = adminExec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return pool, dsnFor(name)
}

func adminExec(ctx context.Context, sql string) error {
	conn, err := pgx.Connect(ctx, dsnFor("postgres"))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

func dsnFor(db string) string {
	u := *templateURL
	u.Path = "/" + db
	return u.String()
}

// InsertCorruptTransaction writes, with triggers disabled, a transaction with
// one +100 entry on a new liability account: total 100, one unbalanced
// transaction, one liability in debit. Only trial-balance tests use it.
func InsertCorruptTransaction(t testing.TB, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	acc, tx := uuid.New(), uuid.New()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`SET session_replication_role = replica`, nil},
		{`INSERT INTO accounts (id, code, type) VALUES ($1, $2, 'liability')`, []any{acc, "corrupt:" + acc.String()}},
		{`INSERT INTO journal_transactions (id, idempotency_key, kind, occurred_at) VALUES ($1, $2, 'PAY', now())`, []any{tx, "corrupt:" + tx.String()}},
		{`INSERT INTO entries (id, transaction_id, account_id, amount) VALUES ($1, $2, $3, 100)`, []any{uuid.New(), tx, acc}},
		{`SET session_replication_role = origin`, nil},
	} {
		_, err := conn.Exec(ctx, stmt.sql, stmt.args...)
		require.NoError(t, err, stmt.sql)
	}
}
