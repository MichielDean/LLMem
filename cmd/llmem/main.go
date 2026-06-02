package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/MichielDean/LLMem/internal/config"
	"github.com/MichielDean/LLMem/internal/dream"
	"github.com/MichielDean/LLMem/internal/embed"
	"github.com/MichielDean/LLMem/internal/extract"
	"github.com/MichielDean/LLMem/internal/paths"
	"github.com/MichielDean/LLMem/internal/store"
	"github.com/spf13/cobra"
)

var (
	dbPath     string
	jsonOutput  bool
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
		statsCmd(),
		updateCmd(),
		invalidateCmd(),
		deleteCmd(),
		exportCmd(),
		importCmd(),
		initCmd(),
		metricsCmd(),
		dreamCmd(),

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



// openExtractionEngine creates an ExtractionEngine for session hooks.
// Returns nil on failure — the coordinator gracefully handles a nil engine
// by skipping extraction (graceful degradation).
func openExtractionEngine() *extract.ExtractionEngine {
	engine, err := extract.NewExtractionEngine(extract.ExtractionConfig{})
	if err != nil {
		slog.Debug("llmem: failed to create extraction engine, skipping", "error", err)
		return nil
	}
	return engine
}

// openEmbeddingEngine creates an EmbeddingEngine for session hooks.
// Returns nil on failure — the coordinator gracefully handles a nil engine
// by storing memories without embeddings.
func openEmbeddingEngine() *embed.EmbeddingEngine {
	engine, err := embed.NewEmbeddingEngine(embed.EmbeddingConfig{})
	if err != nil {
		slog.Debug("llmem: failed to create embedding engine, skipping", "error", err)
		return nil
	}
	return engine
}

func addCmd() *cobra.Command {
	var (
		typeVal      string
		contentVal   string
		summaryVal   string
		sourceVal    string
		confidenceVal float64
		validUntilVal string
		metadataVal  string
		fileVal      string
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
	cmd.Flags().StringVar(&typeVal, "type", "fact", "Memory type")
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
		typeVal    string
		limitVal   int
		validOnly  bool
		ftsOnly    bool
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
				if jsonOutput {
					data, _ := json.MarshalIndent(m, "", "  ")
					fmt.Println(string(data))
				} else {
					fmt.Printf("%s [%s] %.2f %s\n", m.ID, m.Type, m.Confidence, m.Content)
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
		typeVal   string
		limitVal  int
		allVal    bool
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
				if jsonOutput {
					data, _ := json.MarshalIndent(m, "", "  ")
					fmt.Println(string(data))
				} else {
					fmt.Printf("%s [%s] %.2f %s\n", m.ID, m.Type, m.Confidence, m.Content)
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

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show memory statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			total, err := ms.Count(context.Background(), false)
			if err != nil {
				return err
			}
			active, err := ms.Count(context.Background(), true)
			if err != nil {
				return err
			}
			byType, err := ms.CountByType(context.Background(), true)
			if err != nil {
				return err
			}
			expired := total - active

			if jsonOutput {
				data := map[string]any{
					"total":   total,
					"active":  active,
					"expired": expired,
					"by_type": byType,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(data)
			}

			fmt.Printf("Total: %d\nActive: %d\nExpired: %d\n\nBy type:\n", total, active, expired)
			for typ, cnt := range byType {
				fmt.Printf("  %s: %d\n", typ, cnt)
			}
			return nil
		},
	}
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

func invalidateCmd() *cobra.Command {
	var reasonVal string
	cmd := &cobra.Command{
		Use:   "invalidate <id>",
		Short: "Invalidate a memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			ok, err := ms.Invalidate(context.Background(), args[0], reasonVal)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("llmem: invalidate: memory %s not found", args[0])
			}
			fmt.Printf("Invalidated %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&reasonVal, "reason", "", "Invalidation reason")
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
					"ollama_url": defaultIfEmpty(ollamaURLVal, "http://localhost:11434"),
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

func dreamCmd() *cobra.Command {
	var (
		applyVal   bool
		dryRunVal  bool
		phaseVal   string
		reportVal  string
	)
	cmd := &cobra.Command{
		Use:   "dream",
		Short: "Run dream consolidation cycle",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunVal {
				applyVal = false
			}
			ms, err := openStore()
			if err != nil {
				return err
			}
			defer ms.Close()

			// Load config to populate dream settings
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("llmem: dream: load config: %w", err)
			}

			dreamerCfg := cfg.DreamerConfig()
			dreamerCfg.Store = ms

			d, err := dream.NewDreamer(dreamerCfg)
			if err != nil {
				return err
			}

			result, err := d.Run(context.Background(), applyVal, phaseVal)
			if err != nil {
				return err
			}

			if result.Light != nil {
				fmt.Printf("Light: %d duplicate pairs\n", result.Light.DuplicatePairs)
			}
			if result.Deep != nil {
				fmt.Printf("Deep: %d decayed, %d boosted, %d merged, %d auto-linked\n",
					result.Deep.DecayedCount, result.Deep.BoostedCount,
					result.Deep.MergedCount, result.Deep.AutoLinkedCount)
			}
			if result.Rem != nil {
				fmt.Printf("REM: %d total memories, %d active\n",
					result.Rem.TotalMemories, result.Rem.ActiveMemories)
				for _, theme := range result.Rem.Themes {
					fmt.Printf("  Theme: %s\n", theme)
				}
			}

			if reportVal != "" {
				if err := d.GenerateDreamReport(result, reportVal); err != nil {
					return err
				}
				fmt.Printf("Dream report written to %s\n", reportVal)
			}

			return nil
		},
	}
	cmd.Flags().BoolVar(&applyVal, "apply", false, "Apply changes (default: dry run)")
	cmd.Flags().BoolVar(&dryRunVal, "dry-run", false, "Dry run only (default: true). Shorthand for omitting --apply.")
	cmd.Flags().StringVar(&phaseVal, "phase", "", "Run specific phase: light, deep, rem (default: all)")
	cmd.Flags().StringVar(&reportVal, "report", "", "Generate HTML dream report at this path")
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