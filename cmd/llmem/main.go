package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/MichielDean/LLMem/internal/config"
	"github.com/MichielDean/LLMem/internal/embed"
	"github.com/MichielDean/LLMem/internal/extract"
	"github.com/MichielDean/LLMem/internal/paths"
	"github.com/MichielDean/LLMem/internal/store"
	"github.com/spf13/cobra"
)

var (
	dbPath     string
	jsonOutput bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "llmem",
		Short: "LLMem — structured memory for LLM agents",
		Long:  "LLMem provides persistent memory storage, search, and consolidation for LLM agents.",
	}

	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "Path to the memory database (default: ~/.config/llmem/memory.db)")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")

	rootCmd.AddCommand(
		addCmd(),
		getCmd(),
		searchCmd(),
		listCmd(),
		updateCmd(),
		deleteCmd(),
		exportCmd(),
		importCmd(),
		initCmd(),
		metricsCmd(),

		pinCmd(),
		unpinCmd(),
		briefCmd(),
		amendCmd(),
		migrateTypesCmd(),

		backfillEmbeddingsCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// resolveDBPath returns the configured database path.
func resolveDBPath() string {
	if dbPath != "" {
		return dbPath
	}
	return paths.GetDBPath()
}

// loadConfig loads the LLMem configuration from the default config path.
func loadConfig() (*config.Config, error) {
	cfg, err := config.LoadConfig(paths.GetConfigPath())
	if err != nil {
		return nil, fmt.Errorf("llmem: load config: %w", err)
	}
	return cfg, nil
}

// openStore creates a MemoryStore with vector search enabled.
func openStore() (*store.MemoryStore, error) {
	cfg := store.StoreConfig{
		DBPath: resolveDBPath(),
	}
	ms, err := store.NewMemoryStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("llmem: failed to initialize store: %w", err)
	}
	return ms, nil
}

// extractCmd builds the "llmem extract" subcommand, which extracts candidate
// memories from text (or a file) using the configured Ollama extraction model
// and stores them. httpClient is an optional pre-configured HTTP client for
// test injection (httptest.NewServer). When nil, Ollama URLs are validated
// via urlvalidate.
func extractCmd(httpClient *http.Client) *cobra.Command {
	var (
		textVal   string
		fileVal   string
		dryRunVal bool
	)
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract memories from text using the configured Ollama extraction model",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Exactly one of --text / --file is required.
			if textVal != "" && fileVal != "" {
				return fmt.Errorf("llmem: extract: --text and --file are mutually exclusive")
			}
			if textVal == "" && fileVal == "" {
				return fmt.Errorf("llmem: extract: exactly one of --text or --file is required")
			}

			source := textVal
			if fileVal != "" {
				resolvedFile, err := filepath.Abs(fileVal)
				if err != nil {
					return fmt.Errorf("llmem: extract: resolve file path: %w", err)
				}
				if paths.IsBlockedPath(resolvedFile) {
					return fmt.Errorf("llmem: extract: file path targets a blocked system directory: %s", resolvedFile)
				}
				info, err := os.Stat(resolvedFile)
				if err != nil {
					return fmt.Errorf("llmem: extract: stat file: %w", err)
				}
				if info.Size() > 10*1024*1024 {
					return fmt.Errorf("llmem: extract: file too large (max 10 MiB): %s", resolvedFile)
				}
				data, err := os.ReadFile(resolvedFile)
				if err != nil {
					return fmt.Errorf("llmem: extract: read file: %w", err)
				}
				source = string(data)
			}

			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("llmem: extract: load config: %w", err)
			}

			extractEngine, err := extract.NewExtractionEngine(extract.ExtractionConfig{
				Model:      cfg.Memory.ExtractModel,
				BaseURL:    cfg.Memory.OllamaURL,
				HTTPClient: httpClient,
			})
			if err != nil {
				return fmt.Errorf("llmem: extract: failed to create extraction engine: %w", err)
			}

			availCtx, availCancel := context.WithTimeout(context.Background(), 5*time.Second)
			available := extractEngine.CheckAvailable(availCtx)
			availCancel()
			if !available {
				slog.Warn("llmem: extract: extraction model unavailable, skipping",
					"model", cfg.Memory.ExtractModel,
					"ollama_url", cfg.Memory.OllamaURL)
				return nil
			}

			extracted := extractEngine.Extract(context.Background(), source)

			// Embedding engine is only constructed when an embed model is configured.
			var embedEngine *embed.EmbeddingEngine
			if cfg.Memory.EmbedModel != "" {
				embedEngine, err = embed.NewEmbeddingEngine(embed.EmbeddingConfig{
					Model:      cfg.Memory.EmbedModel,
					BaseURL:    cfg.Memory.OllamaURL,
					HTTPClient: httpClient,
				})
				if err != nil {
					slog.Warn("llmem: extract: failed to create embedding engine, storing without embeddings", "error", err)
					embedEngine = nil
				}
			}

			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			for _, item := range extracted {
				memType, _ := item["type"].(string)
				content, _ := item["content"].(string)
				if content == "" || memType == "" {
					slog.Debug("llmem: extract: skipping extracted item with missing type or content",
						"type", memType, "has_content", content != "")
					continue
				}
				confidence, _ := item["confidence"].(float64)
				if dryRunVal {
					if err := printMemoryOutput(&store.Memory{
						Type:       memType,
						Content:    content,
						Confidence: confidence,
						Source:     "extract",
					}); err != nil {
						return fmt.Errorf("llmem: extract: print memory: %w", err)
					}
					continue
				}
				var embedding []byte
				if embedEngine != nil {
					vec, embedErr := embedEngine.Embed(context.Background(), content)
					if embedErr != nil {
						slog.Warn("llmem: extract: failed to embed", "type", memType, "error", embedErr)
					} else {
						embedding = store.VecToBytes(vec)
					}
				}
				id, addErr := ms.Add(context.Background(), store.AddParams{
					Type:       memType,
					Content:    content,
					Source:     "extract",
					Confidence: confidence,
					Embedding:  embedding,
				})
				if addErr != nil {
					if errors.Is(addErr, store.ErrUnregisteredType) {
						slog.Debug("llmem: extract: skipping memory with unregistered type", "type", memType)
						continue
					}
					return fmt.Errorf("llmem: extract: store memory of type %q: %w", memType, addErr)
				}
				storedMem, getErr := ms.Get(context.Background(), id, false)
				if getErr != nil || storedMem == nil {
					slog.Warn("llmem: extract: failed to fetch stored memory for output", "id", id, "error", getErr)
					continue
				}
				if err := printMemoryOutput(storedMem); err != nil {
					return fmt.Errorf("llmem: extract: print memory: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&textVal, "text", "", "Text to extract memories from")
	cmd.Flags().StringVar(&fileVal, "file", "", "Read text from file (max 10 MiB)")
	cmd.Flags().BoolVar(&dryRunVal, "dry-run", false, "Extract and print, but store nothing")
	return cmd
}

// printMemoryOutput renders a memory to stdout in the format selected by --json.
// JSON marshal errors are returned (and thus propagated to RunE) rather than
// being silently swallowed. Never returns an error in human-readable mode.
func printMemoryOutput(m *store.Memory) error {
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	}
	fmt.Printf("%s [%s] %.2f %s\n", m.ID, m.Type, m.Confidence, m.Content)
	return nil
}

func addCmd() *cobra.Command {
	var (
		typeVal       string
		contentVal    string
		summaryVal    string
		sourceVal     string
		confidenceVal float64
		validUntilVal string
		metadataVal   string
		fileVal       string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new memory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if fileVal != "" {
				resolvedFile, err := filepath.Abs(fileVal)
				if err != nil {
					return fmt.Errorf("llmem: add: resolve file path: %w", err)
				}
				if paths.IsBlockedPath(resolvedFile) {
					return fmt.Errorf("llmem: add: file path targets a blocked system directory: %s", resolvedFile)
				}
				data, err := os.ReadFile(resolvedFile)
				if err != nil {
					return fmt.Errorf("llmem: add: read file: %w", err)
				}
				contentVal = string(data)
			}
			if contentVal == "" {
				return fmt.Errorf("llmem: add: content is required (use --content or --file)")
			}

			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			var metadata map[string]any
			if metadataVal != "" {
				if err := json.Unmarshal([]byte(metadataVal), &metadata); err != nil {
					return fmt.Errorf("llmem: add: invalid metadata JSON: %w", err)
				}
			}

			id, err := ms.Add(context.Background(), store.AddParams{
				Type:       typeVal,
				Content:    contentVal,
				Summary:    summaryVal,
				Source:     sourceVal,
				Confidence: confidenceVal,
				ValidUntil: validUntilVal,
				Metadata:   metadata,
			})
			if err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&typeVal, "type", "stable", "Memory type")
	cmd.Flags().StringVar(&contentVal, "content", "", "Memory content")
	cmd.Flags().StringVar(&summaryVal, "summary", "", "Memory summary")
	cmd.Flags().StringVar(&sourceVal, "source", "manual", "Memory source")
	cmd.Flags().Float64Var(&confidenceVal, "confidence", 0.8, "Confidence score (0.0-1.0)")
	cmd.Flags().StringVar(&validUntilVal, "valid-until", "", "ISO 8601 timestamp for validity expiration")
	cmd.Flags().StringVar(&metadataVal, "metadata", "", "JSON metadata")
	cmd.Flags().StringVar(&fileVal, "file", "", "Read content from file")
	return cmd
}

func getCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a memory by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			mem, err := ms.Get(context.Background(), args[0], false)
			if err != nil {
				return err
			}
			if mem == nil {
				return fmt.Errorf("llmem: get: memory %s not found", args[0])
			}
			data, err := json.MarshalIndent(mem, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		},
	}
}

func searchCmd() *cobra.Command {
	var (
		typeVal      string
		limitVal     int
		validOnly    bool
		ftsOnly      bool
		semanticOnly bool
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search memories",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			results, err := ms.Search(context.Background(), store.SearchParams{
				Query:        args[0],
				Type:         typeVal,
				ValidOnly:    validOnly,
				Limit:        limitVal,
				FTSOnly:      ftsOnly,
				SemanticOnly: semanticOnly,
			})
			if err != nil {
				return err
			}
			ids := make([]string, len(results))
			for i, m := range results {
				ids[i] = m.ID
			}
			if len(ids) > 0 {
				if _, err := ms.TouchBatch(context.Background(), ids); err != nil {
					slog.Debug("llmem: search: failed to touch results", "error", err)
				}
			}
			for _, m := range results {
				if err := printMemoryOutput(m); err != nil {
					return fmt.Errorf("llmem: search: print result: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typeVal, "type", "", "Filter by memory type")
	cmd.Flags().IntVar(&limitVal, "limit", 20, "Maximum results")
	cmd.Flags().BoolVar(&validOnly, "valid-only", false, "Only show valid memories")
	cmd.Flags().BoolVar(&ftsOnly, "fts-only", false, "FTS search only")
	cmd.Flags().BoolVar(&semanticOnly, "semantic-only", false, "Semantic search only")
	return cmd
}

func listCmd() *cobra.Command {
	var (
		typeVal  string
		limitVal int
		allVal   bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memories",
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			validOnly := !allVal
			results, err := ms.Search(context.Background(), store.SearchParams{
				Type:      typeVal,
				ValidOnly: validOnly,
				Limit:     limitVal,
			})
			if err != nil {
				return err
			}
			ids := make([]string, len(results))
			for i, m := range results {
				ids[i] = m.ID
			}
			if len(ids) > 0 {
				if _, err := ms.TouchBatch(context.Background(), ids); err != nil {
					slog.Debug("llmem: list: failed to touch results", "error", err)
				}
			}
			for _, m := range results {
				if err := printMemoryOutput(m); err != nil {
					return fmt.Errorf("llmem: list: print result: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typeVal, "type", "", "Filter by memory type")
	cmd.Flags().IntVar(&limitVal, "limit", 100, "Maximum results")
	cmd.Flags().BoolVar(&allVal, "all", false, "Show all memories including expired")
	return cmd
}

func updateCmd() *cobra.Command {
	var (
		contentVal    string
		summaryVal    string
		confidenceVal float64
		validUntilVal string
		metadataVal   string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			params := store.UpdateParams{ID: args[0]}
			if cmd.Flags().Changed("content") {
				params.Content = &contentVal
			}
			if cmd.Flags().Changed("summary") {
				params.Summary = &summaryVal
			}
			if cmd.Flags().Changed("confidence") {
				params.Confidence = &confidenceVal
			}
			if cmd.Flags().Changed("valid-until") {
				params.ValidUntil = &validUntilVal
			}
			if metadataVal != "" {
				var metadata map[string]any
				if err := json.Unmarshal([]byte(metadataVal), &metadata); err != nil {
					return fmt.Errorf("llmem: update: invalid metadata JSON: %w", err)
				}
				params.Metadata = metadata
			}

			ok, err := ms.Update(context.Background(), params)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("llmem: update: memory %s not found", args[0])
			}
			fmt.Printf("Updated %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&contentVal, "content", "", "New content")
	cmd.Flags().StringVar(&summaryVal, "summary", "", "New summary")
	cmd.Flags().Float64Var(&confidenceVal, "confidence", 0, "New confidence score")
	cmd.Flags().StringVar(&validUntilVal, "valid-until", "", "New validity expiration")
	cmd.Flags().StringVar(&metadataVal, "metadata", "", "New JSON metadata")
	return cmd
}

func deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			ok, err := ms.Delete(context.Background(), args[0])
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("llmem: delete: memory %s not found", args[0])
			}
			fmt.Printf("Deleted %s\n", args[0])
			return nil
		},
	}
}

func exportCmd() *cobra.Command {
	var (
		outputVal string
		limitVal  int
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export memories as JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			var limit *int
			if cmd.Flags().Changed("limit") {
				limit = &limitVal
			}

			memories, err := ms.ExportAll(context.Background(), limit)
			if err != nil {
				return err
			}

			data, err := json.MarshalIndent(memories, "", "  ")
			if err != nil {
				return err
			}

			if outputVal != "" {
				resolved, err := paths.ValidateWritePath(outputVal, "export output")
				if err != nil {
					return fmt.Errorf("llmem: export: %w", err)
				}
				if err := os.WriteFile(resolved, data, 0600); err != nil {
					return fmt.Errorf("llmem: export: write: %w", err)
				}
				fmt.Printf("Exported %d memories to %s\n", len(memories), resolved)
			} else {
				fmt.Println(string(data))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outputVal, "output", "", "Output file path")
	cmd.Flags().IntVar(&limitVal, "limit", 10000, "Maximum memories to export")
	return cmd
}

func importCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file>",
		Short: "Import memories from JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			// Validate file path
			resolved, err := paths.ValidateWritePath(args[0], "import file")
			if err != nil {
				return fmt.Errorf("llmem: import: %w", err)
			}

			// Check file size (max 10 MiB)
			info, err := os.Stat(resolved)
			if err != nil {
				return fmt.Errorf("llmem: import: stat: %w", err)
			}
			if info.Size() > 10*1024*1024 {
				return fmt.Errorf("llmem: import: file too large (max 10 MiB)")
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return fmt.Errorf("llmem: import: read: %w", err)
			}

			var memories []store.ImportMemory
			if err := json.Unmarshal(data, &memories); err != nil {
				return fmt.Errorf("llmem: import: parse JSON: %w", err)
			}

			count, err := ms.ImportMemories(context.Background(), memories)
			if err != nil {
				return err
			}
			fmt.Printf("Imported %d memories\n", count)
			return nil
		},
	}
}

func initCmd() *cobra.Command {
	var (
		ollamaURLVal string
		forceVal     bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize LLMem configuration and database",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Migrate from ~/.lobsterdog/ if applicable
			migrated, err := paths.MigrateFromLobsterdog()
			if err != nil {
				slog.Warn("llmem: init: migration check failed", "error", err)
			}
			if migrated {
				fmt.Println("Migrated data from ~/.lobsterdog/ to ~/.config/llmem/")
			}

			// Create home directory
			homeDir := paths.GetHomeDir()
			if err := os.MkdirAll(homeDir, 0700); err != nil {
				return fmt.Errorf("llmem: init: create home directory: %w", err)
			}

			// Write config
			configPath := paths.GetConfigPath()
			defaultCfg := map[string]any{
				"memory": map[string]any{
					"ollama_url":  defaultIfEmpty(ollamaURLVal, "http://localhost:11434"),
					"embed_model": "nomic-embed-text",
				},
			}
			written, err := config.WriteConfigYAML(configPath, defaultCfg, forceVal)
			if err != nil {
				return fmt.Errorf("llmem: init: write config: %w", err)
			}
			if written {
				fmt.Printf("Created config at %s\n", configPath)
			} else {
				fmt.Printf("Config already exists at %s (use --force to overwrite)\n", configPath)
			}

			// Initialize database
			dbPathVal := paths.GetDBPath()
			ms, err := store.NewMemoryStore(store.StoreConfig{
				DBPath:     dbPathVal,
				DisableVec: true,
			})
			if err != nil {
				return fmt.Errorf("llmem: init: create database: %w", err)
			}
			ms.Close()
			fmt.Printf("Created database at %s\n", dbPathVal)

			return nil
		},
	}
	cmd.Flags().StringVar(&ollamaURLVal, "ollama-url", "", "Ollama base URL")
	cmd.Flags().BoolVar(&forceVal, "force", false, "Overwrite existing config")
	return cmd
}

func metricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "Report embedding quality metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			count, err := ms.CountEmbeddings(context.Background())
			if err != nil {
				return err
			}
			fmt.Printf("Embeddings: %d\n", count)
			return nil
		},
	}
}

func pinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pin <id>",
		Short: "Pin a stable memory (exempt from TTL pruning)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			ok, err := ms.Pin(context.Background(), args[0])
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("llmem: pin: memory %s not found", args[0])
			}
			fmt.Printf("Pinned %s\n", args[0])
			return nil
		},
	}
}

func unpinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unpin <id>",
		Short: "Unpin a stable memory (re-enables TTL pruning)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			ok, err := ms.Unpin(context.Background(), args[0])
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("llmem: unpin: memory %s not found", args[0])
			}
			fmt.Printf("Unpinned %s\n", args[0])
			return nil
		},
	}
}

func briefCmd() *cobra.Command {
	var maxLines int
	cmd := &cobra.Command{
		Use:   "brief",
		Short: "Generate and display the session briefing",
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			cfg := store.DefaultBriefingConfig()
			if maxLines > 0 {
				cfg.MaxLines = maxLines
			}

			b, err := ms.GenerateBriefing(context.Background(), cfg)
			if err != nil {
				return err
			}
			fmt.Print(b.Format())
			return nil
		},
	}
	cmd.Flags().IntVar(&maxLines, "max-lines", 0, "Maximum lines in briefing (default: 25)")
	return cmd
}

func amendCmd() *cobra.Command {
	var (
		volatileText string
		pinIDs        []string
		unpinIDs       []string
		pruneOnly      bool
	)
	cmd := &cobra.Command{
		Use:   "amend",
		Short: "Session-end cycle: update volatile state, manage pins, prune expired, regenerate briefing",
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			ctx := context.Background()

			// 1. Update volatile state
			if volatileText != "" {
				_, err := ms.Add(ctx, store.AddParams{
					Type:    "volatile",
					Content: volatileText,
					Source:  "amend",
				})
				if err != nil {
					return fmt.Errorf("llmem: amend: add volatile: %w", err)
				}
				fmt.Println("Updated volatile state")
			}

			// 2. Pin memories
			for _, id := range pinIDs {
				ok, err := ms.Pin(ctx, id)
				if err != nil {
					slog.Warn("llmem: amend: pin failed", "id", id, "error", err)
					continue
				}
				if ok {
					fmt.Printf("Pinned %s\n", id)
				} else {
					slog.Warn("llmem: amend: pin: memory not found", "id", id)
				}
			}

			// 3. Unpin memories
			for _, id := range unpinIDs {
				ok, err := ms.Unpin(ctx, id)
				if err != nil {
					slog.Warn("llmem: amend: unpin failed", "id", id, "error", err)
					continue
				}
				if ok {
					fmt.Printf("Unpinned %s\n", id)
				} else {
					slog.Warn("llmem: amend: unpin: memory not found", "id", id)
				}
			}

			// 4. Prune expired
			pruned, err := ms.PruneExpired(ctx)
			if err != nil {
				return fmt.Errorf("llmem: amend: prune: %w", err)
			}
			if pruned > 0 {
				fmt.Printf("Pruned %d expired memories\n", pruned)
			}

			// 5. Output updated briefing (unless prune-only)
			if !pruneOnly {
				b, err := ms.GenerateBriefing(ctx, store.DefaultBriefingConfig())
				if err != nil {
					return fmt.Errorf("llmem: amend: briefing: %w", err)
				}
				fmt.Print(b.Format())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&volatileText, "volatile", "", "Update volatile session-state memory with this content")
	cmd.Flags().StringArrayVar(&pinIDs, "pin", nil, "Pin a stable memory by ID (repeatable)")
	cmd.Flags().StringArrayVar(&unpinIDs, "unpin", nil, "Unpin a stable memory by ID (repeatable)")
	cmd.Flags().BoolVar(&pruneOnly, "prune-only", false, "Only run TTL pruning, skip briefing output")
	return cmd
}

func migrateTypesCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate-types",
		Short: "One-time migration from 7 legacy types to 3 new tier types",
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			result, err := ms.MigrateTypes(context.Background(), dryRun)
			if err != nil {
				return err
			}

			if dryRun {
				fmt.Println("Dry run — no changes made:")
			}
			fmt.Printf("Retyped:   %d\n", result.Retyped)
			fmt.Printf("Merged:    %d\n", result.Merged)
			fmt.Printf("Pruned:    %d\n", result.Pruned)
			fmt.Printf("Compressed: %d\n", result.Compressed)
			fmt.Printf("Extracted: %d\n", result.Extracted)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without modifying the store")
	return cmd
}

func backfillEmbeddingsCmd() *cobra.Command {
	var (
		batchSize int
		dryRunVal bool
	)
	cmd := &cobra.Command{
		Use:   "backfill-embeddings",
		Short: "Generate embeddings for memories that lack them",
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			embeddingEngine, err := embed.NewEmbeddingEngine(embed.EmbeddingConfig{})
			if err != nil {
				return fmt.Errorf("llmem: backfill-embeddings: failed to create embedding engine: %w", err)
			}

			availCtx, availCancel := context.WithTimeout(context.Background(), 5*time.Second)
			available := embeddingEngine.CheckAvailable(availCtx)
			availCancel()
			if !available {
				return fmt.Errorf("llmem: backfill-embeddings: Ollama embedding model not available (is Ollama running with nomic-embed-text?)")
			}

			memories, err := ms.Search(context.Background(), store.SearchParams{
				ValidOnly: true,
				Limit:     10000,
			})
			if err != nil {
				return err
			}

			var missing []*store.Memory
			for _, m := range memories {
				if len(m.Embedding) == 0 {
					missing = append(missing, m)
				}
			}

			if len(missing) == 0 {
				fmt.Println("All memories have embeddings. Nothing to backfill.")
				return nil
			}

			if dryRunVal {
				fmt.Printf("Would backfill %d memories with embeddings (dry run)\n", len(missing))
				return nil
			}

			backfilled := 0
			failed := 0
			for i, m := range missing {
				vec, embedErr := embeddingEngine.Embed(context.Background(), m.Content)
				if embedErr != nil {
					slog.Warn("llmem: backfill-embeddings: failed to embed", "id", m.ID, "error", embedErr)
					failed++
					continue
				}
				embBytes := store.VecToBytes(vec)
				_, updateErr := ms.Update(context.Background(), store.UpdateParams{
					ID:        m.ID,
					Embedding: embBytes,
				})
				if updateErr != nil {
					slog.Warn("llmem: backfill-embeddings: failed to update", "id", m.ID, "error", updateErr)
					failed++
					continue
				}
				backfilled++

				if batchSize > 0 && (i+1)%batchSize == 0 {
					fmt.Printf("Progress: %d/%d backfilled, %d failed\n", backfilled, len(missing), failed)
				}
			}

			fmt.Printf("Backfilled %d memories with embeddings (%d failed)\n", backfilled, failed)
			return nil
		},
	}
	cmd.Flags().IntVar(&batchSize, "batch-size", 50, "Print progress every N memories")
	cmd.Flags().BoolVar(&dryRunVal, "dry-run", false, "Count memories without embeddings without backfilling")
	return cmd
}

func defaultIfEmpty(val, defaultVal string) string {
	if val == "" {
		return defaultVal
	}
	return val
}
