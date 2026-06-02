# LLMem — Structured Memory with Semantic Search

[![CI](https://github.com/MichielDean/LLMem/actions/workflows/ci.yml/badge.svg)](https://github.com/MichielDean/LLMem/actions/workflows/ci.yml)

LLMem is a SQLite-backed memory store for LLM agents. It provides structured storage with full-text search (FTS5), optional vector similarity search (via sqlite-vec), an extensible type system, and a provider abstraction layer for LLM embeddings and text generation. A background dreaming cycle consolidates, decays, and merges memories over time.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and coding conventions.

## Documentation

| Document | Description |
|----------|-------------|
| [Installation](docs/INSTALLATION.md) | Install from source (Python and Go), optional extras, plugin setup |
| [Providers](docs/PROVIDERS.md) | Embedding/generation providers, fallback chains, configuration |
| [CLI Reference](docs/CLI.md) | All `llmem` commands and options |
| [Python API](docs/API.md) | MemoryStore, Retriever, extension points, database schema, module reference |
| [Go API](docs/API.md#go-api) | Go packages — store, config, dream, extract, ollama, paths, systemd, taxonomy, urlvalidate |
| [Integrations](docs/INTEGRATIONS.md) | OpenCode, Copilot CLI, custom tools, plugins |
| [Configuration](docs/CONFIGURATION.md) | config.yaml reference, path resolution, dream settings |
| [Search Reranking](docs/RERANKING.md) | Multi-signal reranking, signal weights, type priority |
| [Dream Cycle & Extraction](docs/DREAM.md) | Dream phases, extraction pipeline |
| [Security](docs/SECURITY.md) | Path validation, SSRF protection, credential handling, code indexing security |

## Installation

### Go binary (CLI + library)

Clone and build:

```bash
git clone https://github.com/MichielDean/LLMem.git
cd LLMem && make build
```

This produces the `llmem` CLI binary. Install it on your PATH:

```bash
make install   # copies to ~/.local/bin/llmem
```

Or install manually:

```bash
cp llmem ~/.local/bin/llmem
ln -sf ~/.local/bin/llmem /usr/local/bin/llmem   # optional, for system-wide access
```

Then initialize:

```bash
llmem init                  # interactive — detects providers
llmem init --non-interactive  # use defaults, no prompts
```

For detailed installation options (Python extras, providers, requirements), see [docs/INSTALLATION.md](docs/INSTALLATION.md).

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
node install.js --platform claude-code   # Claude Code / Copilot CLI
node install.js --platform copilot       # Copilot CLI (same plugin as claude-code)
node install.js --platform both          # OpenCode + Claude Code / Copilot CLI
node install.js --platform none          # Skills only, no plugins
```

See below for per-platform setup details.

### Go (memory store library)

The Go implementation provides the core memory store as a pure-Go library with no CGo dependency, plus a full CLI, dream cycle, and extraction:

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

See [docs/INSTALLATION.md](docs/INSTALLATION.md) for Go build dependencies and [docs/API.md](docs/API.md) for the full API reference.

## Plugin Architecture: Zero-Config Integration

LLMem uses platform plugins to inject memory context automatically. **No manual instruction editing required.** The plugin handles:

- **Session start**: Injects memory stats and search results as context
- **Compaction**: Preserves key memories across context compaction

| Platform | Plugin source | Install path | How to install |
|----------|---------------|-------------|---------------|
| **OpenCode** | `plugins/opencode/llmem.js` | `~/.config/opencode/plugins/` | Auto-deployed by `npm install` |
| **Claude Code** | `plugins/agent/` | `~/.claude/plugins/llmem/` | `claude plugin install ~/.claude/plugins/llmem` (auto-deployed by `npm install`) |
| **Copilot CLI** | `plugins/agent/` | `~/.copilot/installed-plugins/_direct/llmem/` | `copilot plugin install ~/.copilot/installed-plugins/_direct/llmem` (auto-deployed by `npm install`) |

Claude Code and Copilot CLI share the same plugin source (`plugins/agent/`) because they use the same plugin specification (`.claude-plugin/plugin.json` manifest, `skills/`, `hooks/`). They differ only in where the plugin is installed. The `npm install` postinstall script auto-detects each platform and deploys to the correct location.

The plugin-first approach means your AGENTS.md, CLAUDE.md, or system instructions stay clean — no 80-line memory blocks. See [Integrations](docs/INTEGRATIONS.md) for platform-specific setup.

## Skills

LLMem ships two skills focused on memory management. They load on-demand via the skill system — no need to paste their content into instruction files.

| Skill | Description |
|-------|-------------|
| **llmem** | Manage LLMem memories — add, search, consolidate, and dream. |
| **llmem-setup** | Install and configure LLMem — plugin deployment, provider setup, skill registration. |

## Templates

The `templates/` directory contains generic, personality-agnostic template files that you can copy and customize for your own agent setup:

| Template | Purpose |
|----------|---------|
| **templates/rules.md** | Generic workflow rules — no personal or tool-specific references |
| **templates/identity.md** | Agent identity scaffold — fill in your agent's name, personality, and boundaries |
| **templates/user.md** | User profile scaffold — fill in your name, timezone, and preferences |

To use them, copy the templates into your `harness/` directory and fill them in:

```bash
cp templates/identity.md harness/identity.md
cp templates/user.md harness/user.md
cp templates/rules.md harness/rules.md
# Then edit each file to personalize for your setup
```

The `opencode.json` configuration loads `harness/identity.md`, `harness/user.md`, and `harness/rules.md` — so after copying and customizing, your agent will use your personalized versions.

## Verification

After installing, verify everything works:

```bash
# Check the CLI is available
llmem --help

# Initialize config and database
llmem init

# Confirm the store is working
llmem stats

# Verify skills are deployed
ls ~/.agents/skills/llmem

# Verify plugin deployed (OpenCode)
ls ~/.config/opencode/plugins/llmem.js

# Verify plugin deployed (Claude Code)
ls ~/.claude/plugins/llmem/.claude-plugin/plugin.json

# Verify plugin deployed (Copilot CLI)
ls ~/.copilot/installed-plugins/_direct/llmem/.claude-plugin/plugin.json
```

## Quick Start

```bash
# Initialize the memory system (creates config, database, detects providers)
llmem init

# Non-interactive mode (skip prompts, use defaults)
llmem init --non-interactive

# Add a memory
llmem add --type fact --content "Project uses pytest for testing"

# Search memories
llmem search "testing"
llmem search "testing" --type fact --limit 5 --json
llmem search "testing" --include-code --json

# List all memories
llmem list
llmem list --type decision --all

# Get a specific memory
llmem get <memory-id>

# Show statistics
llmem stats

# Register a custom memory type
llmem register-type my_custom_type
llmem types

# Working memory inbox
llmem note "Important observation from today's session"
llmem note "Tentative insight" --attention-score 0.3
llmem inbox
llmem consolidate --dry-run
llmem consolidate --min-score 0.5

# Dream cycle (automated memory maintenance)
llmem dream                   # Dry run — preview changes only
llmem dream --apply           # Apply changes
llmem dream --phase deep      # Run only the deep phase
llmem dream --report dream.html  # Generate HTML dream report

# Check embedding quality
llmem embed
llmem consolidate --metrics

# Export and import
llmem export --output backup.json
llmem import backup.json
```

## Running Tests

```bash
# Python tests
python -m pytest

# Go tests
go test ./...
```

1349 Python tests and 142 JavaScript tests covering all providers, session adapters (OpenCode, Copilot, none), URL validation, configuration, security, CLI commands, and edge cases.

Go tests covering store operations, FTS5 search, vector search, hybrid retrieval, embedding engine, metrics, URL validation, migrations, type validation, import/export, config, dream cycle, extraction, path validation, systemd unit generation, and taxonomy.

## Makefile

The Go project includes a Makefile with common tasks:

```bash
make build    # go build ./...
make test     # go test ./...
make lint     # go vet ./...
make clean    # remove *.db files
```

## License

MIT