package httpadapter

import (
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

type walletDTO struct {
	Available     int64  `json:"available"`
	PendingPayout int64  `json:"pending_payout"`
	InEscrow      int64  `json:"in_escrow"`
	Currency      string `json:"currency"`
}

type walletEntryDTO struct {
	ID            string    `json:"id"`
	TransactionID string    `json:"transaction_id"`
	Kind          string    `json:"kind"`
	Amount        int64     `json:"amount"`
	OrderID       *string   `json:"order_id"`
	PayoutID      *string   `json:"payout_id"`
	CreatedAt     time.Time `json:"created_at"`
}

type payoutDTO struct {
	ID            string     `json:"id"`
	Amount        int64      `json:"amount"`
	Status        string     `json:"status"`
	FailureReason *string    `json:"failure_reason"`
	RequestedAt   time.Time  `json:"requested_at"`
	SettledAt     *time.Time `json:"settled_at"`
}

type pageDTO[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func optionalID(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

func toWalletDTO(w app.Wallet) walletDTO {
	return walletDTO{Available: w.Available, PendingPayout: w.PendingPayout, InEscrow: w.InEscrow, Currency: "VND"}
}

func toWalletEntryDTO(e app.WalletEntry) walletEntryDTO {
	return walletEntryDTO{ID: e.ID.String(), TransactionID: e.TransactionID.String(), Kind: string(e.Kind),
		Amount: e.Amount, OrderID: optionalID(e.OrderID), PayoutID: optionalID(e.PayoutID), CreatedAt: e.CreatedAt.UTC()}
}

func toPayoutDTO(p domain.Payout) payoutDTO {
	d := payoutDTO{ID: p.ID.String(), Amount: p.Amount, Status: string(p.Status), FailureReason: p.FailureReason,
		RequestedAt: p.RequestedAt.UTC()}
	if p.SettledAt != nil {
		t := p.SettledAt.UTC()
		d.SettledAt = &t
	}
	return d
}

func toPage[S, D any](p app.Page[S], conv func(S) D) pageDTO[D] {
	items := make([]D, len(p.Items))
	for i, s := range p.Items {
		items[i] = conv(s)
	}
	return pageDTO[D]{Items: items, NextCursor: p.NextCursor}
}
