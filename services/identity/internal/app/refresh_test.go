package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
)

func TestRefreshAndLogout(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	login := func(t *testing.T) (domain.User, app.Session) {
		t.Helper()
		u, password := f.register(t)
		sess, err := f.auth.Login(ctx, u.Email, password)
		require.NoError(t, err)
		return u, sess
	}

	t.Run("refresh rotates within the same family", func(t *testing.T) {
		_, first := login(t)
		second, err := f.auth.Refresh(ctx, first.RefreshToken)
		require.NoError(t, err)
		assert.NotEqual(t, first.RefreshToken, second.RefreshToken)
		assert.Equal(t, f.familyOf(t, first.RefreshToken), f.familyOf(t, second.RefreshToken))
		assert.Equal(t, first.User.ID, second.User.ID)
	})

	t.Run("refresh re-reads roles from the database", func(t *testing.T) {
		u, sess := login(t)
		require.NoError(t, f.store.GrantRole(ctx, u.ID, domain.RoleSeller))
		next, err := f.auth.Refresh(ctx, sess.RefreshToken)
		require.NoError(t, err)
		claims := testsupport.ParseAccessToken(t, next.AccessToken, f.signer.JWKS())
		assert.Equal(t, []string{"buyer", "seller"}, claims.Roles)
		assert.Equal(t, []domain.Role{domain.RoleBuyer, domain.RoleSeller}, next.User.Roles)
	})

	t.Run("reusing a rotated token revokes the whole family", func(t *testing.T) {
		_, first := login(t)
		second, err := f.auth.Refresh(ctx, first.RefreshToken)
		require.NoError(t, err)

		_, err = f.auth.Refresh(ctx, first.RefreshToken)
		assert.ErrorIs(t, err, domain.ErrRefreshTokenReused)

		_, err = f.auth.Refresh(ctx, second.RefreshToken)
		assert.Error(t, err, "the newest token of the family is revoked too")

		assert.Contains(t, f.logs.String(), "refresh token reuse detected")
		assert.NotContains(t, f.logs.String(), first.RefreshToken)
	})

	t.Run("another family of the same user survives", func(t *testing.T) {
		u, password := f.register(t)
		a, err := f.auth.Login(ctx, u.Email, password)
		require.NoError(t, err)
		b, err := f.auth.Login(ctx, u.Email, password)
		require.NoError(t, err)
		_, err = f.auth.Refresh(ctx, a.RefreshToken)
		require.NoError(t, err)
		_, err = f.auth.Refresh(ctx, a.RefreshToken) // reuse in family A
		require.ErrorIs(t, err, domain.ErrRefreshTokenReused)
		_, err = f.auth.Refresh(ctx, b.RefreshToken)
		assert.NoError(t, err, "family B is untouched")
	})

	t.Run("expired token is invalid", func(t *testing.T) {
		_, sess := login(t)
		f.clock.Advance(refreshTTL + time.Second)
		defer f.clock.Advance(-(refreshTTL + time.Second))
		_, err := f.auth.Refresh(ctx, sess.RefreshToken)
		assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
	})

	t.Run("missing or unknown token is invalid", func(t *testing.T) {
		_, err := f.auth.Refresh(ctx, "")
		assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
		_, err = f.auth.Refresh(ctx, "garbage")
		assert.ErrorIs(t, err, domain.ErrInvalidRefreshToken)
	})

	t.Run("concurrent refreshes with one token leave no live token", func(t *testing.T) {
		_, sess := login(t)
		const n = 5
		results := make([]app.Session, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() { results[i], errs[i] = f.auth.Refresh(ctx, sess.RefreshToken) })
		}
		wg.Wait()

		var winners []app.Session
		for i, err := range errs {
			if err == nil {
				winners = append(winners, results[i])
			} else {
				assert.ErrorIs(t, err, domain.ErrRefreshTokenReused)
			}
		}
		require.Len(t, winners, 1, "exactly one refresh wins the row lock")
		_, err := f.auth.Refresh(ctx, winners[0].RefreshToken)
		assert.Error(t, err, "reuse by the losers revoked the winner's token as well")
	})

	t.Run("logout revokes the token", func(t *testing.T) {
		_, sess := login(t)
		require.NoError(t, f.auth.Logout(ctx, sess.RefreshToken))
		_, err := f.auth.Refresh(ctx, sess.RefreshToken)
		assert.ErrorIs(t, err, domain.ErrRefreshTokenReused)
	})

	t.Run("logout without a valid token is a no-op", func(t *testing.T) {
		assert.NoError(t, f.auth.Logout(ctx, ""))
		assert.NoError(t, f.auth.Logout(ctx, "garbage"))
	})
}
