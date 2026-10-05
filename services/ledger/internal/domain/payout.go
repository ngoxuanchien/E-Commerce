package domain

import (
	"time"

	"github.com/google/uuid"
)

// PayoutStatus is the payout workflow state. The money truth is the entries.
type PayoutStatus string

const (
	PayoutRequested PayoutStatus = "REQUESTED"
	PayoutSettled   PayoutStatus = "SETTLED"
	PayoutFailed    PayoutStatus = "FAILED"
)

// ParsePayoutStatus accepts the exact API values.
func ParsePayoutStatus(s string) (PayoutStatus, bool) {
	switch st := PayoutStatus(s); st {
	case PayoutRequested, PayoutSettled, PayoutFailed:
		return st, true
	}
	return "", false
}

// Payout is a seller's withdrawal request.
type Payout struct {
	ID            uuid.UUID
	SellerUserID  uuid.UUID
	Amount        int64
	Status        PayoutStatus
	FailureReason *string
	RequestedAt   time.Time
	SettledAt     *time.Time // set only when SETTLED
	Version       int
}

func payoutJournal(p Payout, suffix string, kind Kind, debit, credit Account) (Journal, error) {
	if err := positive(p.Amount); err != nil {
		return Journal{}, err
	}
	return Journal{
		Key: "payout:" + p.ID.String() + ":" + suffix, Kind: kind,
		Refs:  Refs{PayoutID: &p.ID},
		Lines: []Line{{Account: debit, Amount: p.Amount}, {Account: credit, Amount: -p.Amount}},
	}, nil
}

// PayoutRequest moves w from the wallet to payout-pending.
func PayoutRequest(p Payout) (Journal, error) {
	return payoutJournal(p, "request", KindPayoutRequest, SellerWallet(p.SellerUserID), SellerPayoutPending(p.SellerUserID))
}

// PayoutSettle pays w out of platform cash.
func PayoutSettle(p Payout) (Journal, error) {
	return payoutJournal(p, "settle", KindPayoutSettle, SellerPayoutPending(p.SellerUserID), PlatformCash())
}

// PayoutFail returns w to the wallet.
func PayoutFail(p Payout) (Journal, error) {
	return payoutJournal(p, "fail", KindPayoutFail, SellerPayoutPending(p.SellerUserID), SellerWallet(p.SellerUserID))
}
