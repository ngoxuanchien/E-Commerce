package vnpay_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/vnpay"
)

// Known vector. The parameters are the sample pay URL of the VNPay 2.1.0 docs
// (https://sandbox.vnpayment.vn/apis/docs/thanh-toan-pay/pay.html, read 2026-09-29). The docs'
// own hash was made with DEMOV210's unpublished secret, so the expected value was derived
// independently with the test secret below:
//
//	printf '%s' 'vnp_Amount=1806000&vnp_Command=pay&vnp_CreateDate=20210801153333&vnp_CurrCode=VND&vnp_IpAddr=127.0.0.1&vnp_Locale=vn&vnp_OrderInfo=Thanh+toan+don+hang+%3A5&vnp_OrderType=other&vnp_ReturnUrl=https%3A%2F%2Fdomainmerchant.vn%2FReturnUrl&vnp_TmnCode=DEMOV210&vnp_TxnRef=5&vnp_Version=2.1.0' \
//	  | openssl dgst -sha512 -hmac 'TESTSECRETVNPAY2024'
const (
	vectorSecret = "TESTSECRETVNPAY2024"
	vectorHash   = "24ea8300ee579b2278b0b8865d11d9ed41c1859cf942ec77c2fc3488ee09a630c88b9624adf1b95a7e77e6cc2518e24fb88912965b0e91c305197d8e1daca9f3"
	// printf '%s' 'a|b|c' | openssl dgst -sha512 -hmac 'TESTSECRETVNPAY2024'
	pipeVector = "5aabf33d02d3edc53d39590e1ef8a9678145edcc8b687246b228e64bfe193fb656fa31127292bf5edb1fda96c34bc68c8db79820493930c80fc30285a0d475ef"
)

func docSample() url.Values {
	return url.Values{
		"vnp_Amount":     {"1806000"},
		"vnp_Command":    {"pay"},
		"vnp_CreateDate": {"20210801153333"},
		"vnp_CurrCode":   {"VND"},
		"vnp_IpAddr":     {"127.0.0.1"},
		"vnp_Locale":     {"vn"},
		"vnp_OrderInfo":  {"Thanh toan don hang :5"},
		"vnp_OrderType":  {"other"},
		"vnp_ReturnUrl":  {"https://domainmerchant.vn/ReturnUrl"},
		"vnp_TmnCode":    {"DEMOV210"},
		"vnp_TxnRef":     {"5"},
		"vnp_Version":    {"2.1.0"},
	}
}

func TestSecureHashKnownVector(t *testing.T) {
	assert.Equal(t, vectorHash, vnpay.SecureHash(vectorSecret, docSample()))
}

func TestSecureHashIgnoresHashFieldsForeignKeysAndEmptyValues(t *testing.T) {
	q := docSample()
	q.Set("vnp_SecureHash", "deadbeef")
	q.Set("vnp_SecureHashType", "HmacSHA512")
	q.Set("utm_source", "mail")
	q.Set("vnp_BankCode", "")
	assert.Equal(t, vectorHash, vnpay.SecureHash(vectorSecret, q))
}

func TestVerifyQuery(t *testing.T) {
	q := docSample()
	q.Set("vnp_SecureHash", vectorHash)
	assert.True(t, vnpay.VerifyQuery(vectorSecret, q))

	q.Set("vnp_SecureHash", strings.ToUpper(vectorHash))
	assert.True(t, vnpay.VerifyQuery(vectorSecret, q), "hex is compared case-insensitively")

	assert.False(t, vnpay.VerifyQuery("other-secret", q))

	tampered := docSample()
	tampered.Set("vnp_Amount", "1806100")
	tampered.Set("vnp_SecureHash", vectorHash)
	assert.False(t, vnpay.VerifyQuery(vectorSecret, tampered))

	missing := docSample()
	assert.False(t, vnpay.VerifyQuery(vectorSecret, missing))

	garbage := docSample()
	garbage.Set("vnp_SecureHash", "zz"+vectorHash[2:])
	assert.False(t, vnpay.VerifyQuery(vectorSecret, garbage))
}

func TestPipeHashKnownVector(t *testing.T) {
	assert.Equal(t, pipeVector, vnpay.PipeHash(vectorSecret, "a", "b", "c"))
}

func TestTxnRef(t *testing.T) {
	assert.Equal(t, "0192f0c1a2b37c4d8e9fa0b1c2d3e4f5", vnpay.TxnRef("0192f0c1-a2b3-7c4d-8e9f-a0b1c2d3e4f5"))
}
