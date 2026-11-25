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

const mb50 = 50 * 1024 * 1024

type fakePresigner struct {
	calls      int
	got        model.PresignPutInput
	err        error
	deleteErrs map[string]error
	deleted    []string
}

func (f *fakePresigner) PresignPut(_ context.Context, in model.PresignPutInput) (*model.PresignedRequest, error) {
	f.calls++
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return &model.PresignedRequest{
		URL:     "https://storage.test/upload",
		Headers: map[string]string{"Content-Range": "bytes 0-9/10"},
	}, nil
}

func (f *fakePresigner) DeleteObject(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return f.deleteErrs[key]
}

func newTestStorageService(p ObjectStorage) *StorageService {
	s := NewStorageService(p, StorageServiceConfig{MaxBytes: mb50, PresignExpiry: 15 * time.Minute})
	s.now = func() time.Time { return time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC) }
	s.newID = func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }
	return s
}

func TestCreateUpload_Validation(t *testing.T) {
	tests := []struct {
		name string
		req  UploadRequest
		want error
	}{
		{"zero size", UploadRequest{ContentType: "image/png"}, ErrInvalidFileSize},
		{"too large", UploadRequest{ContentType: "video/mp4", Size: mb50 + 1}, ErrFileTooLarge},
		{"html is rejected", UploadRequest{ContentType: "text/html", Size: 10}, ErrUnsupportedContentType},
		{"svg is rejected", UploadRequest{ContentType: "image/svg+xml", Size: 10}, ErrUnsupportedContentType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePresigner{}
			_, err := newTestStorageService(p).CreateUpload(context.Background(), tc.req)
			require.ErrorIs(t, err, tc.want)
			assert.Zero(t, p.calls)
		})
	}
}

func TestCreateUpload_EveryTypeMakesAValidKey(t *testing.T) {
	for contentType := range uploadTypes {
		p := &fakePresigner{}
		grant, err := newTestStorageService(p).CreateUpload(context.Background(), UploadRequest{ContentType: contentType, Size: 10})
		require.NoError(t, err, contentType)
		assert.True(t, model.ValidObjectKey(grant.ObjectKey), "%s made key %s", contentType, grant.ObjectKey)
	}
}

func TestCreateUpload_Grant(t *testing.T) {
	p := &fakePresigner{}
	grant, err := newTestStorageService(p).CreateUpload(context.Background(),
		UploadRequest{ContentType: "Image/JPEG; charset=x", Size: 10})
	require.NoError(t, err)
	assert.Equal(t, "items/2026/10/0123456789abcdef0123456789abcdef.jpg", grant.ObjectKey)
	assert.Equal(t, "PUT", grant.Method)
	assert.Equal(t, "image/jpeg", p.got.ContentType)
	assert.EqualValues(t, mb50, grant.MaxBytes)
}

func TestCreateUpload_StorageDisabled(t *testing.T) {
	_, err := NewStorageService(nil, StorageServiceConfig{MaxBytes: mb50}).
		CreateUpload(context.Background(), UploadRequest{ContentType: "image/png", Size: 10})
	require.ErrorIs(t, err, ErrStorageDisabled)
}

func TestDeleteFiles(t *testing.T) {
	p := &fakePresigner{deleteErrs: map[string]error{"bad": errors.New("boom")}}
	failed := newTestStorageService(p).DeleteFiles(context.Background(), []string{"ok", "bad"})
	assert.Equal(t, []string{"ok", "bad"}, p.deleted)
	assert.Equal(t, []string{"bad"}, failed)
}
