package app

import (
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

func paymentSucceeded(p domain.Payment, allocs []Allocation) event.PaymentSucceeded {
	ref := p.ProviderTxnID
	if ref == "" {
		ref = p.ProviderRef
	}
	out := make([]event.Allocation, len(allocs))
	for i, a := range allocs {
		out[i] = event.Allocation{OrderID: a.OrderID, SellerUserID: a.SellerUserID, Amount: a.Amount, CommissionAmount: a.CommissionAmount}
	}
	return event.PaymentSucceeded{
		PaymentID: p.ID, CheckoutID: p.CheckoutID, BuyerID: p.BuyerID, Provider: p.Provider,
		ProviderRef: ref, Amount: p.Amount, Allocations: out,
	}
}

func paymentFailed(p domain.Payment) event.PaymentFailed {
	return event.PaymentFailed{PaymentID: p.ID, CheckoutID: p.CheckoutID, Provider: p.Provider, Reason: p.FailureReason}
}

func paymentRefunded(r Refund) event.PaymentRefunded {
	return event.PaymentRefunded{
		RefundID: r.ID, PaymentID: r.PaymentID, CheckoutID: r.CheckoutID, OrderID: r.OrderID,
		Amount: r.Amount, Provider: r.Provider,
	}
}

func refundFailed(r Refund, reason string) event.RefundFailed {
	return event.RefundFailed{
		RefundID: r.ID, PaymentID: r.PaymentID, CheckoutID: r.CheckoutID, OrderID: r.OrderID,
		Amount: r.Amount, Provider: r.Provider, Reason: reason,
	}
}
