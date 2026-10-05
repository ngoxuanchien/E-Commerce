package vnpay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// IPN acknowledgement codes and messages (spec §5.1).
const (
	RspConfirmed        = "00"
	RspOrderNotFound    = "01"
	RspAlreadyConfirmed = "02"
	RspInvalidAmount    = "04"
	RspInvalidChecksum  = "97"
	RspUnknownError     = "99"
)

var rspMessages = map[string]string{
	RspConfirmed:        "Confirm Success",
	RspOrderNotFound:    "Order not found",
	RspAlreadyConfirmed: "Order already confirmed",
	RspInvalidAmount:    "Invalid amount",
	RspInvalidChecksum:  "Invalid Checksum",
	RspUnknownError:     "Unknown error",
}

// returnQueryTimeout bounds the querydr call made while the buyer's browser waits on the return.
const returnQueryTimeout = 5 * time.Second

// Callbacks holds the IPN and browser-return logic; the http adapter serves it.
type Callbacks struct {
	provider *Provider
	store    app.Store
	observer *app.Observer
	logger   *slog.Logger
}

// NewCallbacks wires Callbacks; logger defaults to slog.Default().
func NewCallbacks(p *Provider, store app.Store, observer *app.Observer, logger *slog.Logger) Callbacks {
	if logger == nil {
		logger = slog.Default()
	}
	return Callbacks{provider: p, store: store, observer: observer, logger: logger}
}

func rsp(code string) (string, string) { return code, rspMessages[code] }

// eventKey dedupes IPN deliveries: hex(sha256(TxnRef|TransactionNo|ResponseCode|TransactionStatus)).
func eventKey(q url.Values) string {
	sum := sha256.Sum256([]byte(q.Get("vnp_TxnRef") + "|" + q.Get("vnp_TransactionNo") + "|" +
		q.Get("vnp_ResponseCode") + "|" + q.Get("vnp_TransactionStatus")))
	return hex.EncodeToString(sum[:])
}

// IPN applies a VNPay server-to-server notification. The checks run in the spec's order and the
// first failure answers: 97 → 01 → 04 → 02 → 00, with 99 for an unexpected error (VNPay retries).
// A verified IPN is authoritative for its own transaction, so no querydr is made.
func (c Callbacks) IPN(ctx context.Context, q url.Values) (rspCode, message string) {
	if !VerifyQuery(c.provider.cfg.HashSecret, q) {
		return rsp(RspInvalidChecksum)
	}
	if q.Get("vnp_TmnCode") != c.provider.cfg.TmnCode {
		return rsp(RspOrderNotFound)
	}
	p, err := c.store.GetPaymentByProviderRef(ctx, providerName, q.Get("vnp_TxnRef"))
	switch {
	case errors.Is(err, app.ErrNotFound):
		return rsp(RspOrderNotFound)
	case err != nil:
		c.logger.ErrorContext(ctx, "vnpay ipn: load payment", "txn_ref", q.Get("vnp_TxnRef"), "error", err)
		return rsp(RspUnknownError)
	}
	amount, err := strconv.ParseInt(q.Get("vnp_Amount"), 10, 64)
	if err != nil || amount != p.Amount*100 {
		c.logger.WarnContext(ctx, "vnpay ipn: amount mismatch", "payment_id", p.ID, "expected", p.Amount*100, "got", q.Get("vnp_Amount"))
		return rsp(RspInvalidAmount)
	}
	if p.Status.Final() {
		return rsp(RspAlreadyConfirmed)
	}
	obs := c.provider.mapStatus(ctx, q.Get("vnp_ResponseCode"), q.Get("vnp_TransactionStatus"),
		q.Get("vnp_ResponseCode"), p.ID, q.Get("vnp_TransactionNo"))
	if obs.State == domain.ObservedSucceeded {
		obs.Amount, obs.ProviderTxnID = amount/100, q.Get("vnp_TransactionNo")
	}
	if obs.State == domain.ObservedPending {
		// The transaction is not settled: nothing to apply and nothing to record, so a later real
		// IPN is not taken for a duplicate. The reconciler resolves it through querydr.
		c.logger.InfoContext(ctx, "vnpay ipn: transaction not settled; leaving the payment pending",
			"payment_id", p.ID, "transaction_status", q.Get("vnp_TransactionStatus"))
		return rsp(RspConfirmed)
	}
	out, err := c.observer.ApplyObserved(ctx, providerName, eventKey(q), p.ID, obs)
	switch {
	case err != nil:
		c.logger.ErrorContext(ctx, "vnpay ipn: apply", "payment_id", p.ID, "error", err)
		return rsp(RspUnknownError)
	case out.AmountMismatch: // backstop; the amount was checked above
		return rsp(RspInvalidAmount)
	case out.Unchanged: // a duplicate delivery, or nothing left to change
		return rsp(RspAlreadyConfirmed)
	}
	return rsp(RspConfirmed)
}

// Return handles the buyer's browser coming back. With a valid signature it syncs the payment from
// querydr (never from the return parameters, ADR-0008) and returns the payment id for the result
// page; otherwise it returns "". Query and apply errors are logged, not surfaced: the IPN and the
// reconciler catch up.
func (c Callbacks) Return(ctx context.Context, q url.Values) (paymentID string) {
	if !VerifyQuery(c.provider.cfg.HashSecret, q) || q.Get("vnp_TmnCode") != c.provider.cfg.TmnCode {
		return ""
	}
	p, err := c.store.GetPaymentByProviderRef(ctx, providerName, q.Get("vnp_TxnRef"))
	if err != nil {
		if !errors.Is(err, app.ErrNotFound) {
			c.logger.ErrorContext(ctx, "vnpay return: load payment", "txn_ref", q.Get("vnp_TxnRef"), "error", err)
		}
		return ""
	}
	if p.Status.Final() {
		return p.ID
	}
	qctx, cancel := context.WithTimeout(ctx, returnQueryTimeout)
	defer cancel()
	obs, err := c.provider.Query(qctx, p)
	if err != nil {
		c.logger.ErrorContext(ctx, "vnpay return: query", "payment_id", p.ID, "error", err)
		return p.ID
	}
	if _, err := c.observer.ApplyObserved(ctx, providerName, "", p.ID, obs); err != nil {
		c.logger.ErrorContext(ctx, "vnpay return: apply", "payment_id", p.ID, "error", err)
	}
	return p.ID
}
