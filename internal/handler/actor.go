package handler

import (
	"github.com/gofiber/fiber/v2"

	"kompu-backend/pkg/middleware"
)

// actorID returns the authenticated user's id. Mutation routes must run
// behind the SSO middleware; if it is missing we fail closed with 401.
func actorID(c *fiber.Ctx) (string, error) {
	p, ok := middleware.PrincipalFrom(c)
	if !ok || p.UserID == "" {
		return "", fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	}
	return p.UserID, nil
}
