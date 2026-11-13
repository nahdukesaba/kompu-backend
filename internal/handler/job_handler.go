package handler

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/model"
	"kompu-backend/internal/service"
)

type JobService interface {
	Create(ctx context.Context, actor string, in service.JobInput) (*model.Job, error)
	Get(ctx context.Context, id string) (*model.Job, error)
	Update(ctx context.Context, actor, id string, in service.JobInput) (*model.Job, error)
	Delete(ctx context.Context, actor, id string, opts service.DeleteJobOptions) (*service.DeleteJobResult, error)
	List(ctx context.Context, q service.ListJobsQuery) (*service.JobList, error)
}

// FileURLProvider creates short-lived download URLs for stored files.
// Implemented by service.StorageService.
type FileURLProvider interface {
	DownloadURL(ctx context.Context, resource service.UploadResource, key string) (string, error)
}

type JobHandler struct {
	svc   JobService
	files FileURLProvider
}

func NewJobHandler(svc JobService, files FileURLProvider) *JobHandler {
	return &JobHandler{svc: svc, files: files}
}

// Register mounts the jobs routes. GET routes are public; mw (the SSO
// middleware) is applied to POST, PUT and DELETE only.
func (h *JobHandler) Register(r fiber.Router, mw ...fiber.Handler) {
	g := r.Group("/jobs")
	g.Get("/", h.List)
	g.Get("/:id", h.Get)
	g.Post("/", withMiddleware(mw, h.Create)...)
	g.Put("/:id", withMiddleware(mw, h.Update)...)
	g.Delete("/:id", withMiddleware(mw, h.Delete)...)
}

func withMiddleware(mw []fiber.Handler, final fiber.Handler) []fiber.Handler {
	out := make([]fiber.Handler, 0, len(mw)+1)
	out = append(out, mw...)
	return append(out, final)
}

// ---------- request / response shapes ----------

type jobRequest struct {
	ActivityDate  string               `json:"activity_date"` // YYYY-MM-DD
	Title         string               `json:"title"`
	Objective     string               `json:"objective"`
	Location      string               `json:"location"`
	Participants  string               `json:"participants"`
	EvidenceFiles []model.EvidenceFile `json:"evidence_files"`
}

func (r jobRequest) toInput() service.JobInput {
	return service.JobInput{
		ActivityDate:  r.ActivityDate,
		Title:         r.Title,
		Objective:     r.Objective,
		Location:      r.Location,
		Participants:  r.Participants,
		EvidenceFiles: r.EvidenceFiles,
	}
}

type evidenceFileResponse struct {
	Key         string `json:"key"`
	Name        string `json:"name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	URL         string `json:"url"` // presigned, expires; fetch the job again for a fresh one
}

type jobResponse struct {
	ID            string                 `json:"id"`
	ActivityDate  string                 `json:"activity_date"`
	Title         string                 `json:"title"`
	Objective     string                 `json:"objective"`
	Location      string                 `json:"location"`
	Participants  string                 `json:"participants"`
	EvidenceFiles []evidenceFileResponse `json:"evidence_files"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type paginationResponse struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

type jobListResponse struct {
	Data       []jobResponse      `json:"data"`
	Pagination paginationResponse `json:"pagination"`
}

type cleanupResponse struct {
	Requested  bool `json:"requested"`
	Deleted    int  `json:"deleted"`
	KeptShared int  `json:"kept_shared"`
	Failed     int  `json:"failed"`
}

type deleteResponse struct {
	ID           string          `json:"id"`
	DeletedAt    time.Time       `json:"deleted_at"`
	CleanupFiles cleanupResponse `json:"cleanup_files"`
}

func (h *JobHandler) toResponse(ctx context.Context, j *model.Job) (jobResponse, error) {
	files := make([]evidenceFileResponse, 0, len(j.EvidenceFiles))
	for _, f := range j.EvidenceFiles {
		url, err := h.files.DownloadURL(ctx, service.UploadResourceJobs, f.Key)
		if err != nil {
			return jobResponse{}, err
		}
		files = append(files, evidenceFileResponse{
			Key:         f.Key,
			Name:        f.Name,
			ContentType: f.ContentType,
			Size:        f.Size,
			URL:         url,
		})
	}
	return jobResponse{
		ID:            j.ID,
		ActivityDate:  j.ActivityDate.Format("2006-01-02"),
		Title:         j.Title,
		Objective:     j.Objective,
		Location:      j.Location,
		Participants:  j.Participants,
		EvidenceFiles: files,
		CreatedAt:     j.CreatedAt,
		UpdatedAt:     j.UpdatedAt,
	}, nil
}

// ---------- handlers ----------

func (h *JobHandler) List(c *fiber.Ctx) error {
	res, err := h.svc.List(c.UserContext(), service.ListJobsQuery{
		Search:   c.Query("search"),
		DateFrom: c.Query("date_from"),
		DateTo:   c.Query("date_to"),
		Page:     c.QueryInt("page", 1),
		PageSize: c.QueryInt("page_size", 20),
	})
	if err != nil {
		return mapJobError(err)
	}

	data := make([]jobResponse, 0, len(res.Items))
	for i := range res.Items {
		item, err := h.toResponse(c.UserContext(), &res.Items[i])
		if err != nil {
			return err
		}
		data = append(data, item)
	}
	return c.JSON(jobListResponse{
		Data: data,
		Pagination: paginationResponse{
			Page:       res.Page,
			PageSize:   res.PageSize,
			Total:      res.Total,
			TotalPages: res.TotalPages,
		},
	})
}

func (h *JobHandler) Get(c *fiber.Ctx) error {
	job, err := h.svc.Get(c.UserContext(), c.Params("id"))
	if err != nil {
		return mapJobError(err)
	}
	out, err := h.toResponse(c.UserContext(), job)
	if err != nil {
		return err
	}
	return c.JSON(out)
}

func (h *JobHandler) Create(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var req jobRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	job, err := h.svc.Create(c.UserContext(), actor, req.toInput())
	if err != nil {
		return mapJobError(err)
	}
	out, err := h.toResponse(c.UserContext(), job)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

func (h *JobHandler) Update(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var req jobRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	job, err := h.svc.Update(c.UserContext(), actor, c.Params("id"), req.toInput())
	if err != nil {
		return mapJobError(err)
	}
	out, err := h.toResponse(c.UserContext(), job)
	if err != nil {
		return err
	}
	return c.JSON(out)
}

// Delete soft-deletes a job. ?cleanup_files=true also permanently deletes its
// files from storage (shared files are kept).
func (h *JobHandler) Delete(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}

	cleanup := false
	if v := c.Query("cleanup_files"); v != "" {
		b, perr := strconv.ParseBool(v)
		if perr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "cleanup_files must be true or false")
		}
		cleanup = b
	}

	res, err := h.svc.Delete(c.UserContext(), actor, c.Params("id"), service.DeleteJobOptions{CleanupFiles: cleanup})
	if err != nil {
		return mapJobError(err)
	}
	return c.JSON(deleteResponse{
		ID:        res.ID,
		DeletedAt: res.DeletedAt,
		CleanupFiles: cleanupResponse{
			Requested:  res.CleanupRequested,
			Deleted:    res.FilesDeleted,
			KeptShared: res.FilesKeptShared,
			Failed:     res.FilesFailed,
		},
	})
}

func mapJobError(err error) error {
	var ve *service.ValidationError
	switch {
	case errors.As(err, &ve):
		return fiber.NewError(fiber.StatusBadRequest, ve.Error())
	case errors.Is(err, service.ErrMissingActor):
		return fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	case errors.Is(err, model.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "job not found")
	default:
		return err // logged and returned as a generic 500 by ErrorHandler
	}
}
