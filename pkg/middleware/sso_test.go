package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireTrustedOrigin(t *testing.T) {
	app := fiber.New()
	app.Use(RequireTrustedOrigin([]string{"https://kompu.example"}))
	app.All("/x", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	tests := []struct {
		name, method, origin string
		want                 int
	}{
		{"allowed origin POST", fiber.MethodPost, "https://kompu.example", 204},
		{"bad origin POST", fiber.MethodPost, "https://evil.example", 403},
		{"bad origin DELETE", fiber.MethodDelete, "https://evil.example", 403},
		{"bad origin GET is a safe method", fiber.MethodGet, "https://evil.example", 204},
		{"no origin (server-to-server)", fiber.MethodPost, "", 204},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/x", nil)
			if tt.origin != "" {
				req.Header.Set(fiber.HeaderOrigin, tt.origin)
			}
			resp, err := app.Test(req)
			require.NoError(t, err)
			assert.Equal(t, tt.want, resp.StatusCode)
		})
	}
}
