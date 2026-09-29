// Package domain holds payment-svc's entities and rules (WS1').
package domain

import "time"

// Status is the lifecycle state of a payment intent.
type Status string

const (
	StatusPending   Status = "PENDING"
	StatusSucceeded Status = "SUCCEEDED"
	StatusFailed    Status = "FAILED"
	StatusExpired   Status = "EXPIRED"
	StatusDuplicate Status = "DUPLICATE"
)

// Final reports SUCCEEDED/DUPLICATE: nothing changes them. FAILED/EXPIRED can still be overtaken by a provider success.
func (s Status) Final() bool { return s == StatusSucceeded || s == StatusDuplicate }

// Terminal reports every status other than PENDING (a payment shown as done to the buyer).
func (s Status) Terminal() bool { return s != StatusPending }

// ObservedState is the provider-side state seen by a webhook or a poll.
type ObservedState int

const (
	ObservedPending ObservedState = iota + 1
	ObservedSucceeded
	ObservedFailed
	ObservedExpired
)

// Observed is what a provider reported about a payment.
type Observed struct {
	State         ObservedState
	Amount        int64 // VND as confirmed by the provider (checked only for Succeeded)
	ProviderTxnID string
	Reason        string
}

// Payment is a payment intent for one checkout.
type Payment struct {
	ID, CheckoutID, BuyerID                                string
	Method, Provider                                       string
	Status                                                 Status
	Amount                                                 int64
	Captured                                               bool
	ProviderRef, ProviderTxnID, RedirectURL, FailureReason string
	ExpiresAt, CreatedAt, UpdatedAt                        time.Time
	Version                                                int
}
