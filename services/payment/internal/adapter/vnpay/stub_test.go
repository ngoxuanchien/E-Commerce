package vnpay_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/vnpay"
)

const (
	testSecret = "TESTSECRETVNPAY2024"
	testTmn    = "TESTTMN1"
)

// Field orders pinned from https://sandbox.vnpayment.vn/apis/docs/truy-van-hoan-tien/querydr&refund.html
// (read 2026-09-29). They are written out here independently of the adapter on purpose.
var (
	queryReqOrder = []string{"vnp_RequestId", "vnp_Version", "vnp_Command", "vnp_TmnCode", "vnp_TxnRef",
		"vnp_TransactionDate", "vnp_CreateDate", "vnp_IpAddr", "vnp_OrderInfo"}
	queryRespOrder = []string{"vnp_ResponseId", "vnp_Command", "vnp_ResponseCode", "vnp_Message", "vnp_TmnCode",
		"vnp_TxnRef", "vnp_Amount", "vnp_BankCode", "vnp_PayDate", "vnp_TransactionNo", "vnp_TransactionType",
		"vnp_TransactionStatus", "vnp_OrderInfo", "vnp_PromotionCode", "vnp_PromotionAmount"}
	refundReqOrder = []string{"vnp_RequestId", "vnp_Version", "vnp_Command", "vnp_TmnCode", "vnp_TransactionType",
		"vnp_TxnRef", "vnp_Amount", "vnp_TransactionNo", "vnp_TransactionDate", "vnp_CreateBy", "vnp_CreateDate",
		"vnp_IpAddr", "vnp_OrderInfo"}
	refundRespOrder = []string{"vnp_ResponseId", "vnp_Command", "vnp_ResponseCode", "vnp_Message", "vnp_TmnCode",
		"vnp_TxnRef", "vnp_Amount", "vnp_BankCode", "vnp_PayDate", "vnp_TransactionNo", "vnp_TransactionType",
		"vnp_TransactionStatus", "vnp_OrderInfo"}
)

func pick(m map[string]string, order []string) []string {
	out := make([]string, len(order))
	for i, k := range order {
		out[i] = m[k]
	}
	return out
}

// apiStub is VNPay's merchant_webapi: it checks the request pipe hash, serves a recorded fixture
// with vnp_TxnRef (and, for refunds, vnp_Amount) echoed from the request, and signs it for real.
type apiStub struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	fixture   string // testdata/<fixture>.json
	status    int    // 0 → 200
	badHash   bool
	requests  []map[string]string
	hashValid []bool
}

func newAPIStub(t *testing.T, fixture string) *apiStub {
	t.Helper()
	s := &apiStub{t: t, fixture: fixture}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *apiStub) set(fixture string, status int, badHash bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fixture, s.status, s.badHash = fixture, status, badHash
}

func (s *apiStub) calls() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), s.requests...)
}

func (s *apiStub) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var req map[string]string
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" ||
		json.NewDecoder(r.Body).Decode(&req) != nil {
		s.t.Errorf("stub: want a JSON POST of string fields, got %s %s", r.Method, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	reqOrder, respOrder := queryReqOrder, queryRespOrder
	if req["vnp_Command"] == "refund" {
		reqOrder, respOrder = refundReqOrder, refundRespOrder
	}
	s.requests = append(s.requests, req)
	valid := req["vnp_SecureHash"] == vnpay.PipeHash(testSecret, pick(req, reqOrder)...)
	s.hashValid = append(s.hashValid, valid)
	if !valid {
		s.t.Errorf("stub: request pipe hash mismatch for %v", req)
	}
	if s.status >= 400 {
		w.WriteHeader(s.status)
		return
	}
	raw, err := os.ReadFile(filepath.Join("testdata", s.fixture+".json"))
	if err != nil {
		s.t.Errorf("stub: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var resp map[string]string
	if err := json.Unmarshal(raw, &resp); err != nil {
		s.t.Errorf("stub: %v", err)
		return
	}
	resp["vnp_TxnRef"] = req["vnp_TxnRef"]
	if req["vnp_Command"] == "refund" {
		resp["vnp_Amount"] = req["vnp_Amount"]
	}
	resp["vnp_SecureHash"] = vnpay.PipeHash(testSecret, pick(resp, respOrder)...)
	if s.badHash {
		resp["vnp_SecureHash"] = vnpay.PipeHash("wrong-secret", pick(resp, respOrder)...)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func newProvider(api string, now time.Time) *vnpay.Provider {
	return vnpay.New(vnpay.Config{
		TmnCode: testTmn, HashSecret: testSecret,
		PayURL: "https://sandbox.vnpayment.vn/paymentv2/vpcpay.html", APIURL: api,
		PublicBaseURL: "https://gw.test/", Now: func() time.Time { return now },
	})
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
