# WS1 identity-svc Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the WS0 `services/identity` skeleton into a working identity-svc: registration, login, rotating refresh tokens with reuse detection, `GET /api/users/me`, JWKS, and the `GrantRole` gRPC — all matching `api/openapi/identity.yaml` and `proto/identity/v1`.

**Architecture:** Hexagonal layout inside `services/identity/internal`: `domain` (types, validation, sentinel errors) ← `app` (use cases + storage ports) ← adapters (`postgres` via sqlc, `http` via chi, `grpc`). `token` holds JWT signing/JWKS and refresh-token generation. `cmd/server` wires everything into the existing WS0 `newHandler`/`server.Run` skeleton.

**Tech Stack:** Go 1.27.1, chi v5, golang-jwt/jwt v5, golang.org/x/crypto/bcrypt, pgx v5 + sqlc v1.31.1 (`./bin/sqlc`), golang-migrate (via `pkg/pgxx`), gRPC (via `pkg/grpcx`), testify, testcontainers (via `pkg/testutil`), kin-openapi contract validation (`testutil.ContractTransport`).

**Spec:** `docs/superpowers/specs/2026-09-28-marketplace-p1-ws1-identity.md` (contracts in `docs/superpowers/specs/2026-09-28-marketplace-p1-ws0-foundation.md`, overview in `...-overview.md`).

**Branch / worktree:** `ws01-identify-svc` in `/Users/xuanchien/orca/workspaces/E-Commerce/ws01-identify-svc`. All paths below are relative to the repo root unless stated.

## Global Constraints

- Only modify `services/identity/**` and this plan file. Never edit `api/`, `proto/`, `gen/`, `pkg/`, `deploy/`, `Makefile`, or other services. A needed contract/shared-lib change → stop and report it.
- All code, comments, docs and commit messages in English.
- Module path `github.com/ngoxuanchien/e-commerce/services/identity`; Go `1.27.1` (already pinned in `go.mod`).
- IDs are UUIDv7 strings; timestamps UTC RFC 3339; JSON `snake_case`.
- Error body `{"error":{"code","message","details"}}` via `pkg/httpx`; `VALIDATION_ERROR` → `details.fields` maps JSON field names to messages.
- Env: `JWT_PRIVATE_KEY_PATH` (PEM), `ACCESS_TOKEN_TTL=15m`, `REFRESH_TOKEN_TTL=720h`, `COOKIE_SECURE` (default true; compose sets `"false"`).
- Password 8–72 chars and ≤ 72 bytes (bcrypt limit); bcrypt cost 12 in production. `full_name` 1–100 chars. Email trimmed + lowercased + format-checked, ≤ 254 chars.
- Access token: RS256, header `kid` = RFC 7638 SHA-256 thumbprint truncated to 16 base64url chars; claims `iss="identity"`, `sub`, `roles`, `iat`, `exp=iat+15m`.
- Refresh token: 32 random bytes, base64url; only SHA-256 stored. Cookie `refresh_token; HttpOnly; Secure(unless COOKIE_SECURE=false); SameSite=Lax; Path=/api/auth; Max-Age=2592000`; cleared with `Max-Age=0`.
- Passwords, password hashes, access tokens and refresh tokens are never logged or returned (except the tokens in their own response fields/cookie).
- Integration tests use `pkg/testutil.Postgres` and validate every contract-conforming request/response with `testutil.ContractTransport(t, ".../api/openapi/identity.yaml", nil)`.
- Lint: repo `.golangci.yml` (standard + bodyclose, errorlint, misspell, unconvert; gofmt/goimports with local prefix `github.com/ngoxuanchien/e-commerce`). Close bodies as `defer func() { _ = resp.Body.Close() }()`.

## Review Focus

1. **Two refreshes racing with the same cookie (two tabs).** The token row is locked (`FOR UPDATE`); exactly one caller gets a new token, the other hits reuse detection, and the family ends fully revoked — never two live tokens in one family. Pinned in Task 5.
2. **Email case / whitespace.** `" Alice@Example.COM "` registers as `alice@example.com`; login with any casing works; registering `ALICE@example.com` afterwards is 409 (citext + normalization). Pinned in Tasks 1, 3, 4.
3. **Multibyte passwords past bcrypt's 72-byte limit.** 25 × `ệ` (25 chars, 75 bytes) is rejected with 400 at registration rather than silently truncated; at login a >72-byte password never matches its 72-byte prefix. Pinned in Tasks 1, 4, 7.
4. **Missing / garbage refresh cookie.** `/refresh` → 401 + clearing `Set-Cookie`; `/logout` → 204 + clearing cookie; never 500. Pinned in Task 7.
5. **Identity that no longer (or never) maps to a user.** `/api/users/me` with a non-UUID or unknown `X-User-Id` → 401, not 500; `GrantRole` with a non-UUID `user_id` → `INVALID_ARGUMENT`, not `INTERNAL`. Pinned in Tasks 6, 7.

## File Structure

```
services/identity/
  go.mod / go.sum                               + chi, jwt, uuid, x/crypto, pgx, gen, grpc (Tasks 1–8)
  sqlc.yaml                                     + uuid/timestamptz overrides (Task 3)
  migrations/000002_identity.{up,down}.sql      schema (Task 3)
  internal/domain/{user,errors,validation}.go   types, sentinels, input validation (Task 1)
  internal/token/{key,signer,jwks,refresh}.go   PEM loading, RS256 signer, JWKS, refresh tokens (Task 2)
  internal/testsupport/{keys,postgres,clock,http}.go   shared test helpers (Tasks 2, 3, 4, 7)
  internal/app/ports.go                         Store / Tx / RoleStore interfaces (Task 3)
  internal/adapter/postgres/queries/*.sql       sqlc queries (Task 3)
  internal/adapter/postgres/sqlcdb/*            generated (Task 3, committed)
  internal/adapter/postgres/{store,errors}.go   app.Store implementation (Task 3)
  internal/app/auth.go                          AuthService: Register, Login, Me (Task 4); Refresh, Logout (Task 5)
  internal/app/roles.go                         RoleService.Grant (Task 6)
  internal/adapter/grpc/server.go               IdentityInternal server, package grpcapi (Task 6)
  internal/adapter/http/{router,handlers,dto,errors}.go   chi REST API, package httpapi (Task 7)
  cmd/server/{main,components}.go               config + wiring (Task 8)
```

---

### Task 1: Domain types and input validation

**Files:**
- Create: `services/identity/internal/domain/user.go`
- Create: `services/identity/internal/domain/errors.go`
- Create: `services/identity/internal/domain/validation.go`
- Test: `services/identity/internal/domain/validation_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Role string`; `RoleBuyer`, `RoleSeller`, `RoleAdmin`.
  - `type User struct { ID uuid.UUID; Email, FullName string; Roles []Role; CreatedAt time.Time }`
  - `type RefreshToken struct { ID, UserID, FamilyID uuid.UUID; Hash []byte; ExpiresAt time.Time; RevokedAt *time.Time }`
  - Sentinels `ErrNotFound`, `ErrEmailTaken`, `ErrInvalidCredentials`, `ErrInvalidRefreshToken`, `ErrRefreshTokenReused`, `ErrRoleNotGrantable`.
  - `type ValidationError struct { Fields map[string]string }` (pointer receiver `Error()`).
  - `const MaxEmailLength = 254; MinPasswordLength = 8; MaxPasswordBytes = 72; MaxFullNameLength = 100`
  - `func NormalizeEmail(s string) string`
  - `type Registration struct { Email, Password, FullName string }`; `func NewRegistration(email, password, fullName string) (Registration, error)` — error is `*ValidationError`.

- [x] **Step 1: Add the uuid dependency**

Run: `cd services/identity && go get github.com/google/uuid@v1.6.0`
Expected: `go.mod` lists `github.com/google/uuid v1.6.0` (may stay `// indirect` until code imports it; `go mod tidy` in Step 5 fixes that).

- [x] **Step 2: Write the failing test**

`services/identity/internal/domain/validation_test.go`:

```go
package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

func TestNewRegistrationNormalizes(t *testing.T) {
	r, err := domain.NewRegistration("  Alice@Example.COM ", "correct horse", "  Alice Nguyen ")
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", r.Email)
	assert.Equal(t, "Alice Nguyen", r.FullName)
	assert.Equal(t, "correct horse", r.Password, "passwords are never trimmed")
}

func TestNewRegistrationAcceptsBoundaries(t *testing.T) {
	tests := []struct{ name, password, fullName string }{
		{"8-char password", "12345678", "A"},
		{"72-byte password", strings.Repeat("a", 72), "A"},
		{"8 multibyte chars", strings.Repeat("ệ", 8), "A"},
		{"100-char Vietnamese name", "password", strings.Repeat("ễ", 100)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewRegistration("a@example.com", tc.password, tc.fullName)
			assert.NoError(t, err)
		})
	}
}

func TestNewRegistrationRejects(t *testing.T) {
	const okPwd, okName, okEmail = "password1", "Alice", "a@example.com"
	tests := []struct{ name, email, password, fullName, field, msg string }{
		{"missing email", "", okPwd, okName, "email", "is required"},
		{"not an email", "not-an-email", okPwd, okName, "email", "must be a valid email"},
		{"display name form", "Alice <alice@example.com>", okPwd, okName, "email", "must be a valid email"},
		{"two at signs", "a@b@example.com", okPwd, okName, "email", "must be a valid email"},
		{"email too long", strings.Repeat("a", 250) + "@x.io", okPwd, okName, "email", "must be at most 254 characters"},
		{"missing password", okEmail, "", okName, "password", "is required"},
		{"short password", okEmail, "1234567", okName, "password", "must be at least 8 characters"},
		{"password over 72 bytes", okEmail, strings.Repeat("a", 73), okName, "password", "must be at most 72 bytes"},
		{"multibyte password over 72 bytes", okEmail, strings.Repeat("ệ", 25), okName, "password", "must be at most 72 bytes"},
		{"blank full name", okEmail, okPwd, "   ", "full_name", "is required"},
		{"full name too long", okEmail, okPwd, strings.Repeat("a", 101), "full_name", "must be at most 100 characters"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewRegistration(tc.email, tc.password, tc.fullName)
			var ve *domain.ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, map[string]string{tc.field: tc.msg}, ve.Fields)
		})
	}
}

func TestNewRegistrationReportsEveryField(t *testing.T) {
	_, err := domain.NewRegistration("", "", "")
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Len(t, ve.Fields, 3)
}

func TestNormalizeEmail(t *testing.T) {
	assert.Equal(t, "bob@example.com", domain.NormalizeEmail("\tBOB@example.com \n"))
}
```

- [x] **Step 3: Run test to verify it fails**

Run: `cd services/identity && go test ./internal/domain/...`
Expected: FAIL — build error, `undefined: domain.NewRegistration`.

- [x] **Step 4: Write the implementation**

`services/identity/internal/domain/user.go`:

```go
// Package domain holds identity-svc's core types, validation rules and errors.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role is an authorization role carried in access tokens.
type Role string

// Roles known to the marketplace.
const (
	RoleBuyer  Role = "buyer"
	RoleSeller Role = "seller"
	RoleAdmin  Role = "admin"
)

// User is an account as seen by the application. It never carries the password hash.
type User struct {
	ID        uuid.UUID
	Email     string
	FullName  string
	Roles     []Role
	CreatedAt time.Time
}

// RefreshToken is a stored refresh token. Only the SHA-256 hash of the raw
// token is kept; tokens rotated from the same login share a FamilyID.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	Hash      []byte
	ExpiresAt time.Time
	RevokedAt *time.Time
}
```

`services/identity/internal/domain/errors.go`:

```go
package domain

import "errors"

// Sentinel errors returned by the application layer and mapped by adapters.
var (
	ErrNotFound            = errors.New("not found")
	ErrEmailTaken          = errors.New("email already registered")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrRefreshTokenReused  = errors.New("refresh token reused")
	ErrRoleNotGrantable    = errors.New("role cannot be granted")
)

// ValidationError lists invalid input fields (JSON field name → message).
// Messages never contain the submitted values.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return "validation failed" }
```

`services/identity/internal/domain/validation.go`:

```go
package domain

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// Input limits (WS1 §3).
const (
	MaxEmailLength    = 254
	MinPasswordLength = 8
	// MaxPasswordBytes is bcrypt's input limit; longer input would be silently truncated.
	MaxPasswordBytes  = 72
	MaxFullNameLength = 100
)

// NormalizeEmail trims surrounding whitespace and lowercases an email address.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Registration is validated, normalized sign-up input.
type Registration struct {
	Email    string
	Password string
	FullName string
}

// NewRegistration normalizes and validates sign-up input. Every invalid field
// is reported at once in a *ValidationError.
func NewRegistration(email, password, fullName string) (Registration, error) {
	r := Registration{Email: NormalizeEmail(email), Password: password, FullName: strings.TrimSpace(fullName)}
	fields := map[string]string{}
	if msg := emailProblem(r.Email); msg != "" {
		fields["email"] = msg
	}
	if msg := passwordProblem(r.Password); msg != "" {
		fields["password"] = msg
	}
	if msg := fullNameProblem(r.FullName); msg != "" {
		fields["full_name"] = msg
	}
	if len(fields) > 0 {
		return Registration{}, &ValidationError{Fields: fields}
	}
	return r, nil
}

func emailProblem(e string) string {
	if e == "" {
		return "is required"
	}
	if len(e) > MaxEmailLength {
		return fmt.Sprintf("must be at most %d characters", MaxEmailLength)
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Name != "" || addr.Address != e {
		return "must be a valid email"
	}
	return ""
}

func passwordProblem(p string) string {
	switch {
	case p == "":
		return "is required"
	case utf8.RuneCountInString(p) < MinPasswordLength:
		return fmt.Sprintf("must be at least %d characters", MinPasswordLength)
	case len(p) > MaxPasswordBytes:
		return fmt.Sprintf("must be at most %d bytes", MaxPasswordBytes)
	}
	return ""
}

func fullNameProblem(n string) string {
	switch {
	case n == "":
		return "is required"
	case utf8.RuneCountInString(n) > MaxFullNameLength:
		return fmt.Sprintf("must be at most %d characters", MaxFullNameLength)
	}
	return ""
}
```

- [x] **Step 5: Run test to verify it passes**

Run: `cd services/identity && go mod tidy && go test ./internal/domain/... -v`
Expected: PASS for all subtests. If `two at signs` unexpectedly passes validation, add an explicit `strings.Count(e, "@") != 1` check to `emailProblem` and re-run.

- [x] **Step 6: Commit**

```bash
git add services/identity/go.mod services/identity/go.sum services/identity/internal/domain
git commit -m "feat(identity): add domain types and registration validation"
```

---

### Task 2: Token package — RSA key loading, RS256 signer, JWKS, refresh tokens

**Files:**
- Create: `services/identity/internal/token/key.go`
- Create: `services/identity/internal/token/signer.go`
- Create: `services/identity/internal/token/jwks.go`
- Create: `services/identity/internal/token/refresh.go`
- Create: `services/identity/internal/testsupport/keys.go`
- Test: `services/identity/internal/token/token_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `func LoadPrivateKey(path string) (*rsa.PrivateKey, error)`; `func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error)` (PKCS#1 or PKCS#8, RSA ≥ 2048 bits).
  - `const Issuer = "identity"`; `type Claims struct { Roles []string; jwt.RegisteredClaims }` (json `roles`).
  - `type Signer`; `func NewSigner(key *rsa.PrivateKey, ttl time.Duration) *Signer`; methods `KeyID() string`, `TTL() time.Duration`, `Sign(userID string, roles []string, now time.Time) (string, error)`, `JWKS() JWKS`.
  - `type JWK struct { Kty, Kid, Use, Alg, N, E string }` (json tags lowercase); `type JWKS struct { Keys []JWK }` (json `keys`).
  - `func NewRefreshToken() (raw string, hash []byte, err error)`; `func HashRefreshToken(raw string) []byte`.
  - testsupport: `func RSAKey(t testing.TB) *rsa.PrivateKey`, `func PublicKey(t testing.TB, jwk token.JWK) *rsa.PublicKey`, `func ParseAccessToken(t testing.TB, raw string, jwks token.JWKS) *token.Claims`.

- [ ] **Step 1: Add the JWT dependency**

Run: `cd services/identity && go get github.com/golang-jwt/jwt/v5@v5.3.1`
Expected: `go.mod` lists `github.com/golang-jwt/jwt/v5 v5.3.1`.

- [ ] **Step 2: Write the test helpers**

`services/identity/internal/testsupport/keys.go`:

```go
// Package testsupport holds test helpers shared by identity-svc's packages.
// It is imported only from _test.go files.
package testsupport

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"math/big"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

// RSAKey generates a fresh 2048-bit signing key.
func RSAKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

// PublicKey rebuilds the RSA public key described by jwk, the way a JWKS consumer (the gateway) would.
func PublicKey(t testing.TB, jwk token.JWK) *rsa.PublicKey {
	t.Helper()
	n, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		t.Fatalf("decode jwk n: %v", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		t.Fatalf("decode jwk e: %v", err)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
}

// ParseAccessToken verifies raw against jwks (RS256 signature selected by kid,
// issuer, expiry) and returns its claims.
func ParseAccessToken(t testing.TB, raw string, jwks token.JWKS) *token.Claims {
	t.Helper()
	var claims token.Claims
	_, err := jwt.ParseWithClaims(raw, &claims, func(tk *jwt.Token) (any, error) {
		for _, k := range jwks.Keys {
			if k.Kid == tk.Header["kid"] {
				return PublicKey(t, k), nil
			}
		}
		return nil, jwt.ErrTokenUnverifiable
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(token.Issuer), jwt.WithExpirationRequired())
	if err != nil {
		t.Fatalf("verify access token: %v", err)
	}
	return &claims
}
```

- [ ] **Step 3: Write the failing tests**

`services/identity/internal/token/token_test.go`:

```go
package token_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

func TestSignedTokenVerifiesAgainstJWKS(t *testing.T) {
	signer := token.NewSigner(testsupport.RSAKey(t), 15*time.Minute)
	now := time.Now().Truncate(time.Second)

	raw, err := signer.Sign("0192f0c1-0000-7000-8000-000000000001", []string{"buyer", "seller"}, now)
	require.NoError(t, err)

	jwks := signer.JWKS()
	require.Len(t, jwks.Keys, 1)
	k := jwks.Keys[0]
	assert.Equal(t, "RSA", k.Kty)
	assert.Equal(t, "sig", k.Use)
	assert.Equal(t, "RS256", k.Alg)
	assert.Equal(t, signer.KeyID(), k.Kid)

	claims := testsupport.ParseAccessToken(t, raw, jwks)
	assert.Equal(t, "identity", claims.Issuer)
	assert.Equal(t, "0192f0c1-0000-7000-8000-000000000001", claims.Subject)
	assert.Equal(t, []string{"buyer", "seller"}, claims.Roles)
	assert.True(t, claims.IssuedAt.Equal(now))
	assert.True(t, claims.ExpiresAt.Equal(now.Add(15*time.Minute)))
	assert.Equal(t, 15*time.Minute, signer.TTL())
}

func TestTokenHeaderCarriesKid(t *testing.T) {
	signer := token.NewSigner(testsupport.RSAKey(t), time.Minute)
	raw, err := signer.Sign("u", nil, time.Now())
	require.NoError(t, err)
	header := decodeSegment(t, raw, 0)
	assert.Equal(t, "RS256", header["alg"])
	assert.Equal(t, signer.KeyID(), header["kid"])
}

func TestSignWithoutRolesEncodesEmptyArray(t *testing.T) {
	signer := token.NewSigner(testsupport.RSAKey(t), time.Minute)
	raw, err := signer.Sign("u", nil, time.Now())
	require.NoError(t, err)
	payload := decodeSegment(t, raw, 1)
	assert.Equal(t, []any{}, payload["roles"])
}

func TestKeyIDIsTruncatedRFC7638Thumbprint(t *testing.T) {
	key := testsupport.RSAKey(t)
	a := token.NewSigner(key, time.Minute)
	b := token.NewSigner(key, time.Hour)
	c := token.NewSigner(testsupport.RSAKey(t), time.Minute)

	k := a.JWKS().Keys[0]
	sum := sha256.Sum256([]byte(`{"e":"` + k.E + `","kty":"RSA","n":"` + k.N + `"}`))
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:])[:16], a.KeyID())
	assert.Equal(t, a.KeyID(), b.KeyID(), "kid depends only on the key")
	assert.NotEqual(t, a.KeyID(), c.KeyID())
}

func TestLoadPrivateKeyFormats(t *testing.T) {
	key := testsupport.RSAKey(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	for name, block := range map[string]*pem.Block{
		"pkcs1": {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
		"pkcs8": {Type: "PRIVATE KEY", Bytes: pkcs8},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := token.LoadPrivateKey(writePEM(t, block))
			require.NoError(t, err)
			assert.True(t, key.Equal(got))
		})
	}
}

func TestLoadPrivateKeyRejects(t *testing.T) {
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalPKCS8PrivateKey(ec)
	require.NoError(t, err)

	_, err = token.LoadPrivateKey(filepath.Join(t.TempDir(), "missing.pem"))
	assert.ErrorContains(t, err, "read private key")

	_, err = token.ParsePrivateKey([]byte("not pem"))
	assert.ErrorContains(t, err, "no PEM block")

	_, err = token.LoadPrivateKey(writePEM(t, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(small)}))
	assert.ErrorContains(t, err, "at least 2048 bits")

	_, err = token.LoadPrivateKey(writePEM(t, &pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}))
	assert.ErrorContains(t, err, "not an RSA key")

	_, err = token.LoadPrivateKey(writePEM(t, &pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}))
	assert.ErrorContains(t, err, "unsupported PEM type")
}

func TestRefreshToken(t *testing.T) {
	raw, hash, err := token.NewRefreshToken()
	require.NoError(t, err)

	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err, "raw token is unpadded base64url")
	assert.Len(t, decoded, 32)

	want := sha256.Sum256([]byte(raw))
	assert.Equal(t, want[:], hash)
	assert.Equal(t, hash, token.HashRefreshToken(raw))

	other, _, err := token.NewRefreshToken()
	require.NoError(t, err)
	assert.NotEqual(t, raw, other)
}

func writePEM(t *testing.T, block *pem.Block) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	return path
}

func decodeSegment(t *testing.T, raw string, i int) map[string]any {
	t.Helper()
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)
	b, err := base64.RawURLEncoding.DecodeString(parts[i])
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `cd services/identity && go test ./internal/token/...`
Expected: FAIL — build errors `undefined: token.NewSigner` etc.

- [ ] **Step 5: Write the implementation**

`services/identity/internal/token/key.go`:

```go
// Package token issues RS256 access tokens, publishes the matching JWKS and
// generates opaque refresh tokens.
package token

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

const minRSABits = 2048

// LoadPrivateKey reads an RSA private key from a PEM file (PKCS#1 or PKCS#8).
func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	return ParsePrivateKey(b)
}

// ParsePrivateKey decodes a PEM-encoded RSA private key of at least 2048 bits.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("private key: no PEM block found")
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 private key: %w", err)
		}
		key = k
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key: not an RSA key")
		}
		key = rk
	default:
		return nil, fmt.Errorf("private key: unsupported PEM type %q", block.Type)
	}
	if key.N.BitLen() < minRSABits {
		return nil, fmt.Errorf("private key: RSA key must be at least %d bits", minRSABits)
	}
	return key, nil
}
```

`services/identity/internal/token/signer.go`:

```go
package token

import (
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer is the iss claim of every access token; the gateway checks it.
const Issuer = "identity"

// Claims are the access token claims.
type Claims struct {
	Roles []string `json:"roles"`
	jwt.RegisteredClaims
}

// Signer issues RS256 access tokens with a fixed lifetime.
type Signer struct {
	key *rsa.PrivateKey
	kid string
	ttl time.Duration
}

// NewSigner returns a signer for key whose tokens live for ttl.
func NewSigner(key *rsa.PrivateKey, ttl time.Duration) *Signer {
	return &Signer{key: key, kid: keyID(&key.PublicKey), ttl: ttl}
}

// KeyID is the kid header value and the kid of the published JWK.
func (s *Signer) KeyID() string { return s.kid }

// TTL is the access token lifetime.
func (s *Signer) TTL() time.Duration { return s.ttl }

// Sign issues an access token for userID with roles, issued at now.
func (s *Signer) Sign(userID string, roles []string, now time.Time) (string, error) {
	if roles == nil {
		roles = []string{}
	}
	claims := Claims{
		Roles: roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = s.kid
	signed, err := t.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}
```

`services/identity/internal/token/jwks.go`:

```go
package token

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
)

// JWK is an RSA public signing key in JSON Web Key form.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS is a JSON Web Key Set (GET /.well-known/jwks.json).
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWKS returns the set containing the signer's public key.
func (s *Signer) JWKS() JWKS {
	n, e := rsaComponents(&s.key.PublicKey)
	return JWKS{Keys: []JWK{{Kty: "RSA", Kid: s.kid, Use: "sig", Alg: "RS256", N: n, E: e}}}
}

func rsaComponents(pub *rsa.PublicKey) (n, e string) {
	enc := base64.RawURLEncoding
	return enc.EncodeToString(pub.N.Bytes()), enc.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
}

// keyID is the RFC 7638 JWK thumbprint (SHA-256) truncated to 16 base64url characters.
func keyID(pub *rsa.PublicKey) string {
	n, e := rsaComponents(pub)
	sum := sha256.Sum256([]byte(`{"e":"` + e + `","kty":"RSA","n":"` + n + `"}`))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:16]
}
```

`services/identity/internal/token/refresh.go`:

```go
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const refreshTokenBytes = 32

// NewRefreshToken returns a random opaque token (unpadded base64url of 32
// bytes) and the SHA-256 hash that is stored instead of it.
func NewRefreshToken() (raw string, hash []byte, err error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashRefreshToken(raw), nil
}

// HashRefreshToken returns the SHA-256 digest used to look a token up.
func HashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd services/identity && go mod tidy && go test ./internal/token/... -v`
Expected: PASS (all tests).

- [ ] **Step 7: Commit**

```bash
git add services/identity/go.mod services/identity/go.sum services/identity/internal/token services/identity/internal/testsupport
git commit -m "feat(identity): add RS256 signer, JWKS and refresh token helpers"
```

---

### Task 3: Schema, sqlc queries and the Postgres store

**Files:**
- Create: `services/identity/migrations/000002_identity.up.sql`
- Create: `services/identity/migrations/000002_identity.down.sql`
- Modify: `services/identity/sqlc.yaml` (add overrides)
- Create: `services/identity/internal/adapter/postgres/queries/users.sql`
- Create: `services/identity/internal/adapter/postgres/queries/refresh_tokens.sql`
- Generate: `services/identity/internal/adapter/postgres/sqlcdb/*.go` (committed)
- Create: `services/identity/internal/app/ports.go`
- Create: `services/identity/internal/adapter/postgres/store.go`
- Create: `services/identity/internal/adapter/postgres/errors.go`
- Create: `services/identity/internal/testsupport/postgres.go`
- Test: `services/identity/internal/adapter/postgres/store_test.go`

**Interfaces:**
- Consumes: `domain.User`, `domain.RefreshToken`, `domain.Role`, `domain.ErrNotFound`, `domain.ErrEmailTaken` (Task 1).
- Produces:
  - `app.Store`, `app.Tx`, `app.RoleStore` interfaces (exact method sets in Step 5).
  - `func postgres.NewStore(pool *pgxpool.Pool) *postgres.Store` implementing `app.Store` and `app.RoleStore`.
  - testsupport: `func Pool(t testing.TB) *pgxpool.Pool`, `func Email() string`.

- [ ] **Step 1: Write the migration**

`services/identity/migrations/000002_identity.up.sql`:

```sql
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    email         citext NOT NULL UNIQUE,
    password_hash text NOT NULL,
    full_name     text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_roles (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('buyer', 'seller', 'admin')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role)
);

CREATE TABLE refresh_tokens (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id   uuid NOT NULL,
    token_hash  bytea NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    replaced_by uuid REFERENCES refresh_tokens (id),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
```

`services/identity/migrations/000002_identity.down.sql`:

```sql
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS users;
```

- [ ] **Step 2: Configure sqlc type overrides**

Replace `services/identity/sqlc.yaml` with:

```yaml
version: "2"
sql:
  - engine: postgresql
    schema: migrations
    queries: internal/adapter/postgres/queries
    gen:
      go:
        package: sqlcdb
        out: internal/adapter/postgres/sqlcdb
        sql_package: pgx/v5
        overrides:
          - db_type: uuid
            go_type: github.com/google/uuid.UUID
          - db_type: uuid
            nullable: true
            go_type:
              import: github.com/google/uuid
              type: UUID
              pointer: true
          - db_type: timestamptz
            go_type: time.Time
          - db_type: timestamptz
            nullable: true
            go_type:
              import: time
              type: Time
              pointer: true
```

- [ ] **Step 3: Write the queries**

`services/identity/internal/adapter/postgres/queries/users.sql`:

```sql
-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, full_name)
VALUES ($1, $2, $3, $4)
RETURNING created_at;

-- name: AddUserRole :exec
INSERT INTO user_roles (user_id, role)
VALUES ($1, $2)
ON CONFLICT (user_id, role) DO NOTHING;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, full_name, created_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, full_name, created_at
FROM users
WHERE id = $1;

-- name: ListUserRoles :many
SELECT role
FROM user_roles
WHERE user_id = $1
ORDER BY role;
```

`services/identity/internal/adapter/postgres/queries/refresh_tokens.sql`:

```sql
-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetRefreshTokenForUpdate :one
SELECT id, user_id, family_id, token_hash, expires_at, revoked_at
FROM refresh_tokens
WHERE token_hash = $1
FOR UPDATE;

-- name: ReplaceRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = @revoked_at, replaced_by = @replaced_by
WHERE id = @id;

-- name: RevokeRefreshTokenFamily :exec
UPDATE refresh_tokens
SET revoked_at = @revoked_at
WHERE family_id = @family_id AND revoked_at IS NULL;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = @revoked_at
WHERE token_hash = @token_hash AND revoked_at IS NULL;
```

- [ ] **Step 4: Generate sqlc code**

Run: `make gen-sqlc` (from repo root)
Expected: `sqlc generate services/identity`, creating `sqlcdb/{db.go,models.go,users.sql.go,refresh_tokens.sql.go}`. Check `grep -n "Email " services/identity/internal/adapter/postgres/sqlcdb/models.go` shows `Email string`; if sqlc emitted `interface{}` for citext, add an override `- db_type: citext` / `go_type: string` and regenerate. Confirm the generated signatures used in Step 8: `CreateUser(ctx, CreateUserParams) (time.Time, error)`, `AddUserRole(ctx, AddUserRoleParams) error`, `GetUserByEmail(ctx, string) (GetUserByEmailRow, error)`, `GetUserByID(ctx, uuid.UUID) (GetUserByIDRow, error)`, `ListUserRoles(ctx, uuid.UUID) ([]string, error)`, `CreateRefreshToken(ctx, CreateRefreshTokenParams) error`, `GetRefreshTokenForUpdate(ctx, []byte) (GetRefreshTokenForUpdateRow, error)`, `ReplaceRefreshToken(ctx, ReplaceRefreshTokenParams{RevokedAt *time.Time; ReplacedBy *uuid.UUID; ID uuid.UUID})`, `RevokeRefreshTokenFamily(ctx, RevokeRefreshTokenFamilyParams{RevokedAt *time.Time; FamilyID uuid.UUID})`, `RevokeRefreshToken(ctx, RevokeRefreshTokenParams{RevokedAt *time.Time; TokenHash []byte})`. Adjust the store code in Step 8 to the generated names if any differ.

- [ ] **Step 5: Write the ports and test helpers**

`services/identity/internal/app/ports.go`:

```go
// Package app holds identity-svc's use cases and the storage ports they need.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// Store is the persistence port of AuthService.
type Store interface {
	// CreateUser inserts u with its roles and returns it with CreatedAt set.
	// A duplicate email (case-insensitive) returns domain.ErrEmailTaken.
	CreateUser(ctx context.Context, u domain.User, passwordHash string) (domain.User, error)
	// UserByEmail returns the user and password hash, or domain.ErrNotFound.
	UserByEmail(ctx context.Context, email string) (domain.User, string, error)
	// UserByID returns the user with current roles, or domain.ErrNotFound.
	UserByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error
	// RevokeRefreshToken revokes the token with hash if it is still active; unknown hashes are ignored.
	RevokeRefreshToken(ctx context.Context, hash []byte, at time.Time) error
	// InTx runs fn in one transaction, committing when fn returns nil.
	InTx(ctx context.Context, fn func(Tx) error) error
}

// Tx is the transactional view used by refresh-token rotation.
type Tx interface {
	// RefreshTokenForUpdate locks and returns the token with hash, or domain.ErrNotFound.
	RefreshTokenForUpdate(ctx context.Context, hash []byte) (domain.RefreshToken, error)
	RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error
	CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error
	// ReplaceRefreshToken marks oldID revoked at at and points it to newID.
	ReplaceRefreshToken(ctx context.Context, oldID, newID uuid.UUID, at time.Time) error
	UserByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// RoleStore is the persistence port of RoleService.
type RoleStore interface {
	// GrantRole adds role idempotently; an unknown user returns domain.ErrNotFound.
	GrantRole(ctx context.Context, userID uuid.UUID, role domain.Role) error
}
```

`services/identity/internal/testsupport/postgres.go`:

```go
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
```

- [ ] **Step 6: Write the failing store test**

`services/identity/internal/adapter/postgres/store_test.go`:

```go
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
```

- [ ] **Step 7: Run test to verify it fails**

Run: `cd services/identity && go test ./internal/adapter/postgres/...`
Expected: FAIL — build error `undefined: postgres.NewStore`.

- [ ] **Step 8: Write the store**

`services/identity/internal/adapter/postgres/errors.go`:

```go
package postgres

import (
	"errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == pgerrcode.UniqueViolation && pe.ConstraintName == constraint
}

func isForeignKeyViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == pgerrcode.ForeignKeyViolation
}
```

`services/identity/internal/adapter/postgres/store.go`:

```go
// Package postgres implements identity-svc's storage ports with sqlc-generated queries.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

var (
	_ app.Store     = (*Store)(nil)
	_ app.RoleStore = (*Store)(nil)
	_ app.Tx        = repo{}
)

// Store implements app.Store and app.RoleStore on a connection pool.
type Store struct {
	repo
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{repo: repo{q: sqlcdb.New(pool)}, pool: pool}
}

// CreateUser inserts the user and its roles in one transaction.
func (s *Store) CreateUser(ctx context.Context, u domain.User, passwordHash string) (domain.User, error) {
	err := pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		createdAt, err := q.CreateUser(ctx, sqlcdb.CreateUserParams{
			ID: u.ID, Email: u.Email, PasswordHash: passwordHash, FullName: u.FullName,
		})
		if isUniqueViolation(err, "users_email_key") {
			return domain.ErrEmailTaken
		}
		if err != nil {
			return fmt.Errorf("insert user: %w", err)
		}
		u.CreatedAt = createdAt.UTC()
		for _, r := range u.Roles {
			if err := q.AddUserRole(ctx, sqlcdb.AddUserRoleParams{UserID: u.ID, Role: string(r)}); err != nil {
				return fmt.Errorf("insert user role: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return domain.User{}, err
	}
	return u, nil
}

// InTx runs fn in a transaction.
func (s *Store) InTx(ctx context.Context, fn func(app.Tx) error) error {
	return pgxx.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(repo{q: s.q.WithTx(tx)})
	})
}

// repo implements single-statement operations on the pool or inside a transaction.
type repo struct {
	q *sqlcdb.Queries
}

func (r repo) UserByEmail(ctx context.Context, email string) (domain.User, string, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, "", domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, "", fmt.Errorf("get user by email: %w", err)
	}
	roles, err := r.roles(ctx, row.ID)
	if err != nil {
		return domain.User{}, "", err
	}
	u := domain.User{ID: row.ID, Email: row.Email, FullName: row.FullName, Roles: roles, CreatedAt: row.CreatedAt.UTC()}
	return u, row.PasswordHash, nil
}

func (r repo) UserByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user by id: %w", err)
	}
	roles, err := r.roles(ctx, row.ID)
	if err != nil {
		return domain.User{}, err
	}
	return domain.User{ID: row.ID, Email: row.Email, FullName: row.FullName, Roles: roles, CreatedAt: row.CreatedAt.UTC()}, nil
}

func (r repo) roles(ctx context.Context, userID uuid.UUID) ([]domain.Role, error) {
	names, err := r.q.ListUserRoles(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list user roles: %w", err)
	}
	roles := make([]domain.Role, len(names))
	for i, n := range names {
		roles[i] = domain.Role(n)
	}
	return roles, nil
}

func (r repo) GrantRole(ctx context.Context, userID uuid.UUID, role domain.Role) error {
	err := r.q.AddUserRole(ctx, sqlcdb.AddUserRoleParams{UserID: userID, Role: string(role)})
	if isForeignKeyViolation(err) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("grant role: %w", err)
	}
	return nil
}

func (r repo) CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error {
	err := r.q.CreateRefreshToken(ctx, sqlcdb.CreateRefreshTokenParams{
		ID: t.ID, UserID: t.UserID, FamilyID: t.FamilyID, TokenHash: t.Hash, ExpiresAt: t.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (r repo) RefreshTokenForUpdate(ctx context.Context, hash []byte) (domain.RefreshToken, error) {
	row, err := r.q.GetRefreshTokenForUpdate(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshToken{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RefreshToken{}, fmt.Errorf("get refresh token: %w", err)
	}
	return domain.RefreshToken{
		ID: row.ID, UserID: row.UserID, FamilyID: row.FamilyID, Hash: row.TokenHash,
		ExpiresAt: row.ExpiresAt.UTC(), RevokedAt: row.RevokedAt,
	}, nil
}

func (r repo) ReplaceRefreshToken(ctx context.Context, oldID, newID uuid.UUID, at time.Time) error {
	err := r.q.ReplaceRefreshToken(ctx, sqlcdb.ReplaceRefreshTokenParams{ID: oldID, ReplacedBy: &newID, RevokedAt: &at})
	if err != nil {
		return fmt.Errorf("replace refresh token: %w", err)
	}
	return nil
}

func (r repo) RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error {
	err := r.q.RevokeRefreshTokenFamily(ctx, sqlcdb.RevokeRefreshTokenFamilyParams{FamilyID: familyID, RevokedAt: &at})
	if err != nil {
		return fmt.Errorf("revoke refresh token family: %w", err)
	}
	return nil
}

func (r repo) RevokeRefreshToken(ctx context.Context, hash []byte, at time.Time) error {
	err := r.q.RevokeRefreshToken(ctx, sqlcdb.RevokeRefreshTokenParams{TokenHash: hash, RevokedAt: &at})
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}
```

- [ ] **Step 9: Run test to verify it passes**

Run: `cd services/identity && go mod tidy && go test ./internal/adapter/postgres/... -v`
Expected: PASS (Docker must be running; the Postgres container starts once for `TestStore`).

- [ ] **Step 10: Commit**

```bash
git add services/identity
git commit -m "feat(identity): add schema, sqlc queries and postgres store"
```

---

### Task 4: AuthService — Register, Login, Me

**Files:**
- Create: `services/identity/internal/app/auth.go`
- Create: `services/identity/internal/testsupport/clock.go`
- Test: `services/identity/internal/app/auth_test.go`

**Interfaces:**
- Consumes: `app.Store` (Task 3), `domain.NewRegistration`, `domain.NormalizeEmail`, `domain.MaxPasswordBytes`, sentinels (Task 1), `token.Signer`, `token.NewRefreshToken`, `token.HashRefreshToken` (Task 2), `postgres.NewStore`, `testsupport.Pool`, `testsupport.Email` (Task 3).
- Produces:
  - `const app.DefaultBcryptCost = 12`
  - `type app.AuthConfig struct { RefreshTTL time.Duration; BcryptCost int; Now func() time.Time; Logger *slog.Logger }`
  - `type app.Session struct { AccessToken string; ExpiresIn time.Duration; RefreshToken string; User domain.User }`
  - `func app.NewAuthService(store Store, signer *token.Signer, cfg AuthConfig) (*AuthService, error)`
  - `(*AuthService).Register(ctx, email, password, fullName string) (domain.User, error)`
  - `(*AuthService).Login(ctx, email, password string) (Session, error)`
  - `(*AuthService).Me(ctx, userID string) (domain.User, error)`
  - Test fixture (in `auth_test.go`, reused by Task 5): `newFixture(t) *fixture` with fields `store, signer, clock, logs, auth`; methods `register(t) (domain.User, string)`, `familyOf(t, raw) uuid.UUID`; const `refreshTTL = time.Hour`.
  - testsupport: `type Clock`; `func NewClock(t time.Time) *Clock`; `(*Clock).Now() time.Time`; `(*Clock).Advance(d time.Duration)`.

- [ ] **Step 1: Add the bcrypt dependency and the test clock**

Run: `cd services/identity && go get golang.org/x/crypto/bcrypt`

`services/identity/internal/testsupport/clock.go`:

```go
package testsupport

import (
	"sync"
	"time"
)

// Clock is a manually advanced, concurrency-safe clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock reading t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d (negative d moves it back).
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
```

- [ ] **Step 2: Write the failing tests**

`services/identity/internal/app/auth_test.go`:

```go
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd services/identity && go test ./internal/app/...`
Expected: FAIL — build error `undefined: app.NewAuthService`.

- [ ] **Step 4: Write the implementation**

`services/identity/internal/app/auth.go`:

```go
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

// DefaultBcryptCost is the production bcrypt work factor (WS1 §3).
const DefaultBcryptCost = 12

// AuthConfig tunes AuthService. Zero values select production defaults.
type AuthConfig struct {
	RefreshTTL time.Duration
	BcryptCost int              // 0 means DefaultBcryptCost
	Now        func() time.Time // nil means time.Now
	Logger     *slog.Logger     // nil means slog.Default()
}

// Session is the result of a successful login or refresh.
type Session struct {
	AccessToken  string
	ExpiresIn    time.Duration
	RefreshToken string
	User         domain.User
}

// AuthService implements registration, login, token refresh, logout and "me".
type AuthService struct {
	store     Store
	signer    *token.Signer
	cfg       AuthConfig
	dummyHash []byte
}

// NewAuthService builds an AuthService.
func NewAuthService(store Store, signer *token.Signer, cfg AuthConfig) (*AuthService, error) {
	if cfg.BcryptCost == 0 {
		cfg.BcryptCost = DefaultBcryptCost
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	// Login compares against this hash when the email is unknown so both
	// failure paths cost one bcrypt comparison.
	dummy, err := bcrypt.GenerateFromPassword([]byte("timing-equalizer"), cfg.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("generate dummy hash: %w", err)
	}
	return &AuthService{store: store, signer: signer, cfg: cfg, dummyHash: dummy}, nil
}

// Register creates a buyer account. It does not log the user in.
func (s *AuthService) Register(ctx context.Context, email, password, fullName string) (domain.User, error) {
	reg, err := domain.NewRegistration(email, password, fullName)
	if err != nil {
		return domain.User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(reg.Password), s.cfg.BcryptCost)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.User{}, fmt.Errorf("generate user id: %w", err)
	}
	u := domain.User{ID: id, Email: reg.Email, FullName: reg.FullName, Roles: []domain.Role{domain.RoleBuyer}}
	return s.store.CreateUser(ctx, u, string(hash))
}

// Login verifies credentials and starts a new refresh-token family.
// Unknown email and wrong password return the same domain.ErrInvalidCredentials.
func (s *AuthService) Login(ctx context.Context, email, password string) (Session, error) {
	u, hash, err := s.store.UserByEmail(ctx, domain.NormalizeEmail(email))
	switch {
	case errors.Is(err, domain.ErrNotFound):
		hash = string(s.dummyHash)
	case err != nil:
		return Session{}, err
	}
	// bcrypt only reads the first 72 bytes; longer input must not match its prefix.
	matched := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	if err != nil || !matched || len(password) > domain.MaxPasswordBytes {
		return Session{}, domain.ErrInvalidCredentials
	}
	family, err := uuid.NewV7()
	if err != nil {
		return Session{}, fmt.Errorf("generate token family: %w", err)
	}
	sess, _, err := s.newSession(ctx, s.store, u, family)
	return sess, err
}

// Me returns the user behind userID (the gateway-provided X-User-Id).
// A malformed or unknown id returns domain.ErrNotFound.
func (s *AuthService) Me(ctx context.Context, userID string) (domain.User, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return domain.User{}, domain.ErrNotFound
	}
	return s.store.UserByID(ctx, id)
}

type refreshTokenWriter interface {
	CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error
}

// newSession stores a new refresh token in familyID and signs an access token for u.
func (s *AuthService) newSession(ctx context.Context, w refreshTokenWriter, u domain.User, familyID uuid.UUID) (Session, domain.RefreshToken, error) {
	raw, hash, err := token.NewRefreshToken()
	if err != nil {
		return Session{}, domain.RefreshToken{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, domain.RefreshToken{}, fmt.Errorf("generate token id: %w", err)
	}
	now := s.cfg.Now()
	rt := domain.RefreshToken{ID: id, UserID: u.ID, FamilyID: familyID, Hash: hash, ExpiresAt: now.Add(s.cfg.RefreshTTL)}
	if err := w.CreateRefreshToken(ctx, rt); err != nil {
		return Session{}, domain.RefreshToken{}, err
	}
	access, err := s.signer.Sign(u.ID.String(), roleNames(u.Roles), now)
	if err != nil {
		return Session{}, domain.RefreshToken{}, err
	}
	return Session{AccessToken: access, ExpiresIn: s.signer.TTL(), RefreshToken: raw, User: u}, rt, nil
}

func roleNames(roles []domain.Role) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd services/identity && go mod tidy && go test ./internal/app/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add services/identity
git commit -m "feat(identity): add register, login and me use cases"
```

---

### Task 5: AuthService — Refresh rotation with reuse detection, Logout

**Files:**
- Modify: `services/identity/internal/app/auth.go` (append `Refresh`, `Logout`)
- Test: `services/identity/internal/app/refresh_test.go`

**Interfaces:**
- Consumes: `app.Tx`, `Store.InTx`, `Store.RevokeRefreshToken` (Task 3); `newSession` and the `fixture` helpers from `auth_test.go` (Task 4).
- Produces:
  - `(*AuthService).Refresh(ctx, raw string) (Session, error)` — `domain.ErrInvalidRefreshToken` (missing/unknown/expired) or `domain.ErrRefreshTokenReused` (revoked token presented; whole family revoked).
  - `(*AuthService).Logout(ctx, raw string) error` — nil for empty/unknown tokens.

- [ ] **Step 1: Write the failing tests**

`services/identity/internal/app/refresh_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd services/identity && go test ./internal/app/...`
Expected: FAIL — build error `f.auth.Refresh undefined`.

- [ ] **Step 3: Write the implementation**

Append to `services/identity/internal/app/auth.go`:

```go
// Refresh rotates the refresh token raw: the old token is revoked and replaced
// by a new one in the same family, and the access token carries roles re-read
// from the database. Presenting an already revoked token revokes the whole
// family (theft detection) and returns domain.ErrRefreshTokenReused.
func (s *AuthService) Refresh(ctx context.Context, raw string) (Session, error) {
	if raw == "" {
		return Session{}, domain.ErrInvalidRefreshToken
	}
	var (
		sess   Session
		reused *domain.RefreshToken
	)
	err := s.store.InTx(ctx, func(tx Tx) error {
		old, err := tx.RefreshTokenForUpdate(ctx, token.HashRefreshToken(raw))
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrInvalidRefreshToken
		}
		if err != nil {
			return err
		}
		now := s.cfg.Now()
		if old.RevokedAt != nil {
			// Commit the family revocation; the error is reported after the transaction.
			reused = &old
			return tx.RevokeRefreshFamily(ctx, old.FamilyID, now)
		}
		if !now.Before(old.ExpiresAt) {
			return domain.ErrInvalidRefreshToken
		}
		u, err := tx.UserByID(ctx, old.UserID)
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrInvalidRefreshToken
		}
		if err != nil {
			return err
		}
		next, rt, err := s.newSession(ctx, tx, u, old.FamilyID)
		if err != nil {
			return err
		}
		if err := tx.ReplaceRefreshToken(ctx, old.ID, rt.ID, now); err != nil {
			return err
		}
		sess = next
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if reused != nil {
		s.cfg.Logger.WarnContext(ctx, "refresh token reuse detected; token family revoked",
			"user_id", reused.UserID.String(), "family_id", reused.FamilyID.String())
		return Session{}, domain.ErrRefreshTokenReused
	}
	return sess, nil
}

// Logout revokes the refresh token raw if it is active. Missing or unknown
// tokens are not an error.
func (s *AuthService) Logout(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	return s.store.RevokeRefreshToken(ctx, token.HashRefreshToken(raw), s.cfg.Now())
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd services/identity && go test -race ./internal/app/... -v`
Expected: PASS, no race reports.

- [ ] **Step 5: Commit**

```bash
git add services/identity/internal/app
git commit -m "feat(identity): rotate refresh tokens with family reuse detection"
```

---

### Task 6: RoleService and the IdentityInternal gRPC server

**Files:**
- Create: `services/identity/internal/app/roles.go`
- Create: `services/identity/internal/adapter/grpc/server.go` (package `grpcapi`)
- Test: `services/identity/internal/adapter/grpc/server_test.go`

**Interfaces:**
- Consumes: `app.RoleStore`, `postgres.NewStore`, `testsupport.Pool/Email` (Task 3); `domain.ErrRoleNotGrantable`, `domain.ErrNotFound`, `*domain.ValidationError` (Task 1); `identityv1` from `gen/identity/v1`; `grpcx.NewServer`, `grpcx.Dial` from `pkg/grpcx`.
- Produces:
  - `func app.NewRoleService(store RoleStore) *RoleService`; `(*RoleService).Grant(ctx, userID, role string) error`
  - `func grpcapi.NewServer(roles *app.RoleService, logger *slog.Logger) *grpcapi.Server` (implements `identityv1.IdentityInternalServer`).

- [ ] **Step 1: Add gen and grpc dependencies**

Run: `cd services/identity && go get github.com/ngoxuanchien/e-commerce/gen@v0.0.0 google.golang.org/grpc`
Expected: `go.mod` requires `github.com/ngoxuanchien/e-commerce/gen v0.0.0` (resolved through the existing `replace`).

- [ ] **Step 2: Write the failing test**

`services/identity/internal/adapter/grpc/server_test.go`:

```go
package grpcapi_test

import (
	"context"
	"log/slog"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	grpcapi "github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/grpc"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
)

func TestGrantRole(t *testing.T) {
	store := postgres.NewStore(testsupport.Pool(t))
	client := startServer(t, store)
	ctx := context.Background()

	newUser := func(t *testing.T) domain.User {
		t.Helper()
		u, err := store.CreateUser(ctx, domain.User{
			ID: uuid.Must(uuid.NewV7()), Email: testsupport.Email(), FullName: "U",
			Roles: []domain.Role{domain.RoleBuyer},
		}, "h")
		require.NoError(t, err)
		return u
	}

	t.Run("granting seller twice is idempotent", func(t *testing.T) {
		u := newUser(t)
		for range 2 {
			_, err := client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: u.ID.String(), Role: "seller"})
			require.NoError(t, err)
		}
		got, err := store.UserByID(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, []domain.Role{domain.RoleBuyer, domain.RoleSeller}, got.Roles)
	})

	t.Run("unknown user is NOT_FOUND", func(t *testing.T) {
		_, err := client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: uuid.Must(uuid.NewV7()).String(), Role: "seller"})
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("roles other than seller are INVALID_ARGUMENT", func(t *testing.T) {
		u := newUser(t)
		for _, role := range []string{"admin", "buyer", "", "SELLER"} {
			_, err := client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: u.ID.String(), Role: role})
			assert.Equal(t, codes.InvalidArgument, status.Code(err), "role %q", role)
		}
	})

	t.Run("malformed user id is INVALID_ARGUMENT", func(t *testing.T) {
		_, err := client.GrantRole(ctx, &identityv1.GrantRoleRequest{UserId: "not-a-uuid", Role: "seller"})
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func startServer(t *testing.T, store *postgres.Store) identityv1.IdentityInternalClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv, _ := grpcx.NewServer()
	identityv1.RegisterIdentityInternalServer(srv, grpcapi.NewServer(app.NewRoleService(store), slog.New(slog.DiscardHandler)))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpcx.Dial(lis.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return identityv1.NewIdentityInternalClient(conn)
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd services/identity && go test ./internal/adapter/grpc/...`
Expected: FAIL — build error `undefined: grpcapi.NewServer` / `app.NewRoleService`.

- [ ] **Step 4: Write the implementation**

`services/identity/internal/app/roles.go`:

```go
package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// RoleService grants roles on behalf of other services (catalog-svc grants seller).
type RoleService struct {
	store RoleStore
}

// NewRoleService builds a RoleService.
func NewRoleService(store RoleStore) *RoleService {
	return &RoleService{store: store}
}

// Grant adds role to the user. Only seller can be granted; granting a role
// the user already has succeeds.
func (s *RoleService) Grant(ctx context.Context, userID, role string) error {
	if domain.Role(role) != domain.RoleSeller {
		return domain.ErrRoleNotGrantable
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return &domain.ValidationError{Fields: map[string]string{"user_id": "must be a valid UUID"}}
	}
	return s.store.GrantRole(ctx, id, domain.RoleSeller)
}
```

`services/identity/internal/adapter/grpc/server.go`:

```go
// Package grpcapi serves identity-svc's internal gRPC API (proto/identity/v1).
package grpcapi

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// Server implements identityv1.IdentityInternalServer.
type Server struct {
	identityv1.UnimplementedIdentityInternalServer
	roles  *app.RoleService
	logger *slog.Logger
}

// NewServer builds the gRPC server implementation.
func NewServer(roles *app.RoleService, logger *slog.Logger) *Server {
	return &Server{roles: roles, logger: logger}
}

// GrantRole adds a role to a user (idempotent).
func (s *Server) GrantRole(ctx context.Context, req *identityv1.GrantRoleRequest) (*identityv1.GrantRoleResponse, error) {
	err := s.roles.Grant(ctx, req.GetUserId(), req.GetRole())
	var ve *domain.ValidationError
	switch {
	case err == nil:
		return &identityv1.GrantRoleResponse{}, nil
	case errors.Is(err, domain.ErrRoleNotGrantable):
		return nil, status.Errorf(codes.InvalidArgument, "role %q cannot be granted; only %q is allowed", req.GetRole(), domain.RoleSeller)
	case errors.As(err, &ve):
		return nil, status.Error(codes.InvalidArgument, "user_id must be a valid UUID")
	case errors.Is(err, domain.ErrNotFound):
		return nil, status.Error(codes.NotFound, "user not found")
	default:
		s.logger.ErrorContext(ctx, "grant role failed", "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd services/identity && go mod tidy && go test ./internal/adapter/grpc/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add services/identity
git commit -m "feat(identity): add GrantRole gRPC server"
```

---

### Task 7: REST adapter (chi) with contract-validated tests

**Files:**
- Create: `services/identity/internal/adapter/http/router.go` (package `httpapi`)
- Create: `services/identity/internal/adapter/http/handlers.go`
- Create: `services/identity/internal/adapter/http/dto.go`
- Create: `services/identity/internal/adapter/http/errors.go`
- Create: `services/identity/internal/testsupport/http.go`
- Test: `services/identity/internal/adapter/http/router_test.go`

**Interfaces:**
- Consumes: `*app.AuthService` + `app.Session` (Tasks 4–5), `token.JWKS` (Task 2), sentinels (Task 1), `httpx`/`authctx` from `pkg`, `testutil.ContractTransport`.
- Produces:
  - `type httpapi.Config struct { CookieSecure bool; RefreshTTL time.Duration }`
  - `func httpapi.NewRouter(auth *app.AuthService, jwks token.JWKS, cfg Config) http.Handler` — expects `authctx.Middleware` in front of it (the WS0 `newHandler` provides it).
  - testsupport: `func Call(t testing.TB, c *http.Client, method, url string, body any, header http.Header) (*http.Response, []byte)`; `func Cookie(resp *http.Response, name string) *http.Cookie`; `func ErrorCode(t testing.TB, body []byte) string`.

- [ ] **Step 1: Add chi**

Run: `cd services/identity && go get github.com/go-chi/chi/v5@v5.3.2`

- [ ] **Step 2: Write the HTTP test helpers**

`services/identity/internal/testsupport/http.go`:

```go
package testsupport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Call sends a request and returns the response with its fully read body.
// A string body is sent verbatim; any other non-nil body is JSON-encoded.
func Call(t testing.TB, c *http.Client, method, url string, body any, header http.Header) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, data
}

// Cookie returns the Set-Cookie entry called name, or nil.
func Cookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ErrorCode extracts error.code from a standard error body.
func ErrorCode(t testing.TB, body []byte) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return e.Error.Code
}
```

- [ ] **Step 3: Write the failing tests**

`services/identity/internal/adapter/http/router_test.go`:

```go
package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	httpapi "github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/http"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

const specPath = "../../../../../api/openapi/identity.yaml"

type env struct {
	url      string
	contract *http.Client // validates every request/response against identity.yaml
	plain    *http.Client // for requests that deliberately violate the contract
	jwks     token.JWKS
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := postgres.NewStore(testsupport.Pool(t))
	signer := token.NewSigner(testsupport.RSAKey(t), 15*time.Minute)
	auth, err := app.NewAuthService(store, signer, app.AuthConfig{RefreshTTL: 720 * time.Hour, BcryptCost: bcrypt.MinCost})
	require.NoError(t, err)
	router := httpapi.NewRouter(auth, signer.JWKS(), httpapi.Config{CookieSecure: true, RefreshTTL: 720 * time.Hour})
	srv := httptest.NewServer(authctx.Middleware(router))
	t.Cleanup(srv.Close)
	return &env{
		url:      srv.URL,
		contract: &http.Client{Transport: testutil.ContractTransport(t, specPath, nil)},
		plain:    &http.Client{},
		jwks:     signer.JWKS(),
	}
}

func withCookie(value string) http.Header {
	return http.Header{"Cookie": {"refresh_token=" + value}}
}

type userBody struct {
	ID        string   `json:"id"`
	Email     string   `json:"email"`
	FullName  string   `json:"full_name"`
	Roles     []string `json:"roles"`
	CreatedAt string   `json:"created_at"`
}

type tokenBody struct {
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
	ExpiresIn   int      `json:"expires_in"`
	User        userBody `json:"user"`
}

func assertCleared(t *testing.T, resp *http.Response) {
	t.Helper()
	c := testsupport.Cookie(resp, "refresh_token")
	require.NotNil(t, c, "Set-Cookie clears refresh_token")
	assert.Empty(t, c.Value)
	assert.Equal(t, -1, c.MaxAge, "Max-Age=0")
	assert.Equal(t, "/api/auth", c.Path)
}

func TestAuthFlow(t *testing.T) {
	e := newEnv(t)
	email := testsupport.Email()
	password := "correct horse battery"

	// Register (normalizes the email, never returns the hash).
	resp, body := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/register",
		map[string]string{"email": strings.ToUpper(email), "password": password, "full_name": "Alice"}, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
	assert.NotContains(t, string(body), "password")
	var user userBody
	require.NoError(t, json.Unmarshal(body, &user))
	assert.Equal(t, email, user.Email)
	assert.Equal(t, []string{"buyer"}, user.Roles)
	assert.Nil(t, testsupport.Cookie(resp, "refresh_token"), "registration does not log in")

	// Duplicate registration.
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/register",
		map[string]string{"email": email, "password": password, "full_name": "Alice"}, nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "CONFLICT", testsupport.ErrorCode(t, body))

	// Login.
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/login",
		map[string]string{"email": email, "password": password}, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	var tok tokenBody
	require.NoError(t, json.Unmarshal(body, &tok))
	assert.Equal(t, "Bearer", tok.TokenType)
	assert.Equal(t, 900, tok.ExpiresIn)
	assert.Equal(t, user.ID, tok.User.ID)
	claims := testsupport.ParseAccessToken(t, tok.AccessToken, e.jwks)
	assert.Equal(t, user.ID, claims.Subject)

	cookie := testsupport.Cookie(resp, "refresh_token")
	require.NotNil(t, cookie)
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Equal(t, "/api/auth", cookie.Path)
	assert.Equal(t, 2592000, cookie.MaxAge)
	first := cookie.Value

	// Me.
	resp, body = testsupport.Call(t, e.contract, http.MethodGet, e.url+"/api/users/me", nil,
		http.Header{authctx.HeaderUserID: {user.ID}})
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var me userBody
	require.NoError(t, json.Unmarshal(body, &me))
	assert.Equal(t, user, me)

	// Refresh rotates the cookie.
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, withCookie(first))
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	second := testsupport.Cookie(resp, "refresh_token").Value
	assert.NotEqual(t, first, second)

	// Reusing the old cookie is rejected and clears the cookie ...
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, withCookie(first))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "UNAUTHENTICATED", testsupport.ErrorCode(t, body))
	assertCleared(t, resp)

	// ... and revokes the whole family, including the newest token.
	resp, _ = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, withCookie(second))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Logout revokes a fresh session.
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/login",
		map[string]string{"email": email, "password": password}, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	fresh := testsupport.Cookie(resp, "refresh_token").Value

	resp, _ = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/logout", nil, withCookie(fresh))
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	assertCleared(t, resp)

	resp, _ = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, withCookie(fresh))
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestErrorsAndEdgeCases(t *testing.T) {
	e := newEnv(t)

	t.Run("register validation errors list fields", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.plain, http.MethodPost, e.url+"/api/auth/register",
			map[string]string{"email": "nope", "password": "short", "full_name": ""}, nil)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		var eb struct {
			Error struct {
				Code    string `json:"code"`
				Details struct {
					Fields map[string]string `json:"fields"`
				} `json:"details"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(body, &eb))
		assert.Equal(t, "VALIDATION_ERROR", eb.Error.Code)
		assert.Equal(t, map[string]string{
			"email": "must be a valid email", "password": "must be at least 8 characters", "full_name": "is required",
		}, eb.Error.Details.Fields)
	})

	t.Run("multibyte password over 72 bytes is rejected", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/register",
			map[string]string{"email": testsupport.Email(), "password": strings.Repeat("ệ", 25), "full_name": "A"}, nil)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, string(body), "must be at most 72 bytes")
	})

	t.Run("unknown fields and bad JSON are validation errors", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.plain, http.MethodPost, e.url+"/api/auth/register",
			`{"email":"a@example.com","password":"password1","full_name":"A","role":"admin"}`, nil)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "VALIDATION_ERROR", testsupport.ErrorCode(t, body))
		resp, _ = testsupport.Call(t, e.plain, http.MethodPost, e.url+"/api/auth/login", `{`, nil)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("wrong password and unknown email return identical 401s", func(t *testing.T) {
		email := testsupport.Email()
		resp, _ := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/register",
			map[string]string{"email": email, "password": "password1", "full_name": "A"}, nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		r1, b1 := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/login",
			map[string]string{"email": email, "password": "wrong-password"}, nil)
		r2, b2 := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/login",
			map[string]string{"email": testsupport.Email(), "password": "wrong-password"}, nil)
		assert.Equal(t, http.StatusUnauthorized, r1.StatusCode)
		assert.Equal(t, http.StatusUnauthorized, r2.StatusCode)
		assert.Equal(t, string(b1), string(b2))
		assert.Nil(t, testsupport.Cookie(r1, "refresh_token"))
	})

	t.Run("refresh without or with a garbage cookie is 401 and clears it", func(t *testing.T) {
		for name, header := range map[string]http.Header{"missing": nil, "garbage": withCookie("not-a-real-token")} {
			resp, body := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, header)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
			assert.Equal(t, "UNAUTHENTICATED", testsupport.ErrorCode(t, body), name)
			assertCleared(t, resp)
		}
	})

	t.Run("logout without or with a garbage cookie is 204", func(t *testing.T) {
		for name, header := range map[string]http.Header{"missing": nil, "garbage": withCookie("not-a-real-token")} {
			resp, _ := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/logout", nil, header)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode, name)
			assertCleared(t, resp)
		}
	})

	t.Run("me rejects anonymous, malformed and unknown identities", func(t *testing.T) {
		for name, header := range map[string]http.Header{
			"anonymous": nil,
			"malformed": {authctx.HeaderUserID: {"not-a-uuid"}},
			"unknown":   {authctx.HeaderUserID: {uuid.Must(uuid.NewV7()).String()}},
		} {
			resp, body := testsupport.Call(t, e.contract, http.MethodGet, e.url+"/api/users/me", nil, header)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
			assert.Equal(t, "UNAUTHENTICATED", testsupport.ErrorCode(t, body), name)
		}
	})

	t.Run("jwks publishes the signing key", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.contract, http.MethodGet, e.url+"/.well-known/jwks.json", nil, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var got token.JWKS
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, e.jwks, got)
	})

	t.Run("unknown route is a JSON 404", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.plain, http.MethodGet, e.url+"/api/nope", nil, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, "NOT_FOUND", testsupport.ErrorCode(t, body))
	})
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `cd services/identity && go test ./internal/adapter/http/...`
Expected: FAIL — build error `undefined: httpapi.NewRouter`.

- [ ] **Step 5: Write the implementation**

`services/identity/internal/adapter/http/router.go`:

```go
// Package httpapi serves identity-svc's REST API (api/openapi/identity.yaml).
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

// Config holds HTTP-layer settings.
type Config struct {
	CookieSecure bool          // false only for local development over plain HTTP
	RefreshTTL   time.Duration // Max-Age of the refresh cookie
}

type handler struct {
	auth *app.AuthService
	jwks token.JWKS
	cfg  Config
}

// NewRouter returns the REST API. authctx.Middleware must run in front of it.
func NewRouter(auth *app.AuthService, jwks token.JWKS, cfg Config) http.Handler {
	h := &handler{auth: auth, jwks: jwks, cfg: cfg}
	notFound := httpx.NotFound().ServeHTTP

	r := chi.NewRouter()
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)
	r.Post("/api/auth/register", h.register)
	r.Post("/api/auth/login", h.login)
	r.Post("/api/auth/refresh", h.refresh)
	r.Post("/api/auth/logout", h.logout)
	r.With(authctx.RequireUser).Get("/api/users/me", h.me)
	r.Get("/.well-known/jwks.json", h.getJWKS)
	return r
}
```

`services/identity/internal/adapter/http/dto.go`:

```go
package httpapi

import (
	"time"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// registerRequest has no validate tags: domain.NewRegistration owns the rules.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
}

type loginRequest struct {
	Email    string `json:"email" validate:"required"`
	Password string `json:"password" validate:"required"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"created_at"`
}

type tokenResponse struct {
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresIn   int64        `json:"expires_in"`
	User        userResponse `json:"user"`
}

func toUserResponse(u domain.User) userResponse {
	roles := make([]string, len(u.Roles))
	for i, r := range u.Roles {
		roles[i] = string(r)
	}
	return userResponse{ID: u.ID.String(), Email: u.Email, FullName: u.FullName, Roles: roles, CreatedAt: u.CreatedAt.UTC()}
}

func toTokenResponse(s app.Session) tokenResponse {
	return tokenResponse{
		AccessToken: s.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(s.ExpiresIn / time.Second),
		User:        toUserResponse(s.User),
	}
}
```

`services/identity/internal/adapter/http/errors.go`:

```go
package httpapi

import (
	"errors"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// toAppError maps domain errors to API errors; anything else stays a 500.
func toAppError(err error) error {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		return httpx.ValidationError(ve.Fields)
	case errors.Is(err, domain.ErrEmailTaken):
		return httpx.NewError(httpx.CodeConflict, "email is already registered")
	case errors.Is(err, domain.ErrInvalidCredentials):
		return httpx.NewError(httpx.CodeUnauthenticated, "invalid email or password")
	case isSessionError(err):
		return httpx.NewError(httpx.CodeUnauthenticated, "session is invalid or expired")
	case errors.Is(err, domain.ErrNotFound):
		return httpx.NewError(httpx.CodeUnauthenticated, "user no longer exists")
	}
	return err
}

func isSessionError(err error) bool {
	return errors.Is(err, domain.ErrInvalidRefreshToken) || errors.Is(err, domain.ErrRefreshTokenReused)
}
```

`services/identity/internal/adapter/http/handlers.go`:

```go
package httpapi

import (
	"net/http"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
)

const (
	refreshCookieName = "refresh_token"
	refreshCookiePath = "/api/auth"
)

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, err := h.auth.Register(r.Context(), req.Email, req.Password, req.FullName)
	if err != nil {
		httpx.WriteError(w, r, toAppError(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toUserResponse(u))
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	sess, err := h.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		httpx.WriteError(w, r, toAppError(err))
		return
	}
	h.writeSession(w, sess)
}

func (h *handler) refresh(w http.ResponseWriter, r *http.Request) {
	sess, err := h.auth.Refresh(r.Context(), refreshCookie(r))
	if err != nil {
		if isSessionError(err) {
			h.clearRefreshCookie(w)
		}
		httpx.WriteError(w, r, toAppError(err))
		return
	}
	h.writeSession(w, sess)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	err := h.auth.Logout(r.Context(), refreshCookie(r))
	h.clearRefreshCookie(w)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.auth.Me(r.Context(), authctx.UserID(r.Context()))
	if err != nil {
		httpx.WriteError(w, r, toAppError(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserResponse(u))
}

func (h *handler) getJWKS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpx.WriteJSON(w, http.StatusOK, h.jwks)
}

func (h *handler) writeSession(w http.ResponseWriter, s app.Session) {
	h.setRefreshCookie(w, s.RefreshToken, int(h.cfg.RefreshTTL/time.Second))
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, toTokenResponse(s))
}

func refreshCookie(r *http.Request) string {
	c, err := r.Cookie(refreshCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// setRefreshCookie writes the refresh cookie; maxAge < 0 emits Max-Age=0 (delete).
func (h *handler) setRefreshCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     refreshCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *handler) clearRefreshCookie(w http.ResponseWriter) {
	h.setRefreshCookie(w, "", -1)
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd services/identity && go mod tidy && go test ./internal/adapter/http/... -v`
Expected: PASS. A contract violation surfaces as `request violates contract` / `response violates contract` from `testsupport.Call` — fix the handler, never the spec.

- [ ] **Step 7: Commit**

```bash
git add services/identity
git commit -m "feat(identity): add REST handlers validated against identity.yaml"
```

---

### Task 8: Wire the service in cmd/server; cross-protocol and no-secrets-in-logs tests

**Files:**
- Create: `services/identity/cmd/server/components.go`
- Modify: `services/identity/cmd/server/main.go` (Config fields, key loading, gRPC registration, API handler)
- Test: `services/identity/cmd/server/integration_test.go`
- Test: `services/identity/cmd/server/config_test.go`

**Interfaces:**
- Consumes: everything above; existing `newHandler(logger, checks, api)` in `cmd/server/handler.go`.
- Produces:
  - `Config` gains `JWTPrivateKeyPath string`, `AccessTokenTTL`, `RefreshTokenTTL time.Duration`, `CookieSecure bool`.
  - `type components struct { API http.Handler; GRPC *grpcapi.Server }`
  - `func newComponents(pool *pgxpool.Pool, key *rsa.PrivateKey, cfg Config, bcryptCost int, logger *slog.Logger) (components, error)`

- [ ] **Step 1: Write the failing tests**

`services/identity/cmd/server/config_test.go`:

```go
package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

func TestConfigDefaults(t *testing.T) {
	t.Setenv("SERVICE_NAME", "identity")
	t.Setenv("JWT_PRIVATE_KEY_PATH", "/run/secrets/jwt_private.pem")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, cfg.AccessTokenTTL)
	assert.Equal(t, 720*time.Hour, cfg.RefreshTokenTTL)
	assert.True(t, cfg.CookieSecure)
}

func TestConfigCookieSecureCanBeDisabled(t *testing.T) {
	t.Setenv("SERVICE_NAME", "identity")
	t.Setenv("JWT_PRIVATE_KEY_PATH", "/k.pem")
	t.Setenv("COOKIE_SECURE", "false")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.False(t, cfg.CookieSecure)
}

func TestConfigRequiresKeyPath(t *testing.T) {
	t.Setenv("SERVICE_NAME", "identity")
	t.Setenv("JWT_PRIVATE_KEY_PATH", "")
	_, err := config.Load[Config]()
	assert.ErrorContains(t, err, "JWT_PRIVATE_KEY_PATH")
}
```

`services/identity/cmd/server/integration_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

const specPath = "../../../../api/openapi/identity.yaml"

// TestServiceWiring drives the fully wired service (middleware, REST, gRPC)
// and checks that no secret ever reaches the logs.
func TestServiceWiring(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(logger) // httpx.WriteError logs through the default logger
	t.Cleanup(func() { slog.SetDefault(prev) })

	pool := testsupport.Pool(t)
	cfg := Config{AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 720 * time.Hour, CookieSecure: false}
	comps, err := newComponents(pool, testsupport.RSAKey(t), cfg, bcrypt.MinCost, logger)
	require.NoError(t, err)

	srv := httptest.NewServer(newHandler(logger, map[string]server.Check{"postgres": pool.Ping}, comps.API))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: testutil.ContractTransport(t, specPath, nil)}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gs, _ := grpcx.NewServer()
	identityv1.RegisterIdentityInternalServer(gs, comps.GRPC)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpcx.Dial(lis.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	grpcClient := identityv1.NewIdentityInternalClient(conn)

	resp, _ := testsupport.Call(t, &http.Client{}, http.MethodGet, srv.URL+"/readyz", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	email, password := testsupport.Email(), "super secret password"
	resp, body := testsupport.Call(t, client, http.MethodPost, srv.URL+"/api/auth/register",
		map[string]string{"email": email, "password": password, "full_name": "Seller To Be"}, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
	var user struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &user))

	resp, body = testsupport.Call(t, client, http.MethodPost, srv.URL+"/api/auth/login",
		map[string]string{"email": email, "password": password}, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	cookie := testsupport.Cookie(resp, "refresh_token")
	require.NotNil(t, cookie)
	assert.False(t, cookie.Secure, "COOKIE_SECURE=false in dev")
	var login struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &login))

	// catalog-svc grants seller (twice: idempotent) ...
	for range 2 {
		_, err = grpcClient.GrantRole(context.Background(), &identityv1.GrantRoleRequest{UserId: user.ID, Role: "seller"})
		require.NoError(t, err)
	}

	// ... and the next refresh carries the new role, verifiable with the served JWKS.
	resp, body = testsupport.Call(t, client, http.MethodPost, srv.URL+"/api/auth/refresh", nil,
		http.Header{"Cookie": {"refresh_token=" + cookie.Value}})
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var refreshed struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &refreshed))

	resp, body = testsupport.Call(t, client, http.MethodGet, srv.URL+"/.well-known/jwks.json", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var jwks token.JWKS
	require.NoError(t, json.Unmarshal(body, &jwks))
	claims := testsupport.ParseAccessToken(t, refreshed.AccessToken, jwks)
	assert.Equal(t, []string{"buyer", "seller"}, claims.Roles)

	// Reuse of the rotated cookie triggers the warning log.
	resp, _ = testsupport.Call(t, client, http.MethodPost, srv.URL+"/api/auth/refresh", nil,
		http.Header{"Cookie": {"refresh_token=" + cookie.Value}})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	out := logs.String()
	require.NotEmpty(t, out)
	assert.Contains(t, out, "refresh token reuse detected")
	for name, secret := range map[string]string{
		"password": password, "refresh token": cookie.Value,
		"login access token": login.AccessToken, "refreshed access token": refreshed.AccessToken,
	} {
		assert.NotContains(t, out, secret, "%s leaked into logs", name)
	}
	assert.NotContains(t, out, "password_hash")
	assert.NotContains(t, out, "$2a$", "no bcrypt hash in logs")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd services/identity && go test ./cmd/server/...`
Expected: FAIL — build errors `undefined: newComponents`, `unknown field AccessTokenTTL`.

- [ ] **Step 3: Write the wiring**

`services/identity/cmd/server/components.go`:

```go
package main

import (
	"crypto/rsa"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	grpcapi "github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/grpc"
	httpapi "github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/http"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

// components are identity-svc's adapters wired to their dependencies.
type components struct {
	API  http.Handler
	GRPC *grpcapi.Server
}

func newComponents(pool *pgxpool.Pool, key *rsa.PrivateKey, cfg Config, bcryptCost int, logger *slog.Logger) (components, error) {
	store := postgres.NewStore(pool)
	signer := token.NewSigner(key, cfg.AccessTokenTTL)
	auth, err := app.NewAuthService(store, signer, app.AuthConfig{
		RefreshTTL: cfg.RefreshTokenTTL, BcryptCost: bcryptCost, Logger: logger,
	})
	if err != nil {
		return components{}, err
	}
	return components{
		API:  httpapi.NewRouter(auth, signer.JWKS(), httpapi.Config{CookieSecure: cfg.CookieSecure, RefreshTTL: cfg.RefreshTokenTTL}),
		GRPC: grpcapi.NewServer(app.NewRoleService(store), logger),
	}, nil
}
```

Edit `services/identity/cmd/server/main.go`:

1. Replace the `Config` type:

```go
// Config is identity-svc's configuration (WS0 base + WS1 §4).
type Config struct {
	config.Base
	JWTPrivateKeyPath string        `env:"JWT_PRIVATE_KEY_PATH,required,notEmpty"`
	AccessTokenTTL    time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL   time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"`
	CookieSecure      bool          `env:"COOKIE_SECURE" envDefault:"true"`
}
```

2. After the otel setup and before `pgxx.Migrate`, load the key (fail fast on a bad key):

```go
	key, err := token.LoadPrivateKey(cfg.JWTPrivateKeyPath)
	if err != nil {
		return err
	}
```

3. Replace the `checks` / `grpcSrv` / `server.Run` block with:

```go
	comps, err := newComponents(pool, key, cfg, app.DefaultBcryptCost, logger)
	if err != nil {
		return err
	}

	checks := map[string]server.Check{"postgres": pool.Ping}
	grpcSrv, _ := grpcx.NewServer()
	identityv1.RegisterIdentityInternalServer(grpcSrv, comps.GRPC)

	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, checks, comps.API), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		GRPC:            grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
```

4. Add imports: `identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"`, `"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"`, `"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd services/identity && go mod tidy && go test -race ./cmd/server/... -v`
Expected: PASS (including the pre-existing `handler_test.go` tests).

- [ ] **Step 5: Commit**

```bash
git add services/identity
git commit -m "feat(identity): wire REST, gRPC and config into the server"
```

---

### Task 9: Acceptance verification (WS1 §6)

**Files:** none created; fix anything the checks surface in the owning task's files and commit with `fix(identity): ...`.

- [ ] **Step 1: Whole-module tests with the race detector**

Run: `cd services/identity && go test -race -count=1 ./...`
Expected: every package `ok`. (Covers AC1 contract validation, AC2 log/response checks, AC3 reuse detection.)

- [ ] **Step 2: Lint and vet**

Run: `cd services/identity && ../../bin/golangci-lint run ./... && go vet ./...`
Expected: `0 issues.` and no vet output.

- [ ] **Step 3: Generated code is up to date and module is tidy**

Run: `make gen-sqlc && (cd services/identity && go mod tidy) && git status --porcelain services/identity`
Expected: empty output (no diff after regeneration/tidy).

- [ ] **Step 4: Nothing outside the owned paths changed**

Run: `git diff --name-only main...HEAD | grep -vE '^(services/identity/|docs/superpowers/plans/2026-09-28-ws1-identity.md)' || echo "only owned paths"`
Expected: `only owned paths`.

- [ ] **Step 5: Service is healthy in compose (AC4)**

Run:

```bash
make keys
docker compose -f deploy/docker-compose.yml --profile app up -d --build --wait identity
docker compose -f deploy/docker-compose.yml ps identity
ID=$(docker compose -f deploy/docker-compose.yml ps -q identity)
docker run --rm --network container:$ID curlimages/curl -s -w '\n%{http_code}\n' http://localhost:8081/readyz
```

Expected: `ps` shows `(healthy)`; curl prints a JSON body and `200`.

- [ ] **Step 6: Smoke the real container (register → login → JWKS) and scan its logs**

Run:

```bash
C="docker run --rm --network container:$ID curlimages/curl -s"
$C -X POST -H 'Content-Type: application/json' -d '{"email":"smoke@example.com","password":"smoke-secret-pw","full_name":"Smoke"}' -w '\n%{http_code}\n' http://localhost:8081/api/auth/register
$C -i -X POST -H 'Content-Type: application/json' -d '{"email":"smoke@example.com","password":"smoke-secret-pw"}' http://localhost:8081/api/auth/login | grep -iE '^HTTP|^set-cookie'
$C http://localhost:8081/.well-known/jwks.json
docker compose -f deploy/docker-compose.yml logs identity | grep -ciE 'smoke-secret-pw|refresh_token=|eyJ' || true
```

Expected: register `201` (or `409` on a re-run), login `HTTP/1.1 200` with `Set-Cookie: refresh_token=...; Path=/api/auth; Max-Age=2592000; HttpOnly; SameSite=Lax` (no `Secure`: compose sets `COOKIE_SECURE=false`), JWKS with one RS256 key, log grep count `0`.

- [ ] **Step 7: Tear down**

Run: `make down`

- [ ] **Step 8: Report the acceptance criteria**

Map outputs to WS1 §6: endpoints + OpenAPI (Steps 1, 6); no secrets logged / no `password_hash` returned (Steps 1, 6); reuse detection (Step 1: `TestRefreshAndLogout`, `TestAuthFlow`, `TestServiceWiring`); `go test` + lint + `/readyz` 200 (Steps 1, 2, 5). Then hand off to superpowers:finishing-a-development-branch.
