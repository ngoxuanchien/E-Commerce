package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidTransition means the actor may not perform the action on an order
// in its current status.
var ErrInvalidTransition = errors.New("invalid state transition")

// StatusChange is the outcome of a legal transition.
type StatusChange struct {
	From  Status
	To    Status
	Actor Actor
}

type move struct {
	action Action
	from   Status
	role   Role
}

// transitions is the complete state machine (WS3 §5). Every move that is not
// listed is illegal.
var transitions = map[move]Status{
	{ActionSubmit, StatusPending, RoleSystem}:              StatusPendingConfirmation,
	{ActionCancel, StatusPending, RoleSystem}:              StatusCancelled,
	{ActionConfirm, StatusPendingConfirmation, RoleSeller}: StatusConfirmed,
	{ActionCancel, StatusPendingConfirmation, RoleBuyer}:   StatusCancelled,
	{ActionCancel, StatusPendingConfirmation, RoleSeller}:  StatusCancelled,
	{ActionShip, StatusConfirmed, RoleSeller}:              StatusShipping,
	{ActionCancel, StatusConfirmed, RoleSeller}:            StatusCancelled,
	{ActionDeliver, StatusShipping, RoleSeller}:            StatusDelivered,
	{ActionComplete, StatusDelivered, RoleBuyer}:           StatusCompleted,
}

// Transition decides whether actor may perform action on o. It is pure: o is
// not modified (see Order.Apply). Illegal moves wrap ErrInvalidTransition.
func Transition(o Order, action Action, actor Actor) (StatusChange, error) {
	to, ok := transitions[move{action: action, from: o.Status, role: actor.Role}]
	if !ok {
		return StatusChange{}, fmt.Errorf("%w: %s cannot %s an order in status %s", ErrInvalidTransition, actor.Role, action, o.Status)
	}
	return StatusChange{From: o.Status, To: to, Actor: actor}, nil
}
