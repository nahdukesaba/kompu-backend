package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
)

const testKey = "jobs/2026/11/0123456789abcdef0123456789abcdef.pdf"

func TestOneDriveStorage(t *testing.T) {
	var itemGets int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/tenant/oauth2/v2.0/token":
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
			assert.Equal(t, "seed-token", r.Form.Get("refresh_token"))
			_, _ = io.WriteString(w, `{"access_token":"at","refresh_token":"rotated","expires_in":3600}`)
		case strings.HasSuffix(r.URL.Path, ":/createUploadSession"):
			assert.Equal(t, "Bearer at", r.Header.Get("Authorization"))
			assert.Equal(t, "/me/drive/root:/db/kompu/"+testKey+":/createUploadSession", r.URL.Path)
			_, _ = io.WriteString(w, `{"uploadUrl":"https://upload.example/session"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/me/drive/root:/db/kompu/"+testKey:
			atomic.AddInt32(&itemGets, 1)
			_, _ = io.WriteString(w, `{"@microsoft.graph.downloadUrl":"https://dl.example/file"}`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()

	s, err := NewOneDriveStorage(OneDriveConfig{
		TenantID: "tenant", ClientID: "c", ClientSecret: "s", RefreshToken: "seed-token", Root: "db/kompu/",
	}, nil)
	require.NoError(t, err)
	s.loginBase, s.graphBase = srv.URL, srv.URL
	ctx := context.Background()

	grant, err := s.PresignPut(ctx, model.PresignPutInput{Key: testKey, Size: 1000})
	require.NoError(t, err)
	assert.Equal(t, "https://upload.example/session", grant.URL)
	assert.Equal(t, map[string]string{"Content-Range": "bytes 0-999/1000"}, grant.Headers)

	u, err := s.PresignGet(ctx, "bucket", testKey, 0)
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/files/"+testKey, u)

	for range 2 { // second call is served from the cache
		link, err := s.DownloadURL(ctx, testKey)
		require.NoError(t, err)
		assert.Equal(t, "https://dl.example/file", link)
	}
	assert.EqualValues(t, 1, atomic.LoadInt32(&itemGets))

	require.NoError(t, s.DeleteObject(ctx, "bucket", testKey), "a missing file counts as deleted")

	_, err = s.DownloadURL(ctx, "../../secret.pdf")
	require.Error(t, err)
}

func TestValidObjectKey(t *testing.T) {
	assert.True(t, ValidObjectKey(testKey))
	assert.True(t, ValidObjectKey("releases/2026/01/0123456789abcdef0123456789abcdef.mp4"))
	for _, k := range []string{
		"", "jobs/../x.pdf", "other/2026/11/0123456789abcdef0123456789abcdef.pdf",
		"jobs/2026/11/0123456789abcdef0123456789abcdef.exe", "/jobs/2026/11/0123456789abcdef0123456789abcdef.pdf",
	} {
		assert.False(t, ValidObjectKey(k), k)
	}
}
