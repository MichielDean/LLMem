// Package extract provides memory extraction from text using Ollama LLM.
// It uses the OllamaClient for LLM calls with graceful degradation when Ollama is unavailable.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/MichielDean/LLMem/internal/ollama"
)

const (
	defaultModel   = "glm-5.1:cloud"
	defaultBaseURL = "http://localhost:11434"
)

// extractionPrompt is the system prompt for memory extraction.
var extractionPrompt = `You are a memory extraction system. Extract key memories from the text below.

Return a JSON array of objects with these fields:
- type: one of "fact", "decision", "preference", "event", "project_state", "procedure", "conversation"
- content: a clear, specific statement (not vague)
- confidence: 0.0 to 1.0 (how certain this is a lasting memory)

If no memories are worth extracting, return an empty array [].

Text:
`

// regex for extracting JSON arrays from LLM responses
var (
	reCodeBlock = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)\n?```")
	reJSONArray = regexp.MustCompile("(?s)\\[[^\\]]*\\]")
)

// ExtractionConfig contains the configuration for creating an ExtractionEngine.
type ExtractionConfig struct {
	// Model is the extraction model name. Defaults to "glm-5.1:cloud".
	Model string

	// BaseURL is the Ollama API base URL. Defaults to "http://localhost:11434".
	BaseURL string

	// HTTPClient is an optional pre-configured HTTP client (for testing with httptest.NewServer).
	HTTPClient *http.Client

	// OllamaClient is an optional pre-configured OllamaClient. Takes precedence over BaseURL/HTTPClient.
	OllamaClient *ollama.OllamaClient
}

// ExtractionEngine extracts memories from text using Ollama.
// If Ollama is unavailable, it gracefully degrades by returning an empty slice.
type ExtractionEngine struct {
	model  string
	ollama *ollama.OllamaClient
}

// fmtErr wraps an error with the "llmem: extract:" domain prefix.
func fmtErr(format string, args ...any) error {
	return fmt.Errorf("llmem: extract: "+format, args...)
}

// NewExtractionEngine creates and initializes an ExtractionEngine.
// If cfg.Model == "", defaults to "glm-5.1:cloud".
// If cfg.BaseURL == "", defaults to "http://localhost:11434".
// The constructor leaves the engine in a fully usable state.
func NewExtractionEngine(cfg ExtractionConfig) (*ExtractionEngine, error) {
	model := cfg.Model
	if model == "" {
		model = defaultModel
	}

	var client *ollama.OllamaClient
	if cfg.OllamaClient != nil {
		client = cfg.OllamaClient
	} else {
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = defaultBaseURL
		}

		ollamaCfg := ollama.OllamaClientConfig{
			BaseURL:    baseURL,
			HTTPClient: cfg.HTTPClient,
		}

		var err error
		client, err = ollama.NewOllamaClient(ollamaCfg)
		if err != nil {
			return nil, fmtErr("create Ollama client: %w", err)
		}
	}

	return &ExtractionEngine{
		model:  model,
		ollama: client,
	}, nil
}

// Extract extracts memories from text using Ollama.
// Returns an empty slice on failure (graceful degradation).
// Parses JSON array from LLM response, with fallback regex extraction.
func (e *ExtractionEngine) Extract(ctx context.Context, text string) []map[string]any {
	prompt := extractionPrompt + text

	response, err := e.ollama.Generate(ctx, prompt, e.model)
	if err != nil {
		slog.Error("llmem: extract: Ollama extraction failed", "error", err)
		return []map[string]any{}
	}

	// Try direct JSON parse first
	memories := tryParseJSONArray(response)
	if memories != nil {
		return memories
	}

	// Try markdown code block extraction
	memories = tryExtractFromCodeBlock(response)
	if memories != nil {
		return memories
	}

	// Try regex extraction from response text
	memories = tryExtractFromText(response)
	if memories != nil {
		return memories
	}

	slog.Warn("llmem: extract: could not parse extraction response as JSON array")
	return []map[string]any{}
}

// CheckAvailable checks if the extraction model is available in Ollama.
// Returns false on any error. Never returns an error.
func (e *ExtractionEngine) CheckAvailable(ctx context.Context) bool {
	return e.ollama.IsAvailable(ctx)
}

// tryParseJSONArray tries to parse response text directly as a JSON array.
func tryParseJSONArray(text string) []map[string]any {
	text = strings.TrimSpace(text)
	var result []map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil
	}
	return result
}

// tryExtractFromCodeBlock tries to extract JSON from markdown code blocks.
func tryExtractFromCodeBlock(text string) []map[string]any {
	match := reCodeBlock.FindStringSubmatch(text)
	if match == nil {
		return nil
	}
	content := strings.TrimSpace(match[1])
	var result []map[string]any
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil
	}
	return result
}

// tryExtractFromText tries to extract JSON array from raw text using regex.
func tryExtractFromText(text string) []map[string]any {
	match := reJSONArray.FindString(text)
	if match == "" {
		return nil
	}
	var result []map[string]any
	if err := json.Unmarshal([]byte(match), &result); err != nil {
		return nil
	}
	return result
}