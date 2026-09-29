package vnpay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string { return providerName }

func vnDate(t time.Time) string { return t.In(gmt7).Format(dateLayout) }

// ascii keeps printable ASCII only (vnp_OrderInfo must not carry diacritics).
func ascii(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return -1
		}
		return r
	}, s)
}

// CreateSession signs the pay URL (spec §5.1). ProviderRef is vnp_TxnRef.
func (p *Provider) CreateSession(_ context.Context, in app.SessionInput) (app.Session, error) {
	ref := TxnRef(in.PaymentID)
	info := strings.TrimSpace(ascii(in.Description))
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
	v.Set("vnp_CreateDate", vnDate(p.cfg.Now()))
	v.Set("vnp_ExpireDate", vnDate(in.ExpiresAt))
	// Every value is non-empty, so v.Encode() (sorted, QueryEscape'd) is exactly the hash data.
	return app.Session{
		RedirectURL: p.cfg.PayURL + "?" + v.Encode() + "&" + fieldSecureHash + "=" + SecureHash(p.cfg.HashSecret, v),
		ProviderRef: ref,
	}, nil
}

// Query calls querydr and maps vnp_TransactionStatus. vnp_TransactionDate is the payment's creation
// time, the merchant-side date of the transaction.
func (p *Provider) Query(ctx context.Context, pay domain.Payment) (domain.Observed, error) {
	now := p.cfg.Now()
	ref := TxnRef(pay.ID)
	req := map[string]string{
		"vnp_RequestId": TxnRef(uuid.NewString()), "vnp_Version": apiVersion, "vnp_Command": "querydr",
		"vnp_TmnCode": p.cfg.TmnCode, "vnp_TxnRef": ref, "vnp_OrderInfo": "Truy van giao dich " + ref,
		"vnp_TransactionDate": vnDate(pay.CreatedAt), "vnp_CreateDate": vnDate(now), "vnp_IpAddr": serverIP,
	}
	if pay.ProviderTxnID != "" {
		req["vnp_TransactionNo"] = pay.ProviderTxnID // optional, not part of the checksum
	}
	resp, err := p.call(ctx, req, queryRequestFields, queryResponseFields)
	if err != nil {
		return domain.Observed{}, err
	}
	switch code := resp["vnp_ResponseCode"]; code {
	case "00":
	case "91": // transaction not found: the buyer has not paid (yet)
		if now.Before(pay.ExpiresAt) {
			return domain.Observed{State: domain.ObservedPending}, nil
		}
		return domain.Observed{State: domain.ObservedExpired}, nil
	default:
		return domain.Observed{}, fmt.Errorf("%w: vnpay querydr response code %s", app.ErrProviderUnavailable, code)
	}
	if resp["vnp_TxnRef"] != ref {
		return domain.Observed{}, fmt.Errorf("%w: vnpay querydr answered for another transaction", app.ErrProviderUnavailable)
	}
	switch status := resp["vnp_TransactionStatus"]; status {
	case "00":
		amount, err := strconv.ParseInt(resp["vnp_Amount"], 10, 64)
		if err != nil {
			return domain.Observed{}, fmt.Errorf("%w: vnpay querydr amount %q", app.ErrProviderUnavailable, resp["vnp_Amount"])
		}
		return domain.Observed{State: domain.ObservedSucceeded, Amount: amount / 100, ProviderTxnID: resp["vnp_TransactionNo"]}, nil
	case "01", "05": // not completed yet; VNPay still processing
		return domain.Observed{State: domain.ObservedPending}, nil
	default:
		return domain.Observed{State: domain.ObservedFailed, Reason: status}, nil
	}
}

// Cancel is not supported by VNPay; the session becomes unpayable at vnp_ExpireDate.
func (p *Provider) Cancel(context.Context, domain.Payment) (bool, error) { return false, nil }

// Refund calls the refund API: type 02 (full) when the amount is the whole payment, else 03 (partial).
// vnp_RequestId is derived from the refund id, so a retry is recognisable to VNPay; the same value is
// the ProviderRefundRef. VNPay has no refund-status call keyed by request, so an accepted request
// (00) is final unless it says the refund was declined (status 09).
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
	case "02", "03", "91", "94", "95":
		// Rejected by VNPay (bad merchant, bad data, unknown transaction, already refunded, declined).
		return app.RefundObserved{State: app.RefundFailed, Reason: "vnpay_" + code}, nil
	default: // 97 checksum, 99 other: retry later
		return app.RefundObserved{}, fmt.Errorf("%w: vnpay refund response code %s", app.ErrProviderUnavailable, code)
	}
}

// QueryRefund reports what Refund recorded: Refund only returns a ProviderRefundRef for an accepted
// refund, and VNPay offers no per-request refund status to poll.
func (p *Provider) QueryRefund(_ context.Context, r app.Refund) (app.RefundObserved, error) {
	if r.ProviderRefundRef == "" {
		return app.RefundObserved{State: app.RefundPending}, nil
	}
	return app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: r.ProviderRefundRef}, nil
}

// call signs req with the request field order, POSTs it as JSON to APIURL and returns the response
// fields once their checksum is verified. Transport errors, 5xx and bad checksums are ErrProviderUnavailable.
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
	case res.StatusCode >= 500:
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
