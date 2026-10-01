package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

func TestValidate(t *testing.T) {
	a, b := domain.PlatformCash(), domain.CommissionRevenue()
	base := domain.Journal{Key: "k", Kind: domain.KindPay}
	with := func(lines ...domain.Line) domain.Journal { j := base; j.Lines = lines; return j }

	assert.NoError(t, with(domain.Line{Account: a, Amount: 5}, domain.Line{Account: b, Amount: -5}).Validate())
	assert.ErrorIs(t, with(domain.Line{Account: a, Amount: 5}).Validate(), domain.ErrTooFewLines)
	assert.ErrorIs(t, with(domain.Line{Account: a, Amount: 5}, domain.Line{Account: b, Amount: -4}).Validate(), domain.ErrUnbalanced)
	assert.ErrorIs(t, with(domain.Line{Account: a, Amount: 0}, domain.Line{Account: b, Amount: 0}).Validate(), domain.ErrZeroLine)
	noKey := with(domain.Line{Account: a, Amount: 5}, domain.Line{Account: b, Amount: -5})
	noKey.Key = ""
	assert.ErrorIs(t, noKey.Validate(), domain.ErrInvalidPosting)
}

func TestLiabilityDebitsNetsPerAccount(t *testing.T) {
	s := id()
	w, pend, cash := domain.SellerWallet(s), domain.SellerPayoutPending(s), domain.PlatformCash()
	j := domain.Journal{Key: "k", Kind: domain.KindPayoutRequest, Lines: []domain.Line{
		{Account: w, Amount: 70}, {Account: w, Amount: 30}, // nets to +100 on the wallet
		{Account: pend, Amount: -100},
		{Account: cash, Amount: 10}, {Account: pend, Amount: -10}, // an asset debit is never locked
	}}
	assert.Equal(t, []domain.Line{{Account: w, Amount: 100}}, j.LiabilityDebits())
}

func TestEscrowAccountCarriesOwnerAndOrder(t *testing.T) {
	o, s := id(), id()
	acc := domain.Escrow(o, s)
	assert.Equal(t, "escrow:"+o.String(), acc.Code)
	assert.Equal(t, domain.Liability, acc.Type)
	assert.Equal(t, &s, acc.OwnerID)
	assert.Equal(t, &o, acc.OrderID)
	assert.Equal(t, domain.Asset, domain.PSPClearing("vnpay").Type)
	assert.Equal(t, domain.Revenue, domain.CommissionRevenue().Type)
	assert.True(t, domain.ValidProvider("stripe"))
	assert.False(t, domain.ValidProvider("VNPAY"))
}
