package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// deliver schedules the webhook deliveries for a recorded outcome, honouring hooks.
// It returns immediately; the work runs on a detached context and is tracked by Wait.
func (p *Provider) deliver(paymentID, outcome string, h Hooks) {
	if h.Webhook == "drop" {
		return
	}
	events := []Event{{EventID: newID(), PaymentID: paymentID, Outcome: outcome}}
	switch h.Webhook {
	case "duplicate":
		events = append(events, events[0])
	case "stale_after":
		events = append(events, Event{EventID: newID(), PaymentID: paymentID, Outcome: "pending"})
	}
	ctx := context.WithoutCancel(context.Background())
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		timer := time.NewTimer(p.cfg.WebhookDelay)
		defer timer.Stop()
		<-timer.C
		for _, ev := range events {
			if err := p.post(ctx, ev); err != nil {
				p.cfg.Logger.Error("fake webhook delivery failed", "payment_id", ev.PaymentID, "event_id", ev.EventID, "err", err)
			}
		}
	}()
}

func (p *Provider) post(ctx context.Context, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fake-Signature", Sign(p.cfg.WebhookSecret, body, p.cfg.Now()))
	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook answered %d", resp.StatusCode)
	}
	return nil
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }
