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
