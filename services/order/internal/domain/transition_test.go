package domain_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

var roles = []domain.Role{domain.RoleBuyer, domain.RoleSeller, domain.RoleSystem}

// TestTransitionTable checks every (status, action, role) combination: the
// nine legal moves of WS3 §5 succeed, everything else is rejected.
func TestTransitionTable(t *testing.T) {
	type key struct {
		from   domain.Status
		action domain.Action
		role   domain.Role
	}
	legal := map[key]domain.Status{
		{domain.StatusPending, domain.ActionSubmit, domain.RoleSystem}:              domain.StatusPendingConfirmation,
		{domain.StatusPending, domain.ActionCancel, domain.RoleSystem}:              domain.StatusCancelled,
		{domain.StatusPendingConfirmation, domain.ActionConfirm, domain.RoleSeller}: domain.StatusConfirmed,
		{domain.StatusPendingConfirmation, domain.ActionCancel, domain.RoleBuyer}:   domain.StatusCancelled,
		{domain.StatusPendingConfirmation, domain.ActionCancel, domain.RoleSeller}:  domain.StatusCancelled,
		{domain.StatusConfirmed, domain.ActionShip, domain.RoleSeller}:              domain.StatusShipping,
		{domain.StatusConfirmed, domain.ActionCancel, domain.RoleSeller}:            domain.StatusCancelled,
		{domain.StatusShipping, domain.ActionDeliver, domain.RoleSeller}:            domain.StatusDelivered,
		{domain.StatusDelivered, domain.ActionComplete, domain.RoleBuyer}:           domain.StatusCompleted,
	}
	require.Len(t, domain.Statuses, 8, "table must cover every status")
	require.Len(t, domain.Actions, 6, "table must cover every action")

	seen := 0
	for _, from := range domain.Statuses {
		for _, action := range domain.Actions {
			for _, role := range roles {
				t.Run(fmt.Sprintf("%s/%s/%s", from, action, role), func(t *testing.T) {
					actor := domain.Actor{ID: "actor-1", Role: role}
					ch, err := domain.Transition(domain.Order{Status: from}, action, actor)
					want, ok := legal[key{from, action, role}]
					if !ok {
						require.ErrorIs(t, err, domain.ErrInvalidTransition)
						assert.Equal(t, domain.StatusChange{}, ch)
						return
					}
					require.NoError(t, err)
					assert.Equal(t, domain.StatusChange{From: from, To: want, Actor: actor}, ch)
				})
				seen++
			}
		}
	}
	assert.Equal(t, 8*6*3, seen)
}

func TestTransitionRejectsUnknownRole(t *testing.T) {
	_, err := domain.Transition(domain.Order{Status: domain.StatusPendingConfirmation}, domain.ActionConfirm, domain.Actor{ID: "x", Role: "admin"})
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}

func TestTransitionErrorNamesTheMove(t *testing.T) {
	_, err := domain.Transition(domain.Order{Status: domain.StatusConfirmed}, domain.ActionCancel, domain.Actor{ID: "b", Role: domain.RoleBuyer})
	assert.EqualError(t, err, "invalid state transition: buyer cannot cancel an order in status CONFIRMED")
}

func TestParseStatus(t *testing.T) {
	for _, s := range domain.Statuses {
		got, ok := domain.ParseStatus(string(s))
		assert.True(t, ok)
		assert.Equal(t, s, got)
	}
	_, ok := domain.ParseStatus("pending")
	assert.False(t, ok, "status names are case-sensitive")
}
