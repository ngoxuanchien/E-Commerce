package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

func TestRetryableMarksDeadlocksAndSerializationFailures(t *testing.T) {
	for _, code := range []string{"40P01", "40001"} {
		err := retryable(fmt.Errorf("tx: %w", &pgconn.PgError{Code: code}))
		assert.ErrorIs(t, err, domain.ErrRetryable, code)
		var pe *pgconn.PgError
		assert.True(t, errors.As(err, &pe), "cause is kept")
	}
	other := &pgconn.PgError{Code: "23505"}
	assert.NotErrorIs(t, retryable(other), domain.ErrRetryable)
	assert.NoError(t, retryable(nil))
}
