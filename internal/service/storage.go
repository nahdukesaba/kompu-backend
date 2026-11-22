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

var (
	ErrInvalidFileSize        = errors.New("size must be greater than zero")
	ErrFileTooLarge           = errors.New("file exceeds the maximum allowed size")
	ErrUnsupportedContentType = errors.New("content type is not allowed")
	ErrStorageDisabled        = errors.New("file storage is not configured")
)

// ObjectStorage is implemented by the storage adapter (repository layer).
type ObjectStorage interface {
	PresignPut(ctx context.Context, in model.PresignPutInput) (*model.PresignedRequest, error)
	DeleteObject(ctx context.Context, key string) error
}

type StorageServiceConfig struct {
	MaxBytes      int64
	PresignExpiry time.Duration
}

type UploadRequest struct {
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

// Allowed content types and their extensions. EDIT HERE to change what may be
// uploaded, and keep model.objectKeyPattern in sync.
var uploadTypes = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"image/webp":      "webp",
	"application/pdf": "pdf",
	"video/mp4":       "mp4",
	"video/quicktime": "mov",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"text/plain": "txt",
}

type StorageService struct {
	storage      ObjectStorage // nil when OneDrive is not configured
	maxBytes     int64
	uploadExpiry time.Duration

	// Overridable in tests.
	now   func() time.Time
	newID func() (string, error)
}

func NewStorageService(storage ObjectStorage, cfg StorageServiceConfig) *StorageService {
	return &StorageService{
		storage:      storage,
		maxBytes:     cfg.MaxBytes,
		uploadExpiry: cfg.PresignExpiry,
		now:          time.Now,
		newID:        randomID,
	}
}

// CreateUpload validates the declared file and opens an upload the browser
// sends straight to storage.
func (s *StorageService) CreateUpload(ctx context.Context, req UploadRequest) (*UploadGrant, error) {
	if req.Size <= 0 {
		return nil, ErrInvalidFileSize
	}
	if req.Size > s.maxBytes {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrFileTooLarge, s.maxBytes)
	}
	contentType := normalizeContentType(req.ContentType)
	ext, ok := uploadTypes[contentType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedContentType, contentType)
	}
	if s.storage == nil {
		return nil, ErrStorageDisabled
	}

	id, err := s.newID()
	if err != nil {
		return nil, fmt.Errorf("generate object id: %w", err)
	}
	now := s.now().UTC()
	key := fmt.Sprintf("items/%04d/%02d/%s.%s", now.Year(), int(now.Month()), id, ext)

	signed, err := s.storage.PresignPut(ctx, model.PresignPutInput{Key: key, ContentType: contentType, Size: req.Size})
	if err != nil {
		return nil, fmt.Errorf("open upload: %w", err)
	}
	return &UploadGrant{
		UploadURL: signed.URL,
		Method:    "PUT",
		Headers:   signed.Headers,
		ObjectKey: key,
		ExpiresAt: now.Add(s.uploadExpiry),
		MaxBytes:  s.maxBytes,
	}, nil
}

// DeleteFiles removes objects and returns the keys that could NOT be deleted.
func (s *StorageService) DeleteFiles(ctx context.Context, keys []string) []string {
	if s.storage == nil {
		return keys
	}
	var failed []string
	for _, key := range keys {
		if err := s.storage.DeleteObject(ctx, key); err != nil {
			log.Printf("[storage] delete %s failed: %v", key, err)
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
