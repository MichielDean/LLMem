# LLMem API Reference

Python library interface and Go package reference for LLMem, including extension points, database schema, and module reference. [Back to README](../README.md)

## Python API

```python
from llmem import (
    MemoryStore,
    register_memory_type,
    get_config_path,
    get_db_path,
    get_llmem_home,
    migrate_from_lobsterdog,
    load_config,
    validate_session_id,
    SessionAdapter,
    OpenCodeAdapter,
    register_session_adapter,
    register_session_hook,
    get_registered_session_hooks,
    register_dream_hook,
    register_cli_plugin,
)
from llmem.retrieve import Retriever, _rrf_score, DEFAULT_ALPHA, DEFAULT_RRF_K
from llmem.metrics import (
    compute_metrics,
    anisotropy,
    similarity_range,
    discrimination_gap,
    cosine_similarity,
    bytes_to_vec,
    EmbeddingMetrics,
    ANISOTROPY_WARNING_THRESHOLD,
    SIMILARITY_RANGE_WARNING_THRESHOLD,
    METRICS_MAX_EMBEDDINGS,
)
from llmem.config import write_config_yaml
from llmem.ollama import ProviderDetector, is_ollama_running

# Open a store
store = MemoryStore()  # uses default path ~/.config/llmem/memory.db

# Add a memory
mid = store.add(type="fact", content="Project uses SQLite with WAL mode")

# FTS5 search (classic)
results = store.search("SQLite", limit=10)

# Hybrid search (FTS5 + semantic, RRF fusion)
from llmem.retrieve import Retriever
from llmem.embed import EmbeddingEngine

embedder = EmbeddingEngine()
retriever = Retriever(store=store, embedder=embedder)

# Or use any EmbedProvider (e.g. from resolve_provider):
from memory.providers import resolve_provider
embed_provider, _ = resolve_provider({"provider": {"default": "local"}})
retriever = Retriever(store=store, embedder=embed_provider)

# Default: hybrid mode (alpha=0.7, favors semantic), reranking blend=0.3
results = retriever.hybrid_search("Python async patterns", limit=10)

# FTS5-only (no embedder needed)
results = retriever.hybrid_search("Python async patterns", search_mode="fts")

# Semantic-only (requires embedder)
results = retriever.hybrid_search("Python async patterns", search_mode="semantic")

# Control semantic vs. keyword weight (0.0 = pure FTS, 1.0 = pure semantic)
results = retriever.hybrid_search("query", alpha=0.5)

# Control reranking blend (0.0 = pure RRF, 1.0 = pure signal-based)
# blend=0.3 default: 70% RRF score + 30% weighted signals (confidence, recency, access, type)
retriever = Retriever(store=store, embedder=embedder, blend=0.5)

# Restrict code ref resolution to specific directories (default: [Path.cwd()])
retriever = Retriever(store=store, embedder=embedder, allowed_paths=[Path("./project")])

# Skip access tracking (don't increment access_count for this query)
results = retriever.search("analytics query", limit=10, track_access=False)
results = retriever.hybrid_search("analytics query", limit=10, track_access=False)

# Follow code reference edges from search results
# When traverse_refs=True, memories with 'references' relations to code chunks
# will have the referenced file content resolved and appended to results.
results = retriever.search("auth logic", limit=10, traverse_refs=True)

# Control ref expansion depth (1-5, default 3)
results = retriever.search("auth logic", limit=10, traverse_refs=True, max_ref_depth=2)

# Each result dict includes "_rrf_score" (RRF fusion score) and "_rerank_score" (blended final score)

# Get by ID
mem = store.get(mid)

# Update
store.update(mid, content="Updated content")

# Invalidate (soft delete)
store.invalidate(mid, reason="No longer relevant")

# --- Working Memory Inbox ---
# The inbox is a capacity-limited staging area for ephemeral information.
# Items enter via add_to_inbox() and are promoted to long-term memory via
# consolidate() or the dream deep phase.

# Add a note to the inbox (default attention_score=0.5, source=note)
inbox_id = store.add_to_inbox(content="Important observation", attention_score=0.8)

# Add with explicit source and metadata
inbox_id = store.add_to_inbox(
    content="Learned something",
    source="learn",  # note | learn | extract | consolidation
    attention_score=0.7,
    metadata={"context": "session-abc"},
)

# Retrieve an inbox item
item = store.get_from_inbox(inbox_id)
# item = {"id": ..., "content": ..., "source": ..., "attention_score": ..., ...}

# List inbox items (ordered by attention_score DESC, created_at ASC)
items = store.list_inbox(limit=20)

# Get inbox count
count = store.inbox_count()

# Update attention score
store.update_inbox_attention_score(inbox_id, 0.9)

# Remove an inbox item
store.remove_from_inbox(inbox_id)

# Consolidate inbox → long-term memory
# Items with attention_score >= min_score become memories (source=consolidation,
# confidence=attention_score). Items below are evicted. Inbox is empty after.
result = store.consolidate(min_score=0.5)
# result = {"promoted": [...], "evicted": [...]}

# Dry run (shows what would happen without changes)
result = store.consolidate(min_score=0.5, dry_run=True)

# Batch access tracking (efficient single UPDATE for multiple IDs)
# Increments access_count and updates accessed_at for each listed memory.
# Returns the number of rows actually updated (non-existent IDs are silently ignored).
affected = store.touch_batch([id1, id2, id3])

# List with filters
memories = store.list_all(type="fact", valid_only=True, limit=50)

# Relations (memory-to-memory and memory-to-code)
store.add_relation(mem_id_a, mem_id_b, "supersedes")
store.add_relation(mem_id_a, "src/lib.rs:42:58", "references", target_type="code")
relations = store.get_relations(mem_id_a)
related = store.traverse_relations(mem_id_a, relation_type="supersedes", max_depth=3)
code_refs = store.traverse_relations(mem_id_a, max_depth=2, target_type="code")

# Export / Import
data = store.export_all()          # default limit: 10,000 memories
data = store.export_all(limit=None)  # export all memories without limit
count = store.import_memories(data)

# Type registry
register_memory_type("custom_type")
types = get_registered_types()

# Close (or use as context manager)
store.close()

with MemoryStore() as store:
    store.add(type="fact", content="Context-managed store")

# Migration from lobsterdog
migrated = migrate_from_lobsterdog()  # Returns True if anything was copied

# Config
config = load_config()
home = get_llmem_home()
db_path = get_db_path()
config_path = get_config_path()

# Programmatically write config.yaml
written = write_config_yaml(
    config_path,
    {"memory": {"ollama_url": "http://localhost:11434", "embed_model": "nomic-embed-text"}},
    force=False,  # Set True to overwrite existing
)

# Detect available LLM providers
detector = ProviderDetector()
result = detector.detect(ollama_url="http://localhost:11434")
# result["provider"] → "ollama" | "openai" | "anthropic" | "none"
# result["ollama_url"]

# Check if Ollama is running
if is_ollama_running("http://localhost:11434"):
    print("Ollama is reachable")
```

### Embedding Quality Metrics

The `llmem.metrics` module provides functions to detect poor-quality embeddings:

```python
from llmem.metrics import (
    compute_metrics,
    anisotropy,
    similarity_range,
    discrimination_gap,
    cosine_similarity,
    bytes_to_vec,
    EmbeddingMetrics,
    ANISOTROPY_WARNING_THRESHOLD,
    SIMILARITY_RANGE_WARNING_THRESHOLD,
    METRICS_MAX_EMBEDDINGS,
)

# Compute all metrics at once (convenience wrapper)
m = compute_metrics(embeddings, labels=labels)
# m.anisotropy        → float in [0.0, 1.0]; lower is better
# m.similarity_range  → float; higher is better
# m.discrimination_gap → float | None; higher is better (None if no labels)

# Individual metric functions
aniso = anisotropy(embeddings)             # Average pairwise cosine similarity, clamped [0, 1]
sim_range = similarity_range(embeddings)   # Max - min pairwise cosine similarity
disc_gap = discrimination_gap(embeddings, labels)  # Inter-class vs intra-class separation

# Utility functions
sim = cosine_similarity(vec_a, vec_b)  # Cosine similarity, 0.0 for zero vectors
vec = bytes_to_vec(emb_bytes)          # Decode packed float32 bytes to list[float]

# Fetch embeddings from store (for metrics computation)
rows = store.get_embeddings_with_types(limit=10000)  # (embedding_bytes, type) tuples
count = store.count_embeddings()  # Count of valid embedded memories
```

**Warning thresholds:** `ANISOTROPY_WARNING_THRESHOLD = 0.5` (anisotropy above this may indicate poor embeddings), `SIMILARITY_RANGE_WARNING_THRESHOLD = 0.1` (similarity range below this may indicate poor embeddings).

**Performance safeguard:** `METRICS_MAX_EMBEDDINGS = 10000` — metrics computations are O(n²) pairwise, so `compute_metrics()` and `get_embeddings_with_types()` cap the number of vectors to prevent CPU hangs and OOM on large stores.

### `safe_urlopen`

The `safe_urlopen` function is the safe replacement for `urllib.request.urlopen()`. It validates URLs against SSRF, blocks redirects, and re-resolves hostnames before opening:

```python
from llmem.url_validate import safe_urlopen

# Default: allow_remote is inferred from the URL
response = safe_urlopen("http://localhost:11434/api/generate")

# Explicit allow_remote for remote endpoints
response = safe_urlopen("https://api.openai.com/v1/models", allow_remote=True)
```

The `allow_remote` parameter controls whether non-loopback URLs are permitted. If `None` (default), it's inferred from the URL — loopback URLs default to `False`, all others default to `False` as well (fail-closed). Pass `allow_remote=True` explicitly for known-remote endpoints.

### `get_server_auth_token`

```python
from llmem.config import get_server_auth_token

token = get_server_auth_token()
# Returns None if no token configured
# Raises ValueError if token is set but < 16 characters (too weak)
```

### Session Adapters

`SessionAdapter` is an abstract base class for reading session transcripts. Two built-in implementations are available:

**OpenCodeAdapter** reads from the OpenCode SQLite database:

```python
from llmem.adapters import OpenCodeAdapter

adapter = OpenCodeAdapter(db_path=Path("~/.local/share/opencode/opencode.db"))
sessions = adapter.list_sessions(limit=10)
transcript = adapter.get_session_transcript(session_id)
chunks = adapter.get_session_chunks(session_id)
exists = adapter.session_exists(session_id)
adapter.close()
```

`OpenCodeAdapter.__init__` validates `db_path` for security: it rejects paths containing `..` traversal, paths targeting system directories (`/etc`, `/var`, etc.), and symlink paths. Paths that cannot be accessed (e.g. permission denied) also raise `ValueError`.

**CopilotAdapter** reads from the Copilot CLI session state directory:

```python
from llmem.adapters import CopilotAdapter

adapter = CopilotAdapter(
    state_dir="~/.copilot/session-state",  # session metadata
    share_dir=".",                          # --share markdown files
)
sessions = adapter.list_sessions(limit=10)
transcript = adapter.get_session_transcript(session_id)  # None if no --share file
adapter.close()
```

Copilot CLI does not persist conversation transcripts to a database. The adapter reads session metadata from `workspace.yaml` files and full transcripts from `--share` markdown files. Without `--share`, `get_session_transcript()` returns `None` and `on_idle` returns `no_transcript` gracefully.

**No adapter** — The `SessionHookCoordinator` accepts `adapter=None`. In this mode, `on_idle` and `on_ending` return `no_transcript`, while `on_created` and `on_compacting` still work (they query MemoryStore, not the session DB).

```python
from llmem.session_hooks import create_session_hook_coordinator

# Auto-detect: uses OpenCodeAdapter if opencode.db exists,
# CopilotAdapter if only copilot session-state exists, else None
coordinator = create_session_hook_coordinator()

# Explicit adapter choice via config
config = {"session": {"adapter": "copilot"}, "copilot": {"state_dir": "..."}}
coordinator = create_session_hook_coordinator(config=config)
```

### Session Extraction Pipeline

The `session_hooks` module provides `process_opencode_sessions()` — a complete pipeline that discovers OpenCode sessions from the SQLite database, chunks them, and feeds each chunk through the extraction engine:

```python
from llmem.session_hooks import process_opencode_sessions, OPENCODE_RESULT_SUCCESS
from llmem.store import MemoryStore
from llmem.extract import ExtractionEngine
from llmem.embed import EmbeddingEngine

store = MemoryStore()
extractor = ExtractionEngine()
results = process_opencode_sessions(
    store=store,
    extractor=extractor,
    embedder=EmbeddingEngine(),
    force=False,       # skip already-processed sessions
    limit=50,          # max sessions to process
)
# results = {"opencode_success": 3, "opencode_already_processed": 2, ...}
```

Result constants: `OPENCODE_RESULT_SUCCESS`, `OPENCODE_RESULT_DB_NOT_FOUND`, `OPENCODE_RESULT_ALREADY_PROCESSED`, `OPENCODE_RESULT_NO_MEMORIES`, `OPENCODE_RESULT_EMPTY_TRANSCRIPT`, `OPENCODE_RESULT_ADAPTER_ERROR`, `OPENCODE_RESULT_EXTRACTION_FAILED`.

The `process_all_session_sources()` function in `llmem/hooks` orchestrates all session sources, currently delegating to `process_opencode_sessions`:

```python
from llmem.hooks import process_all_session_sources
from llmem.store import MemoryStore

store = MemoryStore()
results = process_all_session_sources(store=store, force=False)
# Returns aggregated result counts from all session sources
```

To implement a custom adapter, subclass `SessionAdapter`:

```python
from llmem.adapters.base import SessionAdapter

class MyAdapter(SessionAdapter):
    def list_sessions(self, limit=50):
        ...

    def get_session_transcript(self, session_id):
        ...

    def get_session_chunks(self, session_id):
        ...

    def session_exists(self, session_id):
        ...

    def close(self):
        ...
```

### Session Hooks

Session hooks inject relevant memories when an OpenCode session lifecycle event occurs, and extract memories when a session goes idle. Three events are supported:

| Event | Hook | Behavior |
|-------|------|----------|
| `session.created` | `on_created(session_id)` | Queries the memory store for relevant memories and writes a context file (`{session_id}.md`). Returns `("success", file_path)`, `("already_processed", None)`, or `("error", None)`. |
| `session.idle` | `on_idle(session_id)` | Extracts memories from the session transcript with 30-second debounce. Returns `("success", count)`, `("debounced", 0)`, or `("no_transcript", 0)`. |
| `session.compacting` | `on_compacting(session_id)` | Injects high-confidence key memories (`decision`, `preference`, `procedure`, `project_state` with confidence ≥ 0.7) to preserve context during compaction. Returns `("success", file_path)` or `("no_memories", None)`. |

`SessionHookCoordinator` orchestrates the three hooks:

```python
from llmem.session_hooks import create_session_hook_coordinator

coordinator = create_session_hook_coordinator()  # uses default config
# or with custom config:
coordinator = create_session_hook_coordinator(config=my_config)

result_type, path = coordinator.on_created("session-abc123")
result_type, count = coordinator.on_idle("session-abc123")
result_type, path = coordinator.on_compacting("session-abc123")
```

`SessionEventManager` dispatches events to registered hooks:

```python
from llmem.session_hooks import SessionEventManager

manager = SessionEventManager()
manager.emit("created", "session-abc123")  # calls registered "created" hook
manager.emit("idle", "session-abc123")     # calls registered "idle" hook
manager.emit("compacting", "session-abc123")  # calls registered "compacting" hook
```

`validate_session_id()` rejects session IDs containing `/`, `\`, or `..` to prevent path traversal attacks on context file paths:

```python
from llmem import validate_session_id

validate_session_id("abc123")    # returns "abc123"
validate_session_id("../etc/passwd")  # raises ValueError
validate_session_id("foo/bar")   # raises ValueError
```

### Code Indexing

The `CodeIndex` class manages the `code_chunks` table for semantic and full-text search over indexed code. It shares the same SQLite database as `MemoryStore` for cross-retrieval.

```python
from llmem.code_index import CodeIndex
from llmem.chunking import ParagraphChunking, FixedLineChunking, detect_language, walk_code_files

# Open the code index (uses the same database as MemoryStore)
code_index = CodeIndex()  # defaults to ~/.config/llmem/memory.db

# Add a single chunk
chunk_id = code_index.add_chunk(
    file_path="src/main.py",
    start_line=1,
    end_line=42,
    content="def main():\n    ...",
    language="python",
    chunk_type="paragraph",
)

# Batch add chunks from CodeChunk named tuples
chunks = chunker.chunk("src/main.py", content, language="python")
chunk_ids = code_index.add_chunks(chunks)

# Remove all chunks for a file (useful before re-indexing)
removed = code_index.remove_by_path("src/main.py")

# Full-text search
results = code_index.search_content("async def", limit=10)

# Semantic search (requires sqlite-vec and embeddings)
results = code_index.search_by_embedding(query_vec, limit=10, threshold=0.5)

code_index.close()
```

**Chunking strategies:**

```python
from llmem.chunking import ParagraphChunking, FixedLineChunking

# Paragraph chunking: splits at blank-line boundaries (default)
chunker = ParagraphChunking(min_lines=1, max_lines=200)
chunks = chunker.chunk("src/app.py", content, language="python")

# Fixed-line chunking: sliding window with overlap
chunker = FixedLineChunking(window_size=50, overlap=10)
chunks = chunker.chunk("src/app.py", content, language="python")
```

**Directory walking:**

```python
from llmem.chunking import walk_code_files, parse_gitignore

# Walk a directory respecting .gitignore
code_files = walk_code_files(Path("./my-project"))

# With custom size/depth limits
code_files = walk_code_files(
    Path("./my-project"),
    max_file_size=2 * 1024 * 1024,  # 2 MiB
    max_depth=30,
)
```

`walk_code_files` skips symlinks, binary files, credential files (`.env`, `.pem`, `.key`, SSH keys), and common non-code directories. `detect_language(file_path)` returns a language string from the file extension, or `None` for unknown extensions.

## Extension Points

LLMem provides a registry system that allows harnesses and external tools to plug in domain-specific behavior without modifying core code. All registry functions validate their inputs and raise `ValueError` or `TypeError` on invalid arguments.

### Session Adapter Registry

Register a custom session adapter so that other parts of the system can discover it by name:

```python
from llmem import register_session_adapter
from llmem.adapters.base import SessionAdapter

class MyAdapter(SessionAdapter):
    # ... implement abstract methods ...
    pass

register_session_adapter("my_adapter", MyAdapter)
```

List or look up registered adapters:

```python
from llmem.registry import get_registered_adapters, get_adapter_class

names = get_registered_adapters()        # frozenset of adapter names
cls = get_adapter_class("my_adapter")     # the adapter class, or None
```

### Dream Hook Registry

Register a function to run after a dream phase completes. Hooks are called with `(Dreamer instance, DreamResult, apply: bool)` and errors are logged without crashing the dream cycle.

```python
from llmem import register_dream_hook

def my_light_hook(dreamer, result, apply):
    # Post-light-phase logic here
    pass

register_dream_hook("light", my_light_hook)
```

Valid phases: `"light"`, `"deep"`, `"rem"`. Only one hook per phase is allowed; registering a duplicate raises `ValueError`.

### Session Hook Registry

Register a callback function for session lifecycle events. When `SessionEventManager.emit()` is called, the corresponding hook is invoked with the session ID.

```python
from llmem import register_session_hook, get_registered_session_hooks

def on_session_created(session_id):
    print(f"Session {session_id} was created")

register_session_hook("created", on_session_created)
```

Valid event types: `"created"`, `"idle"`, `"compacting"`. Only one hook per event type is allowed; registering a duplicate raises `ValueError`. The hook function must be callable; otherwise `TypeError` is raised.

List registered hooks:

```python
hooks = get_registered_session_hooks()  # dict mapping event type to hook function
```

### CLI Plugin Registry

Register a setup function that adds subcommands to the `llmem` CLI. The setup function receives an `argparse._SubParserGroup` and can add its own subparsers. Errors in plugin setup are logged but do not crash the CLI.

```python
from llmem import register_cli_plugin

def my_plugin_setup(subparsers):
    p = subparsers.add_parser("my-cmd", help="My custom command")
    p.add_argument("--flag", help="A flag")
    p.set_defaults(func=my_cmd_handler)

register_cli_plugin("my_plugin", my_plugin_setup)
```

After registration, `llmem my-cmd --flag value` becomes available. List registered plugins:

```python
from llmem.registry import get_registered_cli_plugins

names = get_registered_cli_plugins()  # frozenset of plugin names
```

## Database

LLMem uses SQLite with WAL mode and numbered SQL migrations (stored in the `llmem_migrations` package). Migrations are tracked in a `_schema_migrations` table and run automatically when the database is opened.

### Vector Search

When `sqlite-vec` is available, LLMem creates a `memories_vec` virtual table for cosine similarity search. If the extension isn't installed, vector search is gracefully disabled and the store falls back to FTS-only search.

The embedding dimension defaults to 768 (matching `nomic-embed-text`), configurable via `vec_dimensions`.

### Code Index

LLMem also provides a code indexing system via the `code_chunks` table, created by migration 004. This table stores chunked source code with embeddings for cross-retrieval alongside memories.

The `code_chunks` table schema:

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT PRIMARY KEY | Format: `<file_path>:<start_line>:<end_line>` |
| `file_path` | TEXT NOT NULL | Relative path of the source file |
| `start_line` | INTEGER NOT NULL | Starting line (1-based) |
| `end_line` | INTEGER NOT NULL | Ending line (1-based, inclusive) |
| `content` | TEXT NOT NULL | Chunk text content |
| `embedding` | BLOB | Embedding vector bytes (nullable) |
| `language` | TEXT | Detected programming language |
| `chunk_type` | TEXT NOT NULL | Chunking strategy (`paragraph` or `fixed_line`) |
| `created_at` | TEXT NOT NULL | ISO timestamp |

When `sqlite-vec` is available, a `code_chunks_vec` virtual table enables semantic similarity search over code chunk embeddings, with INSERT/UPDATE/DELETE triggers for automatic synchronization. An FTS5 `code_chunks_fts` virtual table provides full-text search over chunk content, file paths, and language names.

The `--include-code` flag on `llmem search` interleaves code chunk results with memory results using the same RRF scoring formula, enabling unified search across both knowledge stores.

### Code Reference Edges

The `relations` table supports two `target_type` values: `'memory'` (the default, linking two memories) and `'code'` (linking a memory to a code chunk). When `target_type='code'`, the `target_id` uses the format `path:start_line:end_line` (e.g., `src/lib.rs:42:58`) referencing a file location rather than a memory UUID.

The `references` relation type (added to the `relation_type` CHECK constraint) creates edges from memories to code chunks. This enables `--traverse-refs` in search, which follows reference edges from result memories and resolves the referenced file content at query time.

Code ref paths must be relative (no leading `/`) and must not contain `..` traversal. Refs are resolved against an `allowed_paths` allowlist that defaults to `[Path.cwd()]`, preventing arbitrary file reads.

## Module Reference

| Module | Description |
|--------|-------------|
| `memory.providers` | Abstract base classes, concrete providers (`OllamaProvider`, `OpenAIProvider`, `AnthropicProvider`, `SentenceTransformersProvider`, `NoneProvider`), `resolve_provider()`, `dimension()`, `_is_loopback_hostname()`, `_validate_embed_inputs()`, `_strip_credentials()` |
| `memory.ollama` | `check_ollama_model()`, `_call_ollama_generate()` |
| `memory.url_validate` | `is_safe_url()`, `safe_urlopen()`, `_strip_credentials()`, `_NoRedirectHandler()`, `validate_base_url()`, `SafeRedirectHandler` |
| `memory.config` | Configuration loading, defaults, typed accessors (e.g. `get_provider_config()`, `get_ollama_url()` with SSRF validation) |
| `llmem.session_hooks` | `SessionHookCoordinator`, `SessionEventManager`, `create_session_hook_coordinator()`, result constants |
| `llmem.url_validate` | `is_safe_url()`, `safe_urlopen()`, `_strip_credentials()`, `validate_base_url()`, `_NoRedirectHandler`, `_extract_url_string()` (mirrors `memory.url_validate`), DNS rebinding protection |
| `llmem.paths` | `validate_session_id()`, `get_context_dir()`, `_validate_write_path()`, `BLOCKED_SYSTEM_PREFIXES`, home/write path checks |
| `llmem.registry` | `register_session_hook()`, `get_registered_session_hooks()`, `VALID_SESSION_EVENT_TYPES` |
| `llmem.taxonomy` | `ERROR_TAXONOMY`, `REVIEW_SEVERITY_TAXONOMY`, `ERROR_TAXONOMY_KEYS` |
| `llmem.metrics` | `compute_metrics()`, `anisotropy()`, `similarity_range()`, `discrimination_gap()`, `cosine_similarity()`, `bytes_to_vec()`, `EmbeddingMetrics` dataclass, warning thresholds, `METRICS_MAX_EMBEDDINGS` |
| `llmem.store` | `MemoryStore` with `export_all(limit=)`, `import_memories()` validation, brute-force/embedding caps, dimension validation, inbox methods (`add_to_inbox`, `get_from_inbox`, `list_inbox`, `remove_from_inbox`, `update_inbox_attention_score`, `consolidate`), capacity eviction, `get_embeddings_with_types(limit=)`, `count_embeddings()` |
| `llmem.code_index` | `CodeIndex` — manages `code_chunks` table, FTS5/vec virtual tables, add/search/remove operations |
| `llmem.refs` | `resolve_code_ref()`, `validate_code_ref_path()` — code reference resolution for memory-to-code-chunk edges |
| `llmem.chunking` | `ParagraphChunking`, `FixedLineChunking`, `detect_language()`, `walk_code_files()`, `parse_gitignore()`, `is_ignored()` |

---

## Go API

The Go implementation provides the core `MemoryStore` as a library in `github.com/MichielDean/LLMem/internal/store`. It shares the same database schema as the Python implementation, making databases interchangeable between the two.

### Installation

```go
import "github.com/MichielDean/LLMem/internal/store"
```

### Creating a Store

```go
ms, err := store.NewMemoryStore(store.StoreConfig{
    DBPath:         "",               // empty → ~/.config/llmem/memory.db
    VecDimensions:  0,               // 0 → defaults to 768
    DisableVec:     false,           // false → attempt vec0 virtual table
    RegisteredTypes: nil,             // nil → 7 standard types
})
if err != nil {
    log.Fatal(err)
}
defer ms.Close()

// Or with custom types:
ms, err := store.NewMemoryStore(store.StoreConfig{
    DBPath:          "/path/to/custom.db",
    VecDimensions:   1024,
    RegisteredTypes:  []string{"fact", "decision", "custom_type"},
})
```

If `DisableVec` is `true`, the `memories_vec` virtual table is not created and all vector operations fall back to brute-force similarity search. If `VecDimensions` is negative, `NewMemoryStore` returns an error.

### Core Operations

```go
ctx := context.Background()

// Add a memory
id, err := ms.Add(ctx, store.AddParams{
    Type:       "fact",
    Content:    "Project uses SQLite with WAL mode",
    Confidence: 0.9,
    Source:     "manual",
    Hints:      []string{"sqlite", "wal"},
    Metadata:   map[string]any{"source_id": "session-abc"},
})

// Get a memory (returns nil, nil if not found)
mem, err := ms.Get(ctx, id, true)  // true → track access (increment access_count)

// Get multiple memories at once
batch, err := ms.GetBatch(ctx, []string{id1, id2, id3}, true)

// Update a memory
content := "Updated content"
updated, err := ms.Update(ctx, store.UpdateParams{
    ID:      id,
    Content: &content,
})

// Invalidate (soft delete — sets valid_until, clears embedding)
invalidated, err := ms.Invalidate(ctx, id, "No longer relevant")

// Delete (permanent removal — cascades target-side relations)
deleted, err := ms.Delete(ctx, id)

// Touch (increment access_count)
touched, err := ms.Touch(ctx, id)

// Batch touch
affected, err := ms.TouchBatch(ctx, []string{id1, id2, id3})
```

### Search

```go
// FTS5 full-text search (ranked by BM25, falls back to LIKE if FTS fails)
results, err := ms.Search(ctx, store.SearchParams{
    Query:     "SQLite",
    Type:      "fact",          // optional type filter
    ValidOnly: true,             // only valid (not invalidated) memories
    Limit:     20,
    Offset:    0,
})

// Search count
count, err := ms.SearchCount(ctx, store.SearchCountParams{
    Query:     "SQLite",
    Type:      "fact",
    ValidOnly: true,
})

// Vector similarity search (uses vec0 if available, brute-force otherwise)
results, err := ms.SearchByEmbedding(ctx, queryVec, true, 20, 0.5)

// List all memories
memories, err := ms.ListAll(ctx, store.ListParams{
    Type:      "fact",
    ValidOnly: true,
    Limit:     100,
})

// Count
count, err := ms.Count(ctx, true)              // valid only
byType, err := ms.CountByType(ctx, true)        // map[string]int
embCount, err := ms.CountEmbeddings(ctx)        // valid memories with embeddings
```

### Relations

```go
// Add a relation (valid types: "supersedes", "related_to", "derived_from")
relID, err := ms.AddRelation(ctx, sourceID, targetID, "supersedes")

// Get all relations for a memory
relations, err := ms.GetRelations(ctx, memID)

// Get relations for multiple memories at once
batchRels, err := ms.GetRelationsBatch(ctx, []string{id1, id2})

// Traverse relations (bidirectional, recursive CTE, max depth 5)
traversed, err := ms.TraverseRelations(ctx, []string{startID}, 3)
// Returns []*TraversedRelation with TargetID, RelationType, Distance, RelationScore
```

### Extraction Log

```go
// Log an extraction (upsert on source_type + source_id)
err := ms.LogExtraction(ctx, "session", "abc123", nil, 5)

// Check if a source has been extracted
extracted, err := ms.IsExtracted(ctx, "session", "abc123")

// Supersede memories by source metadata
n, err := ms.SupersedeBySource(ctx, "session", "abc123")

// Remove an extraction log entry
removed, err := ms.RemoveExtractionLog(ctx, "session", "abc123")
```

### Embeddings and Duplicates

```go
// Get embeddings with types (for metrics computation)
// limit < 0 → default 10000, limit == 0 → no limit, limit > 0 → applied
embs, err := ms.GetEmbeddingsWithTypes(ctx, 0)  // all embeddings

// Find similar memories by vector or text
similar, err := ms.FindSimilar(ctx, store.FindSimilarParams{
    QueryVec:  queryVec,       // if non-empty, uses vector search
    Content:   "search terms",  // fallback to FTS5 if queryVec is empty
    Threshold: 0.8,
    Limit:     10,
})

// Find duplicate pairs (by cosine similarity)
pairs, err := ms.ConsolidateDuplicates(ctx, 0.92, 500)
```

### Import/Export

```go
// Export all memories (default limit: 10000, pass 0 for no limit)
limit := 0  // no limit
memories, err := ms.ExportAll(ctx, &limit)

// Import memories (validates types, content, ID length, embedding dimensions)
imported, err := ms.ImportMemories(ctx, []store.ImportMemory{
    {
        Type:       "fact",
        Content:    "Imported memory",
        Confidence: 0.8,
    },
})
```

### Memory Types

```go
// Register a custom type (validates name pattern: ^[a-z][a-z0-9_]*$, max 64 chars)
err := ms.RegisterMemoryType("my_custom_type")

// Get the default types
types := store.DefaultRegisteredTypes()
// ["fact", "decision", "preference", "event", "project_state", "procedure", "conversation"]

// Get valid relation types
relTypes := store.ValidRelationTypes()
// ["supersedes", "related_to", "derived_from"]
```

### Configuration Types

```go
type StoreConfig struct {
    DBPath          string   // Database file path (default: ~/.config/llmem/memory.db)
    VecDimensions   int      // Embedding dimensions (default: 768, must be ≥ 0)
    DisableVec      bool     // Skip vec0 virtual table creation
    RegisteredTypes []string // Custom type list (default: 7 standard types)
}

type AddParams struct {
    ID         string
    Type       string
    Content    string
    Summary    string
    Source     string
    Confidence float64
    ValidUntil string
    Metadata   map[string]any
    Embedding  []byte          // Packed float32 little-endian (768 × 4 bytes for default dim)
    Hints      []string
}

type UpdateParams struct {
    ID             string
    Content        *string         // nil → no change
    Summary        *string
    Confidence     *float64
    ValidUntil     *string
    Metadata       map[string]any
    Embedding      []byte
    ClearEmbedding bool            // true → set embedding to NULL
    Hints          []string
}

type SearchParams struct {
    Query     string
    Type      string
    ValidOnly bool
    Limit     int             // ≤ 0 → defaults to 20
    Offset    int             // < 0 → treated as 0
}

type ListParams struct {
    Type      string
    ValidOnly bool
    Limit     int             // ≤ 0 → defaults to 100
}
```

### Default Values

| Parameter | Default | Notes |
|-----------|---------|-------|
| DBPath | `~/.config/llmem/memory.db` | Parent directory created with 0700 permissions |
| VecDimensions | 768 | Matches `nomic-embed-text` embedding model |
| DefaultConfidence | 0.8 | Applied when Confidence is 0 in AddParams |
| ExportLimit | 10000 | Pass `0` for unlimited export |
| BruteForceMaxRows | 10000 | Cap on brute-force embedding scan |
| MaxTraversalDepth | 5 | Hard cap on relation traversal depth |
| MaxIDLength | 256 | Import rejects IDs exceeding this |
| MaxEmbeddingBytes | 1048576 (1 MB) | `Add`, `Update`, and `ImportMemories` reject embeddings exceeding this |

### Error Handling

All errors from store methods are wrapped with the `"llmem: store: "` prefix and include domain context. Use `errors.Is` / `errors.As` for programmatic inspection:

```go
_, err := ms.Add(ctx, store.AddParams{Type: "unknown_type", Content: "test"})
// err.Error() → "llmem: store: add: unregistered type \"unknown_type\": register it with RegisterMemoryType first"
```

### Embedding Byte Format

Embeddings are stored and accepted as packed `[]byte` in little-endian `float32` format. For a 768-dimensional embedding, this is `768 × 4 = 3072` bytes.

Use the exported `VecToBytes` and `BytesToVec` helpers if you need conversion:

```go
// Convert float32 slice to []byte for storage
packed := store.VecToBytes([]float32{0.1, 0.2, 0.3})

// Convert stored []byte back to float32 slice
vec := store.BytesToVec(packed)
```

### Database Schema

The Go implementation uses the identical 7-migration schema as Python:

| Migration | Description |
|-----------|-------------|
| 001 | Initial schema: `memories`, `relations`, `extraction_log` tables, `memories_fts` FTS5 virtual table |
| 002 | Add `hints` column (TEXT, JSON array) |
| 003 | Register default memory types in `memory_types` table |
| 004 | Add `code_chunks` table for code indexing |
| 005 | Add `inbox` table for working memory |
| 006 | Add `supersedes` and `references` relation types |
| 007 | Schema cleanup: drop dead columns, add indexes, add `derived_from` relation type |

Migrations are embedded via `embed.FS` and applied by `pressly/goose`. The database uses WAL mode and foreign keys by default.

### Embedding Engine (internal/embed)

The `internal/embed` package provides an Ollama `/api/embeddings` client with LRU cache.

```go
import "github.com/MichielDean/LLMem/internal/embed"

engine, err := embed.NewEmbeddingEngine(embed.EmbeddingConfig{
    Model:        "nomic-embed-text",  // default
    BaseURL:      "http://localhost:11434",  // default
    MaxCacheSize: 2048,  // default LRU cache entries
    Dimensions:   768,   // default vector dimensions
    Timeout:      30 * time.Second,  // default HTTP timeout
})
if err != nil {
    log.Fatal(err)
}
defer engine.Close()
```

#### EmbeddingConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| Model | string | `"nomic-embed-text"` | Ollama model name |
| BaseURL | string | `"http://localhost:11434"` | Ollama API base URL (validated for SSRF) |
| MaxCacheSize | int | 2048 | LRU cache max entries |
| Dimensions | int | 768 | Expected vector dimensions |
| Timeout | time.Duration | 30s | HTTP client timeout (0 → 30s) |
| HTTPClient | *http.Client | nil → new client | Optional pre-configured client (for testing) |

#### Methods

```go
// Get embedding vector for text. Returns cached result on cache hit.
// On cache miss, makes HTTP POST to {baseURL}/api/embeddings.
// Returns a defensive copy — callers may modify the slice freely.
vec, err := engine.Embed(ctx, "query text")

// Check if the configured Ollama model is available.
// Makes GET to {baseURL}/api/tags and matches exact model name
// or model name with tag suffix (e.g. "nomic-embed-text:latest").
// Returns false on any error (logs at Debug level).
available := engine.CheckAvailable(ctx)

// Close idle HTTP connections. Safe to call multiple times.
engine.Close()
```

**Caching behavior:** `Embed` caches results keyed by input text. Cache hits return a defensive copy of the slice. The LRU cache evicts the least-recently-used entry when full. Cache access is protected by `sync.RWMutex` — safe for concurrent use.

**Dimension mismatch:** If Ollama returns an embedding with a dimension count that doesn't match the configured `Dimensions`, `Embed` returns an error.

**Context cancellation:** `Embed` and `CheckAvailable` respect `context.Context` cancellation.

**URL validation:** When `HTTPClient` is nil (production mode), `BaseURL` is validated via `urlvalidate.ValidateBaseURL` for SSRF protection. When an `HTTPClient` is provided (test mode), URL validation is skipped.

### Retriever (internal/retriever)

The `internal/retriever` package provides hybrid search combining FTS5 and vector cosine similarity via Reciprocal Rank Fusion (RRF) with multi-signal reranking.

```go
import (
    "github.com/MichielDean/LLMem/internal/embed"
    "github.com/MichielDean/LLMem/internal/retriever"
    "github.com/MichielDean/LLMem/internal/store"
)

ms, _ := store.NewMemoryStore(store.StoreConfig{})
eng, _ := embed.NewEmbeddingEngine(embed.EmbeddingConfig{})

r, err := retriever.NewRetriever(retriever.RetrieverConfig{
    Store:    ms,
    Embedder: eng,         // nil → FTS-only mode
    Alpha:    ptrFloat64(0.7),  // nil → default 0.7; *float64(0.0) → pure FTS
    Blend:    ptrFloat64(0.3),  // nil → default 0.3; *float64(0.0) → pure RRF
})
```

#### RetrieverConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| Store | *store.MemoryStore | (required) | Memory store instance |
| Embedder | *embed.EmbeddingEngine | nil (FTS-only) | Embedding engine for semantic search |
| Alpha | *float64 | nil → 0.7 | RRF semantic weight (0.0=pure FTS, 1.0=pure semantic). Use pointer to distinguish nil (default) from explicit 0.0. |
| Blend | *float64 | nil → 0.3 | Reranking blend factor (0.0=pure RRF, 1.0=pure signals). Use pointer to distinguish nil (default) from explicit 0.0. |
| RRF_K | int | 60 | RRF constant |
| TypePriority | map[string]float64 | DefaultTypePriority() | Memory type priority weights |

**Pointer semantics for Alpha and Blend:** These use `*float64` instead of `float64` to distinguish "use the default" (nil) from "explicitly set to 0.0". A nil value applies the default (0.7 for Alpha, 0.3 for Blend). A pointer to 0.0 sets the value to pure FTS (Alpha) or pure RRF (Blend). Out-of-range values (outside [0.0, 1.0]) return an error from `NewRetriever`.

#### Methods

```go
// Basic FTS5 search with optional relation traversal and access tracking.
// When no results are found, returns nil.
results, err := r.Search(ctx, "query", 20, "fact", true, 3, true)

// Hybrid search combining FTS5 and semantic results via RRF fusion.
// searchMode: "hybrid", "fts", or "semantic".
// alpha: per-query override (nil → use retriever default).
// When searchMode="hybrid" and embedder is nil, falls back to FTS-only with slog.Warn.
// When searchMode="semantic" and embedder is nil, returns error.
// Empty query returns empty slice (not nil).
scored, err := r.HybridSearch(ctx, "query", 20, "fact", nil, "hybrid", true)

// Format search results as an LLM context string (truncated to budget chars at UTF-8 boundary).
// Default budget: 4000. Returns empty string when no results.
context, err := r.FormatContext(ctx, "query", 4000, "fact")
```

#### RRF and Reranking

```go
// Compute RRF scores from semantic and FTS rank maps.
// alpha controls semantic/FTS weight (0.0=pure FTS, 1.0=pure semantic).
// k defaults to 60 if 0. Empty inputs return nil.
results := retriever.RRFScore(semanticRanks, ftsRanks, 0.7, 60)

// Compute per-memory reranking signals.
signals := retriever.ComputeRerankSignals(memory, typePriority, time.Now().UTC())

// Combine signals using default weights: 0.4*Confidence + 0.3*Recency + 0.2*Access + 0.1*Type.
weighted := retriever.ComputeWeightedSignal(signals)

// Get default type priority map (returns defensive copy).
priorities := retriever.DefaultTypePriority()
// map[conversation:0.7 decision:1.2 preference:1.1 procedure:1.1 fact:1.0 project_state:1.0 event:0.9]
```

#### Reranking Signals

| Signal | Weight | Formula |
|--------|--------|---------|
| Confidence | 0.4 | Direct use of `confidence` field (0.0–1.0) |
| Recency | 0.3 | `exp(-0.01 * days_since_access)` (0.0 if never accessed) |
| Access frequency | 0.2 | `log(1 + access_count / max(age_days, 1))` (0.0 if never accessed) |
| Type priority | 0.1 | Lookup in type priority map (default 1.0 for unknown types) |

The final score is: `rrf_score * (1 - blend) + weighted_signal * blend`

#### Type Priority Weights

| Type | Priority | | Type | Priority |
|------|----------|-|------|----------|
| decision | 1.2 | | fact | 1.0 |
| preference | 1.1 | | project_state | 1.0 |
| procedure | 1.1 | | event | 0.9 |
| conversation | 0.7 | | | |

### Embedding Metrics (internal/metrics)

The `internal/metrics` package provides embedding quality metrics for detecting poor embedding vectors.

```go
import "github.com/MichielDean/LLMem/internal/metrics"

// Compute all metrics at once
m, err := metrics.ComputeMetrics(embeddings, labels, 0)  // 0 → use MetricsMaxEmbeddings (10000)
// m.Anisotropy        → float64 in [0.0, 1.0]; lower is better
// m.SimilarityRange   → float64; higher is better
// m.DiscriminationGap → float64; higher is better (0.0 if labels nil/empty/single-class)

// Individual metric functions
aniso := metrics.Anisotropy(embeddings)             // Average pairwise cosine similarity, clamped [0, 1]
simRange := metrics.SimilarityRange(embeddings)      // Max - min pairwise cosine similarity
discGap, err := metrics.DiscriminationGap(embeddings, labels)  // Inter-class vs intra-class separation
```

#### Warning Thresholds

| Constant | Value | Meaning |
|----------|-------|---------|
| AnisotropyWarningThreshold | 0.5 | Anisotropy above this may indicate poor embeddings |
| SimilarityRangeWarningThreshold | 0.1 | Similarity range below this may indicate poor embeddings |
| MetricsMaxEmbeddings | 10000 | Cap on embedding count for O(n²) computations |

**Performance safeguard:** `ComputeMetrics` caps the number of vectors to `MetricsMaxEmbeddings` (default 10000) to prevent O(n²) CPU hangs on large stores. When `maxEmbeddings <= 0`, the default is used. Labels are truncated to match the embedding count.

**Edge cases:** `Anisotropy` and `SimilarityRange` return 0.0 for empty or single-vector input. `DiscriminationGap` returns (0.0, nil) for nil/empty labels or single-class labels, and an error if `len(labels) != len(embeddings)`.

### URL Validation (internal/urlvalidate)

The `internal/urlvalidate` package provides SSRF-protected URL validation and safe HTTP access, blocking private/link-local IPs, percent-encoded SSRF bypasses, and redirect-based attacks.

```go
import "github.com/MichielDean/LLMem/internal/urlvalidate"

// Check if a URL is safe to access
safe := urlvalidate.IsSafeURL("http://localhost:11434/api/generate", false)  // true (loopback on Ollama port)
safe := urlvalidate.IsSafeURL("http://192.168.1.1/admin", false)              // false (private IP)
safe := urlvalidate.IsSafeURL("https://api.openai.com/v1/models", true)      // true (allowRemote=true)

// Open a URL with SSRF protections (blocks redirects, re-resolves DNS)
resp, err := urlvalidate.SafeURLOpen(ctx, urlStr, 30*time.Second, false)

// Validate and normalize an Ollama base URL (allowRemote=true for remote Ollama)
url, err := urlvalidate.ValidateBaseURL("http://localhost:11434", "embed")

// Infer whether a URL should be treated as remote
remote := urlvalidate.IsRemoteAllowed("https://api.openai.com")  // true (public IP)
remote := urlvalidate.IsRemoteAllowed("http://localhost:11434") // false (loopback)
```

#### SSRF Protections

- **Private IP blocking:** `IsSafeURL` rejects private, link-local, multicast, and unspecified IPs. Loopback is only allowed on the Ollama default port (11434) when `allowRemote=false`.
- **Percent-decode bypass prevention:** Hostnames are percent-decoded before IP checks (e.g. `%31%32%37%2e%30%2e%30%2e%31` → `127.0.0.1`).
- **Redirect blocking:** `SafeURLOpen` uses a custom transport that blocks all HTTP redirects (3xx responses). Redirect targets are logged via `slog.Warn` with credentials stripped.
- **DNS rebinding mitigation:** `SafeURLOpen` re-resolves the hostname immediately before the HTTP request to detect DNS rebinding TOCTOU attacks.
- **Credential stripping:** Error messages and logs strip userinfo from URLs via `stripCredentials()`, preserving query strings and fragments.
- **Fail-closed:** `IsRemoteAllowed` returns `false` for hostnames that fail DNS resolution.

### Configuration (internal/config)

The `internal/config` package provides configuration loading from YAML files with path resolution, defaults, and validation.

```go
import "github.com/MichielDean/LLMem/internal/config"

// Load config from the default path
cfg, err := config.LoadConfig(paths.GetConfigPath())

// Access config sections
dbPath := cfg.DBPath()           // resolved database path
ollamaURL, err := cfg.OllamaURL() // validated Ollama URL
dreamerCfg := cfg.DreamerConfig() // DreamerConfig for dream.NewDreamer()
dreamCfg := cfg.DreamConfigResolved()
sessionCfg := cfg.SessionConfigResolved()

// Write config YAML (with file permissions 0600)
written, err := config.WriteConfigYAML(path, configMap, false) // false = don't overwrite
```

#### Config Types

```go
type Config struct {
    Memory    MemoryConfig
    Dream     DreamConfig
    OpenCode  OpenCodeConfig
    Session   SessionConfig
}

type MemoryConfig struct {
    DBPath        string
    OllamaURL     string
    EmbedModel    string
    ExtractModel  string
    ContextBudget int
    AutoExtract   bool
    MaxFileSize   int64
}

type DreamConfig struct {
    SimilarityThreshold    float64
    DecayRate               float64
    DecayIntervalDays       int
    DecayFloor              float64
    ConfidenceFloor          float64
    BoostThreshold           int
    BoostAmount             float64
    DiaryPath                string
    ReportPath               string
    AutoLinkThreshold        float64
    StaleProcedureDays       int
}

type SessionConfig struct {
    Adapter         string
    DebounceSeconds int
}
```

#### Validation

- `OllamaURL()` validates the URL via `urlvalidate.ValidateBaseURL` (SSRF protection).
- `DBPath()` resolves `~` and applies defaults.
- `WriteConfigYAML` writes with `0600` permissions.

### Dream Cycle (internal/dream)

See [Dream Cycle & Extraction](DREAM.md#go-api--dream-package) for full documentation.

### Extraction (internal/extract)

The `internal/extract` package provides LLM-based memory extraction via Ollama (see [Dream Cycle & Extraction](DREAM.md#go) for usage).

```go
import "github.com/MichielDean/LLMem/internal/extract"

engine, err := extract.NewExtractionEngine(extract.ExtractionConfig{
    Model:   "glm-5.1:cloud",
    BaseURL: "http://localhost:11434",
})

// Extract returns empty slice on Ollama failure (graceful degradation)
memories := engine.Extract(ctx, text)

// Check model availability
available := engine.CheckAvailable(ctx)
```

#### ExtractionConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| Model | string | `"glm-5.1:cloud"` | Extraction model name |
| BaseURL | string | `"http://localhost:11434"` | Ollama API base URL (validated for SSRF) |
| HTTPClient | *http.Client | nil → new client | Optional pre-configured client (for testing) |
| OllamaClient | *ollama.OllamaClient | nil | Optional pre-configured OllamaClient; takes precedence over BaseURL/HTTPClient |

### Ollama Client (internal/ollama)

The `internal/ollama` package provides an HTTP client for the Ollama `/api/generate` and `/api/tags` endpoints.

```go
import "github.com/MichielDean/LLMem/internal/ollama"

client, err := ollama.NewOllamaClient(ollama.OllamaClientConfig{
    BaseURL:    "http://localhost:11434",
    Timeout:    300 * time.Second,
    HTTPClient: nil,  // nil → new client with timeout
})

// Generate text using Ollama
response, err := client.Generate(ctx, "prompt text", "model-name")

// Check if a model is available
available := client.IsAvailable(ctx)

// Pull a model (returns true if newly pulled, false if already exists)
pulled, err := client.PullModel(ctx, "glm-5.1:cloud")

// Close idle connections
client.Close()
```

#### OllamaClientConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| BaseURL | string | `"http://localhost:11434"` | Validated via `urlvalidate.ValidateBaseURL` for SSRF protection |
| Timeout | time.Duration | 300s | HTTP client timeout (0 → 300s) |
| HTTPClient | *http.Client | nil → new client | Pre-configured client (for testing with httptest) |

### Path Validation (internal/paths)

The `internal/paths` package resolves LLMem paths and validates against path traversal attacks.

```go
import "github.com/MichielDean/LLMem/internal/paths"

// Path resolution
home := paths.GetHomeDir()          // ~/.config/llmem/ (or LMEM_HOME)
dbPath := paths.GetDBPath()         // ~/.config/llmem/memory.db
cfgPath := paths.GetConfigPath()    // ~/.config/llmem/config.yaml
diaryPath := paths.GetDreamDiaryPath()     // ~/.config/llmem/dream-diary.md
reportPath := paths.GetDreamReportPath()   // ~/.config/llmem/dream-report.html
skillDir := paths.GetSkillDir()    // ~/.config/llmem/skills/
ctxDir := paths.GetContextDir()      // ~/.config/llmem/context/

// Validation
validID, err := paths.ValidateSessionID("abc123")   // rejects /, \, ..
resolved, err := paths.ValidateWritePath("/tmp/out.html", "report")
homePath, err := paths.ValidateHomePath("/home/user/.config/llmem", "LMEM_HOME")
blocked := paths.IsBlockedPath("/etc/passwd")  // true — system directories blocked

// Migration
migrated, err := paths.MigrateFromLobsterdog()  // copies ~/.lobsterdog/ to ~/.config/llmem/
```

### Systemd Unit Generation (internal/systemd)

The `internal/systemd` package generates systemd service and timer unit files for the dream cycle.

```go
import "github.com/MichielDean/LLMem/internal/systemd"

// Generate service unit
serviceContent, err := systemd.GenerateServiceUnit("*-*-* 03:00:00")

// Generate timer unit (validates schedule for shell metacharacters)
timerContent, err := systemd.GenerateTimerUnit("*-*-* 03:00:00")

// Validate a systemd schedule expression
valid := systemd.ValidateSchedule("*-*-* 03:00:00")  // true
valid := systemd.ValidateSchedule("$(evil)")            // false — rejects shell metacharacters
```

Templates are embedded via `embed.FS`. `GenerateTimerUnit` calls `ValidateSchedule` before template interpolation to prevent injection.

### Taxonomy (internal/taxonomy)

The `internal/taxonomy` package provides error taxonomy constants.

```go
import "github.com/MichielDean/LLMem/internal/taxonomy"

// Access taxonomy map
for category, description := range taxonomy.ErrorTaxonomy {
    fmt.Println(category, ":", description)
}

// Get ordered category keys
keys := taxonomy.ErrorTaxonomyKeys()
// ["NULL_SAFETY", "ERROR_HANDLING", "OFF_BY_ONE", "RACE_CONDITION", "AUTH_BYPASS",
//  "DATA_INTEGRITY", "MISSING_VERIFICATION", "EDGE_CASE", "PERFORMANCE", "DESIGN", "REVIEW_PASSED"]
```

#### Error Categories

| Category | Description |
|----------|-------------|
| `NULL_SAFETY` | Missing null/None/undefined checks |
| `ERROR_HANDLING` | Missing try/except, bare except, swallowed errors |
| `OFF_BY_ONE` | Boundary errors, wrong loop bounds |
| `RACE_CONDITION` | Concurrency issues, async problems |
| `AUTH_BYPASS` | Missing auth checks, SSRF, injection |
| `DATA_INTEGRITY` | Stale derived fields, cache sync issues |
| `MISSING_VERIFICATION` | Skipped tests, unverified outputs |
| `EDGE_CASE` | Unhandled empty input, unexpected types |
| `PERFORMANCE` | N+1 queries, memory leaks |
| `DESIGN` | Architectural issues, coupling problems |
| `REVIEW_PASSED` | Clean review with no findings — positive outcome for tracking purposes |