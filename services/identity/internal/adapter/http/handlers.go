package httpapi

import (
	"net/http"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
)

const (
	refreshCookieName = "refresh_token"
	refreshCookiePath = "/api/auth"
)

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, err := h.auth.Register(r.Context(), req.Email, req.Password, req.FullName)
	if err != nil {
		httpx.WriteError(w, r, toAppError(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toUserResponse(u))
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	sess, err := h.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		httpx.WriteError(w, r, toAppError(err))
		return
	}
	h.writeSession(w, sess)
}

func (h *handler) refresh(w http.ResponseWriter, r *http.Request) {
	sess, err := h.auth.Refresh(r.Context(), refreshCookie(r))
	if err != nil {
		if isSessionError(err) {
			h.clearRefreshCookie(w)
		}
		httpx.WriteError(w, r, toAppError(err))
		return
	}
	h.writeSession(w, sess)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	err := h.auth.Logout(r.Context(), refreshCookie(r))
	h.clearRefreshCookie(w)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.auth.Me(r.Context(), authctx.UserID(r.Context()))
	if err != nil {
		httpx.WriteError(w, r, toAppError(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserResponse(u))
}

func (h *handler) getJWKS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpx.WriteJSON(w, http.StatusOK, h.jwks)
}

func (h *handler) writeSession(w http.ResponseWriter, s app.Session) {
	h.setRefreshCookie(w, s.RefreshToken, int(h.cfg.RefreshTTL/time.Second))
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, toTokenResponse(s))
}

func refreshCookie(r *http.Request) string {
	c, err := r.Cookie(refreshCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// setRefreshCookie writes the refresh cookie; maxAge < 0 emits Max-Age=0 (delete).
func (h *handler) setRefreshCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     refreshCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *handler) clearRefreshCookie(w http.ResponseWriter) {
	h.setRefreshCookie(w, "", -1)
}
