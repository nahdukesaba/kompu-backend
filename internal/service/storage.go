package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"kompu-backend/internal/model"
)

type UploadResource string

const (
	UploadResourceJobs     UploadResource = "jobs"
	UploadResourceReleases UploadResource = "releases"
)

var (
	ErrInvalidResource        = errors.New("resource must be 'jobs' or 'releases'")
	ErrInvalidFileSize        = errors.New("size must be greater than zero")
	ErrFileTooLarge           = errors.New("file exceeds the maximum allowed size")
	ErrUnsupportedContentType = errors.New("content type is not allowed")
)

// ObjectStorage is implemented by the storage adapter (repository layer).
type ObjectStorage interface {
	PresignPut(ctx context.Context, in model.PresignPutInput) (*model.PresignedRequest, error)
	PresignGet(ctx context.Context, bucket, key string, expires time.Duration) (string, error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

type StorageServiceConfig struct {
	BucketJobs      string
	BucketReleases  string
	MaxJobBytes     int64
	MaxReleaseBytes int64
	PresignExpiry   time.Duration // upload URLs
	DownloadExpiry  time.Duration // display/download URLs
}

type UploadRequest struct {
	Resource    string
	ContentType string
	Size        int64
}

type UploadGrant struct {
	UploadURL string
	Method    string
	Headers   map[string]string
	ObjectKey string
	ExpiresAt time.Time
	MaxBytes  int64
}

type uploadTarget struct {
	bucket   string
	maxBytes int64
	types    map[string]string // allowed content type -> file extension
}

// Allowed content types. EDIT HERE to change what may be uploaded.
var documentAndImageTypes = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"image/webp":      "webp",
	"application/pdf": "pdf",
}

type StorageService struct {
	storage        ObjectStorage
	targets        map[UploadResource]uploadTarget
	uploadExpiry   time.Duration
	downloadExpiry time.Duration

	// Overridable in tests.
	now   func() time.Time
	newID func() (string, error)
}

func NewStorageService(storage ObjectStorage, cfg StorageServiceConfig) *StorageService {
	releaseTypes := map[string]string{"video/mp4": "mp4"}
	for ct, ext := range documentAndImageTypes {
		releaseTypes[ct] = ext
	}

	return &StorageService{
		storage: storage,
		targets: map[UploadResource]uploadTarget{
			UploadResourceJobs: {
				bucket:   cfg.BucketJobs,
				maxBytes: cfg.MaxJobBytes,
				types:    documentAndImageTypes,
			},
			UploadResourceReleases: {
				bucket:   cfg.BucketReleases,
				maxBytes: cfg.MaxReleaseBytes,
				types:    releaseTypes,
			},
		},
		uploadExpiry:   cfg.PresignExpiry,
		downloadExpiry: cfg.DownloadExpiry,
		now:            time.Now,
		newID:          randomID,
	}
}

// CreateUpload validates the declared file and returns a short-lived presigned
// PUT URL. The declared size is signed into the URL, so the storage backend
// can reject a body of any other length.
func (s *StorageService) CreateUpload(ctx context.Context, req UploadRequest) (*UploadGrant, error) {
	resource := UploadResource(req.Resource)
	target, ok := s.targets[resource]
	if !ok {
		return nil, ErrInvalidResource
	}
	if req.Size <= 0 {
		return nil, ErrInvalidFileSize
	}
	if req.Size > target.maxBytes {
		return nil, fmt.Errorf("%w: limit for %s is %d bytes", ErrFileTooLarge, resource, target.maxBytes)
	}

	contentType := normalizeContentType(req.ContentType)
	ext, ok := target.types[contentType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedContentType, contentType)
	}

	id, err := s.newID()
	if err != nil {
		return nil, fmt.Errorf("generate object id: %w", err)
	}

	now := s.now().UTC()
	key := fmt.Sprintf("%s/%04d/%02d/%s.%s", resource, now.Year(), int(now.Month()), id, ext)

	signed, err := s.storage.PresignPut(ctx, model.PresignPutInput{
		Bucket:      target.bucket,
		Key:         key,
		ContentType: contentType,
		Size:        req.Size,
		Expires:     s.uploadExpiry,
	})
	if err != nil {
		return nil, fmt.Errorf("presign upload: %w", err)
	}

	return &UploadGrant{
		UploadURL: signed.URL,
		Method:    "PUT",
		Headers:   signed.Headers,
		ObjectKey: key,
		ExpiresAt: now.Add(s.uploadExpiry),
		MaxBytes:  target.maxBytes,
	}, nil
}

// DownloadURL returns a short-lived presigned URL for displaying a stored
// file. Callers must only pass keys read from live database records.
func (s *StorageService) DownloadURL(ctx context.Context, resource UploadResource, key string) (string, error) {
	target, ok := s.targets[resource]
	if !ok {
		return "", ErrInvalidResource
	}
	u, err := s.storage.PresignGet(ctx, target.bucket, key, s.downloadExpiry)
	if err != nil {
		return "", fmt.Errorf("presign download: %w", err)
	}
	return u, nil
}

// DeleteFiles permanently removes objects and returns the keys that could NOT
// be deleted (empty when everything succeeded). Failures are logged.
func (s *StorageService) DeleteFiles(ctx context.Context, resource UploadResource, keys []string) []string {
	target, ok := s.targets[resource]
	if !ok {
		return keys
	}

	var failed []string
	for _, key := range keys {
		if err := s.storage.DeleteObject(ctx, target.bucket, key); err != nil {
			log.Printf("[storage] delete %s/%s failed: %v", target.bucket, key, err)
			failed = append(failed, key)
		}
	}
	return failed
}

func normalizeContentType(v string) string {
	base, _, _ := strings.Cut(v, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
