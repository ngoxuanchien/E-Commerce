package vnpay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// Config wires the VNPay adapter. Now and HTTPClient default when nil.
type Config struct {
	TmnCode, HashSecret, PayURL, APIURL, PublicBaseURL string
	Now                                                func() time.Time
	HTTPClient                                         *http.Client
	Logger                                             *slog.Logger
	// Payments reads a payment for QueryRefund (its TxnRef and creation date); app.Store fits.
	Payments PaymentReader
}

// PaymentReader loads a payment by id (app.ErrNotFound when missing).
type PaymentReader interface {
	GetPayment(ctx context.Context, id string) (domain.Payment, error)
}

// gmt7 is VNPay's clock. Asia/Ho_Chi_Minh has no DST, so a fixed zone needs no tzdata (distroless).
var gmt7 = time.FixedZone("GMT+7", 7*3600)

const (
	providerName = "vnpay"
	apiVersion   = "2.1.0"
	dateLayout   = "20060102150405"
	// serverIP is vnp_IpAddr on merchant API calls, which come from the server, not a buyer.
	serverIP = "127.0.0.1"
	// maxResponse bounds a merchant API response body.
	maxResponse = 64 << 10
)

// Merchant API checksum field orders, from
// https://sandbox.vnpayment.vn/apis/docs/truy-van-hoan-tien/querydr&refund.html (read 2026-09-29).
var (
	queryRequestFields = []string{"vnp_RequestId", "vnp_Version", "vnp_Command", "vnp_TmnCode", "vnp_TxnRef",
		"vnp_TransactionDate", "vnp_CreateDate", "vnp_IpAddr", "vnp_OrderInfo"}
	queryResponseFields = []string{"vnp_ResponseId", "vnp_Command", "vnp_ResponseCode", "vnp_Message", "vnp_TmnCode",
		"vnp_TxnRef", "vnp_Amount", "vnp_BankCode", "vnp_PayDate", "vnp_TransactionNo", "vnp_TransactionType",
		"vnp_TransactionStatus", "vnp_OrderInfo", "vnp_PromotionCode", "vnp_PromotionAmount"}
	refundRequestFields = []string{"vnp_RequestId", "vnp_Version", "vnp_Command", "vnp_TmnCode", "vnp_TransactionType",
		"vnp_TxnRef", "vnp_Amount", "vnp_TransactionNo", "vnp_TransactionDate", "vnp_CreateBy", "vnp_CreateDate",
		"vnp_IpAddr", "vnp_OrderInfo"}
	refundResponseFields = []string{"vnp_ResponseId", "vnp_Command", "vnp_ResponseCode", "vnp_Message", "vnp_TmnCode",
		"vnp_TxnRef", "vnp_Amount", "vnp_BankCode", "vnp_PayDate", "vnp_TransactionNo", "vnp_TransactionType",
		"vnp_TransactionStatus", "vnp_OrderInfo"}
)

// Provider implements app.Provider for VNPay.
type Provider struct{ cfg Config }

var _ app.Provider = (*Provider)(nil)

func New(cfg Config) *Provider {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string { return providerName }

func vnDate(t time.Time) string { return t.In(gmt7).Format(dateLayout) }

const maxOrderInfo = 255

// orderInfo sanitizes vnp_OrderInfo: only [A-Za-z0-9 ] and ":-_." survive (no diacritics, and no
// character that PHP urlencode and Go's QueryEscape encode differently), runs of spaces collapse,
// and the result is capped at 255 characters.
func orderInfo(s string) string {
	kept := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == ':', r == '-', r == '_', r == '.':
			return r
		}
		return -1
	}, s)
	out := strings.Join(strings.Fields(kept), " ")
	if len(out) > maxOrderInfo {
		out = strings.TrimSpace(out[:maxOrderInfo])
	}
	return out
}

// CreateSession signs the pay URL (spec §5.1). ProviderRef is vnp_TxnRef. vnp_CreateDate is
// in.CreatedAt, the payment's stored created_at, so querydr and refund can send the same instant as
// vnp_TransactionDate; Now is only a fallback when CreatedAt is unset.
func (p *Provider) CreateSession(_ context.Context, in app.SessionInput) (app.Session, error) {
	ref := TxnRef(in.PaymentID)
	info := orderInfo(in.Description)
	if info == "" {
		info = "Thanh toan " + ref
	}
	ip := in.ClientIP
	if ip == "" {
		ip = serverIP
	}
	v := url.Values{}
	v.Set("vnp_Version", apiVersion)
	v.Set("vnp_Command", "pay")
	v.Set("vnp_TmnCode", p.cfg.TmnCode)
	v.Set("vnp_Amount", strconv.FormatInt(in.Amount*100, 10))
	v.Set("vnp_CurrCode", "VND")
	v.Set("vnp_TxnRef", ref)
	v.Set("vnp_OrderInfo", info)
	v.Set("vnp_OrderType", "other")
	v.Set("vnp_Locale", "vn")
	v.Set("vnp_ReturnUrl", strings.TrimRight(p.cfg.PublicBaseURL, "/")+"/api/payments/vnpay/return")
	v.Set("vnp_IpAddr", ip)
	created := in.CreatedAt
	if created.IsZero() {
		created = p.cfg.Now()
	}
	v.Set("vnp_CreateDate", vnDate(created))
	v.Set("vnp_ExpireDate", vnDate(in.ExpiresAt))
	// Every value is non-empty, so v.Encode() (sorted, QueryEscape'd) is exactly the hash data.
	return app.Session{
		RedirectURL: p.cfg.PayURL + "?" + v.Encode() + "&" + fieldSecureHash + "=" + SecureHash(p.cfg.HashSecret, v),
		ProviderRef: ref,
	}, nil
}

// querydr calls querydr for a payment's transaction. vnp_TransactionDate is the payment's created_at,
// which CreateSession signed as vnp_CreateDate. The response is returned for RC 00 and 91 only.
func (p *Provider) querydr(ctx context.Context, pay domain.Payment) (map[string]string, error) {
	ref := TxnRef(pay.ID)
	req := map[string]string{
		"vnp_RequestId": TxnRef(uuid.NewString()), "vnp_Version": apiVersion, "vnp_Command": "querydr",
		"vnp_TmnCode": p.cfg.TmnCode, "vnp_TxnRef": ref, "vnp_OrderInfo": "Truy van giao dich " + ref,
		"vnp_TransactionDate": vnDate(pay.CreatedAt), "vnp_CreateDate": vnDate(p.cfg.Now()), "vnp_IpAddr": serverIP,
	}
	if pay.ProviderTxnID != "" {
		req["vnp_TransactionNo"] = pay.ProviderTxnID // optional, not part of the checksum
	}
	resp, err := p.call(ctx, req, queryRequestFields, queryResponseFields)
	if err != nil {
		return nil, err
	}
	switch code := resp["vnp_ResponseCode"]; code {
	case "00":
		if resp["vnp_TxnRef"] != ref {
			return nil, fmt.Errorf("%w: vnpay querydr answered for another transaction", app.ErrProviderUnavailable)
		}
		return resp, nil
	case "91": // transaction not found
		return resp, nil
	default:
		return nil, fmt.Errorf("%w: vnpay querydr response code %s", app.ErrProviderUnavailable, code)
	}
}

// Query maps querydr's vnp_TransactionStatus for the payment:
//   - 00 → Succeeded (vnp_Amount must be a whole number of dong × 100);
//   - 01 (not completed), 05 (VNPay processing), 06 (refund sent to the bank) → Pending;
//   - 07 (suspected fraud; the money may have been taken) → Pending with a warning, so the reconciler
//     keeps asking and no false PaymentFailed is emitted;
//   - anything else → Failed(status);
//   - RC 91 (not found) → Pending before ExpiresAt, Expired after.
func (p *Provider) Query(ctx context.Context, pay domain.Payment) (domain.Observed, error) {
	resp, err := p.querydr(ctx, pay)
	if err != nil {
		return domain.Observed{}, err
	}
	if resp["vnp_ResponseCode"] == "91" {
		if p.cfg.Now().Before(pay.ExpiresAt) {
			return domain.Observed{State: domain.ObservedPending}, nil
		}
		return domain.Observed{State: domain.ObservedExpired}, nil
	}
	switch status := resp["vnp_TransactionStatus"]; status {
	case "00":
		amount, err := strconv.ParseInt(resp["vnp_Amount"], 10, 64)
		if err != nil || amount%100 != 0 {
			return domain.Observed{}, fmt.Errorf("%w: vnpay querydr amount %q", app.ErrProviderUnavailable, resp["vnp_Amount"])
		}
		return domain.Observed{State: domain.ObservedSucceeded, Amount: amount / 100, ProviderTxnID: resp["vnp_TransactionNo"]}, nil
	case "01", "05", "06":
		return domain.Observed{State: domain.ObservedPending}, nil
	case "07":
		p.cfg.Logger.WarnContext(ctx, "vnpay: transaction suspected of fraud; keeping the payment pending",
			"payment_id", pay.ID, "transaction_no", resp["vnp_TransactionNo"])
		return domain.Observed{State: domain.ObservedPending}, nil
	default:
		return domain.Observed{State: domain.ObservedFailed, Reason: status}, nil
	}
}

// Cancel is not supported by VNPay; the session becomes unpayable at vnp_ExpireDate.
func (p *Provider) Cancel(context.Context, domain.Payment) (bool, error) { return false, nil }

// Refund calls the refund API: type 02 (full) when the amount is the whole payment, else 03 (partial).
// vnp_RequestId is derived from the refund id, so a retry is recognisable to VNPay; the same value is
// the ProviderRefundRef.
//   - RC 00 → Succeeded (optimistic: VNPay accepted it; there is no per-request status to poll),
//     unless vnp_TransactionStatus is 09 (refund declined) → Failed.
//   - RC 94 (a refund was already requested for this transaction, e.g. a retry after a lost answer)
//     → Pending with the ref set, so the worker calls QueryRefund, which resolves it via querydr.
//   - RC 02, 03, 91, 95 → Failed. RC 97, 99 and anything else → ErrProviderUnavailable (retried).
func (p *Provider) Refund(ctx context.Context, in app.RefundInput) (app.RefundObserved, error) {
	typ := "03"
	if in.Amount == in.Payment.Amount {
		typ = "02"
	}
	reqID := TxnRef(in.RefundID)
	req := map[string]string{
		"vnp_RequestId": reqID, "vnp_Version": apiVersion, "vnp_Command": "refund", "vnp_TmnCode": p.cfg.TmnCode,
		"vnp_TransactionType": typ, "vnp_TxnRef": TxnRef(in.Payment.ID), "vnp_Amount": strconv.FormatInt(in.Amount*100, 10),
		"vnp_OrderInfo": "Hoan tien " + reqID, "vnp_TransactionNo": in.Payment.ProviderTxnID,
		"vnp_TransactionDate": vnDate(in.Payment.CreatedAt), "vnp_CreateBy": "system",
		"vnp_CreateDate": vnDate(p.cfg.Now()), "vnp_IpAddr": serverIP,
	}
	resp, err := p.call(ctx, req, refundRequestFields, refundResponseFields)
	if err != nil {
		return app.RefundObserved{}, err
	}
	switch code := resp["vnp_ResponseCode"]; code {
	case "00":
		if s := resp["vnp_TransactionStatus"]; s == "09" {
			return app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_status_" + s}, nil
		}
		return app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: reqID}, nil
	case "94":
		return app.RefundObserved{State: app.RefundPending, ProviderRefundRef: reqID}, nil
	case "02", "03", "91", "95":
		// Rejected by VNPay (bad merchant, bad data, unknown transaction, declined).
		return app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_" + code}, nil
	default: // 97 checksum, 99 other: retry later
		return app.RefundObserved{}, fmt.Errorf("%w: vnpay refund response code %s", app.ErrProviderUnavailable, code)
	}
}

// QueryRefund resolves a refund that Refund left Pending (RC 94) by querying the original transaction
// with querydr, whose vnp_TransactionType reports 02/03 once a refund is recorded against it. It always
// answers a terminal state (or an error), because the worker does not count Pending as an attempt:
//   - type 02/03 with status 00 (done), 05 (VNPay processing) or 06 (sent to the bank) → Succeeded,
//     the same optimism as Refund's RC 00;
//   - type 02/03 with any other status (09 declined, 02 error, …) → Failed(vnpay_status_<status>);
//   - no refund visible (type 01) → Failed(vnpay_refund_not_found); RC 91 → Failed(vnpay_91).
//
// Failed is retried by the worker until maxAttempts, which gives VNPay time to record the refund.
// A refund that was never requested (no ref) is Pending: the worker calls Refund for it.
func (p *Provider) QueryRefund(ctx context.Context, r app.Refund) (app.RefundObserved, error) {
	if r.ProviderRefundRef == "" {
		return app.RefundObserved{State: app.RefundPending}, nil
	}
	if p.cfg.Payments == nil {
		return app.RefundObserved{}, errors.New("vnpay query refund: Config.Payments is not set")
	}
	pay, err := p.cfg.Payments.GetPayment(ctx, r.PaymentID)
	if err != nil {
		return app.RefundObserved{}, fmt.Errorf("vnpay query refund: load payment: %w", err)
	}
	resp, err := p.querydr(ctx, pay)
	if err != nil {
		return app.RefundObserved{}, err
	}
	if code := resp["vnp_ResponseCode"]; code != "00" {
		return app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_" + code}, nil
	}
	if t := resp["vnp_TransactionType"]; t != "02" && t != "03" {
		return app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_refund_not_found"}, nil
	}
	switch s := resp["vnp_TransactionStatus"]; s {
	case "00", "05", "06":
		return app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: r.ProviderRefundRef}, nil
	default:
		return app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_status_" + s}, nil
	}
}

// call signs req with the request field order, POSTs it as JSON to APIURL and returns the response
// fields once their checksum is verified. Transport errors, 5xx, 429 and 409 are ErrProviderUnavailable:
// the first two are outages, the last two mean VNPay is throttling or already processing the request,
// and a retry is the right answer to all of them. Any other non-200 is a plain error.
func (p *Provider) call(ctx context.Context, req map[string]string, reqFields, respFields []string) (map[string]string, error) {
	cmd := req["vnp_Command"]
	req[fieldSecureHash] = PipeHash(p.cfg.HashSecret, pick(req, reqFields)...)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("vnpay %s: encode: %w", cmd, err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.APIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vnpay %s: %w", cmd, err)
	}
	hr.Header.Set("Content-Type", "application/json")
	res, err := p.cfg.HTTPClient.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("%w: vnpay %s: %w", app.ErrProviderUnavailable, cmd, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: vnpay %s: read: %w", app.ErrProviderUnavailable, cmd, err)
	case res.StatusCode >= 500, res.StatusCode == http.StatusTooManyRequests, res.StatusCode == http.StatusConflict:
		return nil, fmt.Errorf("%w: vnpay %s: status %d", app.ErrProviderUnavailable, cmd, res.StatusCode)
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("vnpay %s: status %d", cmd, res.StatusCode)
	}
	resp, err := decodeFields(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: vnpay %s: decode: %w", app.ErrProviderUnavailable, cmd, err)
	}
	want := mac(p.cfg.HashSecret, strings.Join(pick(resp, respFields), "|"))
	if !verifyHex(resp[fieldSecureHash], want) {
		return nil, fmt.Errorf("%w: vnpay %s: response checksum mismatch", app.ErrProviderUnavailable, cmd)
	}
	return resp, nil
}

// decodeFields reads a flat JSON object whose values may be strings, numbers or null.
func decodeFields(raw []byte) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case nil:
		case string:
			out[k] = x
		case json.Number:
			out[k] = x.String()
		default:
			return nil, fmt.Errorf("field %s is not a scalar", k)
		}
	}
	return out, nil
}

func pick(m map[string]string, fields []string) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = m[f]
	}
	return out
}
