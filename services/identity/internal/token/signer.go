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
