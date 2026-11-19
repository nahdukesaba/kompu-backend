// copy-files copies every file referenced by kompu_jobs.evidence_files and
// kompu_releases.attachment_urls (deleted rows included) from Supabase
// Storage (S3) to OneDrive, at the same object key under ONEDRIVE_ROOT.
// Keys do not change, so no database row is updated. Supabase copies are
// left in place. Dry run by default; pass -apply to copy.
//
//	go run ./cmd/copy-files          # list what would be copied
//	go run ./cmd/copy-files -apply   # copy; files already in OneDrive are skipped
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"kompu-backend/internal/config"
	"kompu-backend/internal/database"
	"kompu-backend/internal/repository"
)

func main() {
	apply := flag.Bool("apply", false, "copy the files (default: dry run)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	if !cfg.Storage.OneDriveEnabled() {
		log.Fatal("MS_TENANT_ID, MS_CLIENT_ID and MS_CLIENT_SECRET must be set")
	}
	ctx := context.Background()

	pool, err := database.New(ctx, cfg.Database)
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer pool.Close()

	s3, err := repository.NewS3Storage(repository.S3StorageConfig{
		Endpoint:        cfg.Storage.Endpoint,
		Region:          cfg.Storage.Region,
		AccessKeyID:     cfg.Storage.AccessKeyID,
		SecretAccessKey: cfg.Storage.SecretAccessKey,
	})
	if err != nil {
		log.Fatalf("s3: %v", err)
	}
	od, err := repository.NewOneDriveStorage(repository.OneDriveConfig{
		TenantID:     cfg.Storage.MSTenantID,
		ClientID:     cfg.Storage.MSClientID,
		ClientSecret: cfg.Storage.MSClientSecret,
		RefreshToken: cfg.Storage.MSRefreshToken,
		Root:         cfg.Storage.OneDriveRoot,
	}, pool)
	if err != nil {
		log.Fatalf("onedrive: %v", err)
	}

	rows, err := pool.Query(ctx, `
SELECT f->>'key' FROM public.kompu_jobs j, jsonb_array_elements(j.evidence_files) f
UNION
SELECT f->>'key' FROM public.kompu_releases r, jsonb_array_elements(r.attachment_urls) f
ORDER BY 1`)
	if err != nil {
		log.Fatalf("list keys: %v", err)
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			log.Fatalf("scan: %v", err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Fatalf("list keys: %v", err)
	}

	var copied, skipped, failed int
	hc := &http.Client{Timeout: 5 * time.Minute}
	for _, key := range keys {
		bucket := cfg.Storage.BucketJobs
		if strings.HasPrefix(key, "releases/") {
			bucket = cfg.Storage.BucketReleases
		}
		if !repository.ValidObjectKey(key) {
			log.Printf("SKIP  %s (not a Kompu upload key)", key)
			skipped++
			continue
		}
		if !*apply {
			fmt.Printf("would copy %s/%s -> %s/%s\n", bucket, key, cfg.Storage.OneDriveRoot, key)
			continue
		}
		switch err := copyOne(ctx, hc, s3, od, bucket, key); {
		case err == nil:
			fmt.Printf("OK    %s\n", key)
			copied++
		case errors.Is(err, repository.ErrObjectExists):
			fmt.Printf("EXIST %s\n", key)
			skipped++
		default:
			fmt.Printf("FAIL  %s: %v\n", key, err)
			failed++
		}
	}
	fmt.Printf("%d keys: %d copied, %d skipped, %d failed (apply=%v)\n", len(keys), copied, skipped, failed, *apply)
	if failed > 0 {
		log.Fatal("some files failed; rerun with -apply (copied files are skipped)")
	}
}

func copyOne(ctx context.Context, hc *http.Client, s3 *repository.S3Storage, od *repository.OneDriveStorage, bucket, key string) error {
	src, err := s3.PresignGet(ctx, bucket, key, 15*time.Minute)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength <= 0 {
		return fmt.Errorf("download: HTTP %d, length %d", resp.StatusCode, resp.ContentLength)
	}
	return od.Upload(ctx, key, resp.Body, resp.ContentLength)
}
