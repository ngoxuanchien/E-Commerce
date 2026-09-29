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
