package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kompu-backend/internal/model"
)

// OneDriveStorage keeps uploaded files in the OneDrive of the user who granted
// the delegated refresh token (the same Microsoft app and account SILAPET
// uses), under Root, at the same object keys the S3 adapter used. Browsers
// upload straight to a Graph upload session; downloads go through the API's
// /files redirect so list endpoints never wait on Graph.
type OneDriveStorage struct {
	cfg    OneDriveConfig
	db     *pgxpool.Pool // nil in tests: the env refresh token is used alone
	client *http.Client

	mu        sync.Mutex
	token     string
	expires   time.Time
	urlMu     sync.Mutex
	urlCache  map[string]cachedLink
	loginBase string
	graphBase string
}

type OneDriveConfig struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	RefreshToken string // seed / fallback; the rotated token lives in public.graph_tokens
	Root         string // e.g. "/db/kompu"
}

type cachedLink struct {
	url string
	exp time.Time
}

const (
	oneDriveScope   = "offline_access Files.ReadWrite"
	oneDriveTokenID = "kompu-onedrive"
	// Graph download links live about 1 h.
	downloadLinkTTL = 30 * time.Minute
	// FilesRoute is where the API serves the download redirect for a key.
	FilesRoute = "/api/v1/files/"
)

func NewOneDriveStorage(cfg OneDriveConfig, db *pgxpool.Pool) (*OneDriveStorage, error) {
	if cfg.TenantID == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("onedrive storage: MS_TENANT_ID, MS_CLIENT_ID and MS_CLIENT_SECRET are required")
	}
	cfg.Root = "/" + strings.Trim(cfg.Root, "/")
	return &OneDriveStorage{
		cfg:       cfg,
		db:        db,
		client:    &http.Client{Timeout: 60 * time.Second},
		urlCache:  map[string]cachedLink{},
		loginBase: "https://login.microsoftonline.com",
		graphBase: "https://graph.microsoft.com/v1.0",
	}, nil
}

// PresignPut opens a Graph upload session. Its URL is pre-authenticated, so
// the browser sends the whole file in one PUT with a Content-Range header
// (Graph allows up to 60 MiB per request; Kompu's limit is 50 MB).
// shortcut: Graph does not bind the session to the declared size, so the size
// limit is checked on the declared size only; verify item size on save if abused.
func (s *OneDriveStorage) PresignPut(ctx context.Context, in model.PresignPutInput) (*model.PresignedRequest, error) {
	if in.Size <= 0 {
		return nil, errors.New("onedrive: size must be positive")
	}
	var session struct {
		UploadURL string `json:"uploadUrl"`
	}
	body := strings.NewReader(`{"item":{"@microsoft.graph.conflictBehavior":"fail"}}`)
	if err := s.call(ctx, http.MethodPost, s.itemPath(in.Key)+":/createUploadSession", "application/json", body, &session); err != nil {
		return nil, fmt.Errorf("create upload session: %w", err)
	}
	if !strings.HasPrefix(session.UploadURL, "https://") {
		return nil, errors.New("create upload session: no upload URL")
	}
	return &model.PresignedRequest{
		URL:     session.UploadURL,
		Headers: map[string]string{"Content-Range": fmt.Sprintf("bytes 0-%d/%d", in.Size-1, in.Size)},
	}, nil
}

// DownloadURL resolves key to Graph's short-lived pre-authenticated link.
func (s *OneDriveStorage) DownloadURL(ctx context.Context, key string) (string, error) {
	if !model.ValidObjectKey(key) {
		return "", errors.New("invalid object key")
	}
	s.urlMu.Lock()
	c, ok := s.urlCache[key]
	s.urlMu.Unlock()
	if ok && time.Now().Before(c.exp) {
		return c.url, nil
	}

	var item struct {
		DownloadURL string `json:"@microsoft.graph.downloadUrl"`
	}
	if err := s.call(ctx, http.MethodGet, s.itemPath(key), "", nil, &item); err != nil {
		return "", err
	}
	if u, err := url.Parse(item.DownloadURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("graph returned no download URL")
	}
	s.urlMu.Lock()
	s.urlCache[key] = cachedLink{url: item.DownloadURL, exp: time.Now().Add(downloadLinkTTL)}
	s.urlMu.Unlock()
	return item.DownloadURL, nil
}

// DeleteObject removes the file (OneDrive keeps it in its recycle bin).
// A missing file counts as deleted.
func (s *OneDriveStorage) DeleteObject(ctx context.Context, key string) error {
	s.urlMu.Lock()
	delete(s.urlCache, key)
	s.urlMu.Unlock()
	err := s.call(ctx, http.MethodDelete, s.itemPath(key), "", nil, nil)
	if errors.Is(err, errGraphNotFound) {
		return nil
	}
	return err
}

var imageTypes = map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}

// Thumbnail streams a report-sized image (at most 1280 px), falling back to
// the original when Graph has no thumbnail. Only image keys are served, with
// a type fixed by the extension, so an upload cannot run as a page here.
func (s *OneDriveStorage) Thumbnail(ctx context.Context, key string) (io.ReadCloser, string, error) {
	contentType, ok := imageTypes[path.Ext(key)]
	if !ok || !model.ValidObjectKey(key) {
		return nil, "", errors.New("not an image key")
	}
	tok, err := s.accessToken(ctx)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.graphBase+s.itemPath(key)+":/thumbnails/0/c1280x1280/content", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := s.client.Do(req) // Go drops Authorization on the cross-host redirect
	if err == nil && resp.StatusCode == http.StatusOK {
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "image/jpeg") {
			contentType = "image/jpeg"
		}
		return resp.Body, contentType, nil
	}
	if err == nil {
		resp.Body.Close()
	}

	link, err := s.DownloadURL(ctx, key)
	if err != nil {
		return nil, "", err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err = s.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download original: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf("download original: HTTP %d", resp.StatusCode)
	}
	return resp.Body, contentType, nil
}

var errGraphNotFound = errors.New("graph: not found")

func (s *OneDriveStorage) itemPath(key string) string {
	parts := strings.Split(strings.Trim(s.cfg.Root+"/"+key, "/"), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return "/me/drive/root:/" + strings.Join(parts, "/")
}

// call sends one Graph request and decodes the JSON reply into out (if set).
func (s *OneDriveStorage) call(ctx context.Context, method, target, contentType string, body io.Reader, out any) error {
	tok, err := s.accessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, s.graphBase+target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("graph request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		s.mu.Lock()
		s.token = ""
		s.mu.Unlock()
	}
	if resp.StatusCode == http.StatusNotFound {
		return errGraphNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("graph %s: HTTP %d", method, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(out)
}

// accessToken returns a cached token, redeeming the refresh token under the
// lock. Graph rotates the refresh token; the newest is saved to the DB.
func (s *OneDriveStorage) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.expires) {
		return s.token, nil
	}

	stored := s.storedRefreshToken(ctx)
	candidates := []string{}
	if stored != "" {
		candidates = append(candidates, stored)
	}
	if s.cfg.RefreshToken != "" && s.cfg.RefreshToken != stored {
		candidates = append(candidates, s.cfg.RefreshToken)
	}

	lastErr := errors.New("no Microsoft Graph refresh token (set MS_REFRESH_TOKEN)")
	for _, rt := range candidates {
		tr, err := s.redeem(ctx, rt)
		if err != nil {
			lastErr = err
			continue
		}
		s.token = tr.AccessToken
		s.expires = time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - 5*time.Minute)
		if tr.RefreshToken != "" && tr.RefreshToken != stored && s.db != nil {
			if _, err := s.db.Exec(ctx, `
INSERT INTO public.graph_tokens (id, refresh_token, updated_at) VALUES ($1, $2, now())
ON CONFLICT (id) DO UPDATE SET refresh_token = EXCLUDED.refresh_token, updated_at = now()`,
				oneDriveTokenID, tr.RefreshToken); err != nil {
				log.Printf("[onedrive] save refresh token failed: %v", err)
			}
		}
		return s.token, nil
	}
	log.Printf("[onedrive] sign-in needed: %v; renew MS_REFRESH_TOKEN", lastErr)
	return "", lastErr
}

func (s *OneDriveStorage) storedRefreshToken(ctx context.Context) string {
	if s.db == nil {
		return ""
	}
	var rt string
	err := s.db.QueryRow(ctx, `SELECT refresh_token FROM public.graph_tokens WHERE id = $1`, oneDriveTokenID).Scan(&rt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[onedrive] load refresh token failed: %v", err)
	}
	return strings.TrimSpace(rt)
}

type graphToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (s *OneDriveStorage) redeem(ctx context.Context, refresh string) (*graphToken, error) {
	form := url.Values{
		"client_id":     {s.cfg.ClientID},
		"client_secret": {s.cfg.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"scope":         {oneDriveScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.loginBase+"/"+url.PathEscape(s.cfg.TenantID)+"/oauth2/v2.0/token",
		bytes.NewReader([]byte(form.Encode())))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph token request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("graph token refresh: HTTP %d", resp.StatusCode)
	}
	var tr graphToken
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&tr); err != nil || tr.AccessToken == "" {
		return nil, errors.New("graph token refresh returned no access token")
	}
	return &tr, nil
}
