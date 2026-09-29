package app_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

const refreshTTL = time.Hour

type fixture struct {
	store  *postgres.Store
	signer *token.Signer
	clock  *testsupport.Clock
	logs   *bytes.Buffer
	auth   *app.AuthService
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store:  postgres.NewStore(testsupport.Pool(t)),
		signer: token.NewSigner(testsupport.RSAKey(t), 15*time.Minute),
		clock:  testsupport.NewClock(time.Now().UTC()),
		logs:   &bytes.Buffer{},
	}
	auth, err := app.NewAuthService(f.store, f.signer, app.AuthConfig{
		RefreshTTL: refreshTTL, BcryptCost: bcrypt.MinCost, Now: f.clock.Now,
		Logger: slog.New(slog.NewJSONHandler(f.logs, nil)),
	})
	require.NoError(t, err)
	f.auth = auth
	return f
}

func (f *fixture) register(t *testing.T) (domain.User, string) {
	t.Helper()
	const password = "correct horse battery"
	u, err := f.auth.Register(context.Background(), testsupport.Email(), password, "Test User")
	require.NoError(t, err)
	return u, password
}

// familyOf returns the family of the stored token behind raw.
func (f *fixture) familyOf(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	var family uuid.UUID
	require.NoError(t, f.store.InTx(context.Background(), func(tx app.Tx) error {
		rt, err := tx.RefreshTokenForUpdate(context.Background(), token.HashRefreshToken(raw))
		family = rt.FamilyID
		return err
	}))
	return family
}

func TestRegisterLoginMe(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	t.Run("register normalizes, assigns buyer and hashes the password", func(t *testing.T) {
		email := testsupport.Email()
		u, err := f.auth.Register(ctx, "  "+strings.ToUpper(email)+" ", "correct horse", " Alice ")
		require.NoError(t, err)
		assert.Equal(t, email, u.Email)
		assert.Equal(t, "Alice", u.FullName)
		assert.Equal(t, []domain.Role{domain.RoleBuyer}, u.Roles)
		assert.Equal(t, 7, int(u.ID.Version()), "ids are UUIDv7")

		_, hash, err := f.store.UserByEmail(ctx, email)
		require.NoError(t, err)
		assert.NotEqual(t, "correct horse", hash)
		assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse")))
	})

	t.Run("duplicate email in another case is taken", func(t *testing.T) {
		u, _ := f.register(t)
		_, err := f.auth.Register(ctx, strings.ToUpper(u.Email), "correct horse", "Dup")
		assert.ErrorIs(t, err, domain.ErrEmailTaken)
	})

	t.Run("invalid input is a validation error", func(t *testing.T) {
		_, err := f.auth.Register(ctx, "nope", "short", "")
		var ve *domain.ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Len(t, ve.Fields, 3)
	})

	t.Run("login issues an access token and a refresh token", func(t *testing.T) {
		u, password := f.register(t)
		sess, err := f.auth.Login(ctx, u.Email, password)
		require.NoError(t, err)
		assert.Equal(t, 15*time.Minute, sess.ExpiresIn)
		assert.NotEmpty(t, sess.RefreshToken)
		assert.Equal(t, u.ID, sess.User.ID)

		claims := testsupport.ParseAccessToken(t, sess.AccessToken, f.signer.JWKS())
		assert.Equal(t, u.ID.String(), claims.Subject)
		assert.Equal(t, []string{"buyer"}, claims.Roles)
		assert.Equal(t, 15*time.Minute, claims.ExpiresAt.Sub(claims.IssuedAt.Time))
	})

	t.Run("login accepts any email casing and surrounding spaces", func(t *testing.T) {
		u, password := f.register(t)
		_, err := f.auth.Login(ctx, " "+strings.ToUpper(u.Email)+" ", password)
		assert.NoError(t, err)
	})

	t.Run("wrong password and unknown email fail identically", func(t *testing.T) {
		u, _ := f.register(t)
		_, errWrong := f.auth.Login(ctx, u.Email, "wrong password")
		_, errUnknown := f.auth.Login(ctx, testsupport.Email(), "wrong password")
		assert.ErrorIs(t, errWrong, domain.ErrInvalidCredentials)
		assert.ErrorIs(t, errUnknown, domain.ErrInvalidCredentials)
		assert.Equal(t, errWrong.Error(), errUnknown.Error())
	})

	t.Run("a password past 72 bytes never matches its prefix", func(t *testing.T) {
		prefix := strings.Repeat("p", domain.MaxPasswordBytes)
		u, err := f.auth.Register(ctx, testsupport.Email(), prefix, "Long")
		require.NoError(t, err)
		_, err = f.auth.Login(ctx, u.Email, prefix+"extra")
		assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
	})

	t.Run("each login starts a new token family", func(t *testing.T) {
		u, password := f.register(t)
		a, err := f.auth.Login(ctx, u.Email, password)
		require.NoError(t, err)
		b, err := f.auth.Login(ctx, u.Email, password)
		require.NoError(t, err)
		assert.NotEqual(t, f.familyOf(t, a.RefreshToken), f.familyOf(t, b.RefreshToken))
	})

	t.Run("me returns the user with current roles", func(t *testing.T) {
		u, _ := f.register(t)
		require.NoError(t, f.store.GrantRole(ctx, u.ID, domain.RoleSeller))
		got, err := f.auth.Me(ctx, u.ID.String())
		require.NoError(t, err)
		assert.Equal(t, []domain.Role{domain.RoleBuyer, domain.RoleSeller}, got.Roles)
	})

	t.Run("me with a malformed or unknown id is not found", func(t *testing.T) {
		_, err := f.auth.Me(ctx, "not-a-uuid")
		assert.ErrorIs(t, err, domain.ErrNotFound)
		_, err = f.auth.Me(ctx, uuid.Must(uuid.NewV7()).String())
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}
