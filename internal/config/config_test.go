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
	if cfg.Dream.SimilarityThreshold != 0.92 {
		t.Errorf("expected default similarity threshold 0.92, got %f", cfg.Dream.SimilarityThreshold)
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

func TestConfig_DreamConfigResolved(t *testing.T) {
	cfg := DefaultConfig()
	dreamCfg := cfg.DreamConfigResolved()
	if dreamCfg.SimilarityThreshold != 0.92 {
		t.Errorf("expected similarity threshold 0.92, got %f", dreamCfg.SimilarityThreshold)
	}
	if dreamCfg.DecayRate != 0.05 {
		t.Errorf("expected decay rate 0.05, got %f", dreamCfg.DecayRate)
	}
}

func TestConfig_DreamerConfig(t *testing.T) {
	cfg := DefaultConfig()
	dc := cfg.DreamerConfig()

	// Verify that DreamerConfig maps the live fields from DreamConfig
	if dc.SimilarityThreshold != 0.92 {
		t.Errorf("expected SimilarityThreshold 0.92, got %f", dc.SimilarityThreshold)
	}
	if dc.DecayRate != 0.05 {
		t.Errorf("expected DecayRate 0.05, got %f", dc.DecayRate)
	}
	if dc.BoostThreshold != 5 {
		t.Errorf("expected BoostThreshold 5, got %d", dc.BoostThreshold)
	}
	if dc.AutoLinkThreshold != 0.85 {
		t.Errorf("expected AutoLinkThreshold 0.85, got %f", dc.AutoLinkThreshold)
	}
	// Store field should be nil (caller must set)
	if dc.Store != nil {
		t.Error("expected Store to be nil (caller must set)")
	}
}

func TestConfig_DreamerConfig_CustomValues(t *testing.T) {
	dir := newTestConfigDir(t)
	configPath := filepath.Join(dir, "config.yaml")

	yamlContent := []byte("dream:\n  similarity_threshold: 0.85\n  decay_rate: 0.1\n  boost_threshold: 10\n")
	if err := os.WriteFile(configPath, yamlContent, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	dc := cfg.DreamerConfig()
	if dc.SimilarityThreshold != 0.85 {
		t.Errorf("expected SimilarityThreshold 0.85, got %f", dc.SimilarityThreshold)
	}
	if dc.DecayRate != 0.1 {
		t.Errorf("expected DecayRate 0.1, got %f", dc.DecayRate)
	}
	if dc.BoostThreshold != 10 {
		t.Errorf("expected BoostThreshold 10, got %d", dc.BoostThreshold)
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

func TestConfig_DreamerConfig_StaleProcedureDays(t *testing.T) {
	// Verify that DefaultConfig maps StaleProcedureDays correctly
	cfg := DefaultConfig()
	dc := cfg.DreamerConfig()

	if dc.StaleProcedureDays != 30 {
		t.Errorf("expected StaleProcedureDays=30 from default config, got %d", dc.StaleProcedureDays)
	}
}

func TestConfig_DreamerConfig_StaleProcedureDays_CustomValues(t *testing.T) {
	dir := newTestConfigDir(t)
	configPath := filepath.Join(dir, "config.yaml")

	yamlContent := []byte("dream:\n  similarity_threshold: 0.85\n  decay_rate: 0.1\n  stale_procedure_days: 14\n")
	if err := os.WriteFile(configPath, yamlContent, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	dc := cfg.DreamerConfig()
	if dc.StaleProcedureDays != 14 {
		t.Errorf("expected StaleProcedureDays=14 from custom config, got %d", dc.StaleProcedureDays)
	}
}