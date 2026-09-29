package httpadapter

import (
	"net/http"
	"strings"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

var sellerActions = map[string]domain.Action{
	"confirm": domain.ActionConfirm,
	"ship":    domain.ActionShip,
	"deliver": domain.ActionDeliver,
	"cancel":  domain.ActionCancel,
}

func (h *handler) listMyOrders(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, app.PartyBuyer)
}

func (h *handler) listSellerOrders(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, app.PartySeller)
}

func (h *handler) list(w http.ResponseWriter, r *http.Request, party app.Party) {
	in, err := parseListInput(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	in.Party, in.UserID = party, callerID(r)
	page, err := h.s.Orders.List(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPageDTO(page))
}

func (h *handler) getOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		writeError(w, r, app.ErrNotFound)
		return
	}
	o, err := h.s.Orders.Get(r.Context(), callerID(r), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toOrderDTO(o))
}

func (h *handler) cancelMyOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		writeError(w, r, app.ErrNotFound)
		return
	}
	reason, err := decodeReason(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.act(w, r, app.ActInput{OrderID: id, Actor: domain.Actor{ID: callerID(r), Role: domain.RoleBuyer}, Action: domain.ActionCancel, Note: reason})
}

func (h *handler) completeMyOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		writeError(w, r, app.ErrNotFound)
		return
	}
	h.act(w, r, app.ActInput{OrderID: id, Actor: domain.Actor{ID: callerID(r), Role: domain.RoleBuyer}, Action: domain.ActionComplete})
}

func (h *handler) sellerAction(w http.ResponseWriter, r *http.Request) {
	action, ok := sellerActions[r.PathValue("action")]
	if !ok {
		writeError(w, r, httpx.NewError(httpx.CodeNotFound, "route not found"))
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		writeError(w, r, app.ErrNotFound)
		return
	}
	var note string
	if action == domain.ActionCancel {
		reason, err := decodeReason(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		note = reason
	} else {
		var req noteRequest
		if err := decodeOptional(r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		note = strings.TrimSpace(req.Reason)
	}
	h.act(w, r, app.ActInput{OrderID: id, Actor: domain.Actor{ID: callerID(r), Role: domain.RoleSeller}, Action: action, Note: note})
}

func (h *handler) act(w http.ResponseWriter, r *http.Request, in app.ActInput) {
	o, err := h.s.Orders.Act(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toOrderDTO(o))
}
