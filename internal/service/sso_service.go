package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kompu-backend/internal/model"
)

// Browser SSO with sso-balai: authorization code + PKCE S256, confidential
// backend, session kept in an encrypted HttpOnly cookie. See docs/sso.md.

// SessionCookieName is the app-session cookie. It is also the AES-GCM AAD.
const SessionCookieName = "kompu-session"

// AuthError is an expected authentication outcome. Reason is a stable code the
// frontend matches on; ClearSession tells the handler to delete the cookie.
type AuthError struct {
	Status       int
	Reason       string
	Message      string
	ClearSession bool
}

func (e *AuthError) Error() string { return e.Message }

// SSOStore is the persistence the SSO flow needs (see repository.SSORepository).
type SSOStore interface {
	CreateFlow(ctx context.Context, stateHash []byte, expiresAt time.Time) error
	ConsumeFlow(ctx context.Context, stateHash []byte) (bool, error)
	Member(ctx context.Context, appUserID, sessionID string) (*model.Member, error)
}

type SSOConfig struct {
	BaseURL               string
	ClientID              string
	RedirectURI           string
	PostLogoutRedirectURI string
	FlowHMACKey           string
	FlowTTL               time.Duration
	IntrospectionSecret   string
	Timeout               time.Duration
	SessionKey            string // base64 of exactly 32 bytes
	SessionTTL            time.Duration
}

type SSOService struct {
	cfg    SSOConfig
	store  SSOStore
	client *http.Client
	aead   cipher.AEAD
	now    func() time.Time
}

func NewSSOService(cfg SSOConfig, store SSOStore) (*SSOService, error) {
	key, err := decodeKey(cfg.SessionKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("APP_SESSION_KEY must be a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("session cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("session cipher: %w", err)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &SSOService{
		cfg:    cfg,
		store:  store,
		client: &http.Client{Timeout: cfg.Timeout},
		aead:   aead,
		now:    time.Now,
	}, nil
}

// Login is the result of a code exchange or a refresh: either a pending
// membership, or a new sealed cookie value for the member.
type Login struct {
	Pending bool
	Cookie  string
	Member  *model.Member
}

// sessionData is what the cookie holds. The browser never sees it.
type sessionData struct {
	AppUserID       string `json:"appUserId"`
	Role            string `json:"role"`
	SID             string `json:"sid"`       // Central browser session, checked by introspection
	SessionID       string `json:"sessionId"` // Supabase auth.sessions id of the access token
	AccessToken     string `json:"accessToken"`
	RefreshToken    string `json:"refreshToken"`
	AccessExpiresAt int64  `json:"accessExpiresAt"`
	ExpiresAt       int64  `json:"expiresAt"`
}

// ---------- start / logout ----------

// Start creates a single-use flow and returns the sso-balai /authorize URL.
func (s *SSOService) Start(ctx context.Context) (string, error) {
	key, err := s.flowKey()
	if err != nil {
		return "", ssoUnavailable()
	}
	state, err := randomValue(32)
	if err != nil {
		return "", ssoUnavailable()
	}
	if err := s.store.CreateFlow(ctx, hashOf(state), s.now().Add(s.cfg.FlowTTL)); err != nil {
		return "", ssoUnavailable()
	}
	challenge := sha256.Sum256([]byte(pkceVerifier(key, state)))

	u, err := url.Parse(s.cfg.BaseURL + "/authorize")
	if err != nil {
		return "", ssoUnavailable()
	}
	q := u.Query()
	q.Set("client_id", s.cfg.ClientID)
	q.Set("redirect_uri", s.cfg.RedirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// LogoutURL returns sso-balai's logout confirmation URL. cancelReturn is kept
// only when it is a plain relative path.
func (s *SSOService) LogoutURL(cancelReturn string) (string, error) {
	u, err := url.Parse(s.cfg.BaseURL + "/logout")
	if err != nil || u.Host == "" || s.cfg.ClientID == "" || s.cfg.PostLogoutRedirectURI == "" {
		return "", ssoUnavailable()
	}
	q := u.Query()
	q.Set("client_id", s.cfg.ClientID)
	q.Set("post_logout_redirect_uri", s.cfg.PostLogoutRedirectURI)
	q.Set("immediate", "1")
	if v := sanitizeReturnPath(cancelReturn); v != "" {
		q.Set("cancel_return", v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func sanitizeReturnPath(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.ContainsAny(v, "\\#") {
		return ""
	}
	for i := 0; i < len(v); i++ {
		if v[i] <= ' ' || v[i] == 0x7f {
			return ""
		}
	}
	return v
}

// ---------- callback exchange ----------

// Complete consumes the state once, then exchanges the code with sso-balai and
// checks the Kompu membership. status is sso-balai's pending/denied result.
func (s *SSOService) Complete(ctx context.Context, state, code, status string) (*Login, error) {
	state, code = strings.TrimSpace(state), strings.TrimSpace(code)
	status = strings.ToLower(strings.TrimSpace(status))

	key, err := s.flowKey()
	if err != nil {
		return nil, ssoUnavailable()
	}
	if state == "" {
		return nil, invalidState()
	}
	ok, err := s.store.ConsumeFlow(ctx, hashOf(state))
	if err != nil {
		return nil, ssoUnavailable()
	}
	if !ok {
		return nil, invalidState()
	}

	switch status {
	case "":
	case "pending":
		return &Login{Pending: true}, nil
	case "denied":
		return nil, &AuthError{Status: http.StatusForbidden, Reason: "ACCOUNT_REJECTED", Message: "Kompu access is not available for this account"}
	default:
		return nil, &AuthError{Status: http.StatusBadRequest, Reason: "INVALID_SSO_RESULT", Message: "invalid SSO result"}
	}
	if code == "" {
		return nil, &AuthError{Status: http.StatusBadRequest, Reason: "INVALID_SSO_RESULT", Message: "authorization code is required"}
	}

	var central centralSession
	if err := s.central(ctx, "/api/v1/auth/sso/exchange", map[string]string{
		"code":         code,
		"clientId":     s.cfg.ClientID,
		"redirectUri":  s.cfg.RedirectURI,
		"codeVerifier": pkceVerifier(key, state),
	}, "", &central); err != nil {
		return nil, err
	}
	return s.login(ctx, &central, central.SID)
}

// ---------- per-request check and refresh ----------

// Authenticate validates the cookie on a protected request: the Central
// session must be active (introspection), the access token unexpired, the
// Supabase session alive and the Kompu membership approved with the same role.
// Every failure is fail-closed.
func (s *SSOService) Authenticate(ctx context.Context, cookie string) (*model.Member, error) {
	d, err := s.activeSession(ctx, cookie)
	if err != nil {
		return nil, err
	}
	if s.now().Unix() >= d.AccessExpiresAt {
		// Keep the cookie: the client refreshes and retries.
		return nil, &AuthError{Status: http.StatusUnauthorized, Reason: "TOKEN_EXPIRED", Message: "access token expired"}
	}
	m, err := s.store.Member(ctx, d.AppUserID, d.SessionID)
	if err != nil {
		return nil, err
	}
	if err := memberAllowed(m); err != nil {
		return nil, err
	}
	if !m.SessionValid {
		return nil, &AuthError{Status: http.StatusUnauthorized, Reason: "SESSION_REVOKED", Message: "session was revoked", ClearSession: true}
	}
	if m.Role != d.Role {
		return nil, &AuthError{Status: http.StatusUnauthorized, Reason: "TOKEN_AUTHORIZATION_STALE", Message: "application session authorization is stale"}
	}
	return m, nil
}

// Refresh rotates the Central tokens, re-checks the membership and keeps the
// same sid.
func (s *SSOService) Refresh(ctx context.Context, cookie string) (*Login, error) {
	d, err := s.activeSession(ctx, cookie)
	if err != nil {
		return nil, err
	}
	var central centralSession
	if err := s.central(ctx, "/api/v1/auth/refresh",
		map[string]string{"refreshToken": d.RefreshToken}, "", &central); err != nil {
		var ae *AuthError
		if errors.As(err, &ae) && ae.Status < 500 {
			ae.ClearSession = true
		}
		return nil, err
	}
	return s.login(ctx, &central, d.SID)
}

// activeSession opens the cookie and asks sso-balai whether its sid is active.
func (s *SSOService) activeSession(ctx context.Context, cookie string) (*sessionData, error) {
	d, err := s.open(cookie)
	if err != nil {
		return nil, &AuthError{Status: http.StatusUnauthorized, Reason: "SESSION_INVALID", Message: "invalid or expired session", ClearSession: true}
	}
	if !s.introspect(ctx, d.SID, d.AppUserID) {
		return nil, &AuthError{Status: http.StatusUnauthorized, Reason: "SSO_SESSION_INACTIVE", Message: "SSO session is invalid or expired", ClearSession: true}
	}
	return d, nil
}

// introspect is false on any error or misconfiguration (fail closed).
func (s *SSOService) introspect(ctx context.Context, sid, appUserID string) bool {
	secret := s.cfg.IntrospectionSecret
	if sid == "" || appUserID == "" || s.cfg.ClientID == "" || len(secret) < 32 {
		return false
	}
	var out struct {
		Active bool `json:"active"`
	}
	err := s.central(ctx, "/api/v1/auth/sso/introspect", map[string]string{
		"clientId": s.cfg.ClientID, "sid": sid, "appUserId": appUserID,
	}, secret, &out)
	return err == nil && out.Active
}

// login turns a Central session into a Kompu cookie for an approved member.
// When Kompu refuses the user, the fresh Central token session is revoked.
func (s *SSOService) login(ctx context.Context, c *centralSession, sid string) (*Login, error) {
	sessionID := tokenSessionID(c.AccessToken)
	if c.User.ID == "" || c.RefreshToken == "" || sid == "" || sessionID == "" {
		s.discard(c.AccessToken)
		return nil, &AuthError{Status: http.StatusBadGateway, Reason: "INVALID_SSO_RESPONSE", Message: "central authentication returned an invalid session"}
	}
	m, err := s.store.Member(ctx, c.User.ID, sessionID)
	if err == nil {
		err = memberAllowed(m)
	}
	if err != nil {
		s.discard(c.AccessToken)
		return nil, err
	}
	accessExpiresAt := c.ExpiresAt
	if accessExpiresAt == 0 {
		accessExpiresAt = s.now().Unix() + int64(c.ExpiresIn)
	}
	cookie, err := s.seal(sessionData{
		AppUserID:       m.ID,
		Role:            m.Role,
		SID:             sid,
		SessionID:       sessionID,
		AccessToken:     c.AccessToken,
		RefreshToken:    c.RefreshToken,
		AccessExpiresAt: accessExpiresAt,
	})
	if err != nil {
		return nil, err
	}
	return &Login{Cookie: cookie, Member: m}, nil
}

func memberAllowed(m *model.Member) error {
	deny := func(reason, msg string) error {
		return &AuthError{Status: http.StatusForbidden, Reason: reason, Message: msg, ClearSession: true}
	}
	if m == nil {
		return deny("APPLICATION_ACCESS_UNAVAILABLE", "your account does not have access to Kompu")
	}
	switch m.Status {
	case model.MemberApproved:
		if m.Role != model.RoleUser && m.Role != model.RoleAdmin {
			return deny("APPLICATION_ACCESS_UNAVAILABLE", "your account does not have a Kompu role")
		}
		return nil
	case model.MemberPending:
		return deny("ACCOUNT_PENDING_APPROVAL", "your account is awaiting admin approval")
	case model.MemberRejected:
		return deny("ACCOUNT_REJECTED", "your access request was rejected")
	case model.MemberDisabled:
		return deny("ACCOUNT_DISABLED", "your Kompu access is disabled")
	default:
		return deny("APPLICATION_ACCESS_UNAVAILABLE", "your account does not have active access to Kompu")
	}
}

// tokenSessionID reads the session_id claim. The signature is not checked:
// the token came over TLS from sso-balai and only ever lives inside the sealed
// cookie; the DB lookup of auth.sessions is the actual validity check.
func tokenSessionID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.SessionID
}

// discard best-effort revokes a Central token session Kompu will not use.
func (s *SSOService) discard(accessToken string) {
	if accessToken == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.central(ctx, "/api/v1/auth/logout", nil, accessToken, nil)
}

// ---------- cookie sealing ----------

func (s *SSOService) seal(d sessionData) (string, error) {
	d.ExpiresAt = s.now().Add(s.cfg.SessionTTL).Unix()
	payload, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := s.aead.Seal(nonce, nonce, payload, []byte(SessionCookieName))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *SSOService) open(value string) (*sessionData, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(sealed) <= s.aead.NonceSize() {
		return nil, errors.New("invalid session")
	}
	n := s.aead.NonceSize()
	payload, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(SessionCookieName))
	if err != nil {
		return nil, errors.New("invalid session")
	}
	var d sessionData
	if json.Unmarshal(payload, &d) != nil || d.ExpiresAt <= s.now().Unix() ||
		d.AppUserID == "" || d.SID == "" || d.RefreshToken == "" {
		return nil, errors.New("invalid session")
	}
	return &d, nil
}

// ---------- sso-balai HTTP ----------

type centralSession struct {
	SID          string `json:"sid"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
	ExpiresAt    int64  `json:"expiresAt"`
	User         struct {
		ID string `json:"id"`
	} `json:"user"`
}

// central POSTs JSON to sso-balai. bearer is the access token or the
// introspection secret. Central errors {error, message} become AuthErrors.
func (s *SSOService) central(ctx context.Context, path string, body any, bearer string, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return &AuthError{Status: http.StatusServiceUnavailable, Reason: "AUTH_UNAVAILABLE", Message: "central authentication service is unavailable"}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &AuthError{Status: http.StatusServiceUnavailable, Reason: "AUTH_UNAVAILABLE", Message: "central authentication service is unavailable"}
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		reason := strings.ToUpper(strings.TrimSpace(e.Error))
		if reason == "" {
			reason = "SSO_ERROR"
		}
		if e.Message == "" {
			e.Message = "central authentication request failed"
		}
		status := resp.StatusCode
		if status >= 500 {
			status = http.StatusServiceUnavailable
		}
		return &AuthError{Status: status, Reason: reason, Message: e.Message}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// ---------- helpers ----------

func (s *SSOService) flowKey() ([]byte, error) {
	key, err := decodeKey(s.cfg.FlowHMACKey)
	if err != nil || len(key) < 32 || s.cfg.ClientID == "" || s.cfg.RedirectURI == "" || s.cfg.BaseURL == "" {
		return nil, errors.New("browser SSO is not configured")
	}
	return key, nil
}

// pkceVerifier is derived, never stored: 43 base64url chars, as sso-balai requires.
func pkceVerifier(key []byte, state string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("kompu-pkce:" + state))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func decodeKey(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(v)
	}
	return key, err
}

func randomValue(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashOf(v string) []byte {
	h := sha256.Sum256([]byte(v))
	return h[:]
}

func invalidState() *AuthError {
	return &AuthError{Status: http.StatusUnauthorized, Reason: "INVALID_SSO_STATE", Message: "invalid or expired SSO state"}
}

func ssoUnavailable() *AuthError {
	return &AuthError{Status: http.StatusServiceUnavailable, Reason: "SSO_UNAVAILABLE", Message: "browser SSO is unavailable"}
}
