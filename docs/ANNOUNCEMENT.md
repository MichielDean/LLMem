# Announcing LLMem — Structured Memory with Semantic Search for LLM Agents

We're excited to announce LLMem, an open-source SQLite-backed memory store designed for LLM agents. LLMem gives your agents persistent, searchable memory that goes far beyond simple key-value storage — combining full-text search (FTS5), optional vector similarity search, an extensible type system, and a provider abstraction layer for LLM embeddings and text generation.

The provider abstraction supports Ollama, OpenAI, Anthropic, and local sentence-transformers models, with graceful fallback so your agent always works even when individual providers are unavailable. A working memory inbox with attention scoring lets agents stage ephemeral observations before promoting them to long-term storage.

Getting started is simple — clone and build:

```bash
git clone https://github.com/MichielDean/LLMem.git
cd LLMem && make build && make install
npm install   # deploys skills + plugins for your platform
llmem init
```

LLMem also integrates with Claude Code and GitHub Copilot CLI via a shared plugin that injects memory context at session start. See the [Integrations docs](INTEGRATIONS.md) for platform-specific setup.

Check out the [GitHub repository](https://github.com/MichielDean/LLMem) for full documentation, the [CONTRIBUTING.md](https://github.com/MichielDean/LLMem/blob/main/CONTRIBUTING.md) guide for how to get involved, and the [ecosystem page](https://github.com/MichielDean/LLMem/blob/main/docs/ECOSYSTEM.md) for OpenCode integration details. We welcome contributions — whether it's bug fixes, new features, or documentation improvements.
