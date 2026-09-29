package httpapi

import (
	"time"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// registerRequest has no validate tags: domain.NewRegistration owns the rules.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
}

type loginRequest struct {
	Email    string `json:"email" validate:"required"`
	Password string `json:"password" validate:"required"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"created_at"`
}

type tokenResponse struct {
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresIn   int64        `json:"expires_in"`
	User        userResponse `json:"user"`
}

func toUserResponse(u domain.User) userResponse {
	roles := make([]string, len(u.Roles))
	for i, r := range u.Roles {
		roles[i] = string(r)
	}
	return userResponse{ID: u.ID.String(), Email: u.Email, FullName: u.FullName, Roles: roles, CreatedAt: u.CreatedAt.UTC()}
}

func toTokenResponse(s app.Session) tokenResponse {
	return tokenResponse{
		AccessToken: s.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(s.ExpiresIn / time.Second),
		User:        toUserResponse(s.User),
	}
}
