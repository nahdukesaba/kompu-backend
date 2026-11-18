package handler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
	"kompu-backend/internal/service"
	"kompu-backend/pkg/middleware"
)

const (
	testSecret  = "introspection-secret-of-at-least-32-chars"
	testUserID  = "11111111-1111-1111-1111-111111111111"
	testSession = "22222222-2222-2222-2222-222222222222"
	testOrigin  = "https://kompu.example"
)

// ---------- fake sso-balai ----------

type fakeCentral struct {
	mu         sync.Mutex
	challenge  string // from the last /authorize redirect
	sidActive  bool
	discarded  int
	refreshed  int
	introspect int
	server     *httptest.Server
}

func fakeJWT(sessionID string) string {
	enc := base64.RawURLEncoding
	payload, _ := json.Marshal(map[string]string{"session_id": sessionID, "sub": "auth-user"})
	return enc.EncodeToString([]byte(`{"alg":"ES256"}`)) + "." + enc.EncodeToString(payload) + ".sig"
}

func newFakeCentral(t *testing.T) *fakeCentral {
	f := &fakeCentral{sidActive: true}
	session := func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sid": "sid-1", "accessToken": fakeJWT(testSession), "refreshToken": "refresh-1",
			"tokenType": "bearer", "expiresIn": 3600, "expiresAt": time.Now().Add(time.Hour).Unix(),
			"user": map[string]string{"id": testUserID},
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/sso/exchange", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		sum := sha256.Sum256([]byte(req["codeVerifier"]))
		f.mu.Lock()
		ok := req["code"] == "good-code" && req["clientId"] == "kompu-web" &&
			req["redirectUri"] == testOrigin+"/auth/sso/callback" &&
			base64.RawURLEncoding.EncodeToString(sum[:]) == f.challenge
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid_authorization_code","message":"invalid code"}`)
			return
		}
		session(w)
	})
	mux.HandleFunc("POST /api/v1/auth/sso/introspect", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.introspect++
		if r.Header.Get("Authorization") != "Bearer "+testSecret {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"active":false}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"active": f.sidActive})
	})
	mux.HandleFunc("POST /api/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.refreshed++
		f.mu.Unlock()
		session(w)
	})
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.discarded++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// ---------- in-memory store ----------

type memStore struct {
	mu     sync.Mutex
	flows  map[string]time.Time
	used   map[string]bool
	member *model.Member
}

func (s *memStore) CreateFlow(_ context.Context, hash []byte, exp time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[string(hash)] = exp
	return nil
}

func (s *memStore) ConsumeFlow(_ context.Context, hash []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.flows[string(hash)]
	if !ok || s.used[string(hash)] || time.Now().After(exp) {
		return false, nil
	}
	s.used[string(hash)] = true
	return true, nil
}

func (s *memStore) Member(_ context.Context, appUserID, sessionID string) (*model.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.member == nil || appUserID != s.member.ID {
		return nil, nil
	}
	m := *s.member
	m.SessionValid = m.SessionValid && sessionID == testSession
	return &m, nil
}

// ---------- harness ----------

type authEnv struct {
	app     *fiber.App
	central *fakeCentral
	store   *memStore
}

func newAuthEnv(t *testing.T, appSecret string) *authEnv {
	central := newFakeCentral(t)
	store := &memStore{
		flows: map[string]time.Time{}, used: map[string]bool{},
		member: &model.Member{ID: testUserID, Email: "a@example.com", FullName: "Ana", Role: model.RoleUser,
			Status: model.MemberApproved, SessionValid: true},
	}
	svc, err := service.NewSSOService(service.SSOConfig{
		BaseURL:               central.server.URL,
		ClientID:              "kompu-web",
		RedirectURI:           testOrigin + "/auth/sso/callback",
		PostLogoutRedirectURI: testOrigin + "/",
		FlowHMACKey:           base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		FlowTTL:               5 * time.Minute,
		IntrospectionSecret:   appSecret,
		Timeout:               5 * time.Second,
		SessionKey:            base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))),
		SessionTTL:            time.Hour,
	}, store)
	require.NoError(t, err)

	auth := NewAuthHandler(svc, true, time.Hour)
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	app.Use(middleware.RequireTrustedOrigin([]string{testOrigin}))
	api := app.Group("/api/v1")
	auth.Register(api)
	api.Post("/protected", auth.RequireSession, func(c *fiber.Ctx) error {
		p, _ := middleware.PrincipalFrom(c)
		return c.JSON(p)
	})
	return &authEnv{app: app, central: central, store: store}
}

func (e *authEnv) do(t *testing.T, method, path, body, cookie string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testOrigin)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: cookie})
	}
	resp, err := e.app.Test(req, -1)
	require.NoError(t, err)
	return resp
}

// start runs /auth/sso/start, records the PKCE challenge in the fake Central
// and returns the state.
func (e *authEnv) start(t *testing.T) string {
	resp := e.do(t, http.MethodGet, "/api/v1/auth/sso/start", "", "")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	q := loc.Query()
	assert.Equal(t, "/authorize", loc.Path)
	assert.Equal(t, "kompu-web", q.Get("client_id"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, "code", q.Get("response_type"))
	e.central.mu.Lock()
	e.central.challenge = q.Get("code_challenge")
	e.central.mu.Unlock()
	return q.Get("state")
}

func exchangeBody(state, code, status string) string {
	b, _ := json.Marshal(map[string]string{"state": state, "code": code, "status": status})
	return string(b)
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == service.SessionCookieName {
			return c
		}
	}
	return nil
}

func errorReason(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body.Error.Reason
}

// ---------- tests ----------

func TestSSO_ApprovedLogin(t *testing.T) {
	env := newAuthEnv(t, testSecret)
	state := env.start(t)

	resp := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(state, "good-code", ""), "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	cookie := sessionCookie(resp)
	require.NotNil(t, cookie)
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	raw, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"user":{"id":"`+testUserID+`","email":"a@example.com","fullName":"Ana","role":"user"}}`, string(raw))
	assert.NotContains(t, string(raw), "refresh-1", "tokens never reach the browser")

	resp = env.do(t, http.MethodPost, "/api/v1/protected", "", cookie.Value)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, _ = io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"UserID":"`+testUserID+`","Role":"user"}`, string(raw))

	resp = env.do(t, http.MethodGet, "/api/v1/auth/me", "", cookie.Value)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp = env.do(t, http.MethodPost, "/api/v1/auth/refresh", "", cookie.Value)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotNil(t, sessionCookie(resp), "refresh rotates the cookie")
	assert.Equal(t, 1, env.central.refreshed)
}

func TestSSO_PendingAndDeniedResults(t *testing.T) {
	env := newAuthEnv(t, testSecret)

	resp := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(env.start(t), "", "pending"), "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"status":"pending"}`, string(raw))
	assert.Nil(t, sessionCookie(resp))

	resp = env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(env.start(t), "", "denied"), "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "ACCOUNT_REJECTED", errorReason(t, resp))
}

func TestSSO_PendingMembershipIsRefusedAndDiscarded(t *testing.T) {
	env := newAuthEnv(t, testSecret)
	env.store.member.Status = model.MemberPending

	resp := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(env.start(t), "good-code", ""), "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "ACCOUNT_PENDING_APPROVAL", errorReason(t, resp))
	assert.Equal(t, 1, env.central.discarded, "the fresh Central session is revoked")
}

func TestSSO_ReplayedStateIsRejected(t *testing.T) {
	env := newAuthEnv(t, testSecret)
	state := env.start(t)

	first := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(state, "good-code", ""), "")
	require.Equal(t, http.StatusOK, first.StatusCode)

	replay := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(state, "good-code", ""), "")
	assert.Equal(t, http.StatusUnauthorized, replay.StatusCode)
	assert.Equal(t, "INVALID_SSO_STATE", errorReason(t, replay))

	unknown := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody("never-issued", "good-code", ""), "")
	assert.Equal(t, http.StatusUnauthorized, unknown.StatusCode)
}

func TestSSO_WrongIntrospectionSecretFailsClosed(t *testing.T) {
	env := newAuthEnv(t, "a-different-secret-that-is-also-32-chars-long")
	resp := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(env.start(t), "good-code", ""), "")
	require.Equal(t, http.StatusOK, resp.StatusCode, "login itself does not introspect")
	cookie := sessionCookie(resp)
	require.NotNil(t, cookie)

	resp = env.do(t, http.MethodPost, "/api/v1/protected", "", cookie.Value)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "SSO_SESSION_INACTIVE", errorReason(t, resp))
	cleared := sessionCookie(resp)
	require.NotNil(t, cleared, "cookie is cleared")
	assert.Empty(t, cleared.Value)
}

func TestSSO_CentralLogoutRevokesSession(t *testing.T) {
	env := newAuthEnv(t, testSecret)
	resp := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(env.start(t), "good-code", ""), "")
	cookie := sessionCookie(resp)
	require.NotNil(t, cookie)

	resp = env.do(t, http.MethodPost, "/api/v1/auth/logout", "", cookie.Value)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"centralLogoutRequired":true}`, string(raw))
	assert.Nil(t, sessionCookie(resp), "cookie kept until Central decides")

	env.central.mu.Lock()
	env.central.sidActive = false // user confirmed logout on sso-balai
	env.central.mu.Unlock()
	resp = env.do(t, http.MethodGet, "/api/v1/auth/me", "", cookie.Value)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestSSO_RoleChangeIsStale(t *testing.T) {
	env := newAuthEnv(t, testSecret)
	resp := env.do(t, http.MethodPost, "/api/v1/auth/sso/exchange", exchangeBody(env.start(t), "good-code", ""), "")
	cookie := sessionCookie(resp)
	require.NotNil(t, cookie)

	env.store.member.Role = model.RoleAdmin
	resp = env.do(t, http.MethodPost, "/api/v1/protected", "", cookie.Value)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "TOKEN_AUTHORIZATION_STALE", errorReason(t, resp))
}

func TestSSO_MeWithoutCookieIs204(t *testing.T) {
	env := newAuthEnv(t, testSecret)
	resp := env.do(t, http.MethodGet, "/api/v1/auth/me", "", "")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, 0, env.central.introspect)
}

func TestSSO_LogoutRedirectSanitizesCancelReturn(t *testing.T) {
	env := newAuthEnv(t, testSecret)
	for in, want := range map[string]string{
		"/kegiatan?page=2":     "/kegiatan?page=2",
		"//evil.example/x":     "",
		"https://evil.example": "",
	} {
		resp := env.do(t, http.MethodGet, "/api/v1/auth/sso/logout?cancel_return="+url.QueryEscape(in), "", "")
		require.Equal(t, http.StatusFound, resp.StatusCode)
		loc, _ := url.Parse(resp.Header.Get("Location"))
		assert.Equal(t, "/logout", loc.Path)
		assert.Equal(t, testOrigin+"/", loc.Query().Get("post_logout_redirect_uri"))
		assert.Equal(t, want, loc.Query().Get("cancel_return"), in)
	}
}
