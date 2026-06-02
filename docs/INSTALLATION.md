# LLMem Installation

How to install and set up LLMem. [Back to README](../README.md)

## Go Installation (recommended)

The Go binary provides the full CLI, dream cycle, and extraction.

### Build from source

```bash
git clone https://github.com/MichielDean/LLMem.git
cd LLMem
make build
make install    # copies binary to ~/.local/bin/llmem
```

Or install the CLI manually:

```bash
go build -o ~/.local/bin/llmem ./cmd/llmem
```

### Initialize

```bash
llmem init                  # interactive — detects providers
llmem init --non-interactive  # use defaults, no prompts
```

### Agent integration (skills + plugins)

After building the CLI, install the agent integration layer:

```bash
cd LLMem && npm install
```

This runs the postinstall script which:
1. Copies all skill directories to `~/.agents/skills/`
2. Auto-detects your agent platform (OpenCode, Claude Code, or Copilot CLI)
3. Deploys the correct plugin to the right location

Force a specific platform:

```bash
node install.js --platform opencode      # OpenCode only
node install.js --platform claude-code   # Claude Code / Copilot CLI only
node install.js --platform both          # OpenCode + Claude Code / Copilot CLI
node install.js --platform none          # Skills only, no plugins
```

### Platform-specific setup

**OpenCode:** The plugin is auto-deployed to `~/.config/opencode/plugins/llmem.js`. Optionally add to your `opencode.json`:
```json
{ "plugin": ["llmem"] }
```

**Claude Code / Copilot CLI:** The plugin is auto-deployed to `~/.claude/plugins/llmem/`. Enable it:
```bash
claude plugin install ~/.claude/plugins/llmem
# Or test without installing:
claude --plugin-dir ~/.claude/plugins/llmem
```

### What to install

| Goal | What to install |
|------|-----------------|
| Use the `llmem` CLI only | `make build && make install` |
| Use LLMem with OpenCode | CLI + `npm install` (deploys skills + OpenCode plugin) |
| Use LLMem with Claude Code / Copilot CLI | CLI + `npm install` (deploys skills + agent plugin) |
| Just the skill files (no CLI, no plugin) | Copy `skills/` to `~/.agents/skills/` manually |

### Requirements

- Go 1.26.1+ (for building the CLI)
- Node.js 20+ (for plugin installation only)
- [Ollama](https://ollama.com) running locally for extraction, embedding, and dreaming — **or** set `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` for cloud providers — **or** use FTS5-only mode without any provider

## Python Installation (legacy)

The Python package is still available but the Go binary is recommended for CLI use. The Python package provides the same CLI plus a Python library for programmatic access.

### From source

```bash
git clone https://github.com/MichielDean/LLMem.git
cd LLMem
pip install .
```

**With optional extras:**

```bash
# Vector similarity search (sqlite-vec)
pip install ".[vec]"

# Local embedding without any server (sentence-transformers)
pip install ".[local]"

# Both + dev dependencies
pip install ".[vec,local,dev]"
```

**Initialize and verify:**

```bash
llmem init
llmem stats
```

## Go Package

Use LLMem as a Go library in your own projects:

```bash
go get github.com/MichielDean/LLMem
```

```go
import (
    "github.com/MichielDean/LLMem/internal/store"
    "github.com/MichielDean/LLMem/internal/embed"
    "github.com/MichielDean/LLMem/internal/retriever"
    "github.com/MichielDean/LLMem/internal/metrics"
    "github.com/MichielDean/LLMem/internal/urlvalidate"
)

ms, err := store.NewMemoryStore(store.StoreConfig{
    DBPath:         "",               // defaults to ~/.config/llmem/memory.db
    VecDimensions:  0,               // defaults to 768
    DisableVec:     false,            // set true to skip vec0 virtual table
    RegisteredTypes: nil,             // defaults to 7 standard types
})
if err != nil {
    log.Fatal(err)
}
defer ms.Close()

// Embedding engine (Ollama client with LRU cache)
eng, err := embed.NewEmbeddingEngine(embed.EmbeddingConfig{})

// Hybrid search retriever (FTS5 + semantic with RRF fusion)
r, err := retriever.NewRetriever(retriever.RetrieverConfig{Store: ms, Embedder: eng})

// Embedding quality metrics
m, err := metrics.ComputeMetrics(embeddings, labels, 0)

// SSRF-protected URL validation
safe := urlvalidate.IsSafeURL(urlStr, false)
```

### Run tests

```bash
make test
# or
go test ./...
```

### Key differences from Python

| Feature | Python | Go |
|---------|--------|-----|
| SQLite driver | Built-in `sqlite3` | `modernc.org/sqlite` (pure Go, no CGo) |
| Vector search | `sqlite-vec` Python package | `vec0` virtual table (pure Go via modernc) |
| Migrations | Manual numbered SQL files | `pressly/goose` with embedded SQL files |
| CLI | Full-featured (24 commands) | Core commands (17 commands) |
| Embeddings/Providers | Ollama, OpenAI, Anthropic, local | Ollama (`/api/generate` and `/api/embeddings`) |
| Reranking | RRF + multi-signal | FTS5-only (hybrid coming) |
| Session hooks | Full lifecycle | Full lifecycle (Go implementation) |
| Dream cycle | Full (light, deep, REM) | Full (light, deep, REM) |

The Go `MemoryStore` shares the **exact same database schema** as Python. You can use them interchangeably — a database created by Python is readable by Go, and vice versa.