package postgres

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

func parseUUID(s string) (uuid.UUID, bool) {
	u, err := uuid.Parse(s)
	return u, err == nil
}

func mustParseUUID(s string) (uuid.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid uuid %q: %w", s, err)
	}
	return u, nil
}

// nullable maps "" to NULL: the domain uses empty strings for absent optional text.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toDomainPayment(r sqlcdb.Payment) domain.Payment {
	return domain.Payment{
		ID: r.ID.String(), CheckoutID: r.CheckoutID.String(), BuyerID: r.BuyerID.String(),
		Method: r.Method, Provider: r.Provider, Status: domain.Status(r.Status), Amount: r.Amount,
		Captured: r.Captured, ProviderRef: deref(r.ProviderRef), ProviderTxnID: deref(r.ProviderTxnID),
		RedirectURL: deref(r.RedirectUrl), FailureReason: deref(r.FailureReason),
		ExpiresAt: r.ExpiresAt.UTC(), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
		Version: int(r.Version),
	}
}

func toDomainPayments(rows []sqlcdb.Payment) []domain.Payment {
	out := make([]domain.Payment, len(rows))
	for i, r := range rows {
		out[i] = toDomainPayment(r)
	}
	return out
}

// toRefund is shared by the row types of the refund queries, which carry the
// same columns plus the payment's checkout and provider.
func toRefund(id, paymentID uuid.UUID, orderID *uuid.UUID, amount int64, status string, ref *string,
	attempts int32, next time.Time, lastErr *string, createdAt time.Time, checkoutID uuid.UUID, provider string,
) app.Refund {
	r := app.Refund{
		ID: id.String(), PaymentID: paymentID.String(), CheckoutID: checkoutID.String(), Provider: provider,
		Amount: amount, Status: status, ProviderRefundRef: deref(ref), Attempts: int(attempts), LastError: deref(lastErr),
		NextAttemptAt: next.UTC(), CreatedAt: createdAt.UTC(),
	}
	if orderID != nil {
		s := orderID.String()
		r.OrderID = &s
	}
	return r
}
