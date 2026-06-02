# LLMem Configuration

Configuration reference for paths, providers, and the dream cycle. [Back to README](../README.md)

## General Configuration

LLMem looks for configuration at `~/.config/llmem/config.yaml`. If this file doesn't exist, sensible defaults are used.

### Path Resolution

| Path | Default | Override |
|------|---------|----------|
| Home directory | `~/.config/llmem/` | `LMEM_HOME` env var |
| Database | `~/.config/llmem/memory.db` | `config.yaml: memory.db` |
| Config file | `~/.config/llmem/config.yaml` | — |
| Dream diary | `~/.config/llmem/dream-diary.md` | `config.yaml: dream.diary_path` |

**Backward compatibility:** If `~/.lobsterdog/` exists and `~/.config/llmem/` doesn't, LLMem will use the legacy path. Call `migrate_from_lobsterdog()` to copy data to the new location.

**`LMEM_HOME` env var:** Set this to override the home directory entirely. The path is validated against directory traversal, system directories, and symlink attacks.

### config.yaml Reference

```yaml
memory:
  db: ""                       # Database path (default: ~/.config/llmem/memory.db)
  ollama_url: http://localhost:11434
  embed_model: nomic-embed-text
  extract_model: glm-5.1:cloud
  context_budget: 4000
  auto_extract: true
  max_file_size: 10485760      # 10MB

dream:
  enabled: true                # (Python only — not wired in Go CLI dream command)
  schedule: "*-*-* 03:00:00"    # (Python only — used by systemd timer generation)
  similarity_threshold: 0.92
  decay_rate: 0.05
  decay_interval_days: 30
  decay_floor: 0.3
  confidence_floor: 0.3
  boost_threshold: 5
  boost_amount: 0.05
  diary_path: null             # Auto-resolved from GetDreamDiaryPath()
  report_path: null            # Auto-resolved from GetDreamReportPath()
  auto_link_threshold: 0.85    # Cosine similarity threshold for auto-linking related memories
  stale_procedure_days: 30     # Days after which an unaccessed procedure memory decays at 2x rate

opencode:
  db_path: ~/.local/share/opencode/opencode.db
  context_dir: null            # Auto-resolved from GetContextDir()

session:
  adapter: opencode           # Which session adapter to use: opencode, copilot, or none
  debounce_seconds: 30        # Idle debounce interval in seconds
```

> **Note:** The following fields exist in the Python config but are **not wired** in the Go implementation: `min_score`, `min_recall_count`, `min_unique_queries`, `boost_on_promote`, `merge_model`, `calibration_enabled`, `calibration_lookback_days`, `inbox_capacity`, `correction_detection` (top-level), and `copilot` (top-level). Setting these in `config.yaml` has no effect when using the Go CLI.

> **Note:** The Go config resolves `db_path` for OpenCode as `~/.local/share/opencode/opencode.db` using `filepath.Join` with proper path handling. The database is opened in read-only mode (`mode=ro`) using a `file:` URI prefix to ensure the `modernc.org/sqlite` driver enforces the read-only constraint. Without the `file:` prefix, query parameters like `mode=ro` are silently ignored and the driver opens in read-write mode.

## Session Adapter Configuration

LLMem uses a session adapter to read conversation transcripts for memory extraction and context injection. The adapter type determines where session data comes from:

| Adapter | Source | Full transcripts? | Use when |
|---------|--------|-------------------|----------|
| `opencode` | `~/.local/share/opencode/opencode.db` (SQLite) | Yes | Running with OpenCode (default) |
| `copilot` | `~/.copilot/session-state/` (YAML + markdown) | Only if `--share` is used | Running with GitHub Copilot CLI |
| `none` | No adapter | No | Running standalone, no session data |

**Key behavior differences:**

- `llmem search` (session.created) — works with all adapters, including `none`. Searches relevant memories without needing session transcripts.
- `llmem search --limit 15` (session.compacting) — works with all adapters. Reads from MemoryStore, not the session DB.

**OpenCode adapter specifics:** The adapter reads from the OpenCode SQLite database in read-only mode. When a session has `time_compacting` set (indicating context compaction), `ReadTranscript` returns only messages after the compaction time (recent context), not the full history. The `Close()` method on the adapter is idempotent and should be called when done (typically via `defer`).

**Copilot adapter and transcripts:** Copilot CLI does not persist conversation transcripts to a database. The adapter reads session metadata from `workspace.yaml` files in `~/.copilot/session-state/`. Full transcripts are only available when the user runs Copilot with `--share`, which writes a markdown file. Without `--share`, `on_idle` and `on_ending` return `no_transcript` gracefully.

**Auto-detection:** `llmem init` auto-detects the adapter type based on which session state directory exists. If `~/.local/share/opencode/opencode.db` exists, it uses `opencode`. If only `~/.copilot/session-state/` exists, it uses `copilot`.