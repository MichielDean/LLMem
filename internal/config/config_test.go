package config

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LMEM_HOME", dir)
	return dir
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Memory.OllamaURL != "http://localhost:11434" {
		t.Errorf("expected default OllamaURL, got %q", cfg.Memory.OllamaURL)
	}
	if cfg.Memory.EmbedModel != "nomic-embed-text" {
		t.Errorf("expected default EmbedModel, got %q", cfg.Memory.EmbedModel)
	}
}

func TestLoadConfig_Nonexistent(t *testing.T) {
	_ = newTestConfigDir(t)
	cfg, err := LoadConfig("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Memory.OllamaURL != "http://localhost:11434" {
		t.Errorf("expected default OllamaURL, got %q", cfg.Memory.OllamaURL)
	}
}

func TestLoadConfig_FromYAML(t *testing.T) {
	dir := newTestConfigDir(t)
	configPath := filepath.Join(dir, "config.yaml")

	yamlContent := []byte("memory:\n  ollama_url: http://ollama.example.com:11434\n  embed_model: test-model\n")
	if err := os.WriteFile(configPath, yamlContent, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Memory.OllamaURL != "http://ollama.example.com:11434" {
		t.Errorf("expected custom OllamaURL, got %q", cfg.Memory.OllamaURL)
	}
	if cfg.Memory.EmbedModel != "test-model" {
		t.Errorf("expected test-model, got %q", cfg.Memory.EmbedModel)
	}
}

func TestConfig_DBPath(t *testing.T) {
	_ = newTestConfigDir(t)
	cfg := DefaultConfig()
	dbPath := cfg.DBPath()
	if dbPath == "" {
		t.Error("expected non-empty DB path")
	}
	// Should contain "memory.db"
	if filepath.Base(dbPath) != "memory.db" {
		t.Errorf("expected DB path to end with memory.db, got %q", dbPath)
	}
}

func TestConfig_OllamaURL(t *testing.T) {
	cfg := DefaultConfig()
	url, err := cfg.OllamaURL()
	if err != nil {
		t.Fatalf("OllamaURL: %v", err)
	}
	if url != "http://localhost:11434" {
		t.Errorf("expected http://localhost:11434, got %q", url)
	}
}

func TestWriteConfigYAML_NewFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	written, err := WriteConfigYAML(configPath, map[string]any{
		"memory": map[string]any{
			"ollama_url": "http://localhost:11434",
		},
	}, false)
	if err != nil {
		t.Fatalf("WriteConfigYAML: %v", err)
	}
	if !written {
		t.Error("expected written=true for new file")
	}

	// Verify the file was created with 0600 permissions
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected 0600 permissions, got %o", perm)
	}
}

func TestWriteConfigYAML_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write initial file
	os.WriteFile(configPath, []byte("test: true\n"), 0600)

	written, err := WriteConfigYAML(configPath, map[string]any{"test": false}, false)
	if err != nil {
		t.Fatalf("WriteConfigYAML: %v", err)
	}
	if written {
		t.Error("expected written=false when file exists and force=false")
	}
}

func TestWriteConfigYAML_Force(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write initial file
	os.WriteFile(configPath, []byte("test: true\n"), 0600)

	written, err := WriteConfigYAML(configPath, map[string]any{"test": false}, true)
	if err != nil {
		t.Fatalf("WriteConfigYAML: %v", err)
	}
	if !written {
		t.Error("expected written=true when force=true")
	}
}