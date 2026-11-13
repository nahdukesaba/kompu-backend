package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"kompu-backend/internal/model"
)

type S3StorageConfig struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

// S3Storage talks to any S3-compatible endpoint (here: Supabase Storage):
// presigned uploads, presigned downloads and object deletion.
type S3Storage struct {
	client  *s3.Client
	presign *s3.PresignClient
}

func NewS3Storage(cfg S3StorageConfig) (*S3Storage, error) {
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("s3 storage: endpoint, region and credentials are required")
	}

	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		// Supabase wants the bucket in the path, not in the hostname.
		o.UsePathStyle = true
		// Recent SDK versions add checksum parameters to presigned URLs by
		// default; non-AWS S3 servers can reject those.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})

	return &S3Storage{client: client, presign: s3.NewPresignClient(client)}, nil
}

func (s *S3Storage) PresignPut(ctx context.Context, in model.PresignPutInput) (*model.PresignedRequest, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(in.Bucket),
		Key:           aws.String(in.Key),
		ContentType:   aws.String(in.ContentType),
		ContentLength: aws.Int64(in.Size), // signed: body must be exactly this long
	}, s3.WithPresignExpires(in.Expires))
	if err != nil {
		return nil, fmt.Errorf("presign put object: %w", err)
	}

	return &model.PresignedRequest{
		URL:     req.URL,
		Headers: map[string]string{"Content-Type": in.ContentType},
	}, nil
}

// PresignGet returns a time-limited URL the browser can open directly.
func (s *S3Storage) PresignGet(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", fmt.Errorf("presign get object: %w", err)
	}
	return req.URL, nil
}

// DeleteObject permanently removes one object. Supabase has no versioning,
// so this cannot be undone.
func (s *S3Storage) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}
