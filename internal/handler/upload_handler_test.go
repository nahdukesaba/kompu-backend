package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/service"
)

type fakeUploadService struct {
	grant  *service.UploadGrant
	err    error
	gotReq service.UploadRequest
}

func (f *fakeUploadService) CreateUpload(_ context.Context, req service.UploadRequest) (*service.UploadGrant, error) {
	f.gotReq = req
	return f.grant, f.err
}

func newUploadTestApp(svc UploadService) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	NewUploadHandler(svc).Register(app.Group("/api/v1"))
	return app
}

func postJSON(t *testing.T, app *fiber.App, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/presigned-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	return resp
}

func TestCreatePresignedURL_Success(t *testing.T) {
	svc := &fakeUploadService{grant: &service.UploadGrant{
		UploadURL: "https://storage.test/u?sig=1",
		Method:    "PUT",
		Headers:   map[string]string{"Content-Type": "application/pdf"},
		ObjectKey: "jobs/2026/10/abc.pdf",
		ExpiresAt: time.Date(2026, 10, 27, 8, 15, 0, 0, time.UTC),
		MaxBytes:  10 << 20,
	}}

	resp := postJSON(t, newUploadTestApp(svc), `{"content_type":"application/pdf","size":2048}`)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, service.UploadRequest{ContentType: "application/pdf", Size: 2048}, svc.gotReq)

	var out presignResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, "https://storage.test/u?sig=1", out.UploadURL)
	assert.Equal(t, "PUT", out.Method)
	assert.Equal(t, "jobs/2026/10/abc.pdf", out.ObjectKey)
	assert.Equal(t, int64(10<<20), out.MaxBytes)
}

func TestCreatePresignedURL_Errors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		svcErr     error
		wantStatus int
	}{
		{"malformed JSON", `{not json`, nil, http.StatusBadRequest},
		{"file too large", `{"content_type":"application/pdf","size":99999999}`, service.ErrFileTooLarge, http.StatusRequestEntityTooLarge},
		{"unsupported type", `{"content_type":"text/html","size":10}`, service.ErrUnsupportedContentType, http.StatusUnsupportedMediaType},
		{"invalid size", `{"content_type":"image/png","size":0}`, service.ErrInvalidFileSize, http.StatusBadRequest},
		{"storage failure", `{"content_type":"image/png","size":10}`, errors.New("s3 down"), http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := postJSON(t, newUploadTestApp(&fakeUploadService{err: tc.svcErr}), tc.body)
			assert.Equal(t, tc.wantStatus, resp.StatusCode)

			var out struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
			assert.Equal(t, tc.wantStatus, out.Error.Code)
			assert.NotEmpty(t, out.Error.Message)
			assert.NotContains(t, out.Error.Message, "s3 down", "internal errors must not leak")
		})
	}
}
