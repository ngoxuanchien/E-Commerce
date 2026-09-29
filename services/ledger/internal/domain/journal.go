package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Kind is a journal transaction's posting kind (WS2' §3).
type Kind string

const (
	KindPay             Kind = "PAY"
	KindCapture         Kind = "CAPTURE"
	KindRelease         Kind = "RELEASE"
	KindRefund          Kind = "REFUND"
	KindDuplicateRefund Kind = "DUPLICATE_REFUND"
	KindPayoutRequest   Kind = "PAYOUT_REQUEST"
	KindPayoutSettle    Kind = "PAYOUT_SETTLE"
	KindPayoutFail      Kind = "PAYOUT_FAIL"
)

var (
	ErrInvalidPosting = errors.New("posting: invalid input")
	ErrTooFewLines    = errors.New("journal: a transaction needs at least two lines")
	ErrZeroLine       = errors.New("journal: a line amount must not be zero")
	ErrUnbalanced     = errors.New("journal: lines do not sum to zero")
)

// Line is one entry: debit > 0, credit < 0.
type Line struct {
	Account Account
	Amount  int64
}

// Refs are the business ids a transaction relates to (journal_transactions.refs).
type Refs struct {
	CheckoutID *uuid.UUID `json:"checkout_id,omitempty"`
	PaymentID  *uuid.UUID `json:"payment_id,omitempty"`
	OrderID    *uuid.UUID `json:"order_id,omitempty"`
	RefundID   *uuid.UUID `json:"refund_id,omitempty"`
	PayoutID   *uuid.UUID `json:"payout_id,omitempty"`
}

// Journal is one balanced journal transaction. Key is its idempotency key.
// SourceEventID and OccurredAt are stamped by the caller.
type Journal struct {
	Key           string
	Kind          Kind
	Refs          Refs
	Lines         []Line
	SourceEventID *uuid.UUID
	OccurredAt    time.Time
}

// Validate checks invariant 1 in code: a key and kind, at least two lines,
// no zero line, Σ = 0.
func (j Journal) Validate() error {
	if j.Key == "" || j.Kind == "" {
		return fmt.Errorf("%w: key and kind are required", ErrInvalidPosting)
	}
	if len(j.Lines) < 2 {
		return ErrTooFewLines
	}
	var sum int64
	for _, l := range j.Lines {
		if l.Amount == 0 {
			return ErrZeroLine
		}
		if l.Account.Code == "" {
			return fmt.Errorf("%w: line without account", ErrInvalidPosting)
		}
		sum += l.Amount
	}
	if sum != 0 {
		return fmt.Errorf("%w: sum is %d", ErrUnbalanced, sum)
	}
	return nil
}

// NetByAccount sums the lines per account, in first-seen order.
func (j Journal) NetByAccount() []Line {
	var out []Line
	index := map[string]int{}
	for _, l := range j.Lines {
		if i, ok := index[l.Account.Code]; ok {
			out[i].Amount += l.Amount
			continue
		}
		index[l.Account.Code] = len(out)
		out = append(out, l)
	}
	return out
}

// LiabilityDebits are the liability accounts j debits on net. The poster
// locks them and refuses a posting that would leave one in debit
// (invariant 3). Asset and revenue accounts are never locked.
func (j Journal) LiabilityDebits() []Line {
	var out []Line
	for _, l := range j.NetByAccount() {
		if l.Account.Type == Liability && l.Amount > 0 {
			out = append(out, l)
		}
	}
	return out
}
