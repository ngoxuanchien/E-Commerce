package fake

import (
	"html/template"
	"io"
	"strconv"
	"strings"
	"time"
)

// PageData is what the hosted page shows.
type PageData struct {
	PaymentID string
	Amount    int64
	ExpiresAt time.Time
	Hooks     Hooks // pre-filled into the hidden hook inputs
}

// FormatVND renders 150000 as "150.000 ₫".
func FormatVND(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := b.String() + " ₫"
	if neg {
		out = "-" + out
	}
	return out
}

var pageTmpl = template.Must(template.New("page").Funcs(template.FuncMap{"vnd": FormatVND}).Parse(`<!doctype html>
<html lang="vi"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>FAKE payment</title></head>
<body>
<main>
<h1>Thanh toán thử nghiệm</h1>
<p>Mã thanh toán: <code>{{.PaymentID}}</code></p>
<p>Số tiền: <strong data-testid="fake-amount">{{vnd .Amount}}</strong></p>
<p>Hết hạn: {{.ExpiresAt.Format "2006-01-02 15:04:05 MST"}}</p>
<form method="post" action="/api/payments/fake/checkout/{{.PaymentID}}">
<input type="hidden" name="hook_webhook" value="{{.Hooks.Webhook}}">
<input type="hidden" name="hook_refund" value="{{.Hooks.Refund}}">
<input type="hidden" name="hook_cancel" value="{{.Hooks.Cancel}}">
<button type="submit" name="outcome" value="succeed" data-testid="fake-pay-succeed">Thanh toán</button>
<button type="submit" name="outcome" value="fail" data-testid="fake-pay-fail">Thất bại</button>
</form>
</main>
</body></html>
`))

// RenderPage writes the hosted checkout page. Test hooks are hidden inputs that e2e fills in.
func RenderPage(w io.Writer, d PageData) error { return pageTmpl.Execute(w, d) }
