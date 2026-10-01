package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// Wallet is what GET /api/seller/wallet shows: positive, derived figures.
type Wallet struct {
	Available     int64
	PendingPayout int64
	InEscrow      int64
}

// WalletService serves the seller wallet reads (WS2' §7).
type WalletService struct{ store Store }

func NewWalletService(store Store) *WalletService { return &WalletService{store: store} }

// Wallet derives available, pending_payout and in_escrow from the entries.
// A seller with no accounts gets zeros.
func (s *WalletService) Wallet(ctx context.Context, seller uuid.UUID) (Wallet, error) {
	b, err := s.store.WalletBalances(ctx, seller)
	if err != nil {
		return Wallet{}, err
	}
	return Wallet{Available: domain.Shown(b.Wallet), PendingPayout: domain.Shown(b.PayoutPending), InEscrow: domain.Shown(b.Escrow)}, nil
}

// Entries lists the seller's wallet entries, newest first, with money into
// the wallet positive.
func (s *WalletService) Entries(ctx context.Context, seller uuid.UUID, in ListInput) (Page[WalletEntry], error) {
	before, err := decodeOptionalCursor(in.Cursor)
	if err != nil {
		return Page[WalletEntry]{}, err
	}
	rows, err := s.store.ListWalletEntries(ctx, seller, before, in.Limit+1)
	if err != nil {
		return Page[WalletEntry]{}, err
	}
	for i := range rows {
		rows[i].Amount = domain.Shown(rows[i].Amount)
	}
	return pageOf(rows, in.Limit, func(e WalletEntry) Cursor { return Cursor{At: e.CreatedAt, ID: e.ID} }), nil
}
