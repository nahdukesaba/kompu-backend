package handler

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/service"
)

type UploadService interface {
	CreateUpload(ctx context.Context, req service.UploadRequest) (*service.UploadGrant, error)
}

type UploadHandler struct {
	svc UploadService
}

func NewUploadHandler(svc UploadService) *UploadHandler {
	return &UploadHandler{svc: svc}
}

// Register mounts POST /uploads/presigned-url on r. Pass the SSO middleware
// as mw so only authenticated users can request upload URLs.
func (h *UploadHandler) Register(r fiber.Router, mw ...fiber.Handler) {
	handlers := make([]fiber.Handler, 0, len(mw)+1)
	handlers = append(handlers, mw...)
	handlers = append(handlers, h.CreatePresignedURL)
	r.Post("/uploads/presigned-url", handlers...)
}

type presignRequest struct {
	Resource    string `json:"resource"`     // "jobs" | "releases"
	ContentType string `json:"content_type"` // e.g. "application/pdf"
	Size        int64  `json:"size"`         // bytes
}

type presignResponse struct {
	UploadURL string            `json:"upload_url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ObjectKey string            `json:"object_key"`
	ExpiresAt time.Time         `json:"expires_at"`
	MaxBytes  int64             `json:"max_bytes"`
}

func (h *UploadHandler) CreatePresignedURL(c *fiber.Ctx) error {
	var req presignRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	grant, err := h.svc.CreateUpload(c.UserContext(), service.UploadRequest{
		Resource:    req.Resource,
		ContentType: req.ContentType,
		Size:        req.Size,
	})
	if err != nil {
		return mapUploadError(err)
	}

	// The URL is a bearer credential: never cache it.
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(presignResponse{
		UploadURL: grant.UploadURL,
		Method:    grant.Method,
		Headers:   grant.Headers,
		ObjectKey: grant.ObjectKey,
		ExpiresAt: grant.ExpiresAt,
		MaxBytes:  grant.MaxBytes,
	})
}

func mapUploadError(err error) error {
	switch {
	case errors.Is(err, service.ErrFileTooLarge):
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, service.ErrUnsupportedContentType):
		return fiber.NewError(fiber.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, service.ErrInvalidResource), errors.Is(err, service.ErrInvalidFileSize):
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	default:
		return err // logged and returned as a generic 500 by ErrorHandler
	}
}
