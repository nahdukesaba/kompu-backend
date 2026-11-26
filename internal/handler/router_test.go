package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
	"kompu-backend/internal/service"
	"kompu-backend/pkg/middleware"
)

const testItemID = "11111111-1111-4111-8111-111111111111"

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

type fakeItems struct {
	ItemAPI
	gotFrom, gotTo string
}

func (f *fakeItems) List(_ context.Context, from, to string) ([]*model.Item, error) {
	f.gotFrom, f.gotTo = from, to
	return []*model.Item{{ID: testItemID, Title: "Rapat"}}, nil
}

func (f *fakeItems) Delete(context.Context, string, string) error { return model.ErrNotFound }

func (f *fakeItems) Create(context.Context, string, service.ItemInput) ([]*model.Item, error) {
	return nil, &service.ValidationError{Problems: []string{"title is required"}}
}

type fakeLinker struct{}

func (fakeLinker) DownloadURL(context.Context, string) (string, error) {
	return "https://dl.example/file", nil
}

func (fakeLinker) Thumbnail(context.Context, string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("img")), "image/jpeg", nil
}

func newRouterTestApp(auth, rateLimit fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	RegisterRoutes(app, Routes{
		Health:   NewHealthHandler("kompu-backend", "test", okPinger{}),
		Uploads:  NewUploadHandler(&fakeUploadService{grant: &service.UploadGrant{}}),
		Items:    NewItemHandler(&fakeItems{}),
		Captions: NewCaptionHandler(&fakeCaptionService{res: &service.CaptionResult{}}),
		Files:    NewFileHandler(fakeLinker{}),
	}, auth, rateLimit)
	return app
}

func passThrough(c *fiber.Ctx) error { return c.Next() }

func denyAll(c *fiber.Ctx) error { return fiber.NewError(fiber.StatusUnauthorized, "denied") }

func asUser(id string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		middleware.SetPrincipal(c, &middleware.Principal{UserID: id})
		return c.Next()
	}
}

func doRequest(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	return resp
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var out T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func TestRoutes_HealthIsPublic(t *testing.T) {
	resp := doRequest(t, newRouterTestApp(denyAll, passThrough), http.MethodGet, "/api/v1/health", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// This table is the API contract: everything except health needs a session.
func TestRoutes_EverythingElseNeedsAuth(t *testing.T) {
	app := newRouterTestApp(denyAll, passThrough)
	item := "/api/v1/items/" + testItemID

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/items?from=2026-11-01&to=2026-11-30"},
		{http.MethodPost, "/api/v1/items"},
		{http.MethodGet, item},
		{http.MethodPut, item},
		{http.MethodDelete, item},
		{http.MethodPatch, item + "/schedule"},
		{http.MethodPut, item + "/result-link"},
		{http.MethodPut, item + "/channels/instagram"},
		{http.MethodDelete, item + "/channels/instagram"},
		{http.MethodPost, item + "/files"},
		{http.MethodDelete, item + "/files/" + testItemID},
		{http.MethodGet, "/api/v1/channels"},
		{http.MethodGet, "/api/v1/holidays?from=2026-11-01&to=2026-11-30"},
		{http.MethodGet, "/api/v1/people"},
		{http.MethodGet, "/api/v1/reports?from=2026-07-01&to=2026-09-30"},
		{http.MethodGet, "/api/v1/files/items/2026/11/0123456789abcdef0123456789abcdef.jpg"},
		{http.MethodPost, "/api/v1/uploads/presigned-url"},
		{http.MethodPost, "/api/v1/captions/generate"},
		{http.MethodPost, "/api/v1/captions/objective"},
	} {
		resp := doRequest(t, app, tc.method, tc.path, `{}`)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "%s %s must require auth", tc.method, tc.path)
	}
}

func TestRoutes_ItemsListAndErrors(t *testing.T) {
	app := newRouterTestApp(asUser("user-1"), passThrough)

	resp := doRequest(t, app, http.MethodGet, "/api/v1/items?from=2026-11-01&to=2026-11-30", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	items := decodeJSON[[]model.Item](t, resp)
	assert.Equal(t, "Rapat", items[0].Title)

	resp = doRequest(t, app, http.MethodPost, "/api/v1/items", `{"title":""}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp = doRequest(t, app, http.MethodDelete, "/api/v1/items/"+testItemID, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRoutes_Files(t *testing.T) {
	app := newRouterTestApp(asUser("user-1"), passThrough)
	key := "/api/v1/files/items/2026/11/0123456789abcdef0123456789abcdef.jpg"

	resp := doRequest(t, app, http.MethodGet, key, "")
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "https://dl.example/file", resp.Header.Get("Location"))

	resp = doRequest(t, app, http.MethodGet, key+"?thumb=1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/jpeg", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))

	resp = doRequest(t, app, http.MethodGet, "/api/v1/files/secret/passwords.jpg", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRoutes_CaptionRateLimitRunsAfterAuth(t *testing.T) {
	limiterCalls := 0
	limit := func(c *fiber.Ctx) error {
		limiterCalls++
		return c.Next()
	}

	// rejected callers must not consume the rate limit
	resp := doRequest(t, newRouterTestApp(denyAll, limit), http.MethodPost, captionURL, `{"notes":"x"}`)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Zero(t, limiterCalls)

	resp = doRequest(t, newRouterTestApp(asUser("user-1"), limit), http.MethodPost, captionURL, `{"notes":"x"}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, limiterCalls)
}

func TestRoutes_UnknownRouteUsesTheStandardErrorShape(t *testing.T) {
	resp := doRequest(t, newRouterTestApp(passThrough, passThrough), http.MethodGet, "/api/v1/nope", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	out := decodeJSON[struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, resp)
	assert.Equal(t, 404, out.Error.Code)
	assert.NotEmpty(t, out.Error.Message)
}
