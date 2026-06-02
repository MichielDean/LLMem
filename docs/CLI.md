# LLMem CLI Reference

Complete reference for all `llmem` CLI commands and options. [Back to README](../README.md)

## CLI Reference

```
llmem [OPTIONS] COMMAND

Options:
  --db PATH    Path to memory database (default: ~/.config/llmem/memory.db)
  --json       Output results as JSON

Commands:
  add                 Add a memory
  get                 Get a memory by ID
  search              Search memories
  list                List memories
  stats               Show memory statistics
  update              Update a memory
  invalidate          Invalidate a memory (mark expired/wrong)
  delete              Delete a memory
  export              Export all memories to JSON
  import              Import memories from a JSON file
  init                Initialize the llmem memory system
  metrics             Report embedding quality metrics
  dream               Run the dream consolidation cycle

```

**Python-only commands** (not yet in the Go CLI): `register-type`, `types`, `note`, `inbox`, `consolidate`, `embed`, `learn` (codebase indexing), `suggest-categories`.

### `llmem add`

```bash
llmem add --type TYPE --content TEXT [--summary TEXT] [--source SOURCE] \
  [--confidence FLOAT] [--valid-until TIMESTAMP] [--metadata JSON] \
  [--file PATH]
```

- `--type`: Memory type (default: `fact`).
- `--content` or `--file`: The memory text, or read content from a file. Files in protected system directories (e.g. `/etc`, `/bin`, `/usr/bin`) are blocked to prevent arbitrary file reads.
- `--summary`: Memory summary.
- `--source`: Source of the memory (default: `manual`).
- `--confidence`: Confidence score 0–1 (default: 0.8).
- `--valid-until`: ISO 8601 timestamp for validity expiration.
- `--metadata`: JSON metadata string.
- `--file`: Read content from a file path. The path is resolved and validated against system directory traversal.

### `llmem search`

```bash
llmem search QUERY [--type TYPE] [--limit N] [--json] [--valid-only] \
  [--fts-only | --semantic-only]
```

Hybrid search combining FTS5 keyword search and vector semantic search via Reciprocal Rank Fusion (RRF).

- `--type`: Filter by memory type.
- `--limit`: Maximum results (default: 20).
- `--json`: Output results as JSON.
- `--valid-only`: Only show valid (not invalidated) memories.
- `--fts-only`: Use FTS5 keyword search only (no embedder needed).
- `--semantic-only`: Use semantic (embedding) search only (requires an embedder).

Without `--fts-only` or `--semantic-only`, the default mode is FTS5-only (since the Go CLI currently sets `DisableVec: true`). Python supports full hybrid mode with `--include-code`, `--traverse-refs`, and `--provider` flags.

### `llmem list`

```bash
llmem list [--type TYPE] [--all] [--limit N]
```

By default, excludes expired memories. Use `--all` to include them. `--limit` defaults to 100.

### `llmem stats`

Shows total, active, and expired memory counts, plus a breakdown by type. Use `--json` for JSON output.

### `llmem update`

```bash
llmem update ID [--content TEXT] [--summary TEXT] [--confidence FLOAT] \
  [--valid-until TIMESTAMP] [--metadata JSON]
```

Only fields with `--` flags are updated; omitted fields remain unchanged.

### `llmem invalidate`

```bash
llmem invalidate ID [--reason TEXT]
```

Marks a memory as no longer valid, with an optional reason.

### `llmem delete`

```bash
llmem delete ID
```

Permanently removes a memory (and its embeddings, FTS index entries, relations).

### `llmem export` / `llmem import`

```bash
llmem export [--output FILE] [--limit N]
llmem import FILE
```

Export produces a JSON array of all memories (default limit: 10,000). Import validates that each entry has `type` and `content` string fields before inserting.

- `--output FILE`: Write export to a file. The output path is validated against traversal attacks.
- `--limit N`: Maximum memories to export (default: 10000).
- Import `FILE`: Path to a JSON file. Files are validated against system directory traversal. The file must be under 10 MiB.

### `llmem metrics`

```bash
llmem metrics
```

Report the count of embeddings stored in the database. In the Go CLI, this command outputs the number of embedded memories. For full embedding quality metrics (anisotropy, similarity range, discrimination gap), use the Python CLI's `llmem embed` command.

### `llmem dream`

```bash
llmem dream [--apply] [--phase light|deep|rem] [--report PATH]
```

Run the dream consolidation cycle, which performs automated memory maintenance in three phases:

- **Light phase:** Sort and deduplicate near-duplicate memories (cosine similarity ≥ `dream.similarity_threshold`).
- **Deep phase:** Score, promote, decay, and merge memories. Also promotes inbox items to long-term memory (items with attention_score ≥ `dream.min_score` become permanent; lower-scored items are evicted). Decays confidence on idle memories. Boosts frequently accessed memories. Performs LLM-assisted merging of similar pairs. Auto-links memories with high cosine similarity (≥ `dream.auto_link_threshold`, default 0.85).
- **REM phase:** Extract themes from memory clusters and write a dream diary (read-only reflection). Produces type counts, word clusters, and total/active memory counts.

Without `--apply`, the dream cycle runs as a **dry run** — output is prefixed with `[DRY RUN]` and no changes are written to the database.

Flags:

- `--apply`: Apply changes. Without this flag, the dream cycle runs as a dry run (no modifications to the database).
- `--phase`: Run a specific dream phase only. Choices: `light`, `deep`, `rem`. Default: all phases.
- `--report PATH`: Write an HTML dream report to the given path. The path is validated — it must not target a protected system directory (e.g. `/etc`, `/var`), contain `..` traversal, or be a symlink. Paths outside the llmem home directory are allowed (e.g. custom report output locations). On validation failure, prints an error to stderr and exits with code 1.

All dream configuration (thresholds, model, schedule, etc.) is read from the `dream:` section of `config.yaml` (see [Configuration](../docs/CONFIGURATION.md)).

Output is printed to stdout. On `--report` path validation errors, the error message is printed to stderr.

> **Python-only commands:** The Python CLI includes additional commands not yet in the Go CLI: `register-type`, `types`, `note`, `inbox`, `consolidate`, `embed` (full embedding quality metrics), `learn` (codebase indexing), and `suggest-categories`.

