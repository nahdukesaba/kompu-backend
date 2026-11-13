package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
	"kompu-backend/internal/service"
)

const handlerReleaseID = "22222222-2222-2222-2222-222222222222"

type fakeReleaseService struct {
	rel       *model.Release
	list      *service.ReleaseList
	deletedAt time.Time
	err       error

	called   bool
	gotActor string
	gotID    string
	gotIn    service.ReleaseInput
	gotList  service.ListReleasesQuery
}

func (f *fakeReleaseService) Create(_ context.Context, actor string, in service.ReleaseInput) (*model.Release, error) {
	f.called, f.gotActor, f.gotIn = true, actor, in
	return f.rel, f.err
}
func (f *fakeReleaseService) Get(_ context.Context, id string) (*model.Release, error) {
	f.called, f.gotID = true, id
	return f.rel, f.err
}
func (f *fakeReleaseService) Update(_ context.Context, actor, id string, in service.ReleaseInput) (*model.Release, error) {
	f.called, f.gotActor, f.gotID, f.gotIn = true, actor, id, in
	return f.rel, f.err
}
func (f *fakeReleaseService) Delete(_ context.Context, actor, id string) (time.Time, error) {
	f.called, f.gotActor, f.gotID = true, actor, id
	return f.deletedAt, f.err
}
func (f *fakeReleaseService) List(_ context.Context, q service.ListReleasesQuery) (*service.ReleaseList, error) {
	f.called, f.gotList = true, q
	return f.list, f.err
}

func sampleRelease() *model.Release {
	return &model.Release{
		ID:                  handlerReleaseID,
		StartDate:           time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		Title:               "Panen raya",
		ProgressStatus:      "draft",
		SocialMediaStatus:   map[string]string{"instagram": "posted"},
		SharepointFolderURL: "https://example.sharepoint.com/f",
		Attachments:         []model.EvidenceFile{{Key: "releases/2026/10/abc.pdf", Name: "brosur.pdf"}},
		Caption:             "halo",
		CreatedAt:           time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC),
		CreatedBy:           "user-1",
		UpdatedAt:           time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC),
		UpdatedBy:           "user-1",
	}
}

func newReleaseTestApp(svc ReleaseService, files FileURLProvider, mw ...fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	NewReleaseHandler(svc, files).Register(app.Group("/api/v1"), mw...)
	return app
}

func TestReleaseHandler_Create_Success(t *testing.T) {
	svc := &fakeReleaseService{rel: sampleRelease()}
	body := `{"start_date":"2026-10-27","title":"Panen raya","progress_status":"draft",
		"social_media_status":{"instagram":"posted"},"sharepoint_folder_url":"https://example.sharepoint.com/f",
		"attachment_urls":[{"key":"releases/2026/10/abc.pdf","name":"brosur.pdf"}],"caption":"halo"}`

	resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}, asUser("user-1")), http.MethodPost, "/api/v1/releases", body)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "user-1", svc.gotActor)
	assert.Equal(t, "2026-10-27", svc.gotIn.StartDate)
	assert.Equal(t, map[string]string{"instagram": "posted"}, svc.gotIn.SocialMediaStatus)
	require.Len(t, svc.gotIn.Attachments, 1)

	out := decodeJSON[releaseResponse](t, resp)
	assert.Equal(t, handlerReleaseID, out.ID)
	assert.Equal(t, "2026-10-27", out.StartDate)
	assert.Equal(t, map[string]string{"instagram": "posted"}, out.SocialMediaStatus)
	require.Len(t, out.AttachmentURLs, 1)
	assert.Contains(t, out.AttachmentURLs[0].URL, "https://signed.test/releases/")
}

func TestReleaseHandler_ResponseDoesNotExposeUserIDs(t *testing.T) {
	svc := &fakeReleaseService{rel: sampleRelease()}
	resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}), http.MethodGet, "/api/v1/releases/"+handlerReleaseID, "")

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "created_by")
	assert.NotContains(t, string(raw), "updated_by")
	assert.NotContains(t, string(raw), "user-1")
}

func TestReleaseHandler_Mutations_RequireAuthenticatedUser(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/releases"},
		{http.MethodPut, "/api/v1/releases/" + handlerReleaseID},
		{http.MethodDelete, "/api/v1/releases/" + handlerReleaseID},
	} {
		svc := &fakeReleaseService{rel: sampleRelease()}
		resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}), tc.method, tc.path, `{}`) // no middleware
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, tc.method)
		assert.False(t, svc.called, "service must not be reached without a user: %s", tc.method)
	}
}

func TestReleaseHandler_Create_InvalidBody(t *testing.T) {
	app := newReleaseTestApp(&fakeReleaseService{}, fakeFiles{}, asUser("u"))
	resp := doJobRequest(t, app, http.MethodPost, "/api/v1/releases", `{broken`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestReleaseHandler_Get(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		svc := &fakeReleaseService{rel: sampleRelease()}
		resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}), http.MethodGet, "/api/v1/releases/"+handlerReleaseID, "")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, handlerReleaseID, svc.gotID)
	})

	t.Run("not found", func(t *testing.T) {
		svc := &fakeReleaseService{err: model.ErrNotFound}
		resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}), http.MethodGet, "/api/v1/releases/"+handlerReleaseID, "")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("signing failure is a generic 500", func(t *testing.T) {
		svc := &fakeReleaseService{rel: sampleRelease()}
		app := newReleaseTestApp(svc, fakeFiles{err: errors.New("secret signer detail")})
		resp := doJobRequest(t, app, http.MethodGet, "/api/v1/releases/"+handlerReleaseID, "")
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestReleaseHandler_Update(t *testing.T) {
	svc := &fakeReleaseService{rel: sampleRelease()}
	resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}, asUser("user-2")), http.MethodPut,
		"/api/v1/releases/"+handlerReleaseID, `{"start_date":"2026-10-27","title":"Updated","progress_status":"published"}`)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "user-2", svc.gotActor)
	assert.Equal(t, handlerReleaseID, svc.gotID)
	assert.Equal(t, "published", svc.gotIn.ProgressStatus)
}

func TestReleaseHandler_Delete(t *testing.T) {
	t.Run("soft delete returns id and time", func(t *testing.T) {
		at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
		svc := &fakeReleaseService{deletedAt: at}
		resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}, asUser("user-1")), http.MethodDelete,
			"/api/v1/releases/"+handlerReleaseID, "")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "user-1", svc.gotActor)
		out := decodeJSON[releaseDeleteResponse](t, resp)
		assert.Equal(t, handlerReleaseID, out.ID)
		assert.True(t, out.DeletedAt.Equal(at))
	})

	t.Run("not found is 404", func(t *testing.T) {
		svc := &fakeReleaseService{err: model.ErrNotFound}
		resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}, asUser("u")), http.MethodDelete,
			"/api/v1/releases/"+handlerReleaseID, "")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestReleaseHandler_ErrorMapping(t *testing.T) {
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
			svc := &fakeReleaseService{err: tc.err}
			resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}, asUser("u")), http.MethodPost, "/api/v1/releases", `{}`)
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

func TestReleaseHandler_List(t *testing.T) {
	svc := &fakeReleaseService{list: &service.ReleaseList{
		Items: []model.Release{*sampleRelease()}, Page: 2, PageSize: 10, Total: 11, TotalPages: 2,
	}}

	resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}), http.MethodGet,
		"/api/v1/releases?search=panen&status=draft&date_from=2026-10-01&date_to=2026-10-31&page=2&page_size=10", "")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, service.ListReleasesQuery{
		Search: "panen", Status: "draft", DateFrom: "2026-10-01", DateTo: "2026-10-31", Page: 2, PageSize: 10,
	}, svc.gotList)

	out := decodeJSON[releaseListResponse](t, resp)
	require.Len(t, out.Data, 1)
	assert.Equal(t, paginationResponse{Page: 2, PageSize: 10, Total: 11, TotalPages: 2}, out.Pagination)
}

func TestReleaseHandler_List_EmptyIsArrayNotNull(t *testing.T) {
	svc := &fakeReleaseService{list: &service.ReleaseList{Page: 1, PageSize: 20}}
	resp := doJobRequest(t, newReleaseTestApp(svc, fakeFiles{}), http.MethodGet, "/api/v1/releases", "")

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"data":[]`)
}

func TestReleaseHandler_MiddlewareOnlyGuardsMutations(t *testing.T) {
	deny := func(c *fiber.Ctx) error { return fiber.NewError(fiber.StatusUnauthorized, "denied") }
	svc := &fakeReleaseService{rel: sampleRelease(), list: &service.ReleaseList{}}
	app := newReleaseTestApp(svc, fakeFiles{}, deny)

	assert.Equal(t, http.StatusOK, doJobRequest(t, app, http.MethodGet, "/api/v1/releases", "").StatusCode)
	assert.Equal(t, http.StatusOK, doJobRequest(t, app, http.MethodGet, "/api/v1/releases/"+handlerReleaseID, "").StatusCode)
	assert.Equal(t, http.StatusUnauthorized, doJobRequest(t, app, http.MethodPost, "/api/v1/releases", `{}`).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, doJobRequest(t, app, http.MethodPut, "/api/v1/releases/"+handlerReleaseID, `{}`).StatusCode)
	assert.Equal(t, http.StatusUnauthorized, doJobRequest(t, app, http.MethodDelete, "/api/v1/releases/"+handlerReleaseID, "").StatusCode)
}
