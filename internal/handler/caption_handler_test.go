package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/service"
)

type fakeCaptionService struct {
	res    *service.CaptionResult
	err    error
	called bool
	gotIn  service.CaptionInput
}

func (f *fakeCaptionService) Generate(_ context.Context, in service.CaptionInput) (*service.CaptionResult, error) {
	f.called, f.gotIn = true, in
	return f.res, f.err
}

func (f *fakeCaptionService) GenerateObjective(_ context.Context, in service.CaptionInput) (*service.CaptionResult, error) {
	f.called, f.gotIn = true, in
	return f.res, f.err
}

func newCaptionTestApp(svc CaptionService, mw ...fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	NewCaptionHandler(svc).Register(app.Group("/api/v1"), mw...)
	return app
}

const captionURL = "/api/v1/captions/generate"

func TestCaptionHandler_Success(t *testing.T) {
	svc := &fakeCaptionService{res: &service.CaptionResult{Caption: "Halo petani!", Platform: "instagram", GuidelineApplied: true}}

	resp := doRequest(t, newCaptionTestApp(svc, asUser("user-1")), http.MethodPost, captionURL,
		`{"notes":"Penyuluhan di Deli Serdang","platform":"instagram","title":"Jajar legowo"}`)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, service.CaptionInput{
		Notes: "Penyuluhan di Deli Serdang", Platform: "instagram", Title: "Jajar legowo",
	}, svc.gotIn)

	out := decodeJSON[captionResponse](t, resp)
	assert.Equal(t, captionResponse{Caption: "Halo petani!", Platform: "instagram", GuidelineApplied: true}, out)
}

func TestCaptionHandler_RequiresAuthenticatedUser(t *testing.T) {
	svc := &fakeCaptionService{}
	resp := doRequest(t, newCaptionTestApp(svc), http.MethodPost, captionURL, `{"notes":"x"}`) // no middleware
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.False(t, svc.called)
}

func TestCaptionHandler_InvalidBody(t *testing.T) {
	resp := doRequest(t, newCaptionTestApp(&fakeCaptionService{}, asUser("u")), http.MethodPost, captionURL, `{broken`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestCaptionHandler_ErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"validation", &service.ValidationError{Problems: []string{"notes is required"}}, 400, "notes is required"},
		{"feature switched off", service.ErrCaptionDisabled, 503, "not configured"},
		{"provider failure", service.ErrCaptionUnavailable, 502, "caption generation failed"},
		{"unexpected", errors.New("boom with secret detail"), 500, "internal server error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeCaptionService{err: tc.err}
			resp := doRequest(t, newCaptionTestApp(svc, asUser("u")), http.MethodPost, captionURL, `{"notes":"x"}`)
			assert.Equal(t, tc.wantStatus, resp.StatusCode)

			out := decodeJSON[struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}](t, resp)
			require.Equal(t, tc.wantStatus, out.Error.Code)
			assert.Contains(t, out.Error.Message, tc.wantMsg)
			assert.NotContains(t, out.Error.Message, "secret")
		})
	}
}
