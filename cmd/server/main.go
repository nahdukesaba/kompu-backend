package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/jackc/pgx/v5/pgxpool"

	"kompu-backend/internal/config"
	"kompu-backend/internal/database"
	"kompu-backend/internal/handler"
	"kompu-backend/internal/repository"
	"kompu-backend/internal/service"
	"kompu-backend/pkg/middleware"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	dbCtx, cancelDB := context.WithTimeout(context.Background(), 15*time.Second)
	pool, err := database.New(dbCtx, cfg.Database)
	cancelDB()
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer pool.Close()

	routes, err := newRoutes(cfg, pool)
	if err != nil {
		log.Fatalf("setup error: %v", err)
	}

	app := fiber.New(fiber.Config{
		AppName:               cfg.App.Name,
		ErrorHandler:          handler.ErrorHandler,
		ReadTimeout:           cfg.App.ReadTimeout,
		WriteTimeout:          cfg.App.WriteTimeout,
		IdleTimeout:           cfg.App.IdleTimeout,
		DisableStartupMessage: cfg.App.IsProduction(),
	})

	// Global middleware (order matters: recover first, then request id, then logger).
	app.Use(fiberrecover.New())
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format: "[${time}] ${locals:requestid} ${status} ${latency} ${method} ${path}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: strings.Join(cfg.App.CORSAllowedOrigins, ","),
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization",
	}))

	sso := middleware.SSOAuth(middleware.NewCentralVerifier(cfg.SSO.BaseURL, cfg.SSO.VerifyTimeout))
	handler.RegisterRoutes(app, routes, sso, captionRateLimit())

	// Graceful shutdown on Ctrl+C / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- app.Listen(":" + cfg.App.Port)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			log.Fatalf("server error: %v", err)
		}
	case <-ctx.Done():
		log.Println("shutdown signal received, draining connections...")
		if err := app.ShutdownWithTimeout(cfg.App.ShutdownTimeout); err != nil {
			log.Fatalf("graceful shutdown failed: %v", err)
		}
		log.Println("server stopped cleanly")
	}
}

// newRoutes builds every repository, service and handler.
func newRoutes(cfg *config.Config, pool *pgxpool.Pool) (handler.Routes, error) {
	objectStorage, err := repository.NewS3Storage(repository.S3StorageConfig{
		Endpoint:        cfg.Storage.Endpoint,
		Region:          cfg.Storage.Region,
		AccessKeyID:     cfg.Storage.AccessKeyID,
		SecretAccessKey: cfg.Storage.SecretAccessKey,
	})
	if err != nil {
		return handler.Routes{}, fmt.Errorf("storage: %w", err)
	}
	storageService := service.NewStorageService(objectStorage, service.StorageServiceConfig{
		BucketJobs:      cfg.Storage.BucketJobs,
		BucketReleases:  cfg.Storage.BucketReleases,
		MaxJobBytes:     cfg.Storage.MaxJobFileBytes,
		MaxReleaseBytes: cfg.Storage.MaxReleaseFileBytes,
		PresignExpiry:   cfg.Storage.PresignExpiry,
		DownloadExpiry:  cfg.Storage.DownloadExpiry,
	})

	jobService := service.NewJobService(repository.NewJobRepository(pool), storageService)
	releaseService := service.NewReleaseService(repository.NewReleaseRepository(pool))

	// Caption generation stays switched off (503) unless an AI endpoint is configured.
	var generator service.TextGenerator
	if cfg.AI.Enabled() {
		g, err := repository.NewOpenAICompatGenerator(repository.OpenAICompatConfig{
			BaseURL: cfg.AI.BaseURL,
			APIKey:  cfg.AI.APIKey,
			Model:   cfg.AI.Model,
			Timeout: cfg.AI.Timeout,
		})
		if err != nil {
			return handler.Routes{}, fmt.Errorf("caption AI: %w", err)
		}
		generator = g
	} else {
		log.Println("AI_BASE_URL not set: caption generation is disabled")
	}

	guideline := repository.NewFileGuideline(cfg.Caption.SkillsFile)
	if guideline.Exists() {
		log.Printf("caption guideline: using %s (re-read on every request)", guideline.Path())
	} else {
		log.Printf("caption guideline: %s not found, captions use the built-in style", guideline.Path())
	}
	captionService := service.NewCaptionService(generator, guideline)

	return handler.Routes{
		Health:   handler.NewHealthHandler(cfg.App.Name, cfg.App.Env, pool),
		Uploads:  handler.NewUploadHandler(storageService),
		Jobs:     handler.NewJobHandler(jobService, storageService),
		Releases: handler.NewReleaseHandler(releaseService, storageService),
		Captions: handler.NewCaptionHandler(captionService),
	}, nil
}

// captionRateLimit allows 10 caption requests per minute per user (the AI is a
// shared, limited resource).
func captionRateLimit() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        10,
		Expiration: time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			if p, ok := middleware.PrincipalFrom(c); ok {
				return p.UserID
			}
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "too many caption requests, try again in a minute")
		},
	})
}
