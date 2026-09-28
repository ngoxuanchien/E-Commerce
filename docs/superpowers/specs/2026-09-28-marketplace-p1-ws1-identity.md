# WS1 — identity-svc

> Depends on: WS0 (`api/openapi/identity.yaml`, `proto/identity/v1`, `pkg/`). Owns: `services/identity/`.

## 1. Responsibility

User accounts, login, issuing/refreshing JWTs, role management, and publishing JWKS for the gateway. Knows nothing about shops or orders.

## 2. Data (`identity_db`)

```sql
users(id uuid pk, email citext unique not null, password_hash text not null,
      full_name text not null, created_at, updated_at)
user_roles(user_id uuid fk, role text check (role in ('buyer','seller','admin')),
           created_at, primary key (user_id, role))
refresh_tokens(id uuid pk, user_id uuid fk, family_id uuid not null, token_hash bytea unique not null,
               expires_at, revoked_at null, replaced_by uuid null, created_at)
```

## 3. Behavior

**Register:** email is trimmed, lowercased and format-checked; password 8–72 chars (bcrypt limit); `full_name` 1–100 chars. bcrypt cost 12. Assign role `buyer`. Duplicate email → 409 `CONFLICT`. Registration does not log the user in.

**Login:** wrong email or wrong password → the same 401 `UNAUTHENTICATED` error (does not reveal whether the email exists; runs bcrypt against a dummy hash when the user is missing to equalize timing). Success → access token + new refresh token (new family).

**Access token:** RS256 JWT with `kid` header; claims `iss="identity"`, `sub=user_id`, `roles=[...]`, `iat`, `exp` = +15 min. Private key loaded from `JWT_PRIVATE_KEY_PATH` (PEM); `kid` = truncated SHA-256 thumbprint of the public key.

**Refresh token:** 32 random bytes (base64url); only the SHA-256 hash is stored. Cookie `refresh_token`: `HttpOnly; Secure` (unless `COOKIE_SECURE=false` in dev); `SameSite=Lax; Path=/api/auth; Max-Age=30 days`.
- `/refresh`: valid token → revoke the old token, issue a new one in the same `family_id`, set `replaced_by`; roles are **re-read from the DB** (so a user who just opened a shop gets `seller`).
- Reuse of an already-revoked token → **revoke the whole family** + 401 (theft detection).
- Expired / unknown → 401 + clear cookie.
- `/logout`: revoke the current token if present, clear cookie, always 204.

**`GET /api/users/me`:** reads `X-User-Id` (via `pkg/authctx`) and returns the `User` with current roles from the DB.

**JWKS:** `GET /.well-known/jwks.json` returns the current public key (internal network only; not routed by the gateway).

**gRPC `GrantRole(user_id, role)`:** only `seller` is accepted; unknown user → `NOT_FOUND`; already granted → success (idempotent, `INSERT ... ON CONFLICT DO NOTHING`).

## 4. Code layout

```
internal/domain             User, Role, Email/Password value objects + validation
internal/app                AuthService (Register, Login, Refresh, Logout, Me), RoleService (Grant)
internal/token              JWT signer + JWKS builder, refresh token generator/hasher
internal/adapter/postgres   sqlc repositories
internal/adapter/http       chi handlers, domain errors → httpx.AppError
internal/adapter/grpc       IdentityInternal server
```
Service-specific env: `JWT_PRIVATE_KEY_PATH`, `ACCESS_TOKEN_TTL=15m`, `REFRESH_TOKEN_TTL=720h`, `COOKIE_SECURE`.

## 5. Testing

- Unit: email/password validation; sign & verify JWT against the generated JWKS; refresh token hashing.
- Integration (Postgres container, HTTP via `httptest`): register → login → me → refresh (old token can't be reused) → reuse old token ⇒ whole family revoked → logout.
- gRPC `GrantRole` is idempotent (called twice); unknown user → `NOT_FOUND`; after granting, `/refresh` returns a token containing `seller`.
- Contract: every request/response in integration tests is validated against `api/openapi/identity.yaml`.

## 6. Acceptance criteria

- [ ] All endpoints in `identity.yaml` work and responses match the OpenAPI spec.
- [ ] Passwords/tokens are never logged; `password_hash` is never returned.
- [ ] Refresh-token reuse detection works as described.
- [ ] `go test ./...` + lint green; the service is healthy in compose (`/readyz` 200).
