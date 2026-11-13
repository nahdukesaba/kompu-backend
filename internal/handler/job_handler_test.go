package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
	"kompu-backend/internal/service"
	"kompu-backend/pkg/middleware"
)

const handlerJobID = "11111111-1111-1111-1111-111111111111"

type fakeJobService struct {
	job       *model.Job
	list      *service.JobList
	delResult *service.DeleteJobResult
	err       error

	called    bool
	gotActor  string
	gotID     string
	gotIn     service.JobInput
	gotList   service.ListJobsQuery
	gotDelOpt service.DeleteJobOptions
}

func (f *fakeJobService) Create(_ context.Context, actor string, in service.JobInput) (*model.Job, error) {
	f.called, f.gotActor, f.gotIn = true, actor, in
	return f.job, f.err
}
func (f *fakeJobService) Get(_ context.Context, id string) (*model.Job, error) {
	f.called, f.gotID = true, id
	return f.job, f.err
}
func (f *fakeJobService) Update(_ context.Context, actor, id string, in service.JobInput) (*model.Job, error) {
	f.called, f.gotActor, f.gotID, f.gotIn = true, actor, id, in
	return f.job, f.err
}
func (f *fakeJobService) Delete(_ context.Context, actor, id string, opts service.DeleteJobOptions) (*service.DeleteJobResult, error) {
	f.called, f.gotActor, f.gotID, f.gotDelOpt = true, actor, id, opts
	return f.delResult, f.err
}
func (f *fakeJobService) List(_ context.Context, q service.ListJobsQuery) (*service.JobList, error) {
	f.called, f.gotList = true, q
	return f.list, f.err
}

type fakeFiles struct{ err error }

func (f fakeFiles) DownloadURL(_ context.Context, r service.UploadResource, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "https://signed.test/" + string(r) + "/" + key + "?sig=1", nil
}

func sampleJob() *model.Job {
	return &model.Job{
		ID:            handlerJobID,
		ActivityDate:  time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		Title:         "Rice field visit",
		Objective:     "Check irrigation",
		Location:      "Deli Serdang",
		Participants:  "Farmer group A",
		EvidenceFiles: []model.EvidenceFile{{Key: "jobs/2026/10/abc.jpg", Name: "photo.jpg"}},
		CreatedAt:     time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC),
		CreatedBy:     "user-1",
		UpdatedAt:     time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC),
		UpdatedBy:     "user-1",
	}
}

// asUser stands in for the SSO middleware.
func asUser(id string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		middleware.SetPrincipal(c, &middleware.Principal{UserID: id})
		return c.Next()
	}
}

func newJobTestApp(svc JobService, files FileURLProvider, mw ...fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	NewJobHandler(svc, files).Register(app.Group("/api/v1"), mw...)
	return app
}

func doJobRequest(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
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

func TestJobHandler_Create_Success(t *testing.T) {
	svc := &fakeJobService{job: sampleJob()}
	body := `{"activity_date":"2026-10-27","title":"Rice field visit","objective":"Check irrigation",
		"location":"Deli Serdang","participants":"Farmer group A",
		"evidence_files":[{"key":"jobs/2026/10/abc.jpg","name":"photo.jpg"}]}`

	resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}, asUser("user-1")), http.MethodPost, "/api/v1/jobs", body)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "user-1", svc.gotActor)
	assert.Equal(t, "2026-10-27", svc.gotIn.ActivityDate)
	assert.Equal(t, "Rice field visit", svc.gotIn.Title)
	require.Len(t, svc.gotIn.EvidenceFiles, 1)

	out := decodeJSON[jobResponse](t, resp)
	assert.Equal(t, handlerJobID, out.ID)
	assert.Equal(t, "2026-10-27", out.ActivityDate)
	require.Len(t, out.EvidenceFiles, 1)
	assert.Equal(t, "https://signed.test/jobs/jobs/2026/10/abc.jpg?sig=1", out.EvidenceFiles[0].URL)
}

func TestJobHandler_ResponseDoesNotExposeUserIDs(t *testing.T) {
	svc := &fakeJobService{job: sampleJob()}
	resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}), http.MethodGet, "/api/v1/jobs/"+handlerJobID, "")

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "created_by")
	assert.NotContains(t, string(raw), "updated_by")
	assert.NotContains(t, string(raw), "user-1")
}

func TestJobHandler_Mutations_RequireAuthenticatedUser(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/jobs"},
		{http.MethodPut, "/api/v1/jobs/" + handlerJobID},
		{http.MethodDelete, "/api/v1/jobs/" + handlerJobID},
	} {
		svc := &fakeJobService{job: sampleJob()}
		resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}), tc.method, tc.path, `{}`) // no middleware
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, tc.method)
		assert.False(t, svc.called, "service must not be reached without a user: %s", tc.method)
	}
}

func TestJobHandler_Create_InvalidBody(t *testing.T) {
	resp := doJobRequest(t, newJobTestApp(&fakeJobService{}, fakeFiles{}, asUser("u")), http.MethodPost, "/api/v1/jobs", `{broken`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestJobHandler_Get(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		svc := &fakeJobService{job: sampleJob()}
		resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}), http.MethodGet, "/api/v1/jobs/"+handlerJobID, "")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, handlerJobID, svc.gotID)
	})

	t.Run("not found", func(t *testing.T) {
		svc := &fakeJobService{err: model.ErrNotFound}
		resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}), http.MethodGet, "/api/v1/jobs/"+handlerJobID, "")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("signing failure is a generic 500", func(t *testing.T) {
		svc := &fakeJobService{job: sampleJob()}
		app := newJobTestApp(svc, fakeFiles{err: errors.New("secret signer detail")})
		resp := doJobRequest(t, app, http.MethodGet, "/api/v1/jobs/"+handlerJobID, "")
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestJobHandler_Update(t *testing.T) {
	svc := &fakeJobService{job: sampleJob()}
	resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}, asUser("user-2")), http.MethodPut,
		"/api/v1/jobs/"+handlerJobID, `{"activity_date":"2026-10-27","title":"Updated"}`)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "user-2", svc.gotActor)
	assert.Equal(t, handlerJobID, svc.gotID)
	assert.Equal(t, "Updated", svc.gotIn.Title)
}

func TestJobHandler_Delete(t *testing.T) {
	deletedAt := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

	t.Run("soft delete only by default", func(t *testing.T) {
		svc := &fakeJobService{delResult: &service.DeleteJobResult{ID: handlerJobID, DeletedAt: deletedAt}}
		resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}, asUser("user-1")), http.MethodDelete, "/api/v1/jobs/"+handlerJobID, "")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "user-1", svc.gotActor)
		assert.Equal(t, handlerJobID, svc.gotID)
		assert.False(t, svc.gotDelOpt.CleanupFiles)

		out := decodeJSON[deleteResponse](t, resp)
		assert.Equal(t, handlerJobID, out.ID)
		assert.False(t, out.CleanupFiles.Requested)
	})

	t.Run("cleanup flag is passed through and reported", func(t *testing.T) {
		svc := &fakeJobService{delResult: &service.DeleteJobResult{
			ID: handlerJobID, DeletedAt: deletedAt, CleanupRequested: true,
			FilesDeleted: 2, FilesKeptShared: 1, FilesFailed: 0,
		}}
		resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}, asUser("user-1")), http.MethodDelete,
			"/api/v1/jobs/"+handlerJobID+"?cleanup_files=true", "")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, svc.gotDelOpt.CleanupFiles)

		out := decodeJSON[deleteResponse](t, resp)
		assert.Equal(t, cleanupResponse{Requested: true, Deleted: 2, KeptShared: 1, Failed: 0}, out.CleanupFiles)
	})

	t.Run("invalid flag is rejected", func(t *testing.T) {
		svc := &fakeJobService{}
		resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}, asUser("u")), http.MethodDelete,
			"/api/v1/jobs/"+handlerJobID+"?cleanup_files=maybe", "")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.False(t, svc.called)
	})

	t.Run("not found is 404", func(t *testing.T) {
		svc := &fakeJobService{err: model.ErrNotFound}
		resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}, asUser("u")), http.MethodDelete, "/api/v1/jobs/"+handlerJobID, "")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestJobHandler_ErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"validation", &service.ValidationError{Problems: []string{"title is required"}}, 400, "title is required"},
		{"missing actor", service.ErrMissingActor, 401, "authentication required"},
		{"internal errors are not leaked", errors.New("pq: secret detail"), 500, "internal server error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeJobService{err: tc.err}
			resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}, asUser("u")), http.MethodPost, "/api/v1/jobs", `{}`)
			assert.Equal(t, tc.wantStatus, resp.StatusCode)

			out := decodeJSON[struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}](t, resp)
			assert.Equal(t, tc.wantStatus, out.Error.Code)
			assert.Contains(t, out.Error.Message, tc.wantMsg)
		})
	}
}

func TestJobHandler_List(t *testing.T) {
	svc := &fakeJobService{list: &service.JobList{
		Items: []model.Job{*sampleJob()}, Page: 2, PageSize: 10, Total: 11, TotalPages: 2,
	}}

	resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}), http.MethodGet,
		"/api/v1/jobs?search=rice&date_from=2026-10-01&date_to=2026-10-31&page=2&page_size=10", "")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, service.ListJobsQuery{
		Search: "rice", DateFrom: "2026-10-01", DateTo: "2026-10-31", Page: 2, PageSize: 10,
	}, svc.gotList)

	out := decodeJSON[jobListResponse](t, resp)
	require.Len(t, out.Data, 1)
	assert.Contains(t, out.Data[0].EvidenceFiles[0].URL, "https://signed.test/")
	assert.Equal(t, paginationResponse{Page: 2, PageSize: 10, Total: 11, TotalPages: 2}, out.Pagination)
}

func TestJobHandler_List_EmptyIsArrayNotNull(t *testing.T) {
	svc := &fakeJobService{list: &service.JobList{Items: nil, Page: 1, PageSize: 20}}
	resp := doJobRequest(t, newJobTestApp(svc, fakeFiles{}), http.MethodGet, "/api/v1/jobs", "")

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"data":[]`)
}

func TestJobHandler_MiddlewareOnlyGuardsMutations(t *testing.T) {
	deny := func(c *fiber.Ctx) error { return fiber.NewError(fiber.StatusUnauthorized, "denied") }
	svc := &fakeJobService{job: sampleJob(), list: &service.JobList{}}
	app := newJobTestApp(svc, fakeFiles{}, deny)

	assert.Equal(t, http.StatusOK, doJobRequest(t, app, http.MethodGet, "/api/v1/jobs", "").StatusCode)
	assert.Equal(t, http.StatusOK, doJobRequest(t, app, http.MethodGet, "/api/v1/jobs/"+handlerJobID, "").StatusCode)
	assert.Equal(t, http.StatusUnauthorized, doJobRequest(t, app, http.MethodPost, "/api/v1/jobs", `{}`).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, doJobRequest(t, app, http.MethodPut, "/api/v1/jobs/"+handlerJobID, `{}`).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, doJobRequest(t, app, http.MethodDelete, "/api/v1/jobs/"+handlerJobID, "").StatusCode)
}
