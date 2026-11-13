package handler

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/model"
	"kompu-backend/internal/service"
)

type ReleaseService interface {
	Create(ctx context.Context, actor string, in service.ReleaseInput) (*model.Release, error)
	Get(ctx context.Context, id string) (*model.Release, error)
	Update(ctx context.Context, actor, id string, in service.ReleaseInput) (*model.Release, error)
	Delete(ctx context.Context, actor, id string) (time.Time, error)
	List(ctx context.Context, q service.ListReleasesQuery) (*service.ReleaseList, error)
}

type ReleaseHandler struct {
	svc   ReleaseService
	files FileURLProvider
}

func NewReleaseHandler(svc ReleaseService, files FileURLProvider) *ReleaseHandler {
	return &ReleaseHandler{svc: svc, files: files}
}

// Register mounts the releases routes. GET routes are public; mw (the SSO
// middleware) is applied to POST, PUT and DELETE only.
func (h *ReleaseHandler) Register(r fiber.Router, mw ...fiber.Handler) {
	g := r.Group("/releases")
	g.Get("/", h.List)
	g.Get("/:id", h.Get)
	g.Post("/", withMiddleware(mw, h.Create)...)
	g.Put("/:id", withMiddleware(mw, h.Update)...)
	g.Delete("/:id", withMiddleware(mw, h.Delete)...)
}

// ---------- request / response shapes ----------

type releaseRequest struct {
	StartDate           string               `json:"start_date"` // YYYY-MM-DD
	Title               string               `json:"title"`
	ProgressStatus      string               `json:"progress_status"`
	SocialMediaStatus   map[string]string    `json:"social_media_status"`
	SharepointFolderURL string               `json:"sharepoint_folder_url"`
	AttachmentURLs      []model.EvidenceFile `json:"attachment_urls"` // object keys of uploaded files
	Caption             string               `json:"caption"`
}

func (r releaseRequest) toInput() service.ReleaseInput {
	return service.ReleaseInput{
		StartDate:           r.StartDate,
		Title:               r.Title,
		ProgressStatus:      r.ProgressStatus,
		SocialMediaStatus:   r.SocialMediaStatus,
		SharepointFolderURL: r.SharepointFolderURL,
		Attachments:         r.AttachmentURLs,
		Caption:             r.Caption,
	}
}

type releaseResponse struct {
	ID                  string                 `json:"id"`
	StartDate           string                 `json:"start_date"`
	Title               string                 `json:"title"`
	ProgressStatus      string                 `json:"progress_status"`
	SocialMediaStatus   map[string]string      `json:"social_media_status"`
	SharepointFolderURL string                 `json:"sharepoint_folder_url"`
	AttachmentURLs      []evidenceFileResponse `json:"attachment_urls"` // each with a fresh signed url
	Caption             string                 `json:"caption"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
}

type releaseListResponse struct {
	Data       []releaseResponse  `json:"data"`
	Pagination paginationResponse `json:"pagination"`
}

type releaseDeleteResponse struct {
	ID        string    `json:"id"`
	DeletedAt time.Time `json:"deleted_at"`
}

func (h *ReleaseHandler) toResponse(ctx context.Context, r *model.Release) (releaseResponse, error) {
	files := make([]evidenceFileResponse, 0, len(r.Attachments))
	for _, f := range r.Attachments {
		url, err := h.files.DownloadURL(ctx, service.UploadResourceReleases, f.Key)
		if err != nil {
			return releaseResponse{}, err
		}
		files = append(files, evidenceFileResponse{
			Key:         f.Key,
			Name:        f.Name,
			ContentType: f.ContentType,
			Size:        f.Size,
			URL:         url,
		})
	}

	social := r.SocialMediaStatus
	if social == nil {
		social = map[string]string{}
	}
	return releaseResponse{
		ID:                  r.ID,
		StartDate:           r.StartDate.Format("2006-01-02"),
		Title:               r.Title,
		ProgressStatus:      r.ProgressStatus,
		SocialMediaStatus:   social,
		SharepointFolderURL: r.SharepointFolderURL,
		AttachmentURLs:      files,
		Caption:             r.Caption,
		CreatedAt:           r.CreatedAt,
		UpdatedAt:           r.UpdatedAt,
	}, nil
}

// ---------- handlers ----------

func (h *ReleaseHandler) List(c *fiber.Ctx) error {
	res, err := h.svc.List(c.UserContext(), service.ListReleasesQuery{
		Search:   c.Query("search"),
		Status:   c.Query("status"),
		DateFrom: c.Query("date_from"),
		DateTo:   c.Query("date_to"),
		Page:     c.QueryInt("page", 1),
		PageSize: c.QueryInt("page_size", 20),
	})
	if err != nil {
		return mapReleaseError(err)
	}

	data := make([]releaseResponse, 0, len(res.Items))
	for i := range res.Items {
		item, err := h.toResponse(c.UserContext(), &res.Items[i])
		if err != nil {
			return err
		}
		data = append(data, item)
	}
	return c.JSON(releaseListResponse{
		Data: data,
		Pagination: paginationResponse{
			Page:       res.Page,
			PageSize:   res.PageSize,
			Total:      res.Total,
			TotalPages: res.TotalPages,
		},
	})
}

func (h *ReleaseHandler) Get(c *fiber.Ctx) error {
	rel, err := h.svc.Get(c.UserContext(), c.Params("id"))
	if err != nil {
		return mapReleaseError(err)
	}
	out, err := h.toResponse(c.UserContext(), rel)
	if err != nil {
		return err
	}
	return c.JSON(out)
}

func (h *ReleaseHandler) Create(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var req releaseRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	rel, err := h.svc.Create(c.UserContext(), actor, req.toInput())
	if err != nil {
		return mapReleaseError(err)
	}
	out, err := h.toResponse(c.UserContext(), rel)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

func (h *ReleaseHandler) Update(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var req releaseRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	rel, err := h.svc.Update(c.UserContext(), actor, c.Params("id"), req.toInput())
	if err != nil {
		return mapReleaseError(err)
	}
	out, err := h.toResponse(c.UserContext(), rel)
	if err != nil {
		return err
	}
	return c.JSON(out)
}

// Delete soft-deletes a release. Its attachments stay in storage.
func (h *ReleaseHandler) Delete(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	id := c.Params("id")
	deletedAt, err := h.svc.Delete(c.UserContext(), actor, id)
	if err != nil {
		return mapReleaseError(err)
	}
	return c.JSON(releaseDeleteResponse{ID: id, DeletedAt: deletedAt})
}

func mapReleaseError(err error) error {
	var ve *service.ValidationError
	switch {
	case errors.As(err, &ve):
		return fiber.NewError(fiber.StatusBadRequest, ve.Error())
	case errors.Is(err, service.ErrMissingActor):
		return fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	case errors.Is(err, model.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "release not found")
	default:
		return err // logged and returned as a generic 500 by ErrorHandler
	}
}
