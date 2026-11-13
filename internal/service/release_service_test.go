package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
)

const (
	testReleaseID  = "22222222-2222-2222-2222-222222222222"
	testReleaseKey = "releases/2026/10/0123456789abcdef0123456789abcdef.pdf"
)

type fakeReleaseRepo struct {
	calls     int
	gotRel    *model.Release
	gotID     string
	gotActor  string
	gotFilter model.ReleaseFilter

	err       error
	getResult *model.Release
	deletedAt time.Time
	listItems []model.Release
	listTotal int
}

func (f *fakeReleaseRepo) Create(_ context.Context, r *model.Release) (*model.Release, error) {
	f.calls++
	f.gotRel = r
	if f.err != nil {
		return nil, f.err
	}
	out := *r
	out.ID = testReleaseID
	return &out, nil
}

func (f *fakeReleaseRepo) GetByID(_ context.Context, id string) (*model.Release, error) {
	f.calls++
	f.gotID = id
	if f.err != nil {
		return nil, f.err
	}
	return f.getResult, nil
}

func (f *fakeReleaseRepo) Update(_ context.Context, r *model.Release) (*model.Release, error) {
	f.calls++
	f.gotRel = r
	if f.err != nil {
		return nil, f.err
	}
	return r, nil
}

func (f *fakeReleaseRepo) SoftDelete(_ context.Context, id, actor string) (time.Time, error) {
	f.calls++
	f.gotID, f.gotActor = id, actor
	if f.err != nil {
		return time.Time{}, f.err
	}
	return f.deletedAt, nil
}

func (f *fakeReleaseRepo) List(_ context.Context, filter model.ReleaseFilter) ([]model.Release, int, error) {
	f.calls++
	f.gotFilter = filter
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.listItems, f.listTotal, nil
}

// socialWith returns every platform as not_posted, with the given overrides.
func socialWith(overrides map[string]string) map[string]string {
	out := make(map[string]string, len(socialPlatforms))
	for platform := range socialPlatforms {
		out[platform] = "not_posted"
	}
	for platform, status := range overrides {
		out[platform] = status
	}
	return out
}

func validReleaseInput() ReleaseInput {
	return ReleaseInput{
		StartDate:           "2026-10-27",
		Title:               "Panen raya",
		ProgressStatus:      "not_started",
		SocialMediaStatus:   map[string]string{"instagram": "not_posted"},
		SharepointFolderURL: "https://example.sharepoint.com/sites/kompu/folder",
		Attachments:         []model.EvidenceFile{{Key: testReleaseKey, Name: "brosur.pdf"}},
		Caption:             "Caption awal",
	}
}

func TestReleaseService_Create_NormalisesInput(t *testing.T) {
	repo := &fakeReleaseRepo{}
	in := ReleaseInput{
		StartDate:         "2026-10-27",
		Title:             "  Panen raya  ",
		ProgressStatus:    "  NOT_STARTED ",
		SocialMediaStatus: map[string]string{" Instagram ": " POSTED "},
		Caption:           "  halo  ",
	}

	rel, err := NewReleaseService(repo).Create(context.Background(), testActor, in)

	require.NoError(t, err)
	assert.Equal(t, testReleaseID, rel.ID)
	got := repo.gotRel
	assert.Equal(t, "Panen raya", got.Title)
	assert.Equal(t, "not_started", got.ProgressStatus)
	assert.Equal(t, socialWith(map[string]string{"instagram": "posted"}), got.SocialMediaStatus,
		"instagram is posted; every other platform defaults to not_posted")
	assert.Equal(t, "halo", got.Caption)
	assert.Equal(t, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), got.StartDate)
	assert.Equal(t, testActor, got.CreatedBy)
	assert.Equal(t, testActor, got.UpdatedBy)
	assert.Empty(t, got.SharepointFolderURL)
	assert.NotNil(t, got.Attachments, "must be an empty slice, never nil (stored as JSON [])")
	assert.Empty(t, got.Attachments)
}

func TestReleaseService_Create_MissingPlatformsDefaultToNotPosted(t *testing.T) {
	repo := &fakeReleaseRepo{}
	in := validReleaseInput()
	in.SocialMediaStatus = nil

	_, err := NewReleaseService(repo).Create(context.Background(), testActor, in)

	require.NoError(t, err)
	assert.Equal(t, socialWith(nil), repo.gotRel.SocialMediaStatus)
}

func TestReleaseService_Create_RequiresActor(t *testing.T) {
	for _, actor := range []string{"", "  "} {
		repo := &fakeReleaseRepo{}
		_, err := NewReleaseService(repo).Create(context.Background(), actor, validReleaseInput())
		assert.ErrorIs(t, err, ErrMissingActor)
		assert.Zero(t, repo.calls)
	}
}

func TestReleaseService_Create_ValidationErrors(t *testing.T) {
	tooMany := make([]model.EvidenceFile, 21)
	for i := range tooMany {
		tooMany[i] = model.EvidenceFile{Key: testReleaseKey}
	}

	tests := []struct {
		name   string
		mutate func(*ReleaseInput)
		want   string
	}{
		{"missing title", func(in *ReleaseInput) { in.Title = "" }, "title is required"},
		{"title too long", func(in *ReleaseInput) { in.Title = strings.Repeat("a", 201) }, "title must be at most 200"},
		{"missing date", func(in *ReleaseInput) { in.StartDate = "" }, "start_date"},
		{"impossible date", func(in *ReleaseInput) { in.StartDate = "2026-02-30" }, "start_date"},
		{"unknown status", func(in *ReleaseInput) { in.ProgressStatus = "finished" }, "progress_status must be one of"},
		{"empty status", func(in *ReleaseInput) { in.ProgressStatus = "" }, "progress_status must be one of"},
		{"unknown platform", func(in *ReleaseInput) { in.SocialMediaStatus = map[string]string{"myspace": "posted"} }, "unknown platform"},
		{"unknown post status", func(in *ReleaseInput) { in.SocialMediaStatus = map[string]string{"instagram": "maybe"} }, "social_media_status.instagram"},
		{"ftp sharepoint url", func(in *ReleaseInput) { in.SharepointFolderURL = "ftp://files.example.com/x" }, "sharepoint_folder_url"},
		{"sharepoint url is not a url", func(in *ReleaseInput) { in.SharepointFolderURL = "just some text" }, "sharepoint_folder_url"},
		{"script sharepoint url", func(in *ReleaseInput) { in.SharepointFolderURL = "javascript:alert(1)" }, "sharepoint_folder_url"},
		{"caption too long", func(in *ReleaseInput) { in.Caption = strings.Repeat("a", 5001) }, "caption must be at most 5000"},
		{"attachment from the jobs bucket", func(in *ReleaseInput) {
			in.Attachments = []model.EvidenceFile{{Key: "jobs/2026/10/0123456789abcdef0123456789abcdef.pdf"}}
		}, "attachment_urls[0].key"},
		{"too many attachments", func(in *ReleaseInput) { in.Attachments = tooMany }, "at most 20 files"},
		{"old status name is rejected", func(in *ReleaseInput) { in.ProgressStatus = "draft" }, "progress_status must be one of"},
		{"scheduled is not a post status", func(in *ReleaseInput) { in.SocialMediaStatus = map[string]string{"instagram": "scheduled"} }, "social_media_status.instagram"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeReleaseRepo{}
			in := validReleaseInput()
			tc.mutate(&in)

			rel, err := NewReleaseService(repo).Create(context.Background(), testActor, in)

			var ve *ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Contains(t, ve.Error(), tc.want)
			assert.Nil(t, rel)
			assert.Zero(t, repo.calls)
		})
	}
}

func TestReleaseService_Get(t *testing.T) {
	t.Run("found, with every platform present", func(t *testing.T) {
		svc := NewReleaseService(&fakeReleaseRepo{getResult: &model.Release{
			ID: testReleaseID, Title: "x", SocialMediaStatus: map[string]string{"instagram": "posted"},
		}})
		rel, err := svc.Get(context.Background(), testReleaseID)
		require.NoError(t, err)
		assert.Equal(t, "x", rel.Title)
		assert.Equal(t, socialWith(map[string]string{"instagram": "posted"}), rel.SocialMediaStatus)
	})

	t.Run("malformed id never reaches the database", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		_, err := NewReleaseService(repo).Get(context.Background(), "nope")
		assert.ErrorIs(t, err, model.ErrNotFound)
		assert.Zero(t, repo.calls)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := NewReleaseService(&fakeReleaseRepo{err: model.ErrNotFound}).Get(context.Background(), testReleaseID)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

func TestReleaseService_Update(t *testing.T) {
	t.Run("success records the updater and uses the id from the URL", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		rel, err := NewReleaseService(repo).Update(context.Background(), "user-2", testReleaseID, validReleaseInput())
		require.NoError(t, err)
		assert.Equal(t, testReleaseID, rel.ID)
		assert.Equal(t, "user-2", repo.gotRel.UpdatedBy)
		assert.Empty(t, repo.gotRel.CreatedBy, "the creator must never be overwritten")
	})

	t.Run("requires actor", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		_, err := NewReleaseService(repo).Update(context.Background(), "", testReleaseID, validReleaseInput())
		assert.ErrorIs(t, err, ErrMissingActor)
		assert.Zero(t, repo.calls)
	})

	t.Run("validation error", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		in := validReleaseInput()
		in.Title = ""
		_, err := NewReleaseService(repo).Update(context.Background(), testActor, testReleaseID, in)
		var ve *ValidationError
		assert.ErrorAs(t, err, &ve)
		assert.Zero(t, repo.calls)
	})

	t.Run("malformed id", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		_, err := NewReleaseService(repo).Update(context.Background(), testActor, "nope", validReleaseInput())
		assert.ErrorIs(t, err, model.ErrNotFound)
		assert.Zero(t, repo.calls)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := NewReleaseService(&fakeReleaseRepo{err: model.ErrNotFound}).
			Update(context.Background(), testActor, testReleaseID, validReleaseInput())
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

func TestReleaseService_Delete(t *testing.T) {
	t.Run("soft delete passes the actor and returns the deletion time", func(t *testing.T) {
		at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
		repo := &fakeReleaseRepo{deletedAt: at}
		got, err := NewReleaseService(repo).Delete(context.Background(), testActor, testReleaseID)
		require.NoError(t, err)
		assert.Equal(t, at, got)
		assert.Equal(t, testReleaseID, repo.gotID)
		assert.Equal(t, testActor, repo.gotActor)
	})

	t.Run("requires actor", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		_, err := NewReleaseService(repo).Delete(context.Background(), " ", testReleaseID)
		assert.ErrorIs(t, err, ErrMissingActor)
		assert.Zero(t, repo.calls)
	})

	t.Run("malformed id", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		_, err := NewReleaseService(repo).Delete(context.Background(), testActor, "nope")
		assert.ErrorIs(t, err, model.ErrNotFound)
		assert.Zero(t, repo.calls)
	})

	t.Run("not found or already deleted", func(t *testing.T) {
		_, err := NewReleaseService(&fakeReleaseRepo{err: model.ErrNotFound}).
			Delete(context.Background(), testActor, testReleaseID)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

func TestReleaseService_List(t *testing.T) {
	t.Run("normalises filters, completes social status and computes total pages", func(t *testing.T) {
		repo := &fakeReleaseRepo{
			listTotal: 41,
			listItems: []model.Release{{ID: "a", SocialMediaStatus: map[string]string{"x": "posted"}}},
		}
		res, err := NewReleaseService(repo).List(context.Background(), ListReleasesQuery{
			Search: "  panen ", Status: " In_Progress ", DateFrom: "2026-10-01", DateTo: "2026-10-31",
		})
		require.NoError(t, err)

		f := repo.gotFilter
		assert.Equal(t, "panen", f.Search)
		assert.Equal(t, "in_progress", f.Status)
		require.NotNil(t, f.DateFrom)
		require.NotNil(t, f.DateTo)
		assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), *f.DateFrom)
		assert.Equal(t, 1, f.Page)
		assert.Equal(t, 20, f.PageSize)
		assert.Equal(t, 3, res.TotalPages)
		require.Len(t, res.Items, 1)
		assert.Equal(t, socialWith(map[string]string{"x": "posted"}), res.Items[0].SocialMediaStatus)
	})

	t.Run("paging is clamped", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		_, err := NewReleaseService(repo).List(context.Background(), ListReleasesQuery{Page: -4, PageSize: 500})
		require.NoError(t, err)
		assert.Equal(t, 1, repo.gotFilter.Page)
		assert.Equal(t, 100, repo.gotFilter.PageSize)
	})

	t.Run("omitted filters stay empty", func(t *testing.T) {
		repo := &fakeReleaseRepo{}
		_, err := NewReleaseService(repo).List(context.Background(), ListReleasesQuery{})
		require.NoError(t, err)
		assert.Empty(t, repo.gotFilter.Status)
		assert.Nil(t, repo.gotFilter.DateFrom)
		assert.Nil(t, repo.gotFilter.DateTo)
	})

	invalid := []struct {
		name string
		q    ListReleasesQuery
		want string
	}{
		{"unknown status", ListReleasesQuery{Status: "finished"}, "status must be one of"},
		{"bad date_from", ListReleasesQuery{DateFrom: "yesterday"}, "date_from"},
		{"from after to", ListReleasesQuery{DateFrom: "2026-11-01", DateTo: "2026-10-01"}, "date_from must not be after date_to"},
		{"search too long", ListReleasesQuery{Search: strings.Repeat("a", 101)}, "search must be at most 100"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeReleaseRepo{}
			_, err := NewReleaseService(repo).List(context.Background(), tc.q)
			var ve *ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Contains(t, ve.Error(), tc.want)
			assert.Zero(t, repo.calls)
		})
	}
}
