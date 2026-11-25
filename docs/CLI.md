# LLMem CLI Reference

Complete reference for all `llmem` CLI commands and options. [Back to README](../README.md)

## CLI Reference

```
llmem [OPTIONS] COMMAND

Options:
  --db PATH    Path to memory database (default: ~/.config/llmem/memory.db)
  --json       Output results as JSON

Commands:
  add                 Add a new memory
  amend               Session-end cycle: update volatile state, manage pins, prune expired, regenerate briefing
  backfill-embeddings Generate embeddings for memories that lack them
  brief               Generate and display the session briefing
  curate              Nightly maintenance: prune expired, dedup near-duplicate pins, flag conflicts, emit promotion candidates
  delete              Delete a memory
  export              Export memories as JSON
  get                 Get a memory by ID
  import              Import memories from JSON file
  init                Initialize LLMem configuration and database
  list                List memories
  metrics             Report embedding quality metrics
  migrate-types       One-time migration from 7 legacy types to 3 new tier types
  pin                 Pin a stable memory (exempt from TTL pruning)
  search              Search memories
  unpin               Unpin a stable memory (re-enables TTL pruning)
  update              Update a memory

 ```

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

### `llmem brief`

Generates and displays the session briefing: pinned memories first, then fresh volatile state. Injected automatically by the platform plugins at session start.

```bash
llmem brief [--max-lines N]
```

- `--max-lines`: Maximum lines in the briefing (default: 25).

### `llmem amend`

Session-end cycle. Updates the volatile session-state memory, manages pins, prunes expired memories, and regenerates the briefing.

```bash
llmem amend [--volatile TEXT] [--pin ID] [--unpin ID] [--prune-only]
```

- `--volatile`: Update the volatile session-state memory with this content.
- `--pin` / `--unpin`: Pin or unpin stable memories by ID (repeatable).
- `--prune-only`: Only run TTL pruning, skip briefing output.

### `llmem update`

```bash
llmem update ID [--content TEXT] [--summary TEXT] [--confidence FLOAT] \
  [--valid-until TIMESTAMP] [--metadata JSON]
```

Only fields with `--` flags are updated; omitted fields remain unchanged.

Memories expire automatically via TTL (the `--valid-until` timestamp); there is no separate invalidate command. For memory lifecycle:

### `llmem pin` / `llmem unpin`

```bash
llmem pin ID
llmem unpin ID
```

Pinned stable memories are exempt from TTL pruning, so they stay visible in briefings long-term. Unpinning re-enables TTL pruning for that memory.

Use `llmem amend --pin ID` / `--unpin ID` to manage pins as part of the session-end cycle.

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

Report the count of embeddings stored in the database. For full embedding quality metrics (anisotropy, similarity range, discrimination gap), embed representative memories and analyze them programmatically via the `internal/embed/metrics` package.

### `llmem curate`

Nightly maintenance: prunes expired memories, deduplicates near-duplicate pins, flags conflicts, and emits pin promotion candidates.

```bash
llmem curate [--prune] [--auto-pin-rounds N]
```

- `--prune`: Prune expired memories as part of curation (default: true).
- `--auto-pin-rounds`: Number of curate runs a pin must survive before becoming a promotion candidate (default: 3).

