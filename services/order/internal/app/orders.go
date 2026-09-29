package app

import (
	"context"
	"errors"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// OrderService reads orders and runs buyer/seller lifecycle actions (WS3 §5).
type OrderService struct {
	store Store
	now   func() time.Time
}

// NewOrderService uses now as its clock (nil = UTC wall clock).
func NewOrderService(store Store, now func() time.Time) *OrderService {
	if now == nil {
		now = defaultNow
	}
	return &OrderService{store: store, now: now}
}

// Get returns an order visible to userID (its buyer or seller), else ErrNotFound.
func (s *OrderService) Get(ctx context.Context, userID, orderID string) (domain.Order, error) {
	o, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return domain.Order{}, err
	}
	if o.BuyerID != userID && o.SellerUserID != userID {
		return domain.Order{}, ErrNotFound
	}
	return o, nil
}

// ListInput selects a page of the caller's orders. Limit must be ≥ 1.
type ListInput struct {
	Party  Party
	UserID string
	Status *domain.Status
	Limit  int
	Cursor string // opaque, from a previous Page.NextCursor
}

// Page is one page of orders, newest first.
type Page struct {
	Orders     []domain.Order
	NextCursor *string // nil on the last page
}

func (s *OrderService) List(ctx context.Context, in ListInput) (Page, error) {
	q := ListQuery{Party: in.Party, UserID: in.UserID, Status: in.Status, Limit: in.Limit + 1}
	if in.Cursor != "" {
		c, err := DecodeCursor(in.Cursor)
		if err != nil {
			return Page{}, err
		}
		q.After = &c
	}
	orders, err := s.store.ListOrders(ctx, q)
	if err != nil {
		return Page{}, err
	}
	page := Page{Orders: orders}
	if len(orders) > in.Limit {
		page.Orders = orders[:in.Limit]
		last := page.Orders[in.Limit-1]
		next := EncodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = &next
	}
	return page, nil
}

// ActInput is a buyer or seller action on an order.
type ActInput struct {
	OrderID string
	Actor   domain.Actor
	Action  domain.Action
	Note    string // cancel reason, or optional seller note
}

// Act performs the action. The actor must be the order's buyer (buyer role) or
// seller (seller role), otherwise ErrNotFound. Illegal moves return
// domain.ErrInvalidTransition. A lost optimistic-lock race is retried once
// against the fresh state; losing again returns ErrConcurrentUpdate.
func (s *OrderService) Act(ctx context.Context, in ActInput) (domain.Order, error) {
	for range 2 {
		o, err := s.store.GetOrder(ctx, in.OrderID)
		if err != nil {
			return domain.Order{}, err
		}
		if !canAct(o, in.Actor) {
			return domain.Order{}, ErrNotFound
		}
		err = s.store.InTx(ctx, func(tx Tx) error {
			return applyTransition(ctx, tx, &o, in.Action, in.Actor, in.Note, s.now())
		})
		if errors.Is(err, ErrConcurrentUpdate) {
			continue
		}
		if err != nil {
			return domain.Order{}, err
		}
		return o, nil
	}
	return domain.Order{}, ErrConcurrentUpdate
}

func canAct(o domain.Order, a domain.Actor) bool {
	switch a.Role {
	case domain.RoleBuyer:
		return o.BuyerID == a.ID
	case domain.RoleSeller:
		return o.SellerUserID == a.ID
	default:
		return false
	}
}
