package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	App      AppConfig
	Database DatabaseConfig
	SSO      SSOConfig
	Storage  StorageConfig
	AI       AIConfig
	Caption  CaptionConfig
}

type AppConfig struct {
	Name               string
	Env                string
	Port               string
	CORSAllowedOrigins []string
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	ShutdownTimeout    time.Duration
}

func (a AppConfig) IsProduction() bool { return a.Env == "production" }

type DatabaseConfig struct {
	URL      string
	MaxConns int
	MinConns int
}

type SSOConfig struct {
	BaseURL       string
	VerifyTimeout time.Duration
}

type StorageConfig struct {
	Endpoint            string
	Region              string
	BucketJobs          string
	BucketReleases      string
	AccessKeyID         string
	SecretAccessKey     string
	PresignExpiry       time.Duration
	MaxJobFileBytes     int64
	MaxReleaseFileBytes int64
	DownloadExpiry      time.Duration
}

type AIConfig struct {
	BaseURL string // OpenAI-compatible endpoint, e.g. http://localhost:20128/v1
	APIKey  string // optional
	Model   string // model id or 9Router combo name
	Timeout time.Duration
}

// Enabled reports whether caption generation is configured.
func (a AIConfig) Enabled() bool { return a.BaseURL != "" }

// Load reads .env (if present) plus the real environment and returns a
// validated Config. Malformed values are reported, never silently defaulted.
func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	r := &envReader{}

	cfg := &Config{
		App: AppConfig{
			Name:               r.str("APP_NAME", "kompu-backend"),
			Env:                strings.ToLower(r.str("APP_ENV", "development")),
			Port:               r.str("PORT", "8080"),
			CORSAllowedOrigins: r.list("CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173"), // 5173 = kompu-web dev server
			ReadTimeout:        r.duration("READ_TIMEOUT", 15*time.Second),
			WriteTimeout:       r.duration("WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:        r.duration("IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout:    r.duration("SHUTDOWN_TIMEOUT", 10*time.Second),
		},
		Database: DatabaseConfig{
			URL:      r.str("DATABASE_URL", ""),
			MaxConns: r.int("DB_MAX_CONNS", 10),
			MinConns: r.int("DB_MIN_CONNS", 2),
		},
		SSO: SSOConfig{
			BaseURL:       strings.TrimRight(r.str("SSO_BASE_URL", ""), "/"),
			VerifyTimeout: r.duration("SSO_VERIFY_TIMEOUT", 5*time.Second),
		},
		Storage: StorageConfig{
			Endpoint:            strings.TrimRight(r.str("STORAGE_ENDPOINT", ""), "/"),
			Region:              r.str("STORAGE_REGION", ""),
			BucketJobs:          r.str("STORAGE_BUCKET_JOBS", ""),
			BucketReleases:      r.str("STORAGE_BUCKET_RELEASES", ""),
			AccessKeyID:         r.str("STORAGE_ACCESS_KEY_ID", ""),
			SecretAccessKey:     r.str("STORAGE_SECRET_ACCESS_KEY", ""),
			PresignExpiry:       r.duration("STORAGE_PRESIGN_EXPIRY", 15*time.Minute),
			MaxJobFileBytes:     r.int64("MAX_JOB_FILE_BYTES", 10*1024*1024),
			MaxReleaseFileBytes: r.int64("MAX_RELEASE_FILE_BYTES", 50*1024*1024),
			DownloadExpiry:      r.duration("STORAGE_DOWNLOAD_EXPIRY", time.Hour),
		},
		AI: AIConfig{
			BaseURL: strings.TrimRight(r.str("AI_BASE_URL", ""), "/"),
			APIKey:  r.str("AI_API_KEY", ""),
			Model:   r.str("AI_MODEL", ""),
			Timeout: r.duration("AI_TIMEOUT", 45*time.Second),
		},
		Caption: CaptionConfig{
			SkillsFile: r.str("CAPTION_SKILLS_FILE", "skills.md"),
		},
	}

	if err := errors.Join(r.errs...); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var errs []error

	if c.App.Env != "development" && c.App.Env != "production" {
		errs = append(errs, fmt.Errorf("APP_ENV must be 'development' or 'production', got %q", c.App.Env))
	}
	if p, err := strconv.Atoi(c.App.Port); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be a number between 1 and 65535, got %q", c.App.Port))
	}
	if c.Database.MinConns > c.Database.MaxConns {
		errs = append(errs, errors.New("DB_MIN_CONNS must not exceed DB_MAX_CONNS"))
	}

	// The database is required from Phase 2 onward.
	if c.Database.URL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}

	// Secrets are optional while developing phase by phase,
	// but mandatory in production.
	if c.App.IsProduction() {
		if c.Database.URL == "" {
			errs = append(errs, errors.New("DATABASE_URL is required in production"))
		}
		// Central SSO is required: without it every mutation endpoint would be unprotected.
		if c.SSO.BaseURL == "" {
			errs = append(errs, errors.New("SSO_BASE_URL is required"))
		} else if u, err := url.Parse(c.SSO.BaseURL); err != nil ||
			(u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("SSO_BASE_URL must be an http(s) URL, got %q", c.SSO.BaseURL))
		} else if c.App.IsProduction() && u.Scheme != "https" {
			errs = append(errs, errors.New("SSO_BASE_URL must use https in production (bearer tokens are sent to it)"))
		}
	}

	// Object storage is required from Phase 4 onward.
	for _, f := range []struct{ name, val string }{
		{"STORAGE_ENDPOINT", c.Storage.Endpoint},
		{"STORAGE_REGION", c.Storage.Region},
		{"STORAGE_BUCKET_JOBS", c.Storage.BucketJobs},
		{"STORAGE_BUCKET_RELEASES", c.Storage.BucketReleases},
		{"STORAGE_ACCESS_KEY_ID", c.Storage.AccessKeyID},
		{"STORAGE_SECRET_ACCESS_KEY", c.Storage.SecretAccessKey},
	} {
		if f.val == "" {
			errs = append(errs, fmt.Errorf("%s is required", f.name))
		}
	}
	if c.Storage.Endpoint != "" {
		if u, err := url.Parse(c.Storage.Endpoint); err != nil ||
			(u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("STORAGE_ENDPOINT must be an http(s) URL, got %q", c.Storage.Endpoint))
		}
	}
	if c.Storage.MaxJobFileBytes <= 0 || c.Storage.MaxReleaseFileBytes <= 0 {
		errs = append(errs, errors.New("MAX_JOB_FILE_BYTES and MAX_RELEASE_FILE_BYTES must be positive"))
	}
	if c.Storage.PresignExpiry <= 0 || c.Storage.PresignExpiry > time.Hour {
		errs = append(errs, errors.New("STORAGE_PRESIGN_EXPIRY must be between 1s and 1h"))
	}

	if c.Storage.DownloadExpiry <= 0 || c.Storage.DownloadExpiry > 24*time.Hour {
		errs = append(errs, errors.New("STORAGE_DOWNLOAD_EXPIRY must be between 1s and 24h"))
	}

	if c.AI.APIKey != "" && !c.AI.Enabled() {
		errs = append(errs, errors.New("AI_BASE_URL is required when AI_API_KEY is set"))
	}
	if c.AI.Enabled() {
		if c.AI.Model == "" {
			errs = append(errs, errors.New("AI_MODEL is required when AI_BASE_URL is set"))
		}
		if u, err := url.Parse(c.AI.BaseURL); err != nil ||
			(u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("AI_BASE_URL must be an http(s) URL, got %q", c.AI.BaseURL))
		}
		if c.AI.Timeout <= 0 || c.AI.Timeout > 5*time.Minute {
			errs = append(errs, errors.New("AI_TIMEOUT must be between 1s and 5m"))
		}
	}

	return errors.Join(errs...)
}

// ---------- env helpers ----------

type envReader struct{ errs []error }

func (r *envReader) str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func (r *envReader) int(key string, def int) int {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s must be an integer, got %q", key, v))
		return def
	}
	return n
}

func (r *envReader) int64(key string, def int64) int64 {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s must be an integer, got %q", key, v))
		return def
	}
	return n
}

func (r *envReader) duration(key string, def time.Duration) time.Duration {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s must be a duration like '15s' or '10m', got %q", key, v))
		return def
	}
	return d
}

func (r *envReader) list(key, def string) []string {
	raw := r.str(key, def)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type CaptionConfig struct {
	SkillsFile string
}
