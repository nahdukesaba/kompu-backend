package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const centralMePath = "/api/v1/auth/me"

// CentralVerifier validates a token by asking sso-balai itself
// (GET /api/v1/auth/me, protected by Central's own RequireAuth).
// Central stays the single source of truth, so no signing keys live here.
type CentralVerifier struct {
	meURL  string
	client *http.Client
}

func NewCentralVerifier(baseURL string, timeout time.Duration) *CentralVerifier {
	return &CentralVerifier{
		meURL:  strings.TrimRight(baseURL, "/") + centralMePath,
		client: &http.Client{Timeout: timeout},
	}
}

func (v *CentralVerifier) Verify(ctx context.Context, token string) (*Principal, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.meURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build central request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVerifierUnavailable, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrVerifierUnavailable, err)
		}
		userID := extractUserID(body)
		if userID == "" {
			// Fail closed: without an identity we cannot attribute writes.
			return nil, fmt.Errorf("%w: central %s response has no user id", ErrVerifierUnavailable, centralMePath)
		}
		p := &Principal{UserID: userID}
		if json.Valid(body) {
			p.Profile = json.RawMessage(body)
		}
		return p, nil
	case http.StatusUnauthorized:
		return nil, ErrInvalidToken
	case http.StatusForbidden:
		return nil, ErrForbidden
	default:
		return nil, fmt.Errorf("%w: central returned status %d", ErrVerifierUnavailable, resp.StatusCode)
	}
}

// extractUserID finds the user id in Central's /me response. It supports a
// top-level {"id": "..."} and a nested {"user": {"id": "..."}}.
func extractUserID(body []byte) string {
	var probe struct {
		ID   string `json:"id"`
		User *struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	if id := strings.TrimSpace(probe.ID); id != "" {
		return id
	}
	if probe.User != nil {
		return strings.TrimSpace(probe.User.ID)
	}
	return ""
}
