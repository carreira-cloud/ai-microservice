package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/provider"
	"github.com/sirupsen/logrus"
)

const (
	tokenURL       = "https://api.github.com/copilot_internal/v2/token"
	completionsURL = "https://api.githubcopilot.com/chat/completions"
	defaultTimeout = 30 * time.Second
)

type tokenEntry struct {
	token     string
	expiresAt time.Time
}

// Provider implements provider.Provider using the GitHub Copilot API.
type Provider struct {
	oauthToken string
	httpClient *http.Client

	mu          sync.Mutex
	cachedToken *tokenEntry
}

// New creates a CopilotProvider. httpClient is optional; if nil, a default
// client with 30s timeout is used. Pass a custom client in tests to point
// at an httptest.Server.
func New(oauthToken string, httpClient *http.Client) *Provider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Provider{oauthToken: oauthToken, httpClient: httpClient}
}

func (p *Provider) Name() string { return "copilot" }

// Complete calls the GitHub Copilot chat/completions endpoint.
// Retries once on network errors.
func (p *Provider) Complete(ctx context.Context, req provider.CompletionRequest) (*provider.CompletionResponse, error) {
	token, err := p.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("copilot: token: %w", err)
	}

	model := req.Model
	if model == "" {
		model = "gpt-4o"
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 1024
	}
	temp := req.Temperature
	if temp == 0 {
		temp = 0.3
	}

	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    req.Messages,
		"temperature": temp,
		"max_tokens":  maxTokens,
	})

	started := time.Now()
	res, err := p.doRequest(ctx, token, body)
	if err != nil {
		return nil, fmt.Errorf("copilot: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot: http %d", res.StatusCode)
	}

	var resp struct {
		Choices []struct {
			Message      provider.Message `json:"message"`
			FinishReason string           `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("copilot: decode: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("copilot: empty choices")
	}

	return &provider.CompletionResponse{
		Content:      resp.Choices[0].Message.Content,
		FinishReason: resp.Choices[0].FinishReason,
		LatencyMs:    time.Since(started).Milliseconds(),
	}, nil
}

// doRequest performs the HTTP call with one retry on network (non-context) errors.
// The body bytes are re-used safely because bytes.NewReader is seekable.
func (p *Provider) doRequest(ctx context.Context, token string, body []byte) (*http.Response, error) {
	do := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, completionsURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Copilot-Integration-Id", "vscode-chat")
		req.Header.Set("Editor-Version", "vscode/1.99.0")
		return p.httpClient.Do(req)
	}

	res, err := do()
	if err != nil {
		// Do NOT retry on context cancellation or deadline — the caller has moved on.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("context: %w", ctx.Err())
		}
		logrus.WithError(err).Warn("copilot: network error — retrying once")
		res, err = do()
		if err != nil {
			return nil, fmt.Errorf("network (after retry): %w", err)
		}
	}
	return res, nil
}

// getToken returns a cached Copilot API token, refreshing if expired.
func (p *Provider) getToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cachedToken != nil && time.Now().Before(p.cachedToken.expiresAt) {
		return p.cachedToken.token, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+p.oauthToken)
	req.Header.Set("Accept", "application/json")

	res, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange: http %d", res.StatusCode)
	}

	var data struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("token decode: %w", err)
	}

	p.cachedToken = &tokenEntry{
		token:     data.Token,
		expiresAt: time.Unix(data.ExpiresAt, 0).Add(-60 * time.Second),
	}
	return p.cachedToken.token, nil
}
