package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
)

const (
	mb10 = 10 * 1024 * 1024
	mb50 = 50 * 1024 * 1024
)

type getCall struct {
	bucket, key string
	expires     time.Duration
}

type fakePresigner struct {
	calls int
	got   model.PresignPutInput
	err   error

	getErr     error
	gotGet     getCall
	deleteErrs map[string]error // by key
	deleted    []string         // "bucket/key"
}

func (f *fakePresigner) PresignPut(_ context.Context, in model.PresignPutInput) (*model.PresignedRequest, error) {
	f.calls++
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return &model.PresignedRequest{
		URL:     "https://storage.test/upload?sig=1",
		Headers: map[string]string{"Content-Type": in.ContentType},
	}, nil
}

func (f *fakePresigner) PresignGet(_ context.Context, bucket, key string, expires time.Duration) (string, error) {
	f.gotGet = getCall{bucket: bucket, key: key, expires: expires}
	if f.getErr != nil {
		return "", f.getErr
	}
	return "https://storage.test/download/" + key + "?sig=1", nil
}

func (f *fakePresigner) DeleteObject(_ context.Context, bucket, key string) error {
	f.deleted = append(f.deleted, bucket+"/"+key)
	return f.deleteErrs[key]
}

func newTestStorageService(p ObjectStorage) *StorageService {
	s := NewStorageService(p, StorageServiceConfig{
		BucketJobs:      "kompu-jobs",
		BucketReleases:  "kompu-releases",
		MaxJobBytes:     mb10,
		MaxReleaseBytes: mb50,
		PresignExpiry:   15 * time.Minute,
		DownloadExpiry:  time.Hour,
	})
	s.now = func() time.Time { return time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC) }
	s.newID = func() (string, error) { return "abc123", nil }
	return s
}

func TestCreateUpload_Validation(t *testing.T) {
	tests := []struct {
		name    string
		req     UploadRequest
		wantErr error
	}{
		{"job at exactly 10MB is allowed", UploadRequest{"jobs", "application/pdf", mb10}, nil},
		{"job 1 byte over 10MB is rejected", UploadRequest{"jobs", "application/pdf", mb10 + 1}, ErrFileTooLarge},
		{"release at exactly 50MB is allowed", UploadRequest{"releases", "video/mp4", mb50}, nil},
		{"release 1 byte over 50MB is rejected", UploadRequest{"releases", "video/mp4", mb50 + 1}, ErrFileTooLarge},
		{"11MB is too big for a job", UploadRequest{"jobs", "image/png", 11 * 1024 * 1024}, ErrFileTooLarge},
		{"11MB is fine for a release", UploadRequest{"releases", "image/png", 11 * 1024 * 1024}, nil},
		{"zero size", UploadRequest{"jobs", "image/png", 0}, ErrInvalidFileSize},
		{"negative size", UploadRequest{"jobs", "image/png", -5}, ErrInvalidFileSize},
		{"unknown resource", UploadRequest{"users", "image/png", 100}, ErrInvalidResource},
		{"empty resource", UploadRequest{"", "image/png", 100}, ErrInvalidResource},
		{"html is not allowed", UploadRequest{"jobs", "text/html", 100}, ErrUnsupportedContentType},
		{"executable is not allowed", UploadRequest{"releases", "application/x-msdownload", 100}, ErrUnsupportedContentType},
		{"empty content type", UploadRequest{"jobs", "", 100}, ErrUnsupportedContentType},
		{"video is not allowed for jobs", UploadRequest{"jobs", "video/mp4", 100}, ErrUnsupportedContentType},
		{"content type is case/parameter insensitive", UploadRequest{"jobs", "IMAGE/JPEG; charset=binary", 100}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePresigner{}
			grant, err := newTestStorageService(p).CreateUpload(context.Background(), tc.req)

			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				assert.Nil(t, grant)
				assert.Zero(t, p.calls, "presigner must not be called for invalid requests")
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, grant)
			assert.Equal(t, 1, p.calls)
		})
	}
}

func TestCreateUpload_GrantForJob(t *testing.T) {
	p := &fakePresigner{}
	grant, err := newTestStorageService(p).CreateUpload(context.Background(),
		UploadRequest{Resource: "jobs", ContentType: "application/pdf", Size: 2048})
	require.NoError(t, err)

	assert.Equal(t, "jobs/2026/10/abc123.pdf", grant.ObjectKey)
	assert.Equal(t, "PUT", grant.Method)
	assert.Equal(t, "https://storage.test/upload?sig=1", grant.UploadURL)
	assert.Equal(t, map[string]string{"Content-Type": "application/pdf"}, grant.Headers)
	assert.Equal(t, int64(mb10), grant.MaxBytes)
	assert.Equal(t, time.Date(2026, 10, 27, 8, 15, 0, 0, time.UTC), grant.ExpiresAt)

	// What was handed to the storage layer: right bucket, exact size, expiry.
	assert.Equal(t, model.PresignPutInput{
		Bucket:      "kompu-jobs",
		Key:         "jobs/2026/10/abc123.pdf",
		ContentType: "application/pdf",
		Size:        2048,
		Expires:     15 * time.Minute,
	}, p.got)
}

func TestCreateUpload_GrantForRelease(t *testing.T) {
	p := &fakePresigner{}
	grant, err := newTestStorageService(p).CreateUpload(context.Background(),
		UploadRequest{Resource: "releases", ContentType: "video/mp4", Size: 5 * 1024 * 1024})
	require.NoError(t, err)

	assert.Equal(t, "releases/2026/10/abc123.mp4", grant.ObjectKey)
	assert.Equal(t, int64(mb50), grant.MaxBytes)
	assert.Equal(t, "kompu-releases", p.got.Bucket)
}

func TestCreateUpload_PresignerFailure(t *testing.T) {
	boom := errors.New("storage down")
	s := newTestStorageService(&fakePresigner{err: boom})

	grant, err := s.CreateUpload(context.Background(),
		UploadRequest{Resource: "jobs", ContentType: "image/png", Size: 100})

	assert.ErrorIs(t, err, boom)
	assert.Nil(t, grant)
}

func TestCreateUpload_IDFailure(t *testing.T) {
	p := &fakePresigner{}
	s := newTestStorageService(p)
	s.newID = func() (string, error) { return "", errors.New("no entropy") }

	_, err := s.CreateUpload(context.Background(),
		UploadRequest{Resource: "jobs", ContentType: "image/png", Size: 100})

	assert.Error(t, err)
	assert.Zero(t, p.calls)
}

func TestDownloadURL(t *testing.T) {
	t.Run("signs for the right bucket and expiry", func(t *testing.T) {
		p := &fakePresigner{}
		s := newTestStorageService(p)

		u, err := s.DownloadURL(context.Background(), UploadResourceJobs, "jobs/2026/10/abc.pdf")
		require.NoError(t, err)
		assert.Equal(t, "https://storage.test/download/jobs/2026/10/abc.pdf?sig=1", u)
		assert.Equal(t, getCall{bucket: "kompu-jobs", key: "jobs/2026/10/abc.pdf", expires: time.Hour}, p.gotGet)

		_, err = s.DownloadURL(context.Background(), UploadResourceReleases, "releases/2026/10/x.mp4")
		require.NoError(t, err)
		assert.Equal(t, "kompu-releases", p.gotGet.bucket)
	})

	t.Run("unknown resource", func(t *testing.T) {
		_, err := newTestStorageService(&fakePresigner{}).DownloadURL(context.Background(), "users", "k")
		assert.ErrorIs(t, err, ErrInvalidResource)
	})

	t.Run("signer failure", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := newTestStorageService(&fakePresigner{getErr: boom}).
			DownloadURL(context.Background(), UploadResourceJobs, "k")
		assert.ErrorIs(t, err, boom)
	})
}

func TestDeleteFiles(t *testing.T) {
	t.Run("deletes from the resource's bucket", func(t *testing.T) {
		p := &fakePresigner{}
		failed := newTestStorageService(p).DeleteFiles(context.Background(), UploadResourceJobs, []string{"a", "b"})
		assert.Empty(t, failed)
		assert.Equal(t, []string{"kompu-jobs/a", "kompu-jobs/b"}, p.deleted)
	})

	t.Run("returns the keys that failed and keeps going", func(t *testing.T) {
		p := &fakePresigner{deleteErrs: map[string]error{"b": errors.New("denied")}}
		failed := newTestStorageService(p).DeleteFiles(context.Background(), UploadResourceJobs, []string{"a", "b", "c"})
		assert.Equal(t, []string{"b"}, failed)
		assert.Len(t, p.deleted, 3)
	})

	t.Run("unknown resource deletes nothing", func(t *testing.T) {
		p := &fakePresigner{}
		failed := newTestStorageService(p).DeleteFiles(context.Background(), "users", []string{"a"})
		assert.Equal(t, []string{"a"}, failed)
		assert.Empty(t, p.deleted)
	})
}
