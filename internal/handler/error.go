package handler

import (
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/service"
)

// ErrorHandler is the global Fiber error handler. Every error leaves the API
// in one consistent JSON shape:
//
//	{ "error": { "code": 404, "message": "Cannot GET /nope" } }
//
// Authentication errors add a stable "reason" (e.g. ACCOUNT_PENDING_APPROVAL).
func ErrorHandler(c *fiber.Ctx, err error) error {
	var ae *service.AuthError
	if errors.As(err, &ae) {
		return c.Status(ae.Status).JSON(fiber.Map{
			"error": fiber.Map{"code": ae.Status, "message": ae.Message, "reason": ae.Reason},
		})
	}

	code := fiber.StatusInternalServerError
	message := "internal server error"

	var fe *fiber.Error
	if errors.As(err, &fe) {
		code = fe.Code
		message = fe.Message
	}

	if code >= fiber.StatusInternalServerError {
		log.Printf("[ERROR] %s %s: %v", c.Method(), c.Path(), err)
	}

	return c.Status(code).JSON(fiber.Map{
		"error": fiber.Map{
			"code":    code,
			"message": message,
		},
	})
}
