package domain

// Status is an order's lifecycle state (WS3 §5).
type Status string

const (
	StatusPending             Status = "PENDING"
	StatusPendingConfirmation Status = "PENDING_CONFIRMATION"
	StatusConfirmed           Status = "CONFIRMED"
	StatusShipping            Status = "SHIPPING"
	StatusDelivered           Status = "DELIVERED"
	StatusCompleted           Status = "COMPLETED"
	StatusCancelled           Status = "CANCELLED"
)

// Statuses lists every status in lifecycle order.
var Statuses = []Status{
	StatusPending, StatusPendingConfirmation, StatusConfirmed, StatusShipping,
	StatusDelivered, StatusCompleted, StatusCancelled,
}

// ParseStatus returns the Status named s (case-sensitive).
func ParseStatus(s string) (Status, bool) {
	for _, st := range Statuses {
		if string(st) == s {
			return st, true
		}
	}
	return "", false
}

// Role is who performs a transition.
type Role string

const (
	RoleBuyer  Role = "buyer"
	RoleSeller Role = "seller"
	RoleSystem Role = "system"
)

// Actor performs a transition. ID is empty for the system.
type Actor struct {
	ID   string
	Role Role
}

// SystemActor is checkout and the sweeper.
func SystemActor() Actor { return Actor{Role: RoleSystem} }

// Action is a requested transition.
type Action string

const (
	ActionSubmit   Action = "submit" // checkout reserved all stock: PENDING → PENDING_CONFIRMATION
	ActionConfirm  Action = "confirm"
	ActionShip     Action = "ship"
	ActionDeliver  Action = "deliver"
	ActionComplete Action = "complete"
	ActionCancel   Action = "cancel"
)

// Actions lists every action.
var Actions = []Action{ActionSubmit, ActionConfirm, ActionShip, ActionDeliver, ActionComplete, ActionCancel}
