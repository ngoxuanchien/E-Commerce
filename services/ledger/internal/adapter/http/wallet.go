package httpadapter

import (
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

func (h *handler) getWallet(w http.ResponseWriter, r *http.Request) {
	wallet, err := h.s.Wallet.Wallet(r.Context(), callerID(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toWalletDTO(wallet))
}

func (h *handler) listWalletEntries(w http.ResponseWriter, r *http.Request) {
	in, _, err := parseList(r, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := h.s.Wallet.Entries(r.Context(), callerID(r), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPage(page, toWalletEntryDTO))
}
