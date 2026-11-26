package handler

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"

	"kompu-backend/internal/model"
	"kompu-backend/internal/service"
)

// ItemAPI is the tracker behaviour the handler needs (service.ItemService).
type ItemAPI interface {
	List(ctx context.Context, from, to string) ([]*model.Item, error)
	Report(ctx context.Context, from, to string) ([]*model.Item, error)
	Get(ctx context.Context, id string) (*model.Item, error)
	Create(ctx context.Context, actor string, in service.ItemInput) ([]*model.Item, error)
	Update(ctx context.Context, actor, id string, in service.ItemInput) (*model.Item, error)
	Reschedule(ctx context.Context, actor, id, start, end string) (*model.Item, error)
	SetChannel(ctx context.Context, actor, id, code string, posted bool, postedOn string) (*model.Item, error)
	SetResultLink(ctx context.Context, actor, id, link string) (*model.Item, error)
	AddFiles(ctx context.Context, actor, id string, in []service.FileInput) (*model.Item, error)
	DeleteFile(ctx context.Context, actor, id, fileID string) (*model.Item, error)
	Delete(ctx context.Context, actor, id string) error
	Channels(ctx context.Context) ([]model.Channel, error)
	Holidays(ctx context.Context, from, to string) ([]model.Holiday, error)
	People(ctx context.Context) ([]string, error)
}

type ItemHandler struct {
	svc ItemAPI
}

func NewItemHandler(svc ItemAPI) *ItemHandler { return &ItemHandler{svc: svc} }

// Register mounts the tracker. Every route, reads included, needs a session:
// items hold meeting transcripts and staff names.
func (h *ItemHandler) Register(r fiber.Router, auth fiber.Handler) {
	r.Get("/channels", auth, h.channels)
	r.Get("/holidays", auth, h.holidays)
	r.Get("/people", auth, h.people)
	r.Get("/reports", auth, h.report)

	g := r.Group("/items", auth)
	g.Get("/", h.list)
	g.Post("/", h.create)
	g.Get("/:id", h.get)
	g.Put("/:id", h.update)
	g.Patch("/:id/schedule", h.schedule)
	g.Put("/:id/result-link", h.resultLink)
	g.Delete("/:id", h.remove)
	g.Put("/:id/channels/:code", h.setChannel)
	g.Delete("/:id/channels/:code", h.unsetChannel)
	g.Post("/:id/files", h.addFiles)
	g.Delete("/:id/files/:fileId", h.deleteFile)
}

func (h *ItemHandler) list(c *fiber.Ctx) error {
	items, err := h.svc.List(c.UserContext(), c.Query("from"), c.Query("to"))
	return respondItem(c, items, err)
}

func (h *ItemHandler) report(c *fiber.Ctx) error {
	items, err := h.svc.Report(c.UserContext(), c.Query("from"), c.Query("to"))
	return respondItem(c, items, err)
}

func (h *ItemHandler) channels(c *fiber.Ctx) error {
	channels, err := h.svc.Channels(c.UserContext())
	return respondItem(c, channels, err)
}

func (h *ItemHandler) holidays(c *fiber.Ctx) error {
	holidays, err := h.svc.Holidays(c.UserContext(), c.Query("from"), c.Query("to"))
	return respondItem(c, holidays, err)
}

func (h *ItemHandler) people(c *fiber.Ctx) error {
	people, err := h.svc.People(c.UserContext())
	return respondItem(c, people, err)
}

func (h *ItemHandler) get(c *fiber.Ctx) error {
	it, err := h.svc.Get(c.UserContext(), c.Params("id"))
	return respondItem(c, it, err)
}

func (h *ItemHandler) create(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var in service.ItemInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	items, err := h.svc.Create(c.UserContext(), actor, in)
	if err != nil {
		return mapItemError(err)
	}
	return c.Status(fiber.StatusCreated).JSON(items)
}

func (h *ItemHandler) update(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var in service.ItemInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	it, err := h.svc.Update(c.UserContext(), actor, c.Params("id"), in)
	return respondItem(c, it, err)
}

func (h *ItemHandler) schedule(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var in struct {
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	it, err := h.svc.Reschedule(c.UserContext(), actor, c.Params("id"), in.StartDate, in.EndDate)
	return respondItem(c, it, err)
}

func (h *ItemHandler) resultLink(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var in struct {
		ResultLink string `json:"result_link"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	it, err := h.svc.SetResultLink(c.UserContext(), actor, c.Params("id"), in.ResultLink)
	return respondItem(c, it, err)
}

func (h *ItemHandler) remove(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.UserContext(), actor, c.Params("id")); err != nil {
		return mapItemError(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *ItemHandler) setChannel(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var in struct {
		PostedOn string `json:"posted_on"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
		}
	}
	it, err := h.svc.SetChannel(c.UserContext(), actor, c.Params("id"), c.Params("code"), true, in.PostedOn)
	return respondItem(c, it, err)
}

func (h *ItemHandler) unsetChannel(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	it, err := h.svc.SetChannel(c.UserContext(), actor, c.Params("id"), c.Params("code"), false, "")
	return respondItem(c, it, err)
}

func (h *ItemHandler) addFiles(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	var in struct {
		Files []service.FileInput `json:"files"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	it, err := h.svc.AddFiles(c.UserContext(), actor, c.Params("id"), in.Files)
	return respondItem(c, it, err)
}

func (h *ItemHandler) deleteFile(c *fiber.Ctx) error {
	actor, err := actorID(c)
	if err != nil {
		return err
	}
	it, err := h.svc.DeleteFile(c.UserContext(), actor, c.Params("id"), c.Params("fileId"))
	return respondItem(c, it, err)
}

func respondItem(c *fiber.Ctx, body any, err error) error {
	if err != nil {
		return mapItemError(err)
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(body)
}

func mapItemError(err error) error {
	var ve *service.ValidationError
	switch {
	case errors.As(err, &ve):
		return fiber.NewError(fiber.StatusBadRequest, ve.Error())
	case errors.Is(err, model.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "item not found")
	default:
		return err
	}
}
