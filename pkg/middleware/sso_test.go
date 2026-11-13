package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeVerifier struct {
	err    error
	called bool
	token  string
}

func (f *fakeVerifier) Verify(_ context.Context, token string) (*Principal, error) {
	f.called = true
	f.token = token
	if f.err != nil {
		return nil, f.err
	}
	return &Principal{Profile: json.RawMessage(`{"id":"u1"}`)}, nil
}

func newTestApp(v TokenVerifier) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fe *fiber.Error
			if errors.As(err, &fe) {
				code = fe.Code
			}
			return c.Status(code).SendString(err.Error())
		},
	})
	app.Use(SSOAuth(v))
	app.All("/x", func(c *fiber.Ctx) error {
		if p, ok := PrincipalFrom(c); ok {
			return c.SendString(string(p.Profile))
		}
		return c.SendString("anonymous")
	})
	return app
}

func TestSSOAuth(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		header     string
		verifyErr  error
		wantStatus int
		wantCalled bool
	}{
		{"GET is public", http.MethodGet, "", nil, 200, false},
		{"OPTIONS is public", http.MethodOptions, "", nil, 200, false},
		{"POST without header", http.MethodPost, "", nil, 401, false},
		{"POST with wrong scheme", http.MethodPost, "Basic abc", nil, 401, false},
		{"POST with empty bearer", http.MethodPost, "Bearer ", nil, 401, false},
		{"POST with valid token", http.MethodPost, "Bearer abc", nil, 200, true},
		{"scheme is case-insensitive", http.MethodPost, "bearer abc", nil, 200, true},
		{"PUT with invalid token", http.MethodPut, "Bearer abc", ErrInvalidToken, 401, true},
		{"DELETE with forbidden account", http.MethodDelete, "Bearer abc", ErrForbidden, 403, true},
		{"verifier unavailable", http.MethodPost, "Bearer abc", fmt.Errorf("%w: boom", ErrVerifierUnavailable), 503, true},
		{"unexpected verifier error", http.MethodPost, "Bearer abc", errors.New("boom"), 500, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := &fakeVerifier{err: tc.verifyErr}
			req := httptest.NewRequest(tc.method, "/x", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			resp, err := newTestApp(v).Test(req)
			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, resp.StatusCode)
			assert.Equal(t, tc.wantCalled, v.called)
		})
	}
}

func TestSSOAuth_StoresPrincipalAndPassesToken(t *testing.T) {
	v := &fakeVerifier{}
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Authorization", "Bearer abc")

	resp, err := newTestApp(v).Test(req)
	require.NoError(t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"u1"}`, string(body))
	assert.Equal(t, "abc", v.token)
}

func TestSSOAuth_UnauthorizedSetsChallengeHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	resp, err := newTestApp(&fakeVerifier{}).Test(req)
	require.NoError(t, err)
	assert.Equal(t, "Bearer", resp.Header.Get("WWW-Authenticate"))
}

func TestCentralVerifier_Verify(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/auth/me", r.URL.Path)
			assert.Equal(t, "Bearer good", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"u1"}`))
		}))
		defer srv.Close()

		p, err := NewCentralVerifier(srv.URL, time.Second).Verify(context.Background(), "good")
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":"u1"}`, string(p.Profile))
		assert.Equal(t, "u1", p.UserID)
	})

	statusTests := []struct {
		name    string
		status  int
		wantErr error
	}{
		{"unauthorized", http.StatusUnauthorized, ErrInvalidToken},
		{"forbidden", http.StatusForbidden, ErrForbidden},
		{"server error", http.StatusInternalServerError, ErrVerifierUnavailable},
		{"rate limited", http.StatusTooManyRequests, ErrVerifierUnavailable},
	}
	for _, tc := range statusTests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			_, err := NewCentralVerifier(srv.URL, time.Second).Verify(context.Background(), "tok")
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}

	t.Run("central unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()

		_, err := NewCentralVerifier(url, time.Second).Verify(context.Background(), "tok")
		assert.ErrorIs(t, err, ErrVerifierUnavailable)
	})
}

func TestExtractUserID(t *testing.T) {
	assert.Equal(t, "u1", extractUserID([]byte(`{"id":"u1","email":"a@b.c"}`)))
	assert.Equal(t, "u2", extractUserID([]byte(`{"user":{"id":"u2"}}`)))
	assert.Equal(t, "", extractUserID([]byte(`{"email":"a@b.c"}`)))
	assert.Equal(t, "", extractUserID([]byte(`not json`)))
	assert.Equal(t, "", extractUserID([]byte(`{"id":123}`)))
}

func TestCentralVerifier_NoUserIDFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"email":"a@b.c"}`))
	}))
	defer srv.Close()

	_, err := NewCentralVerifier(srv.URL, time.Second).Verify(context.Background(), "tok")
	assert.ErrorIs(t, err, ErrVerifierUnavailable)
}
