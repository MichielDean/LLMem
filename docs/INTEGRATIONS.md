# LLMem Integrations

Integration guides for OpenCode, Claude Code, Copilot CLI, and custom tool implementations. [Back to README](../README.md)

## Architecture: Plugin-First, Zero-Config

LLMem uses a **plugin-first architecture**. The plugin handles automatic memory context injection at session start and key memory preservation during compaction. **No manual instruction editing required.**

```
Agent Session
    │
    ├── Plugin (auto, no instructions needed)
    │   ├── session.created/start → llmem stats + llmem search → inject context
    │   └── session.compacting    → llmem search → preserve memories
    │
    ├── Skills (on-demand, loaded by trigger)
    │   ├── llmem                      → CLI reference, memory types, commands
    │   └── llmem-setup                → Install and configure LLMem
    │
    └── Custom Tools (structural, zero-instruction)
        ├── llmem-search   → Search memories
        ├── llmem-add      → Add a memory
        ├── llmem-context  → Get context for a topic
        ├── llmem-invalidate → Soft-delete a memory
        └── llmem-stats    → Show memory statistics
```

### Why Plugin-First?

- **No instruction pollution.** The plugin injects context automatically. Skills load on-demand. Your AGENTS.md/CLAUDE.md stays clean.
- **Platform-agnostic core.** Same Go binary, same skills, same CLI across OpenCode, Claude Code, and Copilot CLI. Only the thin adapter plugin differs.

## OpenCode Integration

### Installation

#### Quick install (one command)

```bash
curl -sSL https://raw.githubusercontent.com/MichielDean/LLMem/main/setup.sh | bash
```

#### Manual install

```bash
git clone https://github.com/MichielDean/LLMem.git
cd LLMem && ./setup.sh
```

The setup script installs the Go binary, runs `npm install` (which deploys skills, plugins, and tools), and initializes the database.

### Plugin

The OpenCode plugin (`plugins/opencode/llmem.js`) handles:

| Event | Action |
|-------|--------|
| `session.created` | Runs `llmem stats` + `llmem search` — injects results as log context |
| `experimental.session.compacting` | Runs `llmem search` — preserves key memories |

The plugin is deployed to `~/.config/opencode/plugins/llmem.js` by the install script. No manual configuration needed — OpenCode auto-discovers plugins in this directory.

To explicitly add it to your `opencode.json`:

```json
{
  "plugin": ["llmem"]
}
```

### Custom Tools

The `.opencode/tools/` directory contains five type-safe tools that the agent can invoke directly without loading a skill:

| Tool | CLI Equivalent | Description |
|------|----------------|-------------|
| `llmem-search` | `llmem search <query> --json` | Search memories; returns JSON array |
| `llmem-add` | `llmem add --type T --content C` | Add a memory; returns ID and type |
| `llmem-context` | `llmem search <query> --limit 20` | Retrieve formatted context for a topic |
| `llmem-invalidate` | `llmem invalidate <ID>` | Invalidate a memory by ID |
| `llmem-stats` | `llmem stats` | Show memory statistics |

### Skills

Two skills ship with LLMem and are installed to `~/.agents/skills/`:

| Skill | Description |
|-------|-------------|
| **llmem** | Full CLI reference, memory types, commands, dream config |
| **llmem-setup** | Install, configure, and integrate LLMem into a harness |

### Optional: AGENTS.md Pointer

If you want a persistent reminder (not required — the plugin handles context injection), add this minimal line:

```markdown
## Memory

Plugin-managed. Search when uncertain: `llmem search "topic"`. Add when you learn: `llmem add --type fact --content "..."`.
```

### Configuring Providers

LLMem uses a provider abstraction layer. The simplest configuration:

```bash
llmem init
```

This detects available providers (Ollama, OpenAI, Anthropic, local) and writes `config.yaml`. For non-interactive setup:

```bash
llmem init --non-interactive
```

See [Provider Configuration](PROVIDERS.md) for the full YAML reference.

## Claude Code Integration

The agent plugin (`plugins/agent/`) uses the standard `.claude-plugin/` format.

### Plugin Structure

```
plugins/agent/
├── .claude-plugin/
│   └── plugin.json          # Plugin manifest
├── hooks/
│   └── hooks.json           # Session lifecycle hooks
└── skills/
    ├── llmem/SKILL.md
    └── llmem-setup/SKILL.md
```

### Installation

The plugin is auto-deployed to `~/.claude/plugins/llmem/` by `npm install`. To enable:

```bash
claude plugin install ~/.claude/plugins/llmem
# Or for testing:
claude --plugin-dir ./plugins/agent
```

The `hooks.json` declares:

| Event | Action |
|-------|--------|
| `SessionStart` | Runs `llmem stats` + `llmem search` — stdout injected as context |
| `PreCompact` | Runs `llmem search` — preserves key memories |

The `SessionStart` hook's **stdout is added as context that Claude can see and act on** — this is the key mechanism for zero-config integration.

### Skills

Skills are namespaced under the plugin: `/llmem:llmem`, `/llmem:llmem-setup`, etc. Discovered automatically when the plugin is enabled.

### Optional: CLAUDE.md Pointer

If you want a persistent reminder (not required):

```markdown
## Memory

Plugin-managed. Search when uncertain: `llmem search "topic"`. Add when you learn: `llmem add --type fact --content "..."`.
```

## Copilot CLI Integration

Copilot CLI uses the same plugin source as Claude Code (`plugins/agent/` with `.claude-plugin/plugin.json`), but installs to a different location (`~/.copilot/installed-plugins/`).

### Installation

The plugin is auto-deployed to `~/.copilot/installed-plugins/_direct/llmem/` by `npm install`. To enable:

```bash
copilot plugin install ~/.copilot/installed-plugins/_direct/llmem
```

Or install directly from the repo:

```bash
copilot plugin install MichielDean/LLMem:plugins/agent
```

The same hooks and skills as Claude Code apply. Copilot CLI discovers `.claude-plugin/plugin.json` as a valid manifest location.

### Skills

Skills are discovered from the plugin's `skills/` directory. Also available project-level via `~/.agents/skills/` or `.copilot/skills/`.

### Optional: COPILOT.md Pointer

If you want a persistent reminder (not required):

```markdown
## Memory

Plugin-managed. Search when uncertain: `llmem search "topic"`. Add when you learn: `llmem add --type fact --content "..."`.
```

## Verification

After installation, verify the skills and plugins are discoverable:

```bash
ls ~/.agents/skills/llmem ~/.agents/skills/llmem-setup

# OpenCode plugin
ls ~/.config/opencode/plugins/llmem.js

# Claude Code plugin
ls ~/.claude/plugins/llmem/.claude-plugin/plugin.json

# Copilot CLI plugin
ls ~/.copilot/installed-plugins/_direct/llmem/.claude-plugin/plugin.json
```

Run the bundled tests:

```bash
npm test
```