package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

// DefaultBcryptCost is the production bcrypt work factor (WS1 §3).
const DefaultBcryptCost = 12

// AuthConfig tunes AuthService. Zero values select production defaults.
type AuthConfig struct {
	RefreshTTL time.Duration
	BcryptCost int              // 0 means DefaultBcryptCost
	Now        func() time.Time // nil means time.Now
	Logger     *slog.Logger     // nil means slog.Default()
}

// Session is the result of a successful login or refresh.
type Session struct {
	AccessToken  string
	ExpiresIn    time.Duration
	RefreshToken string
	User         domain.User
}

// AuthService implements registration, login, token refresh, logout and "me".
type AuthService struct {
	store     Store
	signer    *token.Signer
	cfg       AuthConfig
	dummyHash []byte
}

// NewAuthService builds an AuthService.
func NewAuthService(store Store, signer *token.Signer, cfg AuthConfig) (*AuthService, error) {
	if cfg.BcryptCost == 0 {
		cfg.BcryptCost = DefaultBcryptCost
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	// Login compares against this hash when the email is unknown so both
	// failure paths cost one bcrypt comparison.
	dummy, err := bcrypt.GenerateFromPassword([]byte("timing-equalizer"), cfg.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("generate dummy hash: %w", err)
	}
	return &AuthService{store: store, signer: signer, cfg: cfg, dummyHash: dummy}, nil
}

// Register creates a buyer account. It does not log the user in.
func (s *AuthService) Register(ctx context.Context, email, password, fullName string) (domain.User, error) {
	reg, err := domain.NewRegistration(email, password, fullName)
	if err != nil {
		return domain.User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(reg.Password), s.cfg.BcryptCost)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.User{}, fmt.Errorf("generate user id: %w", err)
	}
	u := domain.User{ID: id, Email: reg.Email, FullName: reg.FullName, Roles: []domain.Role{domain.RoleBuyer}}
	return s.store.CreateUser(ctx, u, string(hash))
}

// Login verifies credentials and starts a new refresh-token family.
// Unknown email and wrong password return the same domain.ErrInvalidCredentials.
func (s *AuthService) Login(ctx context.Context, email, password string) (Session, error) {
	u, hash, err := s.store.UserByEmail(ctx, domain.NormalizeEmail(email))
	switch {
	case errors.Is(err, domain.ErrNotFound):
		hash = string(s.dummyHash)
	case err != nil:
		return Session{}, err
	}
	// bcrypt only reads the first 72 bytes; longer input must not match its prefix.
	matched := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	if err != nil || !matched || len(password) > domain.MaxPasswordBytes {
		return Session{}, domain.ErrInvalidCredentials
	}
	family, err := uuid.NewV7()
	if err != nil {
		return Session{}, fmt.Errorf("generate token family: %w", err)
	}
	sess, _, err := s.newSession(ctx, s.store, u, family)
	return sess, err
}

// Me returns the user behind userID (the gateway-provided X-User-Id).
// A malformed or unknown id returns domain.ErrNotFound.
func (s *AuthService) Me(ctx context.Context, userID string) (domain.User, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return domain.User{}, domain.ErrNotFound
	}
	return s.store.UserByID(ctx, id)
}

type refreshTokenWriter interface {
	CreateRefreshToken(ctx context.Context, t domain.RefreshToken) error
}

// newSession stores a new refresh token in familyID and signs an access token for u.
func (s *AuthService) newSession(ctx context.Context, w refreshTokenWriter, u domain.User, familyID uuid.UUID) (Session, domain.RefreshToken, error) {
	raw, hash, err := token.NewRefreshToken()
	if err != nil {
		return Session{}, domain.RefreshToken{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, domain.RefreshToken{}, fmt.Errorf("generate token id: %w", err)
	}
	now := s.cfg.Now()
	rt := domain.RefreshToken{ID: id, UserID: u.ID, FamilyID: familyID, Hash: hash, ExpiresAt: now.Add(s.cfg.RefreshTTL)}
	if err := w.CreateRefreshToken(ctx, rt); err != nil {
		return Session{}, domain.RefreshToken{}, err
	}
	access, err := s.signer.Sign(u.ID.String(), roleNames(u.Roles), now)
	if err != nil {
		return Session{}, domain.RefreshToken{}, err
	}
	return Session{AccessToken: access, ExpiresIn: s.signer.TTL(), RefreshToken: raw, User: u}, rt, nil
}

func roleNames(roles []domain.Role) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}
