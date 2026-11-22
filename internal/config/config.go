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
	Session  SessionConfig
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

// SSOConfig is the browser-SSO client registration with sso-balai
// (authorization code + PKCE S256, see docs/sso.md).
type SSOConfig struct {
	BaseURL               string
	VerifyTimeout         time.Duration // timeout for every server-to-server call to sso-balai
	ClientID              string
	RedirectURI           string // exact match with sso-balai's allowlist
	PostLogoutRedirectURI string // exact match with sso-balai's allowlist
	FlowHMACKey           string // base64, >= 32 bytes; derives the PKCE verifier
	FlowTTL               time.Duration
	IntrospectionSecret   string // >= 32 chars, equal to sso-balai's SSO_KOMPU_INTROSPECTION_SECRET
}

// SessionConfig is the encrypted HttpOnly app-session cookie.
type SessionConfig struct {
	Key          string // base64 of exactly 32 bytes (AES-256-GCM)
	TTL          time.Duration
	CookieSecure bool
}

type StorageConfig struct {
	PresignExpiry time.Duration
	MaxFileBytes  int64

	// OneDrive (Microsoft Graph, delegated refresh token). Required in
	// production; without it, development runs with uploads switched off.
	MSTenantID     string
	MSClientID     string
	MSClientSecret string
	MSRefreshToken string
	OneDriveRoot   string
}

// OneDriveEnabled reports whether uploads go to OneDrive.
func (s StorageConfig) OneDriveEnabled() bool {
	return s.MSTenantID != "" && s.MSClientID != "" && s.MSClientSecret != ""
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
			CORSAllowedOrigins: r.list("CORS_ALLOWED_ORIGINS", "http://kompu.test:8084"), // kompu-web dev server, as registered in sso-balai
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
			BaseURL:               strings.TrimRight(r.str("SSO_BASE_URL", ""), "/"),
			VerifyTimeout:         r.duration("SSO_VERIFY_TIMEOUT", 10*time.Second),
			ClientID:              r.str("SSO_CLIENT_ID", ""),
			RedirectURI:           r.str("SSO_REDIRECT_URI", ""),
			PostLogoutRedirectURI: r.str("SSO_POST_LOGOUT_REDIRECT_URI", ""),
			FlowHMACKey:           r.str("SSO_FLOW_HMAC_KEY", ""),
			FlowTTL:               r.duration("SSO_FLOW_TTL", 5*time.Minute),
			IntrospectionSecret:   r.str("SSO_INTROSPECTION_SECRET", ""),
		},
		Session: SessionConfig{
			Key:          r.str("APP_SESSION_KEY", ""),
			TTL:          r.duration("APP_SESSION_TTL", 8*time.Hour),
			CookieSecure: r.str("APP_SESSION_COOKIE_SECURE", "true") == "true",
		},
		Storage: StorageConfig{
			PresignExpiry:  r.duration("STORAGE_PRESIGN_EXPIRY", 15*time.Minute),
			MaxFileBytes:   r.int64("MAX_UPLOAD_BYTES", 50*1024*1024),
			MSTenantID:     r.str("MS_TENANT_ID", ""),
			MSClientID:     r.str("MS_CLIENT_ID", ""),
			MSClientSecret: r.str("MS_CLIENT_SECRET", ""),
			MSRefreshToken: r.str("MS_REFRESH_TOKEN", ""),
			OneDriveRoot:   r.str("ONEDRIVE_ROOT", "/db/kompu"),
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
			errs = append(errs, errors.New("SSO_BASE_URL must use https in production (tokens are sent to it)"))
		}
		for _, f := range []struct{ name, val string }{
			{"SSO_CLIENT_ID", c.SSO.ClientID},
			{"SSO_REDIRECT_URI", c.SSO.RedirectURI},
			{"SSO_POST_LOGOUT_REDIRECT_URI", c.SSO.PostLogoutRedirectURI},
			{"SSO_FLOW_HMAC_KEY", c.SSO.FlowHMACKey},
			{"SSO_INTROSPECTION_SECRET", c.SSO.IntrospectionSecret},
			{"APP_SESSION_KEY", c.Session.Key},
			{"MS_TENANT_ID", c.Storage.MSTenantID},
			{"MS_CLIENT_ID", c.Storage.MSClientID},
			{"MS_CLIENT_SECRET", c.Storage.MSClientSecret},
		} {
			if f.val == "" {
				errs = append(errs, fmt.Errorf("%s is required in production", f.name))
			}
		}
		if !c.Session.CookieSecure {
			errs = append(errs, errors.New("APP_SESSION_COOKIE_SECURE must be true in production"))
		}
	}
	// Short secrets make every SSO request fail; report it at boot instead.
	if s := c.SSO.IntrospectionSecret; s != "" && len(s) < 32 {
		errs = append(errs, errors.New("SSO_INTROSPECTION_SECRET must be at least 32 characters"))
	}
	if c.SSO.FlowTTL < time.Minute || c.SSO.FlowTTL > 30*time.Minute {
		errs = append(errs, errors.New("SSO_FLOW_TTL must be between 1m and 30m"))
	}
	if c.Session.TTL < time.Minute {
		errs = append(errs, errors.New("APP_SESSION_TTL must be at least 1m"))
	}

	// Uploads are one PUT to a Graph upload session, limited to 60 MiB.
	if c.Storage.MaxFileBytes <= 0 || c.Storage.MaxFileBytes > 60*1024*1024 {
		errs = append(errs, errors.New("MAX_UPLOAD_BYTES must be between 1 and 62914560"))
	}
	if c.Storage.PresignExpiry <= 0 || c.Storage.PresignExpiry > time.Hour {
		errs = append(errs, errors.New("STORAGE_PRESIGN_EXPIRY must be between 1s and 1h"))
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
