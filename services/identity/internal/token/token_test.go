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
