package repository

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
)

const testEndpoint = "https://abc.storage.supabase.co/storage/v1/s3"

func newTestStorage(t *testing.T, endpoint string) *S3Storage {
	t.Helper()
	s, err := NewS3Storage(S3StorageConfig{
		Endpoint:        endpoint,
		Region:          "ap-southeast-1",
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "secretexample",
	})
	require.NoError(t, err)
	return s
}

func assertNoChecksumParams(t *testing.T, q url.Values) {
	t.Helper()
	for key := range q {
		lk := strings.ToLower(key)
		assert.False(t, strings.HasPrefix(lk, "x-amz-checksum") || strings.HasPrefix(lk, "x-amz-sdk-checksum"),
			"unexpected checksum parameter %q", key)
	}
}

func TestS3Storage_PresignPut(t *testing.T) {
	got, err := newTestStorage(t, testEndpoint).PresignPut(context.Background(), model.PresignPutInput{
		Bucket:      "kompu-jobs",
		Key:         "jobs/2026/10/abc123.pdf",
		ContentType: "application/pdf",
		Size:        2048,
		Expires:     15 * time.Minute,
	})
	require.NoError(t, err)

	u, err := url.Parse(got.URL)
	require.NoError(t, err)
	assert.Equal(t, "abc.storage.supabase.co", u.Host)
	assert.Equal(t, "/storage/v1/s3/kompu-jobs/jobs/2026/10/abc123.pdf", u.Path)

	q := u.Query()
	assert.Equal(t, "AWS4-HMAC-SHA256", q.Get("X-Amz-Algorithm"))
	assert.Equal(t, "900", q.Get("X-Amz-Expires"))
	assert.NotEmpty(t, q.Get("X-Amz-Signature"))

	// The size limit only holds if these headers are part of the signature.
	signed := strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	assert.Contains(t, signed, "content-length")
	assert.Contains(t, signed, "content-type")
	assert.Contains(t, signed, "host")

	assertNoChecksumParams(t, q)
	assert.Equal(t, map[string]string{"Content-Type": "application/pdf"}, got.Headers)
}

func TestS3Storage_PresignGet(t *testing.T) {
	raw, err := newTestStorage(t, testEndpoint).
		PresignGet(context.Background(), "kompu-jobs", "jobs/2026/10/abc123.pdf", time.Hour)
	require.NoError(t, err)

	u, err := url.Parse(raw)
	require.NoError(t, err)
	assert.Equal(t, "abc.storage.supabase.co", u.Host)
	assert.Equal(t, "/storage/v1/s3/kompu-jobs/jobs/2026/10/abc123.pdf", u.Path)

	q := u.Query()
	assert.Equal(t, "3600", q.Get("X-Amz-Expires"))
	assert.NotEmpty(t, q.Get("X-Amz-Signature"))
	signed := strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	assert.Contains(t, signed, "host")
	assert.NotContains(t, signed, "content-length")
	assertNoChecksumParams(t, q)
}

func TestS3Storage_DeleteObject(t *testing.T) {
	t.Run("sends DELETE to the path-style object URL", func(t *testing.T) {
		var gotMethod, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		err := newTestStorage(t, srv.URL).DeleteObject(context.Background(), "kompu-jobs", "jobs/2026/10/abc123.pdf")

		require.NoError(t, err)
		assert.Equal(t, http.MethodDelete, gotMethod)
		assert.Equal(t, "/kompu-jobs/jobs/2026/10/abc123.pdf", gotPath)
	})

	t.Run("storage errors are returned", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`))
		}))
		defer srv.Close()

		err := newTestStorage(t, srv.URL).DeleteObject(context.Background(), "kompu-jobs", "k")
		assert.Error(t, err)
	})
}

func TestNewS3Storage_RequiresConfig(t *testing.T) {
	_, err := NewS3Storage(S3StorageConfig{})
	assert.Error(t, err)
}
