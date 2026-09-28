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
