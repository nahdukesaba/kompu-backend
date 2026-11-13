package handler

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/service"
)

type CaptionService interface {
	Generate(ctx context.Context, in service.CaptionInput) (*service.CaptionResult, error)
}

type CaptionHandler struct {
	svc CaptionService
}

func NewCaptionHandler(svc CaptionService) *CaptionHandler {
	return &CaptionHandler{svc: svc}
}

// Register mounts POST /releases/generate-caption. Pass the SSO middleware
// first, then a rate limiter, as mw.
func (h *CaptionHandler) Register(r fiber.Router, mw ...fiber.Handler) {
	r.Post("/releases/generate-caption", withMiddleware(mw, h.Generate)...)
}

type captionRequest struct {
	Notes    string `json:"notes"`
	Platform string `json:"platform"` // optional, defaults to instagram
	Title    string `json:"title"`    // optional
}

type captionResponse struct {
	Caption          string `json:"caption"`
	Platform         string `json:"platform"`
	GuidelineApplied bool   `json:"guideline_applied"` // true when skills.md shaped the caption
}

func (h *CaptionHandler) Generate(c *fiber.Ctx) error {
	if _, err := actorID(c); err != nil {
		return err
	}
	var req captionRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	res, err := h.svc.Generate(c.UserContext(), service.CaptionInput{
		Notes:    req.Notes,
		Platform: req.Platform,
		Title:    req.Title,
	})
	if err != nil {
		return mapCaptionError(err)
	}

	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(captionResponse{
		Caption:          res.Caption,
		Platform:         res.Platform,
		GuidelineApplied: res.GuidelineApplied,
	})
}

func mapCaptionError(err error) error {
	var ve *service.ValidationError
	switch {
	case errors.As(err, &ve):
		return fiber.NewError(fiber.StatusBadRequest, ve.Error())
	case errors.Is(err, service.ErrCaptionDisabled):
		return fiber.NewError(fiber.StatusServiceUnavailable, "caption generation is not configured")
	case errors.Is(err, service.ErrCaptionUnavailable):
		return fiber.NewError(fiber.StatusBadGateway, "caption generation failed, please try again")
	default:
		return err
	}
}
