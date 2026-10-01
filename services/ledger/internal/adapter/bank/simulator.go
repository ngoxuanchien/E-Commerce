// Package bank is the BankPort adapter. P2 has no real bank transfer
// (overview §1.7): Simulator accepts every transfer.
package bank

import (
	"context"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

// Simulator always succeeds; repeating a payout id is harmless (idempotent).
type Simulator struct{}

var _ app.Bank = Simulator{}

func (Simulator) Transfer(context.Context, uuid.UUID, uuid.UUID, int64) error { return nil }
