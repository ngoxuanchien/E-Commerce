package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/routes"
)

// Issuer is the only "iss" claim value the gateway accepts on access tokens.
const Issuer = "identity"

// Leeway is the clock-skew tolerance applied to the exp and nbf claims.
const Leeway = 30 * time.Second

// KeySource resolves a JWKS key id to the RSA public key that should verify
// a token's signature. *JWKS implements this.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// accessTokenClaims is the claim set identity issues on access tokens.
type accessTokenClaims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles"`
}

// Verifier checks access tokens issued by identity: RS256 signature over a
// key resolved by kid, issuer, expiration and subject.
type Verifier struct {
	keys   KeySource
	parser *jwt.Parser
}

// NewVerifier builds a Verifier that resolves signing keys through keys.
func NewVerifier(keys KeySource) *Verifier {
	return &Verifier{
		keys: keys,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{"RS256"}),
			jwt.WithIssuer(Issuer),
			jwt.WithLeeway(Leeway),
			jwt.WithExpirationRequired(),
		),
	}
}

// Verify parses and validates raw as an access token, returning the caller
// identity it carries. Beyond the parser's own checks (signature, method,
// issuer, expiration, leeway) it requires a non-empty kid header, a
// non-empty sub claim, and roles that are each non-empty and contain no
// comma or whitespace (which would let a forged role value smuggle extra
// roles through the comma-joined X-User-Roles header).
func (v *Verifier) Verify(ctx context.Context, raw string) (authctx.Identity, error) {
	var claims accessTokenClaims
	keyFunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("auth: token has no kid")
		}
		return v.keys.Key(ctx, kid)
	}

	if _, err := v.parser.ParseWithClaims(raw, &claims, keyFunc); err != nil {
		return authctx.Identity{}, fmt.Errorf("auth: verify token: %w", err)
	}

	sub := strings.TrimSpace(claims.Subject)
	if sub == "" {
		return authctx.Identity{}, errors.New("auth: token has no sub")
	}

	for _, role := range claims.Roles {
		if invalidRole(role) {
			return authctx.Identity{}, fmt.Errorf("auth: token has invalid role %q", role)
		}
	}

	return authctx.Identity{UserID: sub, Roles: claims.Roles}, nil
}

// invalidRole reports whether role cannot safely be joined into the
// comma-separated X-User-Roles header: empty, containing a comma, or
// containing whitespace.
func invalidRole(role string) bool {
	if role == "" {
		return true
	}
	if strings.ContainsRune(role, ',') {
		return true
	}
	return strings.ContainsFunc(role, unicode.IsSpace)
}

// StripIdentityHeaders deletes every header in h whose name, lower-cased
// and with '_' mapped to '-', is x-user-id or x-user-roles. It catches
// spelling variants (X_User_Id, x-user-id, ...) that Go's own header
// canonicalization would not normalize to the same key, and so must run
// before any code trusts those headers.
func StripIdentityHeaders(h http.Header) {
	for name := range h {
		normalized := strings.ReplaceAll(strings.ToLower(name), "_", "-")
		if normalized == "x-user-id" || normalized == "x-user-roles" {
			delete(h, name)
		}
	}
}

// bearerToken extracts the bearer token from h's Authorization header.
// present reports whether an Authorization header exists at all; ok
// reports whether it holds exactly one well-formed "Bearer <token>" value
// (case-insensitive scheme, a single non-empty token with no whitespace).
func bearerToken(h http.Header) (token string, present, ok bool) {
	values := h.Values("Authorization")
	if len(values) == 0 {
		return "", false, false
	}
	if len(values) != 1 {
		return "", true, false
	}

	scheme, rest, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", true, false
	}
	if rest == "" || strings.ContainsFunc(rest, unicode.IsSpace) {
		return "", true, false
	}
	return rest, true, true
}

// Middleware builds the gateway's auth gate for a route at the given level.
// It always strips client-supplied identity headers first. If an
// Authorization header is present, it must be a single well-formed bearer
// token that verifies — otherwise the request is rejected even on a Public
// route, since a client should never be able to smuggle a bad credential
// past the gateway. A route at User or Seller level then requires an
// identity, and Seller additionally requires the "seller" role. On
// success, the verified identity is set both as X-User-Id/X-User-Roles
// request headers (for the upstream) and in the request context (for
// authctx.UserID and friends).
func Middleware(v *Verifier, level routes.Level) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			StripIdentityHeaders(r.Header)

			var identity authctx.Identity
			var authenticated bool

			token, present, ok := bearerToken(r.Header)
			if present {
				if !ok {
					reject(w, r, httpx.CodeUnauthenticated, "invalid authorization header",
						"malformed Authorization header")
					return
				}
				id, err := v.Verify(r.Context(), token)
				if err != nil {
					reject(w, r, httpx.CodeUnauthenticated, "invalid or expired token", err.Error())
					return
				}
				identity, authenticated = id, true
			}

			if level >= routes.User && !authenticated {
				reject(w, r, httpx.CodeUnauthenticated, "authentication required", "no credentials for a protected route")
				return
			}

			if level == routes.Seller && !slices.Contains(identity.Roles, authctx.RoleSeller) {
				reject(w, r, httpx.CodeForbidden, "requires seller role", "authenticated user lacks seller role")
				return
			}

			if authenticated {
				r.Header.Set(authctx.HeaderUserID, identity.UserID)
				r.Header.Set(authctx.HeaderUserRoles, strings.Join(identity.Roles, ","))
				r = r.WithContext(authctx.WithIdentity(r.Context(), identity))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// reject writes a standard error response and logs why, at info level:
// rejecting a credential is an expected outcome, not a server fault, and
// the reason must never include the token itself.
func reject(w http.ResponseWriter, r *http.Request, code, message, reason string) {
	slog.InfoContext(r.Context(), "auth: rejected request", "reason", reason)
	httpx.WriteError(w, r, httpx.NewError(code, message))
}
