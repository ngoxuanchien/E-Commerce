// Package authtest provides test helpers for signing RS256 access tokens
// and serving a JWKS endpoint, for use by tests in internal/auth and its
// consumers. It does not import internal/auth, to avoid an import cycle.
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer is the "iss" claim used by every token this package signs. It
// mirrors what identity issues in production, but is defined here (rather
// than imported from internal/auth) to avoid an import cycle between
// authtest and the auth package it helps test.
const Issuer = "identity"

// Key is an RSA key pair paired with a JWKS "kid", used to sign test tokens
// and to build the matching JWKS entry.
type Key struct {
	kid        string
	privateKey *rsa.PrivateKey
}

// NewKey generates a fresh 2048-bit RSA key pair identified by kid.
func NewKey(t *testing.T, kid string) *Key {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("authtest: generate RSA key: %v", err)
	}
	return &Key{kid: kid, privateKey: priv}
}

// JWK returns this key's public JWKS entry: the string-valued fields
// ("kty", "kid", "use", "alg", "n", "e") a JWKS document embeds in "keys".
func (k *Key) JWK() map[string]string {
	pub := k.privateKey.PublicKey
	return map[string]string{
		"kty": "RSA",
		"kid": k.kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(minimalBigEndian(pub.E)),
	}
}

// minimalBigEndian returns the shortest big-endian encoding of a
// non-negative int, as JWK "e" (public exponent) fields use.
func minimalBigEndian(n int) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(n))
	i := 0
	for i < len(buf)-1 && buf[i] == 0 {
		i++
	}
	return buf[i:]
}

// Sign signs claims as an RS256 JWT, setting this key's kid in the token
// header.
func (k *Key) Sign(t *testing.T, claims jwt.Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = k.kid
	signed, err := token.SignedString(k.privateKey)
	if err != nil {
		t.Fatalf("authtest: sign token: %v", err)
	}
	return signed
}

// Claims is the access token claim set identity issues.
type Claims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles"`
}

// ValidClaims returns a Claims for subject sub with the given roles: issuer
// Issuer, issued now, expiring 15 minutes from now.
func ValidClaims(sub string, roles ...string) Claims {
	now := time.Now()
	return Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   sub,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
		Roles: roles,
	}
}

// JWKSBody renders keys as a JWKS document body: {"keys":[...]}.
func JWKSBody(keys ...*Key) []byte {
	jwks := make([]map[string]string, len(keys))
	for i, k := range keys {
		jwks[i] = k.JWK()
	}
	body, err := json.Marshal(map[string]any{"keys": jwks})
	if err != nil {
		// Marshaling a map of strings can never fail.
		panic(fmt.Sprintf("authtest: marshal JWKS: %v", err))
	}
	return body
}

// Server is an httptest.Server serving a JWKS document, reconfigurable
// mid-test to simulate key rotation or upstream failures.
type Server struct {
	*httptest.Server

	mu     sync.Mutex
	status int
	body   []byte

	hits atomic.Int64
}

// NewServer starts a JWKS server that initially serves keys with a 200
// response. The server is closed automatically when t completes.
func NewServer(t *testing.T, keys ...*Key) *Server {
	t.Helper()
	s := &Server{status: http.StatusOK, body: JWKSBody(keys...)}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		status, body := s.status, s.body
		s.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Server.Close)
	return s
}

// SetKeys replaces the server's key set, resetting it to a healthy 200
// response.
func (s *Server) SetKeys(keys ...*Key) {
	s.Set(http.StatusOK, JWKSBody(keys...))
}

// Set replaces the server's response with the given status and raw body,
// for tests exercising failure paths (non-200, malformed JSON, empty key
// sets, oversized bodies).
func (s *Server) Set(status int, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	s.body = body
}

// Hits returns the number of requests the server has received so far.
func (s *Server) Hits() int {
	return int(s.hits.Load())
}
