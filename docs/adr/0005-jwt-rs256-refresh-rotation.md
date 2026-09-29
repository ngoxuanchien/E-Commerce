# ADR 0005 — RS256 JWT access tokens with rotating refresh tokens

- Status: Accepted
- Date: 2026-09-28

## Context

Every request passes through the gateway, which must authenticate it
without calling identity each time. The browser app needs a session that
survives reloads without exposing long-lived credentials to JavaScript. A
user's roles change at runtime: opening a shop makes them a seller.

## Decision

- **Access tokens:** identity signs short-lived (15 min) RS256 JWTs with a
  `kid` header and the claims `iss=identity`, `sub`, `roles`, `iat` and
  `exp`. It publishes the public key as JWKS on the internal network only.
- **Gateway verification:** the gateway verifies tokens locally against a
  cached JWKS. The cache is loaded at startup (`/readyz` stays 503 until
  then), refreshed every 5 minutes, and refreshed on an unknown `kid` at
  most every 30 s. After verifying, the gateway injects `X-User-Id` /
  `X-User-Roles` and strips any client-supplied copies.
- **Refresh tokens:** 32 random bytes; only a SHA-256 hash is stored. They're
  sent as an `HttpOnly; SameSite=Lax; Path=/api/auth` cookie valid for 30
  days. Every `/api/auth/refresh` rotates the token within its family and
  re-reads roles from the database. Presenting an already-rotated token
  revokes the whole family, which is how theft is detected.
- **Web session:** the web app keeps the access token in memory only. On
  load it calls `/refresh` to restore the session. On a 401 it runs one
  single-flight refresh and retries once.

## Consequences

- Services never see tokens, and they trust headers only because the
  gateway is the only published entry point.
- A role change takes effect on the next refresh. The web app refreshes
  right after opening a shop; other tokens keep their old roles for up to
  15 minutes.
- Key rotation would work by adding a key to the JWKS (the gateway picks it
  up on `kid` miss); P1 runs with one dev key from `make keys`.
- The e2e suite checks the real-gateway behavior: rotation, reuse detection,
  logout, and the session surviving a reload.
