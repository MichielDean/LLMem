// Package ollama provides a client for the Ollama /api/generate and /api/tags endpoints.
// It is used by the extract package to call the Ollama LLM generation API.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MichielDean/LLMem/internal/urlvalidate"
)

const (
	defaultBaseURL = "http://localhost:11434"
	defaultTimeout = 300 * time.Second
)

// OllamaClientConfig contains the configuration for creating an OllamaClient.
type OllamaClientConfig struct {
	// BaseURL is the Ollama API base URL. Defaults to "http://localhost:11434".
	BaseURL string

	// Timeout is the HTTP client timeout for generation requests.
	// Timeout defaults to 300s if zero.
	Timeout time.Duration

	// HTTPClient is an optional pre-configured HTTP client (for testing with httptest.NewServer).
	HTTPClient *http.Client
}

// OllamaClient calls the Ollama /api/generate and /api/tags endpoints.
// It validates BaseURL via urlvalidate.ValidateBaseURL in the constructor.
// The constructor leaves the client in a fully usable state.
type OllamaClient struct {
	baseURL    string
	timeout    time.Duration
	httpClient *http.Client
}

// fmtErr wraps an error with the "llmem: ollama:" domain prefix.
func fmtErr(format string, args ...any) error {
	return fmt.Errorf("llmem: ollama: "+format, args...)
}

// NewOllamaClient creates and initializes an OllamaClient.
// Validates BaseURL via urlvalidate.ValidateBaseURL.
// If cfg.BaseURL == "", defaults to "http://localhost:11434".
// If cfg.Timeout == 0, defaults to 300s.
// If cfg.HTTPClient == nil, creates a new http.Client with cfg.Timeout.
// The constructor leaves the client in a fully usable state.
func NewOllamaClient(cfg OllamaClientConfig) (*OllamaClient, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		validatedURL, err := urlvalidate.ValidateBaseURL(baseURL, "ollama")
		if err != nil {
			return nil, fmtErr("unsafe Ollama URL: %w", err)
		}
		baseURL = validatedURL
		httpClient = &http.Client{Timeout: timeout}
	} else {
		// For test environments, just strip trailing slash
		baseURL = strings.TrimRight(baseURL, "/")
	}

	return &OllamaClient{
		baseURL:    baseURL,
		timeout:    timeout,
		httpClient: httpClient,
	}, nil
}

// Generate calls /api/generate with stream:false and returns the response text.
// On error, wraps with llmem: ollama: prefix. On unavailable Ollama, returns ("", error) — never panics.
// Context cancellation is respected via ctx.
func (c *OllamaClient) Generate(ctx context.Context, prompt, model string) (string, error) {
	reqBody := struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		Stream bool   `json:"stream"`
	}{
		Model:  model,
		Prompt: prompt,
		Stream: false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmtErr("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/generate", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmtErr("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmtErr("generate request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmtErr("generate request failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmtErr("decode response: %w", err)
	}

	return strings.TrimSpace(result.Response), nil
}

// IsAvailable calls /api/tags and returns true if Ollama is reachable.
// Returns false on any error (never returns an error). Logs at slog.Debug.
func (c *OllamaClient) IsAvailable(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		slog.Debug("llmem: ollama: IsAvailable: create request failed", "error", err)
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Debug("llmem: ollama: IsAvailable: request failed", "error", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Debug("llmem: ollama: IsAvailable: unexpected status", "status", resp.StatusCode)
		return false
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		slog.Debug("llmem: ollama: IsAvailable: decode failed", "error", err)
		return false
	}

	return len(tagsResp.Models) > 0
}

// PullModel calls /api/pull to download a model. Returns (true, nil) on success.
func (c *OllamaClient) PullModel(ctx context.Context, model string) (bool, error) {
	reqBody := struct {
		Name  string `json:"name"`
		Stream bool   `json:"stream"`
	}{
		Name:  model,
		Stream: false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return false, fmtErr("marshal pull request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/pull", bytes.NewReader(bodyBytes))
	if err != nil {
		return false, fmtErr("create pull request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmtErr("pull model request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return false, fmtErr("pull model failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmtErr("decode pull response: %w", err)
	}

	return result.Status == "success", nil
}