package idemkey_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

const scope = "payment.create"

var base = time.Now().UTC().Truncate(time.Microsecond)
var never time.Time

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, testutil.Postgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, idemkey.SchemaSQL)
	require.NoError(t, err)
	return pool
}

func TestClaimCompleteReplay(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000001"
	rec, created, err := idemkey.Claim(ctx, db, scope, user, "k1", []byte("h"), base, never)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, base, rec.ClaimedAt)

	_, created, err = idemkey.Claim(ctx, db, scope, user, "k1", []byte("h"), base, never)
	require.NoError(t, err)
	assert.False(t, created, "in progress")

	resp := idemkey.Response{Status: 201, Body: []byte(`{"id":"p1"}`)}
	require.NoError(t, idemkey.Complete(ctx, db, scope, user, "k1", base, resp))
	rec, created, err = idemkey.Claim(ctx, db, scope, user, "k1", []byte("h"), base, never)
	require.NoError(t, err)
	assert.False(t, created)
	require.NotNil(t, rec.Response)
	assert.Equal(t, 201, rec.Response.Status)
	assert.JSONEq(t, `{"id":"p1"}`, string(rec.Response.Body))
}

func TestDifferentHashIsReportedForReuse(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000002"
	_, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("a"), base, never)
	require.NoError(t, err)
	rec, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("b"), base, base.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, created, "a different request never takes a key over, even when stale")
	assert.Equal(t, []byte("a"), rec.RequestHash, "the caller compares hashes and answers IDEMPOTENCY_KEY_REUSED")
}

func TestKeysAreScopedByScopeAndUser(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	u1, u2 := "01890000-0000-7000-8000-000000000003", "01890000-0000-7000-8000-000000000004"
	for _, c := range []struct{ scope, user string }{{scope, u1}, {scope, u2}, {"ledger.payout", u1}} {
		_, created, err := idemkey.Claim(ctx, db, c.scope, c.user, "same", []byte("h"), base, never)
		require.NoError(t, err)
		assert.True(t, created, "%+v", c)
	}
}

func TestOrphanIsReclaimedOnlyWhenStale(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000005"
	old := base.Add(-10 * time.Minute)
	_, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), old, never)
	require.NoError(t, err)

	_, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, old)
	require.NoError(t, err)
	assert.False(t, created, "not older than staleBefore")

	rec, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, base.Add(-5*time.Minute))
	require.NoError(t, err)
	assert.True(t, created, "same request, no response, stale → reclaimed")
	assert.Equal(t, base, rec.ClaimedAt)

	// The zombie that held `old` cannot complete or release the new claim.
	err = idemkey.Complete(ctx, db, scope, user, "k", old, idemkey.Response{Status: 201, Body: []byte(`{}`)})
	assert.ErrorIs(t, err, idemkey.ErrClaimLost)
	require.NoError(t, idemkey.Release(ctx, db, scope, user, "k", old))
	rec, found, err := idemkey.Get(ctx, db, scope, user, "k")
	require.NoError(t, err)
	require.True(t, found, "the zombie's release was a no-op")
	assert.Equal(t, base, rec.ClaimedAt)
	assert.Nil(t, rec.Response, "nor did its completion land")
}

func TestCompletedKeyIsNeverReclaimed(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000006"
	old := base.Add(-time.Hour)
	_, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), old, never)
	require.NoError(t, err)
	require.NoError(t, idemkey.Complete(ctx, db, scope, user, "k", old, idemkey.Response{Status: 409, Body: []byte(`{}`)}))
	rec, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, base)
	require.NoError(t, err)
	assert.False(t, created)
	assert.NotNil(t, rec.Response)
}

func TestGetNeverInserts(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000007"
	for range 2 {
		_, found, err := idemkey.Get(ctx, db, scope, user, "missing")
		require.NoError(t, err)
		assert.False(t, found)
	}
}

func TestReleaseFreesTheKeyAndPurgeDeletesOldRows(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000008"
	_, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, never)
	require.NoError(t, err)
	require.NoError(t, idemkey.Release(ctx, db, scope, user, "k", base))
	_, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, never)
	require.NoError(t, err)
	assert.True(t, created)

	_, _, err = idemkey.Claim(ctx, db, scope, user, "old", []byte("h"), base.Add(-25*time.Hour), never)
	require.NoError(t, err)
	n, err := idemkey.Purge(ctx, db, base.Add(-24*time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))
	_, found, err := idemkey.Get(ctx, db, scope, user, "old")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestClaimTruncatesToMicroseconds(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000009"
	rec, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base.Add(123*time.Nanosecond), never)
	require.NoError(t, err)
	require.NoError(t, idemkey.Complete(ctx, db, scope, user, "k", rec.ClaimedAt, idemkey.Response{Status: 201, Body: []byte(`{}`)}),
		"the returned claim matches the stored µs timestamp")
}
