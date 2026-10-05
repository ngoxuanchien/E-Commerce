package api_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// trialBalance executes the ledger service trial-balance command.
// Returns combined output; error on non-zero exit.
func trialBalance(ctx context.Context) ([]byte, error) {
	repo := filepath.Join("..", "..")
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f",
		filepath.Join(repo, "deploy", "docker-compose.yml"),
		"exec", "-T", "ledger", "/server", "trial-balance")
	return cmd.CombinedOutput()
}

// assertTrialBalance checks that the ledger trial balance is zero.
func assertTrialBalance(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := trialBalance(ctx)
	require.NoError(t, err, "trial balance:\n%s", out)
}

// TestTrialBalanceFailsLoudlyWithoutStack verifies that trialBalance errors
// when the stack is not running.
func TestTrialBalanceFailsLoudlyWithoutStack(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "no-such-stack-"+runID())
	_, err := trialBalance(ctx(t))
	require.Error(t, err)
}

// TestTrialBalanceIsZero verifies that the trial balance is zero at the start.
func TestTrialBalanceIsZero(t *testing.T) {
	assertTrialBalance(t)
}
