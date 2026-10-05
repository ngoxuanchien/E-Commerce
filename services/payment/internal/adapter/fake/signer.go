// Package fake is the FAKE payment provider: a hosted page, signed delayed webhooks and
// test hooks. It behaves like an asynchronous provider so the real verification path runs.
package fake

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// ErrBadSignature is returned for a malformed, forged or stale X-Fake-Signature.
var ErrBadSignature = errors.New("fake: bad signature")

const tolerance = 300 * time.Second

func mac(secret string, ts int64, body []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write(body)
	return m.Sum(nil)
}

// Sign returns "t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + body)>".
func Sign(secret string, body []byte, t time.Time) string {
	ts := t.Unix()
	return "t=" + strconv.FormatInt(ts, 10) + ",v1=" + hex.EncodeToString(mac(secret, ts, body))
}

// Verify checks the header MAC (constant time) and that |now - t| <= 300 s.
func Verify(secret string, body []byte, header string, now time.Time) error {
	var ts int64
	var sig []byte
	var haveT, haveV bool
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return ErrBadSignature
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return ErrBadSignature
			}
			ts, haveT = n, true
		case "v1":
			b, err := hex.DecodeString(v)
			if err != nil {
				return ErrBadSignature
			}
			sig, haveV = b, true
		}
	}
	if !haveT || !haveV {
		return ErrBadSignature
	}
	if !hmac.Equal(sig, mac(secret, ts, body)) {
		return ErrBadSignature
	}
	if d := now.Sub(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return ErrBadSignature
	}
	return nil
}

// Event is the webhook body.
type Event struct {
	EventID   string `json:"event_id"`
	PaymentID string `json:"payment_id"`
	Outcome   string `json:"outcome"` // succeed | fail | pending
}

// Observed maps the event to what the provider reported; amount is the payment's amount.
func (e Event) Observed(amount int64) domain.Observed {
	switch e.Outcome {
	case "succeed":
		return domain.Observed{State: domain.ObservedSucceeded, Amount: amount, ProviderTxnID: txnID(e.PaymentID)}
	case "fail":
		return domain.Observed{State: domain.ObservedFailed, Reason: "fake_declined"}
	default:
		return domain.Observed{State: domain.ObservedPending}
	}
}

func txnID(paymentID string) string { return "fake_txn_" + paymentID }
