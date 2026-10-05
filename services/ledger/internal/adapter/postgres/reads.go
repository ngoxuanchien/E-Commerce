package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres/sqlcdb"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

func (s *Store) WalletBalances(ctx context.Context, seller uuid.UUID) (app.WalletBalances, error) {
	var b app.WalletBalances
	var err error
	if b.Wallet, err = s.q.BalanceByCode(ctx, domain.SellerWallet(seller).Code); err != nil {
		return b, fmt.Errorf("wallet balance: %w", err)
	}
	if b.PayoutPending, err = s.q.BalanceByCode(ctx, domain.SellerPayoutPending(seller).Code); err != nil {
		return b, fmt.Errorf("payout pending balance: %w", err)
	}
	if b.Escrow, err = s.q.EscrowBalanceBySeller(ctx, &seller); err != nil {
		return b, fmt.Errorf("escrow balance: %w", err)
	}
	return b, nil
}

func cursorArgs(c *app.Cursor) (*time.Time, *uuid.UUID) {
	if c == nil {
		return nil, nil
	}
	return &c.At, &c.ID
}

func (s *Store) ListWalletEntries(ctx context.Context, seller uuid.UUID, before *app.Cursor, limit int) ([]app.WalletEntry, error) {
	at, id := cursorArgs(before)
	rows, err := s.q.ListAccountEntries(ctx, sqlcdb.ListAccountEntriesParams{
		Code: domain.SellerWallet(seller).Code, BeforeCreatedAt: at, BeforeID: id, PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list wallet entries: %w", err)
	}
	out := make([]app.WalletEntry, 0, len(rows))
	for _, r := range rows {
		var refs domain.Refs
		if err := json.Unmarshal(r.Refs, &refs); err != nil {
			return nil, fmt.Errorf("decode refs of %s: %w", r.TransactionID, err)
		}
		out = append(out, app.WalletEntry{ID: r.ID, TransactionID: r.TransactionID, Kind: domain.Kind(r.Kind),
			Amount: r.Amount, OrderID: refs.OrderID, PayoutID: refs.PayoutID, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

func (s *Store) ListPayouts(ctx context.Context, seller uuid.UUID, status *domain.PayoutStatus, before *app.Cursor, limit int) ([]domain.Payout, error) {
	at, id := cursorArgs(before)
	var st pgtype.Text
	if status != nil {
		st = pgtype.Text{String: string(*status), Valid: true}
	}
	rows, err := s.q.ListSellerPayouts(ctx, sqlcdb.ListSellerPayoutsParams{
		SellerUserID: seller, Status: st, BeforeRequestedAt: at, BeforeID: id, PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list payouts: %w", err)
	}
	out := make([]domain.Payout, len(rows))
	for i, r := range rows {
		out[i] = toDomainPayout(r)
	}
	return out, nil
}

func (s *Store) TrialBalance(ctx context.Context) (app.TrialBalance, error) {
	var r app.TrialBalance
	byType, err := s.q.BalancesByAccountType(ctx)
	if err != nil {
		return r, fmt.Errorf("balances by type: %w", err)
	}
	r.BalancesByType = make(map[string]int64, len(byType))
	for _, row := range byType {
		r.BalancesByType[row.Type] = row.Balance
	}
	if r.Total, err = s.q.TotalOfAllEntries(ctx); err != nil {
		return r, fmt.Errorf("total: %w", err)
	}
	if r.UnbalancedTransactions, err = s.q.CountUnbalancedTransactions(ctx); err != nil {
		return r, fmt.Errorf("unbalanced transactions: %w", err)
	}
	if r.LiabilitiesInDebit, err = s.q.CountLiabilitiesInDebit(ctx); err != nil {
		return r, fmt.Errorf("liabilities in debit: %w", err)
	}
	return r, nil
}
