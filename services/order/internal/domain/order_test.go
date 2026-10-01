package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

var at = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

func TestApplyCancelRecordsReasonAndHistory(t *testing.T) {
	o := domain.Order{
		Status:  domain.StatusPendingConfirmation,
		History: []domain.HistoryEntry{{To: domain.StatusPending, ActorRole: domain.RoleSystem}},
	}
	buyer := domain.Actor{ID: "b1", Role: domain.RoleBuyer}
	ch, err := domain.Transition(o, domain.ActionCancel, buyer)
	require.NoError(t, err)

	o.Apply(ch, "changed my mind", at)

	assert.Equal(t, domain.StatusCancelled, o.Status)
	assert.Equal(t, at, o.UpdatedAt)
	require.NotNil(t, o.CancelReason)
	assert.Equal(t, "changed my mind", *o.CancelReason)
	require.Len(t, o.History, 2)
	last := o.History[1]
	require.NotNil(t, last.From)
	assert.Equal(t, domain.StatusPendingConfirmation, *last.From)
	assert.Equal(t, domain.StatusCancelled, last.To)
	assert.Equal(t, "b1", last.ActorID)
	assert.Equal(t, domain.RoleBuyer, last.ActorRole)
	require.NotNil(t, last.Note)
	assert.Equal(t, "changed my mind", *last.Note)
	assert.Equal(t, at, last.At)
	assert.Zero(t, o.Version, "the version is owned by persistence")
}

func TestApplyWithoutNoteLeavesNoteAndCancelReasonNil(t *testing.T) {
	o := domain.Order{Status: domain.StatusPendingConfirmation}
	ch, err := domain.Transition(o, domain.ActionConfirm, domain.Actor{ID: "s1", Role: domain.RoleSeller})
	require.NoError(t, err)

	o.Apply(ch, "", at)

	assert.Equal(t, domain.StatusConfirmed, o.Status)
	assert.Nil(t, o.CancelReason)
	require.Len(t, o.History, 1)
	assert.Nil(t, o.History[0].Note)
}

func TestApplyPaymentSideEffects(t *testing.T) {
	buyer := domain.Actor{ID: "b1", Role: domain.RoleBuyer}
	seller := domain.Actor{ID: "s1", Role: domain.RoleSeller}
	sys := domain.SystemActor()
	for _, tc := range []struct {
		name     string
		from     domain.Status
		ps       domain.PaymentStatus
		action   domain.Action
		actor    domain.Actor
		wantSt   domain.Status
		wantPS   domain.PaymentStatus
		delivery bool
	}{
		{"mark paid", domain.StatusAwaitingPayment, domain.PaymentUnpaid, domain.ActionMarkPaid, sys, domain.StatusPendingConfirmation, domain.PaymentPaid, false},
		{"buyer cancels paid", domain.StatusPendingConfirmation, domain.PaymentPaid, domain.ActionCancel, buyer, domain.StatusCancelled, domain.PaymentRefundPending, false},
		{"seller cancels paid", domain.StatusConfirmed, domain.PaymentPaid, domain.ActionCancel, seller, domain.StatusCancelled, domain.PaymentRefundPending, false},
		{"system cancels unpaid", domain.StatusAwaitingPayment, domain.PaymentUnpaid, domain.ActionCancel, sys, domain.StatusCancelled, domain.PaymentUnpaid, false},
		{"cancel COD", domain.StatusPendingConfirmation, domain.PaymentNone, domain.ActionCancel, buyer, domain.StatusCancelled, domain.PaymentNone, false},
		{"deliver", domain.StatusShipping, domain.PaymentPaid, domain.ActionDeliver, seller, domain.StatusDelivered, domain.PaymentPaid, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := domain.Order{Status: tc.from, PaymentStatus: tc.ps}
			ch, err := domain.Transition(o, tc.action, tc.actor)
			require.NoError(t, err)
			o.Apply(ch, "", at)
			assert.Equal(t, tc.wantSt, o.Status)
			assert.Equal(t, tc.wantPS, o.PaymentStatus)
			if tc.delivery {
				require.NotNil(t, o.DeliveredAt)
				assert.Equal(t, at, *o.DeliveredAt)
			} else {
				assert.Nil(t, o.DeliveredAt)
			}
		})
	}
}

func TestLateCapture(t *testing.T) {
	for _, tc := range []struct {
		status domain.Status
		ps     domain.PaymentStatus
		want   bool
	}{
		{domain.StatusCancelled, domain.PaymentUnpaid, true},
		{domain.StatusCancelled, domain.PaymentRefundPending, false},
		{domain.StatusCancelled, domain.PaymentNone, false},
		{domain.StatusPendingConfirmation, domain.PaymentPaid, false},
	} {
		o := domain.Order{Status: tc.status, PaymentStatus: tc.ps}
		before := o
		assert.Equal(t, tc.want, o.LateCapture(at), "%s/%s", tc.status, tc.ps)
		if !tc.want {
			assert.Equal(t, before, o)
			continue
		}
		assert.Equal(t, domain.PaymentRefundPending, o.PaymentStatus)
		assert.Equal(t, at, o.UpdatedAt)
		assert.Equal(t, tc.status, o.Status)
		assert.Empty(t, o.History)
	}
}

func TestMarkRefunded(t *testing.T) {
	for _, tc := range []struct {
		ps   domain.PaymentStatus
		want bool
	}{
		{domain.PaymentRefundPending, true}, {domain.PaymentPaid, true},
		{domain.PaymentUnpaid, false}, {domain.PaymentNone, false}, {domain.PaymentRefunded, false},
	} {
		o := domain.Order{Status: domain.StatusCancelled, PaymentStatus: tc.ps}
		before := o
		assert.Equal(t, tc.want, o.MarkRefunded(at), string(tc.ps))
		if !tc.want {
			assert.Equal(t, before, o)
			continue
		}
		assert.Equal(t, domain.PaymentRefunded, o.PaymentStatus)
		assert.Equal(t, at, o.UpdatedAt)
		assert.Equal(t, domain.StatusCancelled, o.Status)
		assert.Empty(t, o.History)
	}
}

func TestIsOnline(t *testing.T) {
	assert.True(t, domain.IsOnline(domain.PaymentVNPay))
	assert.True(t, domain.IsOnline(domain.PaymentStripe))
	assert.False(t, domain.IsOnline(domain.PaymentCOD))
	assert.False(t, domain.IsOnline(""))
}
