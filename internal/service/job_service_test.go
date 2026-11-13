package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
)

const (
	testJobID  = "11111111-1111-1111-1111-111111111111"
	testJobKey = "jobs/2026/10/0123456789abcdef0123456789abcdef.jpg"
	testActor  = "user-1"
)

func keyN(n int) string { return fmt.Sprintf("jobs/2026/10/%032d.jpg", n) }

// ---------- fakes ----------

type fakeJobRepo struct {
	calls     int
	gotJob    *model.Job
	gotID     string
	gotActor  string
	gotFilter model.JobFilter

	err       error // returned by every method except KeysReferencedByOthers
	getResult *model.Job
	deleted   *model.Job // returned by SoftDelete

	shared     []string
	sharedErr  error
	gotExclude string
	gotKeys    []string

	listItems []model.Job
	listTotal int
}

func (f *fakeJobRepo) Create(_ context.Context, j *model.Job) (*model.Job, error) {
	f.calls++
	f.gotJob = j
	if f.err != nil {
		return nil, f.err
	}
	out := *j
	out.ID = testJobID
	return &out, nil
}

func (f *fakeJobRepo) GetByID(_ context.Context, id string) (*model.Job, error) {
	f.calls++
	f.gotID = id
	if f.err != nil {
		return nil, f.err
	}
	return f.getResult, nil
}

func (f *fakeJobRepo) Update(_ context.Context, j *model.Job) (*model.Job, error) {
	f.calls++
	f.gotJob = j
	if f.err != nil {
		return nil, f.err
	}
	return j, nil
}

func (f *fakeJobRepo) SoftDelete(_ context.Context, id, actor string) (*model.Job, error) {
	f.calls++
	f.gotID, f.gotActor = id, actor
	if f.err != nil {
		return nil, f.err
	}
	return f.deleted, nil
}

func (f *fakeJobRepo) KeysReferencedByOthers(_ context.Context, excludeID string, keys []string) ([]string, error) {
	f.calls++
	f.gotExclude, f.gotKeys = excludeID, keys
	return f.shared, f.sharedErr
}

func (f *fakeJobRepo) List(_ context.Context, filter model.JobFilter) ([]model.Job, int, error) {
	f.calls++
	f.gotFilter = filter
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.listItems, f.listTotal, nil
}

type fakeFileStore struct {
	calls       int
	gotResource UploadResource
	gotKeys     []string
	failed      []string
}

func (f *fakeFileStore) DeleteFiles(_ context.Context, r UploadResource, keys []string) []string {
	f.calls++
	f.gotResource, f.gotKeys = r, keys
	return f.failed
}

func newJobSvc(repo *fakeJobRepo) (*JobService, *fakeFileStore) {
	files := &fakeFileStore{}
	return NewJobService(repo, files), files
}

func validJobInput() JobInput {
	return JobInput{
		ActivityDate: "2026-10-27",
		Title:        "Rice field visit",
		Objective:    "Check irrigation",
		Location:     "Deli Serdang",
		Participants: "Farmer group A",
		EvidenceFiles: []model.EvidenceFile{
			{Key: testJobKey, Name: "photo.jpg", ContentType: "image/jpeg", Size: 1234},
		},
	}
}

func deletedJob(keys ...string) *model.Job {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	files := make([]model.EvidenceFile, 0, len(keys))
	for _, k := range keys {
		files = append(files, model.EvidenceFile{Key: k})
	}
	return &model.Job{ID: testJobID, DeletedAt: &at, EvidenceFiles: files}
}

// ---------- create ----------

func TestJobService_Create_Success(t *testing.T) {
	repo := &fakeJobRepo{}
	svc, _ := newJobSvc(repo)
	in := validJobInput()
	in.Title = "  Rice field visit  "
	in.EvidenceFiles = nil

	job, err := svc.Create(context.Background(), testActor, in)

	require.NoError(t, err)
	assert.Equal(t, testJobID, job.ID)
	assert.Equal(t, "Rice field visit", repo.gotJob.Title, "title must be trimmed")
	assert.Equal(t, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), repo.gotJob.ActivityDate)
	assert.Equal(t, testActor, repo.gotJob.CreatedBy)
	assert.Equal(t, testActor, repo.gotJob.UpdatedBy)
	assert.NotNil(t, repo.gotJob.EvidenceFiles, "must be an empty slice, never nil (stored as JSON [])")
	assert.Empty(t, repo.gotJob.EvidenceFiles)
}

func TestJobService_Create_RequiresActor(t *testing.T) {
	for _, actor := range []string{"", "   "} {
		repo := &fakeJobRepo{}
		svc, _ := newJobSvc(repo)
		_, err := svc.Create(context.Background(), actor, validJobInput())
		assert.ErrorIs(t, err, ErrMissingActor)
		assert.Zero(t, repo.calls)
	}
}

func TestJobService_Create_ValidationErrors(t *testing.T) {
	tooMany := make([]model.EvidenceFile, 21)
	for i := range tooMany {
		tooMany[i] = model.EvidenceFile{Key: testJobKey}
	}

	tests := []struct {
		name   string
		mutate func(*JobInput)
		want   string
	}{
		{"missing title", func(in *JobInput) { in.Title = "" }, "title is required"},
		{"blank title", func(in *JobInput) { in.Title = "   " }, "title is required"},
		{"title too long", func(in *JobInput) { in.Title = strings.Repeat("a", 201) }, "title must be at most 200"},
		{"missing date", func(in *JobInput) { in.ActivityDate = "" }, "activity_date"},
		{"wrong date format", func(in *JobInput) { in.ActivityDate = "27-10-2026" }, "activity_date"},
		{"impossible date", func(in *JobInput) { in.ActivityDate = "2026-02-30" }, "activity_date"},
		{"objective too long", func(in *JobInput) { in.Objective = strings.Repeat("a", 5001) }, "objective must be at most 5000"},
		{"location too long", func(in *JobInput) { in.Location = strings.Repeat("a", 301) }, "location must be at most 300"},
		{"participants too long", func(in *JobInput) { in.Participants = strings.Repeat("a", 2001) }, "participants must be at most 2000"},
		{"key from another bucket", func(in *JobInput) {
			in.EvidenceFiles = []model.EvidenceFile{{Key: "releases/2026/10/0123456789abcdef0123456789abcdef.jpg"}}
		}, "evidence_files[0].key"},
		{"path traversal key", func(in *JobInput) {
			in.EvidenceFiles = []model.EvidenceFile{{Key: "jobs/../../etc/passwd"}}
		}, "evidence_files[0].key"},
		{"empty key", func(in *JobInput) {
			in.EvidenceFiles = []model.EvidenceFile{{Key: ""}}
		}, "evidence_files[0].key"},
		{"negative size", func(in *JobInput) {
			in.EvidenceFiles = []model.EvidenceFile{{Key: testJobKey, Size: -1}}
		}, "evidence_files[0].size"},
		{"too many files", func(in *JobInput) { in.EvidenceFiles = tooMany }, "at most 20 files"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeJobRepo{}
			svc, _ := newJobSvc(repo)
			in := validJobInput()
			tc.mutate(&in)

			job, err := svc.Create(context.Background(), testActor, in)

			var ve *ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Contains(t, ve.Error(), tc.want)
			assert.Nil(t, job)
			assert.Zero(t, repo.calls, "repository must not be called for invalid input")
		})
	}
}

func TestJobService_Create_ReportsAllProblemsAtOnce(t *testing.T) {
	svc, _ := newJobSvc(&fakeJobRepo{})
	_, err := svc.Create(context.Background(), testActor, JobInput{})

	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.GreaterOrEqual(t, len(ve.Problems), 2)
}

func TestJobService_Create_RepositoryError(t *testing.T) {
	boom := errors.New("db down")
	svc, _ := newJobSvc(&fakeJobRepo{err: boom})
	_, err := svc.Create(context.Background(), testActor, validJobInput())
	assert.ErrorIs(t, err, boom)
}

// ---------- get ----------

func TestJobService_Get(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		svc, _ := newJobSvc(&fakeJobRepo{getResult: &model.Job{ID: testJobID, Title: "x"}})
		job, err := svc.Get(context.Background(), testJobID)
		require.NoError(t, err)
		assert.Equal(t, "x", job.Title)
	})

	t.Run("malformed id is not found and never reaches the database", func(t *testing.T) {
		repo := &fakeJobRepo{}
		svc, _ := newJobSvc(repo)
		_, err := svc.Get(context.Background(), "not-a-uuid")
		assert.ErrorIs(t, err, model.ErrNotFound)
		assert.Zero(t, repo.calls)
	})

	t.Run("not found", func(t *testing.T) {
		svc, _ := newJobSvc(&fakeJobRepo{err: model.ErrNotFound})
		_, err := svc.Get(context.Background(), testJobID)
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

// ---------- update ----------

func TestJobService_Update(t *testing.T) {
	t.Run("success records the updater and keeps the id from the URL", func(t *testing.T) {
		repo := &fakeJobRepo{}
		svc, _ := newJobSvc(repo)
		job, err := svc.Update(context.Background(), "user-2", testJobID, validJobInput())
		require.NoError(t, err)
		assert.Equal(t, testJobID, job.ID)
		assert.Equal(t, "user-2", repo.gotJob.UpdatedBy)
		assert.Empty(t, repo.gotJob.CreatedBy, "the creator must never be overwritten")
	})

	t.Run("requires actor", func(t *testing.T) {
		repo := &fakeJobRepo{}
		svc, _ := newJobSvc(repo)
		_, err := svc.Update(context.Background(), "", testJobID, validJobInput())
		assert.ErrorIs(t, err, ErrMissingActor)
		assert.Zero(t, repo.calls)
	})

	t.Run("validation error", func(t *testing.T) {
		repo := &fakeJobRepo{}
		svc, _ := newJobSvc(repo)
		in := validJobInput()
		in.Title = ""
		_, err := svc.Update(context.Background(), testActor, testJobID, in)

		var ve *ValidationError
		assert.ErrorAs(t, err, &ve)
		assert.Zero(t, repo.calls)
	})

	t.Run("malformed id", func(t *testing.T) {
		repo := &fakeJobRepo{}
		svc, _ := newJobSvc(repo)
		_, err := svc.Update(context.Background(), testActor, "nope", validJobInput())
		assert.ErrorIs(t, err, model.ErrNotFound)
		assert.Zero(t, repo.calls)
	})

	t.Run("not found", func(t *testing.T) {
		svc, _ := newJobSvc(&fakeJobRepo{err: model.ErrNotFound})
		_, err := svc.Update(context.Background(), testActor, testJobID, validJobInput())
		assert.ErrorIs(t, err, model.ErrNotFound)
	})
}

// ---------- delete ----------

func TestJobService_Delete_SoftDeleteOnly(t *testing.T) {
	repo := &fakeJobRepo{deleted: deletedJob(keyN(1))}
	svc, files := newJobSvc(repo)

	res, err := svc.Delete(context.Background(), testActor, testJobID, DeleteJobOptions{})

	require.NoError(t, err)
	assert.Equal(t, testJobID, repo.gotID)
	assert.Equal(t, testActor, repo.gotActor)
	assert.Equal(t, testJobID, res.ID)
	assert.False(t, res.DeletedAt.IsZero())
	assert.False(t, res.CleanupRequested)
	assert.Zero(t, files.calls, "files must be left alone unless cleanup is requested")
	assert.Nil(t, repo.gotKeys, "no reference check without cleanup")
}

func TestJobService_Delete_Guards(t *testing.T) {
	t.Run("requires actor", func(t *testing.T) {
		repo := &fakeJobRepo{}
		svc, _ := newJobSvc(repo)
		_, err := svc.Delete(context.Background(), " ", testJobID, DeleteJobOptions{})
		assert.ErrorIs(t, err, ErrMissingActor)
		assert.Zero(t, repo.calls)
	})

	t.Run("malformed id", func(t *testing.T) {
		repo := &fakeJobRepo{}
		svc, _ := newJobSvc(repo)
		_, err := svc.Delete(context.Background(), testActor, "nope", DeleteJobOptions{})
		assert.ErrorIs(t, err, model.ErrNotFound)
		assert.Zero(t, repo.calls)
	})

	t.Run("not found or already deleted", func(t *testing.T) {
		svc, files := newJobSvc(&fakeJobRepo{err: model.ErrNotFound})
		_, err := svc.Delete(context.Background(), testActor, testJobID, DeleteJobOptions{CleanupFiles: true})
		assert.ErrorIs(t, err, model.ErrNotFound)
		assert.Zero(t, files.calls)
	})
}

func TestJobService_Delete_CleanupDeletesOnlyUnsharedFiles(t *testing.T) {
	repo := &fakeJobRepo{
		deleted: deletedJob(keyN(1), keyN(2), keyN(3)),
		shared:  []string{keyN(2)},
	}
	svc, files := newJobSvc(repo)

	res, err := svc.Delete(context.Background(), testActor, testJobID, DeleteJobOptions{CleanupFiles: true})

	require.NoError(t, err)
	assert.Equal(t, testJobID, repo.gotExclude)
	assert.Equal(t, []string{keyN(1), keyN(2), keyN(3)}, repo.gotKeys)
	assert.Equal(t, UploadResourceJobs, files.gotResource)
	assert.Equal(t, []string{keyN(1), keyN(3)}, files.gotKeys, "the shared file must be kept")
	assert.True(t, res.CleanupRequested)
	assert.Equal(t, 2, res.FilesDeleted)
	assert.Equal(t, 1, res.FilesKeptShared)
	assert.Equal(t, 0, res.FilesFailed)
}

func TestJobService_Delete_CleanupReportsFailures(t *testing.T) {
	repo := &fakeJobRepo{deleted: deletedJob(keyN(1), keyN(2))}
	svc, files := newJobSvc(repo)
	files.failed = []string{keyN(1)}

	res, err := svc.Delete(context.Background(), testActor, testJobID, DeleteJobOptions{CleanupFiles: true})

	require.NoError(t, err, "the soft delete itself succeeded")
	assert.Equal(t, 1, res.FilesDeleted)
	assert.Equal(t, 1, res.FilesFailed)
}

func TestJobService_Delete_CleanupSkippedWhenSharingCannotBeChecked(t *testing.T) {
	repo := &fakeJobRepo{deleted: deletedJob(keyN(1), keyN(2)), sharedErr: errors.New("db hiccup")}
	svc, files := newJobSvc(repo)

	res, err := svc.Delete(context.Background(), testActor, testJobID, DeleteJobOptions{CleanupFiles: true})

	require.NoError(t, err)
	assert.Zero(t, files.calls, "never delete files we could not prove are unshared")
	assert.Equal(t, 2, res.FilesFailed)
	assert.Zero(t, res.FilesDeleted)
}

func TestJobService_Delete_CleanupWithNoFiles(t *testing.T) {
	repo := &fakeJobRepo{deleted: deletedJob()}
	svc, files := newJobSvc(repo)

	res, err := svc.Delete(context.Background(), testActor, testJobID, DeleteJobOptions{CleanupFiles: true})

	require.NoError(t, err)
	assert.Zero(t, files.calls)
	assert.Zero(t, res.FilesDeleted)
}

func TestJobService_Delete_CleanupDeduplicatesKeys(t *testing.T) {
	repo := &fakeJobRepo{deleted: deletedJob(keyN(1), keyN(1))}
	svc, files := newJobSvc(repo)

	res, err := svc.Delete(context.Background(), testActor, testJobID, DeleteJobOptions{CleanupFiles: true})

	require.NoError(t, err)
	assert.Equal(t, []string{keyN(1)}, files.gotKeys)
	assert.Equal(t, 1, res.FilesDeleted)
}

// ---------- list ----------

func TestJobService_List_PagingNormalisation(t *testing.T) {
	tests := []struct {
		name               string
		page, size         int
		wantPage, wantSize int
	}{
		{"defaults", 0, 0, 1, 20},
		{"negative page", -3, 10, 1, 10},
		{"negative size", 2, -1, 2, 20},
		{"size is capped at 100", 1, 500, 1, 100},
		{"normal values pass through", 3, 50, 3, 50},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeJobRepo{}
			svc, _ := newJobSvc(repo)
			res, err := svc.List(context.Background(), ListJobsQuery{Page: tc.page, PageSize: tc.size})
			require.NoError(t, err)

			assert.Equal(t, tc.wantPage, repo.gotFilter.Page)
			assert.Equal(t, tc.wantSize, repo.gotFilter.PageSize)
			assert.Equal(t, tc.wantPage, res.Page)
			assert.Equal(t, tc.wantSize, res.PageSize)
		})
	}
}

func TestJobService_List_TotalPages(t *testing.T) {
	tests := []struct{ total, size, want int }{
		{0, 20, 0}, {1, 20, 1}, {20, 20, 1}, {21, 20, 2}, {41, 20, 3},
	}
	for _, tc := range tests {
		svc, _ := newJobSvc(&fakeJobRepo{listTotal: tc.total})
		res, err := svc.List(context.Background(), ListJobsQuery{Page: 1, PageSize: tc.size})
		require.NoError(t, err)
		assert.Equal(t, tc.want, res.TotalPages, "total=%d size=%d", tc.total, tc.size)
	}
}

func TestJobService_List_Filters(t *testing.T) {
	repo := &fakeJobRepo{}
	svc, _ := newJobSvc(repo)
	_, err := svc.List(context.Background(), ListJobsQuery{
		Search: "  rice  ", DateFrom: "2026-10-01", DateTo: "2026-10-31",
	})
	require.NoError(t, err)

	assert.Equal(t, "rice", repo.gotFilter.Search)
	require.NotNil(t, repo.gotFilter.DateFrom)
	require.NotNil(t, repo.gotFilter.DateTo)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), *repo.gotFilter.DateFrom)
	assert.Equal(t, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), *repo.gotFilter.DateTo)
}

func TestJobService_List_OmittedDatesStayNil(t *testing.T) {
	repo := &fakeJobRepo{}
	svc, _ := newJobSvc(repo)
	_, err := svc.List(context.Background(), ListJobsQuery{})
	require.NoError(t, err)
	assert.Nil(t, repo.gotFilter.DateFrom)
	assert.Nil(t, repo.gotFilter.DateTo)
}

func TestJobService_List_ValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		q    ListJobsQuery
		want string
	}{
		{"bad date_from", ListJobsQuery{DateFrom: "yesterday"}, "date_from"},
		{"bad date_to", ListJobsQuery{DateTo: "2026-13-01"}, "date_to"},
		{"from after to", ListJobsQuery{DateFrom: "2026-11-01", DateTo: "2026-10-01"}, "date_from must not be after date_to"},
		{"search too long", ListJobsQuery{Search: strings.Repeat("a", 101)}, "search must be at most 100"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeJobRepo{}
			svc, _ := newJobSvc(repo)
			_, err := svc.List(context.Background(), tc.q)

			var ve *ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Contains(t, ve.Error(), tc.want)
			assert.Zero(t, repo.calls)
		})
	}
}

func TestJobService_List_ReturnsItems(t *testing.T) {
	svc, _ := newJobSvc(&fakeJobRepo{listItems: []model.Job{{ID: "a"}, {ID: "b"}}, listTotal: 2})
	res, err := svc.List(context.Background(), ListJobsQuery{})
	require.NoError(t, err)
	assert.Len(t, res.Items, 2)
	assert.Equal(t, 2, res.Total)
}
