package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// ErrAllocationsExceedPayment: a capture's allocations sum to more than the payment.
var ErrAllocationsExceedPayment = fmt.Errorf("%w: allocations exceed the payment amount", ErrInvalidPosting)

// Allocation is one order's share of a captured payment. It is written once
// with CAPTURE (escrow_allocations) and is what RELEASE and REFUND move.
type Allocation struct {
	OrderID      uuid.UUID
	PaymentID    uuid.UUID
	CheckoutID   uuid.UUID
	SellerUserID uuid.UUID
	Amount       int64 // the order total held in escrow
	Commission   int64 // the platform's cut, 0 ≤ Commission < Amount
	Provider     string
}

func (a Allocation) validate() error {
	switch {
	case a.Amount <= 0:
		return fmt.Errorf("%w: allocation amount %d must be positive", ErrInvalidPosting, a.Amount)
	case a.Commission < 0 || a.Commission >= a.Amount:
		return fmt.Errorf("%w: commission %d must be in [0, %d)", ErrInvalidPosting, a.Commission, a.Amount)
	case !ValidProvider(a.Provider):
		return fmt.Errorf("%w: unknown provider %q", ErrInvalidPosting, a.Provider)
	}
	return nil
}

func positive(amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("%w: amount %d must be positive", ErrInvalidPosting, amount)
	}
	return nil
}

func knownProvider(p string) error {
	if !ValidProvider(p) {
		return fmt.Errorf("%w: unknown provider %q", ErrInvalidPosting, p)
	}
	return nil
}

// Pay records a provider charge into the checkout's suspense account.
func Pay(paymentID, checkoutID uuid.UUID, provider string, amount int64) (Journal, error) {
	if err := positive(amount); err != nil {
		return Journal{}, err
	}
	if err := knownProvider(provider); err != nil {
		return Journal{}, err
	}
	return Journal{
		Key: "pay:" + paymentID.String(), Kind: KindPay,
		Refs: Refs{CheckoutID: &checkoutID, PaymentID: &paymentID},
		Lines: []Line{
			{Account: PSPClearing(provider), Amount: amount},
			{Account: CheckoutSuspense(checkoutID), Amount: -amount},
		},
	}, nil
}

// Capture moves the allocated part of a payment from suspense into each
// order's escrow. Allocations must belong to this payment, name distinct
// orders and not exceed paymentAmount.
func Capture(paymentID, checkoutID uuid.UUID, paymentAmount int64, allocs []Allocation) (Journal, error) {
	if len(allocs) == 0 {
		return Journal{}, fmt.Errorf("%w: capture without allocations", ErrInvalidPosting)
	}
	seen := map[uuid.UUID]bool{}
	var total int64
	lines := make([]Line, 1, len(allocs)+1) // lines[0] is the suspense debit, set below
	for _, a := range allocs {
		if err := a.validate(); err != nil {
			return Journal{}, err
		}
		if a.PaymentID != paymentID || a.CheckoutID != checkoutID {
			return Journal{}, fmt.Errorf("%w: allocation for order %s belongs to another payment", ErrInvalidPosting, a.OrderID)
		}
		if seen[a.OrderID] {
			return Journal{}, fmt.Errorf("%w: order %s allocated twice", ErrInvalidPosting, a.OrderID)
		}
		seen[a.OrderID] = true
		total += a.Amount
		lines = append(lines, Line{Account: Escrow(a.OrderID, a.SellerUserID), Amount: -a.Amount})
	}
	if total > paymentAmount {
		return Journal{}, fmt.Errorf("%w: %d > %d", ErrAllocationsExceedPayment, total, paymentAmount)
	}
	lines[0] = Line{Account: CheckoutSuspense(checkoutID), Amount: total}
	return Journal{
		Key: "capture:" + paymentID.String(), Kind: KindCapture,
		Refs: Refs{CheckoutID: &checkoutID, PaymentID: &paymentID}, Lines: lines,
	}, nil
}

// Release pays a completed order's escrow to the seller wallet, less the
// commission. The commission line is omitted when the commission is 0.
func Release(a Allocation) (Journal, error) {
	if err := a.validate(); err != nil {
		return Journal{}, err
	}
	lines := []Line{
		{Account: Escrow(a.OrderID, a.SellerUserID), Amount: a.Amount},
		{Account: SellerWallet(a.SellerUserID), Amount: -(a.Amount - a.Commission)},
	}
	if a.Commission > 0 {
		lines = append(lines, Line{Account: CommissionRevenue(), Amount: -a.Commission})
	}
	return Journal{
		Key: "release:" + a.OrderID.String(), Kind: KindRelease,
		Refs:  Refs{CheckoutID: &a.CheckoutID, PaymentID: &a.PaymentID, OrderID: &a.OrderID},
		Lines: lines,
	}, nil
}

// Refund returns a cancelled order's escrow to the provider.
func Refund(refundID uuid.UUID, a Allocation) (Journal, error) {
	if err := a.validate(); err != nil {
		return Journal{}, err
	}
	return Journal{
		Key: "refund:" + refundID.String(), Kind: KindRefund,
		Refs: Refs{CheckoutID: &a.CheckoutID, PaymentID: &a.PaymentID, OrderID: &a.OrderID, RefundID: &refundID},
		Lines: []Line{
			{Account: Escrow(a.OrderID, a.SellerUserID), Amount: a.Amount},
			{Account: PSPClearing(a.Provider), Amount: -a.Amount},
		},
	}, nil
}

// DuplicateRefund returns a duplicate charge from the checkout's suspense.
func DuplicateRefund(refundID, paymentID, checkoutID uuid.UUID, provider string, amount int64) (Journal, error) {
	if err := positive(amount); err != nil {
		return Journal{}, err
	}
	if err := knownProvider(provider); err != nil {
		return Journal{}, err
	}
	return Journal{
		Key: "refund:" + refundID.String(), Kind: KindDuplicateRefund,
		Refs: Refs{CheckoutID: &checkoutID, PaymentID: &paymentID, RefundID: &refundID},
		Lines: []Line{
			{Account: CheckoutSuspense(checkoutID), Amount: amount},
			{Account: PSPClearing(provider), Amount: -amount},
		},
	}, nil
}
