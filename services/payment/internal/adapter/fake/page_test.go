package fake_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
)

func TestRenderPage(t *testing.T) {
	var sb strings.Builder
	require.NoError(t, fake.RenderPage(&sb, fake.PageData{PaymentID: "p<1>", Amount: 150000, ExpiresAt: time.Unix(0, 0).UTC()}))
	out := sb.String()
	for _, want := range []string{
		`data-testid="fake-pay-succeed"`, `data-testid="fake-pay-fail"`,
		"Thanh toán", "Thất bại", "150.000 ₫",
		`name="hook_webhook"`, `name="hook_refund"`, `name="hook_cancel"`,
		`value="succeed"`, `value="fail"`,
	} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "p<1>", "payment id must be escaped")
}

func TestFormatVND(t *testing.T) {
	for in, want := range map[int64]string{0: "0 ₫", 999: "999 ₫", 1000: "1.000 ₫", 1234567: "1.234.567 ₫"} {
		assert.Equal(t, want, fake.FormatVND(in))
	}
}
