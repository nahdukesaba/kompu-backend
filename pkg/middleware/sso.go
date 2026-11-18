package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Principal is the authenticated Kompu member of the current request. The
// session middleware (handler.AuthHandler.RequireSession) sets it.
type Principal struct {
	UserID string // public.app_users.id (Central user.id)
	Role   string // Kompu membership role: user | admin
}

const localsPrincipalKey = "sso.principal"

// PrincipalFrom returns the authenticated member for the current request.
func PrincipalFrom(c *fiber.Ctx) (*Principal, bool) {
	p, ok := c.Locals(localsPrincipalKey).(*Principal)
	return p, ok && p != nil
}

// SetPrincipal stores p on the request.
func SetPrincipal(c *fiber.Ctx, p *Principal) { c.Locals(localsPrincipalKey, p) }

// RequireTrustedOrigin rejects unsafe browser requests (POST, PUT, PATCH,
// DELETE) whose Origin is outside the CORS allowlist. The session cookie is
// SameSite=Lax, so this closes the cross-site form-post gap. Requests without
// Origin (server-to-server, curl) pass.
func RequireTrustedOrigin(allowedOrigins []string) fiber.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[strings.TrimSpace(o)] = true
	}
	return func(c *fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		if origin := strings.TrimSpace(c.Get(fiber.HeaderOrigin)); origin != "" && !allowed[origin] {
			return fiber.NewError(fiber.StatusForbidden, "request origin is not allowed")
		}
		return c.Next()
	}
}
