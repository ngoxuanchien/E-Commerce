package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
)

func TestStore(t *testing.T) {
	store := postgres.NewStore(testsupport.Pool(t))
	ctx := context.Background()

	newUser := func(t *testing.T) domain.User {
		t.Helper()
		u, err := store.CreateUser(ctx, domain.User{
			ID: uuid.Must(uuid.NewV7()), Email: testsupport.Email(), FullName: "Test User",
			Roles: []domain.Role{domain.RoleBuyer},
		}, "bcrypt-hash")
		require.NoError(t, err)
		return u
	}
	newToken := func(u domain.User, family uuid.UUID) domain.RefreshToken {
		return domain.RefreshToken{
			ID: uuid.Must(uuid.NewV7()), UserID: u.ID, FamilyID: family,
			Hash: []byte(uuid.NewString()), ExpiresAt: time.Now().Add(time.Hour).UTC(),
		}
	}
	lookup := func(t *testing.T, hash []byte) (domain.RefreshToken, error) {
		t.Helper()
		var got domain.RefreshToken
		err := store.InTx(ctx, func(tx app.Tx) error {
			var err error
			got, err = tx.RefreshTokenForUpdate(ctx, hash)
			return err
		})
		return got, err
	}

	t.Run("create and read user", func(t *testing.T) {
		u := newUser(t)
		assert.False(t, u.CreatedAt.IsZero())
		assert.Equal(t, time.UTC, u.CreatedAt.Location())

		got, hash, err := store.UserByEmail(ctx, u.Email)
		require.NoError(t, err)
		assert.Equal(t, "bcrypt-hash", hash)
		assert.Equal(t, u.ID, got.ID)
		assert.Equal(t, "Test User", got.FullName)
		assert.Equal(t, []domain.Role{domain.RoleBuyer}, got.Roles)

		byID, err := store.UserByID(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, u.Email, byID.Email)
		assert.True(t, u.CreatedAt.Equal(byID.CreatedAt))
	})

	t.Run("email uniqueness ignores case", func(t *testing.T) {
		u := newUser(t)
		_, err := store.CreateUser(ctx, domain.User{
			ID: uuid.Must(uuid.NewV7()), Email: strings.ToUpper(u.Email), FullName: "Dup",
			Roles: []domain.Role{domain.RoleBuyer},
		}, "h")
		assert.ErrorIs(t, err, domain.ErrEmailTaken)
	})

	t.Run("unknown user", func(t *testing.T) {
		_, _, err := store.UserByEmail(ctx, testsupport.Email())
		assert.ErrorIs(t, err, domain.ErrNotFound)
		_, err = store.UserByID(ctx, uuid.Must(uuid.NewV7()))
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("grant role is idempotent", func(t *testing.T) {
		u := newUser(t)
		require.NoError(t, store.GrantRole(ctx, u.ID, domain.RoleSeller))
		require.NoError(t, store.GrantRole(ctx, u.ID, domain.RoleSeller))
		got, err := store.UserByID(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, []domain.Role{domain.RoleBuyer, domain.RoleSeller}, got.Roles)
	})

	t.Run("grant role to unknown user", func(t *testing.T) {
		err := store.GrantRole(ctx, uuid.Must(uuid.NewV7()), domain.RoleSeller)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("rotate and revoke family", func(t *testing.T) {
		u := newUser(t)
		family := uuid.Must(uuid.NewV7())
		first, second := newToken(u, family), newToken(u, family)
		require.NoError(t, store.CreateRefreshToken(ctx, first))

		now := time.Now().UTC()
		err := store.InTx(ctx, func(tx app.Tx) error {
			got, err := tx.RefreshTokenForUpdate(ctx, first.Hash)
			if err != nil {
				return err
			}
			if got.RevokedAt != nil || got.FamilyID != family || got.UserID != u.ID {
				return errors.New("unexpected token state")
			}
			if err := tx.CreateRefreshToken(ctx, second); err != nil {
				return err
			}
			return tx.ReplaceRefreshToken(ctx, first.ID, second.ID, now)
		})
		require.NoError(t, err)

		got, err := lookup(t, first.Hash)
		require.NoError(t, err)
		require.NotNil(t, got.RevokedAt)
		got, err = lookup(t, second.Hash)
		require.NoError(t, err)
		assert.Nil(t, got.RevokedAt)
		assert.True(t, second.ExpiresAt.Truncate(time.Microsecond).Equal(got.ExpiresAt))

		require.NoError(t, store.InTx(ctx, func(tx app.Tx) error { return tx.RevokeRefreshFamily(ctx, family, now) }))
		got, err = lookup(t, second.Hash)
		require.NoError(t, err)
		assert.NotNil(t, got.RevokedAt)
	})

	t.Run("revoke by hash", func(t *testing.T) {
		u := newUser(t)
		tok := newToken(u, uuid.Must(uuid.NewV7()))
		require.NoError(t, store.CreateRefreshToken(ctx, tok))
		require.NoError(t, store.RevokeRefreshToken(ctx, tok.Hash, time.Now()))
		got, err := lookup(t, tok.Hash)
		require.NoError(t, err)
		assert.NotNil(t, got.RevokedAt)
		assert.NoError(t, store.RevokeRefreshToken(ctx, []byte("unknown"), time.Now()), "unknown hash is a no-op")
	})

	t.Run("unknown token", func(t *testing.T) {
		_, err := lookup(t, []byte("missing"))
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("InTx rolls back on error", func(t *testing.T) {
		u := newUser(t)
		tok := newToken(u, uuid.Must(uuid.NewV7()))
		boom := errors.New("boom")
		err := store.InTx(ctx, func(tx app.Tx) error {
			if err := tx.CreateRefreshToken(ctx, tok); err != nil {
				return err
			}
			return boom
		})
		require.ErrorIs(t, err, boom)
		_, err = lookup(t, tok.Hash)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}
