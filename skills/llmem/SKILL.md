---
name: llmem
description: >
  Manage LLMem — structured memory system with SQLite-backed storage,
  semantic search, TTL-based retention, and session briefing generation.
  Use when the user wants to: (1) add, search, update, or delete memories,
  (2) generate a session briefing, (3) pin/unpin stable memories, (4) run
  session-end amend cycle. Triggers on: "memory", "remember", "recall",
  "llmem", "memories", "forget", "briefing", "amend".
license: MIT
---

# LLMem

LLMem is a structured memory system (Go binary). It stores memories in SQLite with optional embedding-based semantic search via Ollama (nomic-embed-text). Semantic search uses a sqlite-vec HNSW ANN index for sub-linear retrieval, with automatic fallback to brute-force cosine similarity if the sqlite-vec extension is not available.

## Installation

- **CLI:** `~/.local/bin/llmem`
- **Config:** `~/.config/llmem/config.yaml`
- **DB:** `~/.config/llmem/memory.db`
- **Ollama:** `http://localhost:11434` (local, for embeddings)

## Session Adapter Configuration

LLMem uses a session adapter to read conversation transcripts for memory extraction. Set `session.adapter` in config.yaml:

- `opencode` (default) — reads from `~/.local/share/opencode/opencode.db`. Auto-selected if the database exists.
- `copilot` — reads from `~/.copilot/session-state/`. Auto-selected if only Copilot session data exists. Full transcripts require `--share`.
- `none` — no adapter. Context injection still works, but transcript extraction returns `no_transcript`.

```yaml
# For Copilot CLI:
session:
  adapter: copilot
copilot:
  state_dir: ~/.copilot/session-state
  share_dir: .
```

`llmem init` auto-detects the adapter type based on which session state directory exists.

## Memory Types

Three tier types with different TTL and briefing behavior:

| Type | TTL | Briefing | Use for |
|------|-----|-----------|---------|
| stable | 90 days (reset on retrieval) | Yes (pinnable) | Procedures, decisions, preferences, non-obvious facts |
| volatile | 14 days (reset on retrieval) | Yes (recent) | Project state, active work, blockers |
| log | 90 days (hard, no reset) | No (searchable only) | Audit records, application logs, dedup history |

**Write discipline:** Only write memories for non-obvious, reusable knowledge. If the filesystem has the answer, do not write a memory.

## Session Briefing

The briefing is a compact (~25 line) context block auto-injected at session start by the opencode plugin. It includes:

1. **Pinned stable memories** — explicitly pinned, always present
2. **Recent volatile memories** — non-expired, current work state
3. **Top-5 recent stable memories** — most recently updated, not already pinned
4. **Store health** — count by tier, pinned count, expired count

Capped at ~25 lines. Over cap: recent stable dropped first, then volatile beyond most recent 3.

## Key Commands

```bash
# Add a memory (default type: stable)
llmem add --content "content" --type stable
llmem add --content "current project state" --type volatile
llmem add --content "Applied to Acme Corp" --type log

# Generate session briefing
llmem brief
llmem brief --max-lines 15

# Session-end cycle: update volatile, manage pins, prune, regenerate briefing
llmem amend --volatile "current state text"
llmem amend --pin <id> --unpin <id>
llmem amend --prune-only  # just prune expired, skip briefing

# Pin/unpin stable memories (TTL exempt)
llmem pin <id>
llmem unpin <id>

# Search memories (for exploratory queries only — briefing covers routine context)
llmem search "query"
llmem search "query" --type stable --limit 5
llmem search "query" --fts-only        # FTS5 keyword search only
llmem search "query" --semantic-only   # Semantic search only (requires embedder)

# List all
llmem list
llmem list --type stable --limit 20
llmem list --all                       # Include expired memories

# Get specific memory
llmem get <id>

# Update a memory
llmem update <id> --content "new content"
llmem update <id> --confidence 0.95

# Delete permanently
llmem delete <id>

# One-time migration from legacy 7 types to 3 new tiers
llmem migrate-types --dry-run    # Preview changes
llmem migrate-types               # Execute migration

# Export/import
llmem export --output memories.json
llmem import memories.json

# Initialize config and database
llmem init
llmem init --ollama-url http://localhost:11434
```

## Retention Model

TTL + pinning is the entire retention mechanism. No manual invalidation, no dreaming.

- **stable**: 90-day TTL. Reset on retrieval. Pinned memories exempt (valid_until = NULL while pinned).
- **volatile**: 14-day TTL. Reset on retrieval. Auto-pruned when inactive.
- **log**: 90-day hard retention. No reset on retrieval.

`llmem amend` at session end triggers TTL pruning — deletes all non-pinned memories where valid_until is in the past.

## Relations

Memories can be linked by typed relations: `supersedes`, `related_to`, `derived_from`.

Relations are managed internally by the extraction pipeline. The Go backend supports relation traversal in search (exposed via the `Retriever` API) but the CLI does not yet expose `--traverse-relations` as a flag.

### Multi-signal Reranking

After RRF fusion, search results are automatically reranked using a blend of the RRF score and four weighted signals:

```
final_score = rrf_score * (1 - blend) + weighted_signal * blend
```

**Default blend factor: 0.3** (70% RRF, 30% signals).

**Signals and weights:**

| Signal | Weight | Formula |
|--------|--------|---------|
| Confidence | 0.4 | Direct use of `confidence` field (0.0–1.0, default 0.0) |
| Recency | 0.3 | `exp(-0.01 * days_since_access)` (0.0 if never accessed) |
| Access frequency | 0.2 | `log(1 + access_count / max(age_days, 1))` (0.0 if never accessed) |
| Type priority | 0.1 | Lookup in `TYPE_PRIORITY` dict (default 1.0 for unknown types) |

**Type priority weights:**

| Type | Priority |
|------|----------|
| volatile | 1.2 |
| stable | 1.1 |
| log | 0.7 |

## Important Notes

- **Go binary** — `llmem` is a compiled Go binary at `~/.local/bin/llmem`, symlinked from `/usr/local/bin/llmem`. No Python virtualenv needed.
- **TTL, not invalidation** — memories auto-expire based on their tier TTL. Pin stable memories to preserve them. Use `llmem amend` at session end to prune expired.
- **Embeddings** require Ollama running with `nomic-embed-text` pulled. If Ollama is down, semantic search falls back to FTS5-only.
- **ANN vector index** — semantic search uses sqlite-vec (`vec0` virtual table) for fast ANN retrieval, with automatic fallback to brute-force cosine similarity if sqlite-vec is not available.
- **Confidence** is 0.0-1.0. Higher = more certain. Facts from the user directly should be 0.9+, auto-extracted should be 0.7.
- **Briefing auto-injected** — the opencode plugin runs `llmem brief` at session start. No manual command needed for routine context.
- **Access tracking** — `llmem get` is read-only and does not update `access_count` or `accessed_at`. Search operations automatically track access and reset TTL for stable/volatile.
