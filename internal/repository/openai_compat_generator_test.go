package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kompu-backend/internal/model"
)

func newTestOpenAIGen(t *testing.T, baseURL, apiKey string) *OpenAICompatGenerator {
	t.Helper()
	g, err := NewOpenAICompatGenerator(OpenAICompatConfig{
		BaseURL: baseURL, APIKey: apiKey, Model: "free-combo", Timeout: 2 * time.Second,
	})
	require.NoError(t, err)
	return g
}

type chatBody struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Stream    bool   `json:"stream"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func TestOpenAICompatGenerator_Success(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody chatBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Halo petani!"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	out, err := newTestOpenAIGen(t, srv.URL+"/v1", "sk-test-key").Generate(context.Background(),
		model.TextGenerationRequest{System: "sys", User: "usr", MaxTokens: 123})

	require.NoError(t, err)
	assert.Equal(t, "Halo petani!", out)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v1/chat/completions", gotPath)
	assert.Equal(t, "Bearer sk-test-key", gotAuth)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, "free-combo", gotBody.Model)
	assert.Equal(t, 123, gotBody.MaxTokens)
	assert.False(t, gotBody.Stream)
	require.Len(t, gotBody.Messages, 2)
	assert.Equal(t, "system", gotBody.Messages[0].Role)
	assert.Equal(t, "sys", gotBody.Messages[0].Content)
	assert.Equal(t, "user", gotBody.Messages[1].Role)
	assert.Equal(t, "usr", gotBody.Messages[1].Content)
}

func TestOpenAICompatGenerator_OptionalPieces(t *testing.T) {
	var gotAuth string
	var gotBody chatBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	_, err := newTestOpenAIGen(t, srv.URL, "").Generate(context.Background(),
		model.TextGenerationRequest{User: "usr", MaxTokens: 10})

	require.NoError(t, err)
	assert.Empty(t, gotAuth, "no key configured means no Authorization header")
	require.Len(t, gotBody.Messages, 1, "no system prompt means a single user message")
	assert.Equal(t, "user", gotBody.Messages[0].Role)
}

func TestOpenAICompatGenerator_Errors(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantContain []string
	}{
		{"auth error", 401, `{"error":{"message":"invalid api key","type":"auth"}}`, []string{"401", "invalid api key"}},
		{"error as a plain string", 429, `{"error":"free quota exhausted"}`, []string{"429", "free quota exhausted"}},
		{"top-level message", 503, `{"message":"all providers failed"}`, []string{"503", "all providers failed"}},
		{"non-JSON error body", 500, `upstream exploded`, []string{"500", "upstream exploded"}},
		{"no choices", 200, `{"choices":[]}`, []string{"no text"}},
		{"empty content", 200, `{"choices":[{"message":{"content":"   "}}]}`, []string{"no text"}},
		{"unparsable success body", 200, `not json`, []string{"decode"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := newTestOpenAIGen(t, srv.URL, "sk-test-key").Generate(context.Background(),
				model.TextGenerationRequest{System: "s", User: "u", MaxTokens: 10})

			require.Error(t, err)
			for _, want := range tc.wantContain {
				assert.Contains(t, err.Error(), want)
			}
			assert.NotContains(t, err.Error(), "sk-test-key", "the API key must never appear in errors")
		})
	}
}

func TestOpenAICompatGenerator_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	_, err := newTestOpenAIGen(t, url, "sk-test-key").Generate(context.Background(),
		model.TextGenerationRequest{System: "s", User: "u", MaxTokens: 10})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sk-test-key")
}

func TestNewOpenAICompatGenerator_Config(t *testing.T) {
	_, err := NewOpenAICompatGenerator(OpenAICompatConfig{})
	assert.Error(t, err)
	_, err = NewOpenAICompatGenerator(OpenAICompatConfig{BaseURL: "http://localhost:20128/v1"})
	assert.Error(t, err, "model is required")
	_, err = NewOpenAICompatGenerator(OpenAICompatConfig{Model: "m"})
	assert.Error(t, err, "base URL is required")

	_, err = NewOpenAICompatGenerator(OpenAICompatConfig{BaseURL: "http://localhost:20128/v1", Model: "m"})
	assert.NoError(t, err, "the API key is optional")
}
