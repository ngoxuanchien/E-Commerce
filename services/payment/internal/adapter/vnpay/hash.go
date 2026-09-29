// Package vnpay is the VNPay sandbox adapter (API 2.1.0, WS1' spec §5.1): the signed pay URL,
// the IPN and browser return, and the querydr and refund merchant API calls.
package vnpay

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

const (
	fieldSecureHash     = "vnp_SecureHash"
	fieldSecureHashType = "vnp_SecureHashType"
)

// hashData is the string VNPay signs for URL parameters: every non-empty vnp_* parameter except
// the hash fields, sorted by key, as key=urlencode(value) joined by "&". Empty values are left
// out, as VNPay's own samples do. url.QueryEscape encodes a space as "+", like PHP urlencode.
func hashData(params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if strings.HasPrefix(k, "vnp_") && k != fieldSecureHash && k != fieldSecureHashType && params.Get(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(k))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(params.Get(k)))
	}
	return b.String()
}

func mac(secret, data string) []byte {
	m := hmac.New(sha512.New, []byte(secret))
	m.Write([]byte(data))
	return m.Sum(nil)
}

// SecureHash is the lower-case hex HMAC-SHA512 of params' hash data (see hashData).
func SecureHash(secret string, params url.Values) string {
	return hex.EncodeToString(mac(secret, hashData(params)))
}

// VerifyQuery reports whether q carries a valid vnp_SecureHash. The hex is decoded first, so the
// comparison is case-insensitive, and it runs in constant time.
func VerifyQuery(secret string, q url.Values) bool {
	return verifyHex(q.Get(fieldSecureHash), mac(secret, hashData(q)))
}

func verifyHex(got string, want []byte) bool {
	b, err := hex.DecodeString(got)
	return err == nil && len(b) == len(want) && hmac.Equal(b, want)
}

// PipeHash is the lower-case hex HMAC-SHA512 over fields joined by "|", the merchant API's checksum.
func PipeHash(secret string, fields ...string) string {
	return hex.EncodeToString(mac(secret, strings.Join(fields, "|")))
}

// TxnRef is the vnp_TxnRef for a payment (or the vnp_RequestId for a refund): the UUID without dashes.
func TxnRef(paymentID string) string { return strings.ReplaceAll(paymentID, "-", "") }
