package httpadapter

import (
	"time"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// paymentDTO is api/openapi/payment.yaml's Payment.
type paymentDTO struct {
	ID            string    `json:"id"`
	CheckoutID    string    `json:"checkout_id"`
	Method        string    `json:"method"`
	Provider      string    `json:"provider"`
	Status        string    `json:"status"`
	Amount        int64     `json:"amount"`
	RedirectURL   *string   `json:"redirect_url"`
	ExpiresAt     time.Time `json:"expires_at"`
	FailureReason *string   `json:"failure_reason"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// toPaymentDTO hides the redirect once the payment is no longer PENDING and renders unset text as null.
func toPaymentDTO(p domain.Payment) paymentDTO {
	d := paymentDTO{
		ID: p.ID, CheckoutID: p.CheckoutID, Method: p.Method, Provider: p.Provider, Status: string(p.Status),
		Amount: p.Amount, ExpiresAt: p.ExpiresAt.UTC(), CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
	}
	if p.Status == domain.StatusPending && p.RedirectURL != "" {
		d.RedirectURL = &p.RedirectURL
	}
	if p.FailureReason != "" {
		d.FailureReason = &p.FailureReason
	}
	return d
}

type createPaymentRequest struct {
	CheckoutID string `json:"checkout_id" validate:"required,uuid"`
}
