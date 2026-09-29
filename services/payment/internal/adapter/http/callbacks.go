package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

const (
	maxWebhookBody   = 64 << 10
	fakeProviderName = "fake"
)

// resultURL is the web result page the buyer's browser ends up on.
func resultURL(paymentID string) string {
	return "/checkout/result?payment_id=" + url.QueryEscape(paymentID)
}

// FakeProvider is what the FAKE callback routes need from the FAKE adapter.
type FakeProvider interface {
	Page(ctx context.Context, paymentID string) (fake.PageData, error)
	ArmHooks(ctx context.Context, paymentID string, h fake.Hooks) error
	Submit(ctx context.Context, paymentID, outcome string, form fake.Hooks) error
	Query(ctx context.Context, p domain.Payment) (domain.Observed, error)
}

var _ FakeProvider = (*fake.Provider)(nil)

// FakeDeps wires FakeCallbacks.
type FakeDeps struct {
	Provider      FakeProvider
	Store         app.Store
	Observer      *app.Observer
	WebhookSecret string
	Now           func() time.Time // defaults to time.Now
	Logger        *slog.Logger     // defaults to slog.Default()
}

// FakeCallbacks serves the FAKE provider's hosted page, browser return and signed webhook.
type FakeCallbacks struct{ FakeDeps }

var _ Routes = (*FakeCallbacks)(nil)

func NewFakeCallbacks(d FakeDeps) *FakeCallbacks {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &FakeCallbacks{d}
}

func (c *FakeCallbacks) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/payments/fake/checkout/{id}", c.page)
	mux.HandleFunc("POST /api/payments/fake/checkout/{id}", c.submit)
	mux.HandleFunc("GET /api/payments/fake/return", c.returnURL)
	mux.HandleFunc("POST /api/payments/fake/webhook", c.webhook)
}

// hooksFrom reads the optional hook_* values; invalid values are a validation error.
func hooksFrom(get func(string) string) (fake.Hooks, error) {
	h := fake.Hooks{Webhook: get("hook_webhook"), Refund: get("hook_refund"), Cancel: get("hook_cancel")}
	fields := map[string]string{}
	if !oneOf(h.Webhook, "duplicate", "drop", "stale_after") {
		fields["hook_webhook"] = "must be one of duplicate, drop, stale_after"
	}
	if !oneOf(h.Refund, "fail") {
		fields["hook_refund"] = "must be fail"
	}
	if !oneOf(h.Cancel, "unsupported") {
		fields["hook_cancel"] = "must be unsupported"
	}
	if len(fields) > 0 {
		return fake.Hooks{}, httpx.ValidationError(fields)
	}
	return h, nil
}

// oneOf reports whether v is empty or one of allowed.
func oneOf(v string, allowed ...string) bool {
	if v == "" {
		return true
	}
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// page renders the hosted page. When any hook_* query parameter is present the hooks are armed first
// (e2e arms hook_cancel=unsupported before paying); a session that already has an outcome is still
// rendered, with nothing armed.
func (c *FakeCallbacks) page(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		writeError(w, r, app.ErrNotFound)
		return
	}
	q := r.URL.Query()
	var armed fake.Hooks
	if q.Has("hook_webhook") || q.Has("hook_refund") || q.Has("hook_cancel") {
		h, err := hooksFrom(q.Get)
		if err != nil {
			writeError(w, r, err)
			return
		}
		switch err := c.Provider.ArmHooks(r.Context(), id, h); {
		case err == nil:
			armed = h
		case errors.Is(err, fake.ErrSessionClosed):
		default:
			writeError(w, r, err)
			return
		}
	}
	data, err := c.Provider.Page(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	data.Hooks = armed
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := fake.RenderPage(w, data); err != nil {
		c.Logger.ErrorContext(r.Context(), "render fake page", "payment_id", id, "error", err)
	}
}

// submit records the buyer's choice and sends the browser to the return route. A session that was
// already answered (a double click or a stale tab) sends the buyer straight to the result page,
// which shows the real status.
func (c *FakeCallbacks) submit(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		writeError(w, r, app.ErrNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBody)
	if err := r.ParseForm(); err != nil {
		writeError(w, r, httpx.ValidationError(map[string]string{"body": "must be a form of at most 64 KiB"}))
		return
	}
	outcome := r.PostForm.Get("outcome")
	if outcome != "succeed" && outcome != "fail" {
		writeError(w, r, httpx.ValidationError(map[string]string{"outcome": "must be succeed or fail"}))
		return
	}
	hooks, err := hooksFrom(r.PostForm.Get)
	if err != nil {
		writeError(w, r, err)
		return
	}
	switch err := c.Provider.Submit(r.Context(), id, outcome, hooks); {
	case err == nil:
		http.Redirect(w, r, "/api/payments/fake/return?payment_id="+url.QueryEscape(id), http.StatusFound)
	case errors.Is(err, fake.ErrSessionClosed):
		http.Redirect(w, r, resultURL(id), http.StatusFound)
	default:
		writeError(w, r, err)
	}
}

// returnURL is the browser return: it syncs the payment with the provider's current state, then
// always redirects to the result page (payment.yaml documents only 302; an unknown or unparseable id
// lands on the page without an id). A failed sync is logged; the result page and the reconciler catch up.
func (c *FakeCallbacks) returnURL(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r.URL.Query().Get("payment_id"))
	if !ok {
		http.Redirect(w, r, "/checkout/result", http.StatusFound)
		return
	}
	defer http.Redirect(w, r, resultURL(id), http.StatusFound)
	p, err := c.Store.GetPayment(r.Context(), id)
	if err != nil {
		if !errors.Is(err, app.ErrNotFound) {
			c.Logger.ErrorContext(r.Context(), "fake return: load payment", "payment_id", id, "error", err)
		}
		return
	}
	if p.Provider != fakeProviderName {
		return
	}
	if obs, err := c.Provider.Query(r.Context(), p); err != nil {
		c.Logger.ErrorContext(r.Context(), "fake return: query", "payment_id", id, "error", err)
	} else if _, err := c.Observer.ApplyObserved(r.Context(), fakeProviderName, "", id, obs); err != nil {
		c.Logger.ErrorContext(r.Context(), "fake return: apply", "payment_id", id, "error", err)
	}
}

// webhook applies a signed FAKE event. The signature is checked over the raw bytes before they are parsed.
func (c *FakeCallbacks) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		writeError(w, r, httpx.ValidationError(map[string]string{"body": "could not be read (at most 64 KiB)"}))
		return
	}
	if err := fake.Verify(c.WebhookSecret, body, r.Header.Get("X-Fake-Signature"), c.Now()); err != nil {
		writeError(w, r, httpx.NewError(httpx.CodeValidation, "invalid signature"))
		return
	}
	var ev fake.Event
	if err := json.Unmarshal(body, &ev); err != nil {
		writeError(w, r, httpx.ValidationError(map[string]string{"body": "must be a JSON event"}))
		return
	}
	paymentID, ok := parseID(ev.PaymentID)
	if !ok || ev.EventID == "" || (ev.Outcome != "succeed" && ev.Outcome != "fail" && ev.Outcome != "pending") {
		writeError(w, r, httpx.ValidationError(map[string]string{"body": "needs event_id, a payment_id and outcome succeed, fail or pending"}))
		return
	}
	p, err := c.Store.GetPayment(r.Context(), paymentID)
	switch {
	case errors.Is(err, app.ErrNotFound):
		w.WriteHeader(http.StatusOK) // nothing to apply; do not make the sender retry
		return
	case err != nil:
		writeError(w, r, err)
		return
	}
	if _, err := c.Observer.ApplyObserved(r.Context(), fakeProviderName, ev.EventID, paymentID, ev.Observed(p.Amount)); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
