package handler

import (
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
)

// ErrorHandler is the global Fiber error handler. Every error leaves the API
// in one consistent JSON shape:
//
//	{ "error": { "code": 404, "message": "Cannot GET /nope" } }
func ErrorHandler(c *fiber.Ctx, err error) error {
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
