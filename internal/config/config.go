// Package config provides configuration loading and writing for LLMem.
// It handles YAML config files with path resolution, defaults, and validation.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichielDean/LLMem/internal/paths"
	"github.com/MichielDean/LLMem/internal/urlvalidate"
	"gopkg.in/yaml.v3"
)

// Config holds the full LLMem configuration.
type Config struct {
	Memory MemoryConfig `yaml:"memory"`
}

// MemoryConfig holds memory store settings.
type MemoryConfig struct {
	DBPath        string `yaml:"db"`
	OllamaURL     string `yaml:"ollama_url"`
	EmbedModel    string `yaml:"embed_model"`
	ExtractModel  string `yaml:"extract_model"`
	ContextBudget int    `yaml:"context_budget"`
	AutoExtract   bool   `yaml:"auto_extract"`
	MaxFileSize   int64  `yaml:"max_file_size"`
}

// fmtErr wraps an error with the "llmem: config:" domain prefix.
func fmtErr(format string, args ...any) error {
	return fmt.Errorf("llmem: config: "+format, args...)
}

// DefaultConfig returns a Config with all defaults applied.
func DefaultConfig() Config {
	return Config{
		Memory: MemoryConfig{
			DBPath:        paths.GetDBPath(),
			OllamaURL:     "http://localhost:11434",
			EmbedModel:    "nomic-embed-text",
			ExtractModel:  "glm-5.1:cloud",
			ContextBudget: 4000,
			AutoExtract:   true,
			MaxFileSize:   10 * 1024 * 1024,
		},
	}
}

// LoadConfig loads configuration from a YAML file.
// If the file doesn't exist, returns default config.
// If the file exists but is malformed, returns an error.
func LoadConfig(configPath string) (*Config, error) {
	if configPath == "" {
		configPath = paths.GetConfigPath()
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			return &cfg, nil
		}
		return nil, fmtErr("read config %s: %w", configPath, err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		slog.Warn("llmem: config: failed to parse config file, using defaults", "path", configPath, "error", err)
		return &cfg, nil
	}

	// Apply path resolution for empty DB path
	if cfg.Memory.DBPath == "" {
		cfg.Memory.DBPath = paths.GetDBPath()
	}

	return &cfg, nil
}

// DBPath returns the resolved database path.
func (c *Config) DBPath() string {
	if c.Memory.DBPath != "" {
		return expandHome(c.Memory.DBPath)
	}
	return paths.GetDBPath()
}

// OllamaURL returns the validated Ollama URL.
func (c *Config) OllamaURL() (string, error) {
	url := c.Memory.OllamaURL
	if url == "" {
		url = "http://localhost:11434"
	}
	validated, err := urlvalidate.ValidateBaseURL(url, "config")
	if err != nil {
		return "", fmtErr("invalid Ollama URL: %w", err)
	}
	return validated, nil
}

// WriteConfigYAML writes config as YAML to the given path with 0600 permissions.
// Creates parent directories with 0700 permissions.
// Returns false if file exists and force is false.
func WriteConfigYAML(path string, config map[string]any, force bool) (bool, error) {
	if !force {
		if _, err := os.Stat(path); err == nil {
			slog.Info("llmem: config: file already exists, skipping", "path", path)
			return false, nil
		}
	}

	// Create parent directory with 0700 permissions
	dir := paths.GetDirFromPath(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return false, fmtErr("create config directory: %w", err)
		}
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return false, fmtErr("marshal config: %w", err)
	}

	// Write with 0600 permissions to protect API keys
	if err := os.WriteFile(path, data, 0600); err != nil {
		return false, fmtErr("write config: %w", err)
	}

	return true, nil
}

// expandHome expands ~ to the user's home directory.
func expandHome(pathStr string) string {
	if strings.HasPrefix(pathStr, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return pathStr
		}
		return filepath.Join(homeDir, pathStr[2:])
	}
	return pathStr
}