// Package domain is ledger-svc's pure model (WS2' §2–§3): the chart of
// accounts, journal transactions and one builder per posting kind. It does
// no I/O; internal/app posts what these builders return.
package domain

import (
	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
)

// AccountType classifies an account. Liability and revenue accounts normally
// carry a credit (negative) balance.
type AccountType string

const (
	Asset     AccountType = "asset"
	Liability AccountType = "liability"
	Revenue   AccountType = "revenue"
)

// Account identifies a ledger account by its unique code. Accounts are
// created lazily the first time a posting uses them.
type Account struct {
	Code    string
	Type    AccountType
	OwnerID *uuid.UUID // the seller, for escrow, wallet and payout-pending accounts
	OrderID *uuid.UUID // escrow accounts only
}

// Fixed account codes and the escrow prefix (the in_escrow index predicate).
const (
	CodeCommissionRevenue = "platform_commission_revenue"
	CodePlatformCash      = "platform_cash"
	EscrowCodePrefix      = "escrow:"
)

// PSPClearing is money held at a provider for the platform.
func PSPClearing(provider string) Account {
	return Account{Code: "psp_clearing:" + provider, Type: Asset}
}

// CheckoutSuspense is money received for a checkout but not yet allocated.
func CheckoutSuspense(checkoutID uuid.UUID) Account {
	return Account{Code: "checkout_suspense:" + checkoutID.String(), Type: Liability}
}

// Escrow holds one order's share until the order completes or is refunded.
func Escrow(orderID, sellerID uuid.UUID) Account {
	return Account{Code: EscrowCodePrefix + orderID.String(), Type: Liability, OwnerID: &sellerID, OrderID: &orderID}
}

// SellerWallet is the seller's available balance.
func SellerWallet(sellerID uuid.UUID) Account {
	return Account{Code: "seller_wallet:" + sellerID.String(), Type: Liability, OwnerID: &sellerID}
}

// SellerPayoutPending holds requested, not yet settled withdrawals.
func SellerPayoutPending(sellerID uuid.UUID) Account {
	return Account{Code: "seller_payout_pending:" + sellerID.String(), Type: Liability, OwnerID: &sellerID}
}

// CommissionRevenue is the platform's commission income.
func CommissionRevenue() Account { return Account{Code: CodeCommissionRevenue, Type: Revenue} }

// PlatformCash is the simulated bank account payouts are paid from. P2 does
// not model provider settlement into it, so it goes negative (ADR-0006).
func PlatformCash() Account { return Account{Code: CodePlatformCash, Type: Asset} }

// ValidProvider reports whether p is a payment.events provider.
func ValidProvider(p string) bool {
	switch p {
	case event.ProviderVNPay, event.ProviderStripe, event.ProviderFake:
		return true
	}
	return false
}

// Shown converts the Σ of a credit-normal account (liability, revenue) to the
// positive figure the API shows.
func Shown(balance int64) int64 { return -balance }
