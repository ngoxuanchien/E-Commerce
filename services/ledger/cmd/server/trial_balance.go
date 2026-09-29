package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

// trialBalance runs `server trial-balance` (WS2' §8). It prints the report as
// JSON to stdout and returns 0 when the ledger is consistent; 1 when it is
// not, or when the check cannot run (errors go to stderr).
func trialBalance(ctx context.Context, dsn string, stdout, stderr io.Writer) int {
	if dsn == "" {
		_, _ = fmt.Fprintln(stderr, "trial-balance: DATABASE_URL is not set")
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxx.NewPool(ctx, dsn)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trial-balance:", err)
		return 1
	}
	defer pool.Close()
	report, err := postgres.NewStore(pool).TrialBalance(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trial-balance:", err)
		return 1
	}
	out := struct {
		app.TrialBalance
		OK bool `json:"ok"`
	}{report, report.OK()}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if !report.OK() {
		return 1
	}
	return 0
}
