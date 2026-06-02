---
name: llmem-setup
description: >
  Install and configure LLMem for an agent harness. Handles cloning, pip/npm
  installs, llmem init, and harness integration (AGENTS.md injection or minimal
  harness creation). Use when setting up memory for a new or existing agent.
  Triggers on: "install llmem", "set up memory", "configure memory",
  "add llmem to harness", "memory setup".
license: MIT
---

# LLMem Setup

Install, configure, and integrate LLMem into an agent's harness so it can use structured memory.

## When to Run

- Setting up memory for a new agent
- Adding memory to an existing agent harness
- After cloning LLMem and before first use
- When an agent asks "how do I get llmem working?"

## Procedure

### Step 1: Install LLMem

Choose the right installation method based on the agent's platform:

**All agents (CLI + library):**

```bash
git clone https://github.com/MichielDean/LLMem.git
cd LLMem && ./setup.sh --plugin <platform>
```

Where `<platform>` is:
- `opencode` — for OpenCode-based agents
- `copilot` — for GitHub Copilot CLI agents
- `both` — for agents using both (default)
- `none` — CLI only, no agent plugins

**Or the one-liner:**

```bash
curl -sSL https://raw.githubusercontent.com/MichielDean/LLMem/main/setup.sh | bash
```

**Platform-specific plugin install (after CLI install):**

OpenCode:

```bash
cd LLMem/opencode-llmem && npm install
```

Copilot CLI — use the `copilot plugin install` command with the repo subdirectory syntax. See [Installation docs](../docs/INSTALLATION.md) for the exact command.

### Step 2: Initialize

```bash
llmem init
```

This creates `~/.config/llmem/config.yaml` and `~/.config/llmem/memory.db`. It detects available providers automatically.

For non-interactive setup (scripts, CI):

```bash
llmem init --non-interactive
```

### Step 3: Configure Provider

LLMem needs an embedding/generation provider. Choose one:

**Option A: Ollama (local, free)**

```bash
curl -fsSL https://ollama.com/install.sh | sh
ollama pull nomic-embed-text
ollama pull qwen2.5:1.5b
```

Config is auto-detected. No additional setup needed.

**Option B: OpenAI (cloud, needs API key)**

```bash
export OPENAI_API_KEY=sk-...
```

Then run `llmem init --non-interactive` to regenerate config.

**Option C: Local (no server, uses sentence-transformers)**

```bash
pip install ".[local]"
```

Config requires setting `provider.default: local` in `~/.config/llmem/config.yaml`.

**Option D: None (FTS5-only mode)**

Works without any provider. Semantic search is disabled. Full-text search still works.

### Step 4: Integrate Into Harness

An agent harness needs three things to use memory:

1. **AGENTS.md (or equivalent) memory instructions** — tells the agent when to read/write memory
2. **Session hooks** — automatically inject context on session start and extract on idle
3. **Provider config** — already done in Step 3

#### Existing Harness (has AGENTS.md)

Add memory instructions to the agent's AGENTS.md. Insert these sections:

```markdown
## Memory — Search at Every Decision Point

Memory is working memory, not a startup ritual. Search before assuming.

**Session start — MANDATORY:**
1. `llmem stats` — check memory health
2. `llmem search "topic" --limit 5` — search for relevant memories

**Mid-session search triggers — search whenever:**
- Looking up how something works
- Making a choice between approaches
- Encountering a project-specific name/concept
- Answering a state question ("where are we with X?")
- Topic shift (debugging → design, one codebase → another)

**Write when you learn:**
- `llmem add --type decision --content "chose X over Y because Z"` — decisions and rationale
- `llmem add --type fact --content "project uses pytest for testing"` — objective truths
- `llmem add --type preference --content "prefer dark theme" --confidence 0.9` — user preferences
- `llmem add --type procedure --content "how to deploy: step 1, step 2..."` — how-to knowledge

**Invalidate, don't delete:**
- `llmem invalidate <id> --reason "no longer relevant"` — soft-delete, stays for reference
```

#### New Harness (no AGENTS.md)

Create a minimal harness with memory support:

```bash
mkdir -p harness
cp LLMem/templates/identity.md harness/identity.md
cp LLMem/templates/rules.md harness/rules.md
cp LLMem/templates/user.md harness/user.md
```

Then edit `harness/identity.md` to set the agent's name and personality. Edit `harness/user.md` to set the user's name and timezone. Add the memory instructions from "Existing Harness" above into `harness/rules.md`.

#### OpenCode Agents

OpenCode agents need the `opencode-llmem` plugin (installed in Step 1) and the llmem skill registered in `opencode.json`:

```json
{
  "skills": ["llmem", "llmem-setup"]
}
```

The plugin handles session hooks automatically:
- `session.created` → `llmem stats` + `llmem search` (injects relevant memories)
- `session.compacting` → `llmem search` (preserves key memories)

Skills are installed to `~/.agents/skills/`. OpenCode discovers them automatically.

### Step 5: Verify

Run these checks to confirm everything works:

```bash
# CLI is available
llmem --help

# Database is initialized
llmem stats

# Can add and search
llmem add --type fact --content "test memory"
llmem search "test"

# Provider is working (will show provider type)
llmem search "test" --json | head -5

# Skills are discoverable (OpenCode/Copilot only)
ls ~/.agents/skills/llmem/SKILL.md
ls ~/.agents/skills/llmem-setup/SKILL.md

# OpenCode plugin (OpenCode only)
ls ~/.agents/plugins/llmem/

# Session hooks work
llmem stats && llmem search "test" --limit 5
```

### Step 6: Dream Timer (Optional)

For automatic memory consolidation, set up the systemd dream timer:

```bash
# Copy timer and service files (adjust paths for your system)
cp LLMem/harness/llmem-dream.service ~/.config/systemd/user/
cp LLMem/harness/llmem-dream.timer ~/.config/systemd/user/

# Edit the service file to point to your llmem binary
# ExecStart=/path/to/llmem dream --apply

systemctl --user daemon-reload
systemctl --user enable llmem-dream.timer
systemctl --user start llmem-dream.timer
```

Dream runs nightly at 3am by default. Configure in `~/.config/llmem/config.yaml` under `dream:`.

## Troubleshooting

**`llmem: command not found`** — The pip install didn't put the binary on PATH. Try `python3 -m llmem.cli` or check `pip show llmem` for the install location. On some systems you may need `pip install --break-system-packages .` or a virtual environment.

**`Ollama not reachable`** — Either start Ollama (`ollama serve`), pull the models (`ollama pull nomic-embed-text`), or switch to a different provider. LLMem falls back through: Ollama → OpenAI → Anthropic → local → none (FTS5-only).

**`Permission denied on ~/.config/llmem/`** — Check directory permissions: `ls -la ~/.config/llmem/`. The database and config need to be readable/writable by the current user. `llmem init` creates them with correct permissions.

**`sqlite-vec not available`** — Semantic search falls back to brute-force cosine similarity automatically. For ANN vector search, install with `pip install ".[vec]"`.

**Skills not discovered** — Verify the skill directories exist: `ls ~/.agents/skills/`. If missing, re-run the plugin install step: `cd LLMem/opencode-llmem && npm install`.