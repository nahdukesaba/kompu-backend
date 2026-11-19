package handler

import (
	"context"
	"log"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/repository"
)

// FileLinker resolves an object key to a short-lived download link
// (repository.OneDriveStorage).
type FileLinker interface {
	DownloadURL(ctx context.Context, key string) (string, error)
}

// FileHandler serves GET /files/<key>: a 302 to the file's current OneDrive
// download link. Public like the job and release reads that list the keys;
// keys are 128-bit random, and only Kompu upload keys are accepted.
type FileHandler struct {
	files FileLinker
}

func NewFileHandler(files FileLinker) *FileHandler { return &FileHandler{files: files} }

func (h *FileHandler) Register(r fiber.Router) {
	r.Get("/files/*", h.redirect)
}

func (h *FileHandler) redirect(c *fiber.Ctx) error {
	key := c.Params("*")
	if !repository.ValidObjectKey(key) {
		return fiber.NewError(fiber.StatusNotFound, "file not found")
	}
	link, err := h.files.DownloadURL(c.UserContext(), key)
	if err != nil {
		log.Printf("[files] %s: %v", key, err)
		return fiber.NewError(fiber.StatusNotFound, "file not found")
	}
	// The link expires in about an hour: never let a cache keep it longer.
	c.Set(fiber.HeaderCacheControl, "private, max-age=300")
	return c.Redirect(link, fiber.StatusFound)
}
