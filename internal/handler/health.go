package handler

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Pinger is satisfied by *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

type HealthHandler struct {
	service string
	env     string
	db      Pinger
}

func NewHealthHandler(service, env string, db Pinger) *HealthHandler {
	return &HealthHandler{service: service, env: env, db: db}
}

// Register mounts the health route on the given router group.
func (h *HealthHandler) Register(r fiber.Router) {
	r.Get("/health", h.Check)
}

func (h *HealthHandler) Check(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
	defer cancel()

	status, dbStatus, code := "ok", "ok", fiber.StatusOK
	if err := h.db.Ping(ctx); err != nil {
		status, dbStatus, code = "degraded", "unavailable", fiber.StatusServiceUnavailable
	}

	return c.Status(code).JSON(fiber.Map{
		"status":      status,
		"database":    dbStatus,
		"service":     h.service,
		"environment": h.env,
		"time":        time.Now().UTC().Format(time.RFC3339),
	})
}
