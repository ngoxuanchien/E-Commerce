package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

func runTrialBalance(t *testing.T, dsn string) (int, map[string]any, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := trialBalance(context.Background(), dsn, &out, &errOut)
	var report map[string]any
	if out.Len() > 0 {
		require.NoError(t, json.Unmarshal(out.Bytes(), &report), out.String())
	}
	return code, report, errOut.String()
}

func TestTrialBalanceExitsZeroOnConsistentLedger(t *testing.T) {
	db, dsn := testsupport.DBWithDSN(t)
	events := app.NewEventHandler(postgres.NewStore(db), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	order := uuid.NewString()
	for _, p := range []event.Payload{
		event.PaymentSucceeded{PaymentID: uuid.NewString(), CheckoutID: uuid.NewString(), BuyerID: uuid.NewString(), Provider: "vnpay",
			ProviderRef: "r", Amount: 230000, Allocations: []event.Allocation{{OrderID: order, SellerUserID: uuid.NewString(), Amount: 230000, CommissionAmount: 10000}}},
	} {
		env, err := event.New(p, time.Now())
		require.NoError(t, err)
		require.NoError(t, events.Handle(context.Background(), env))
	}
	code, report, _ := runTrialBalance(t, dsn)
	assert.Equal(t, 0, code)
	assert.Equal(t, true, report["ok"])
	assert.Equal(t, 0.0, report["total"])
	assert.Equal(t, 0.0, report["unbalanced_transactions"])
	assert.Equal(t, 0.0, report["liabilities_in_debit"])
	assert.Equal(t, map[string]any{"asset": 230000.0, "liability": -230000.0}, report["balances_by_type"])
}

func TestTrialBalanceExitsOneOnInconsistentLedger(t *testing.T) {
	db, dsn := testsupport.DBWithDSN(t)
	testsupport.InsertCorruptTransaction(t, db)
	code, report, _ := runTrialBalance(t, dsn)
	assert.Equal(t, 1, code)
	assert.Equal(t, false, report["ok"])
	assert.Equal(t, 1.0, report["unbalanced_transactions"])
	assert.Equal(t, 1.0, report["liabilities_in_debit"])
}

func TestTrialBalanceExitsOneWithoutDatabase(t *testing.T) {
	code, _, stderr := runTrialBalance(t, "")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "DATABASE_URL")
	code, _, stderr = runTrialBalance(t, "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable")
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, stderr)
}
