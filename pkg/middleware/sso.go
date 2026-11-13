package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
)

var (
	ErrInvalidToken        = errors.New("invalid or expired token")
	ErrForbidden           = errors.New("account not allowed")
	ErrVerifierUnavailable = errors.New("authentication service unavailable")
)

// Principal is what a successful verification leaves in the request context.
// Profile is the raw JSON Central returned; later phases can decode it once
// we need identity (e.g. roles).
type Principal struct {
	UserID  string          // Central's user id, taken from GET /api/v1/auth/me
	Profile json.RawMessage // raw /me response
}

// TokenVerifier decides whether a bearer token is acceptable. It must return
// one of the Err* values above (wrapped is fine) for expected failures.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (*Principal, error)
}

const localsPrincipalKey = "sso.principal"

// PrincipalFrom returns the verified principal for the current request.
func PrincipalFrom(c *fiber.Ctx) (*Principal, bool) {
	p, ok := c.Locals(localsPrincipalKey).(*Principal)
	return p, ok && p != nil
}

// SetPrincipal stores p on the request. Used by SSOAuth; tests use it too.
func SetPrincipal(c *fiber.Ctx, p *Principal) { c.Locals(localsPrincipalKey, p) }

// SSOAuth protects mutation requests (everything except GET, HEAD, OPTIONS)
// by requiring a valid "Authorization: Bearer <token>" header.
//
// Usage (Phase 7 wiring): api.Use(middleware.SSOAuth(verifier)) before the
// routes, or pass it per route.
func SSOAuth(v TokenVerifier) fiber.Handler {
	return func(c *fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}

		token, ok := bearerToken(c.Get(fiber.HeaderAuthorization))
		if !ok {
			return unauthorized(c, "missing or malformed bearer token")
		}

		principal, err := v.Verify(c.UserContext(), token)
		switch {
		case err == nil:
			// verified, continue below
		case errors.Is(err, ErrInvalidToken):
			return unauthorized(c, ErrInvalidToken.Error())
		case errors.Is(err, ErrForbidden):
			return fiber.NewError(fiber.StatusForbidden, "your account does not have access")
		case errors.Is(err, ErrVerifierUnavailable):
			log.Printf("[sso] verifier unavailable: %v", err)
			return fiber.NewError(fiber.StatusServiceUnavailable, "authentication service unavailable")
		default:
			return err // unexpected: becomes a logged 500 via the global ErrorHandler
		}

		SetPrincipal(c, principal)
		return c.Next()
	}
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func unauthorized(c *fiber.Ctx, message string) error {
	c.Set(fiber.HeaderWWWAuthenticate, "Bearer")
	return fiber.NewError(fiber.StatusUnauthorized, message)
}
