package handler

import (
	"context"
	"io"
	"log"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/model"
)

// FileLinker resolves an object key to its content in OneDrive
// (repository.OneDriveStorage).
type FileLinker interface {
	DownloadURL(ctx context.Context, key string) (string, error)
	Thumbnail(ctx context.Context, key string) (io.ReadCloser, string, error)
}

// FileHandler serves GET /files/<key> behind the session: a 302 to the file's
// current OneDrive link, or with ?thumb=1 a same-origin image for PDF export.
type FileHandler struct {
	files FileLinker
}

func NewFileHandler(files FileLinker) *FileHandler { return &FileHandler{files: files} }

func (h *FileHandler) Register(r fiber.Router, mw ...fiber.Handler) {
	r.Get("/files/*", withMiddleware(mw, h.serve)...)
}

func (h *FileHandler) serve(c *fiber.Ctx) error {
	key := c.Params("*")
	if !model.ValidObjectKey(key) {
		return fiber.NewError(fiber.StatusNotFound, "file not found")
	}
	if c.Query("thumb") == "1" {
		body, contentType, err := h.files.Thumbnail(c.UserContext(), key)
		if err != nil {
			log.Printf("[files] thumbnail %s: %v", key, err)
			return fiber.NewError(fiber.StatusNotFound, "file not found")
		}
		c.Set(fiber.HeaderContentType, contentType)
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set(fiber.HeaderCacheControl, "private, max-age=3600")
		return c.SendStream(body)
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
