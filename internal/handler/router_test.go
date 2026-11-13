package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/service"
)

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

func newRouterTestApp(auth, rateLimit fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	RegisterRoutes(app, Routes{
		Health:   NewHealthHandler("kompu-backend", "test", okPinger{}),
		Uploads:  NewUploadHandler(&fakeUploadService{grant: &service.UploadGrant{}}),
		Jobs:     NewJobHandler(&fakeJobService{job: sampleJob(), list: &service.JobList{}, delResult: &service.DeleteJobResult{}}, fakeFiles{}),
		Releases: NewReleaseHandler(&fakeReleaseService{rel: sampleRelease(), list: &service.ReleaseList{}}, fakeFiles{}),
		Captions: NewCaptionHandler(&fakeCaptionService{res: &service.CaptionResult{}}),
	}, auth, rateLimit)
	return app
}

func passThrough(c *fiber.Ctx) error { return c.Next() }

func denyAll(c *fiber.Ctx) error { return fiber.NewError(fiber.StatusUnauthorized, "denied") }

// This table is the API contract: reads are public, every write needs auth.
func TestRoutes_ReadsArePublic(t *testing.T) {
	app := newRouterTestApp(denyAll, passThrough)

	for _, path := range []string{
		"/api/v1/health",
		"/api/v1/jobs",
		"/api/v1/jobs/" + handlerJobID,
		"/api/v1/releases",
		"/api/v1/releases/" + handlerReleaseID,
	} {
		resp := doJobRequest(t, app, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "GET %s", path)
	}
}

func TestRoutes_EveryWriteNeedsAuth(t *testing.T) {
	app := newRouterTestApp(denyAll, passThrough)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/uploads/presigned-url"},
		{http.MethodPost, "/api/v1/jobs"},
		{http.MethodPut, "/api/v1/jobs/" + handlerJobID},
		{http.MethodDelete, "/api/v1/jobs/" + handlerJobID},
		{http.MethodPost, "/api/v1/releases"},
		{http.MethodPut, "/api/v1/releases/" + handlerReleaseID},
		{http.MethodDelete, "/api/v1/releases/" + handlerReleaseID},
		{http.MethodPost, "/api/v1/releases/generate-caption"},
	} {
		resp := doJobRequest(t, app, tc.method, tc.path, `{}`)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "%s %s must require auth", tc.method, tc.path)
	}
}

func TestRoutes_CaptionRateLimitRunsAfterAuth(t *testing.T) {
	limiterCalls := 0
	limit := func(c *fiber.Ctx) error {
		limiterCalls++
		return c.Next()
	}

	// rejected callers must not consume the rate limit
	resp := doJobRequest(t, newRouterTestApp(denyAll, limit), http.MethodPost, captionURL, `{"notes":"x"}`)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Zero(t, limiterCalls)

	resp = doJobRequest(t, newRouterTestApp(asUser("user-1"), limit), http.MethodPost, captionURL, `{"notes":"x"}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, limiterCalls)
}

func TestRoutes_UnknownRouteUsesTheStandardErrorShape(t *testing.T) {
	resp := doJobRequest(t, newRouterTestApp(passThrough, passThrough), http.MethodGet, "/api/v1/nope", "")
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
