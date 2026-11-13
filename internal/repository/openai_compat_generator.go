package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kompu-backend/internal/model"
)

type OpenAICompatConfig struct {
	BaseURL string // e.g. http://localhost:20128/v1 ; /chat/completions is appended
	APIKey  string // optional; sent as a Bearer token when set
	Model   string // model id, or the name of a 9Router combo
	Timeout time.Duration
}

// OpenAICompatGenerator implements service.TextGenerator against any
// OpenAI-compatible chat-completions endpoint (9Router, OpenRouter, OpenAI, ...),
// using only the standard library.
type OpenAICompatGenerator struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

func NewOpenAICompatGenerator(cfg OpenAICompatConfig) (*OpenAICompatGenerator, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, errors.New("openai-compatible: base URL and model are required")
	}
	return &OpenAICompatGenerator{
		endpoint: strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions",
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		client:   &http.Client{Timeout: cfg.Timeout},
	}, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (g *OpenAICompatGenerator) Generate(ctx context.Context, req model.TextGenerationRequest) (string, error) {
	messages := make([]chatMessage, 0, 2)
	if req.System != "" {
		messages = append(messages, chatMessage{Role: "system", Content: req.System})
	}
	messages = append(messages, chatMessage{Role: "user", Content: req.User})

	payload, err := json.Marshal(chatRequest{Model: g.model, Messages: messages, MaxTokens: req.MaxTokens})
	if err != nil {
		return "", fmt.Errorf("ai: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("ai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ai: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("ai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ai: endpoint returned %d: %s", resp.StatusCode, errorDetail(raw))
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("ai: decode response: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("ai: response contained no text")
	}
	return out.Choices[0].Message.Content, nil
}

// errorDetail extracts a short human-readable reason from an error body. It
// understands {"error":{"message":"..."}}, {"error":"..."} and {"message":"..."},
// and falls back to the first 200 characters of the body.
func errorDetail(raw []byte) string {
	var body struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &body) == nil {
		if len(body.Error) > 0 {
			var obj struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(body.Error, &obj) == nil && obj.Message != "" {
				return obj.Message
			}
			var s string
			if json.Unmarshal(body.Error, &s) == nil && s != "" {
				return s
			}
		}
		if body.Message != "" {
			return body.Message
		}
	}

	text := strings.TrimSpace(string(raw))
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return text
}
