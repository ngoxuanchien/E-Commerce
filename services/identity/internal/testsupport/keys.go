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
