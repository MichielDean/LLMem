# LLMem Security

Security measures, validation, and hardening in LLMem. [Back to README](../README.md)

## Security

- `LMEM_HOME` is validated against path traversal, system directories, and symlink attacks.
- Write paths are validated against system directories and symbolic links. `is_symlink()` checks are wrapped in `try/except OSError` to handle inaccessible paths gracefully.
- System directory blocking uses a shared `_BLOCKED_PATH_PREFIXES` tuple (with prefix + `/` matching to avoid false positives) across `_validate_home_path`, `_validate_write_path`, `OpenCodeAdapter.__init__`, and `CopilotAdapter.__init__` to prevent DRY violations.
- `validate_session_id()` rejects session IDs containing `/`, `\`, or `..` to prevent path traversal when constructing context file paths.
- **CLI path validation**: `llmem add --file`, `llmem import`, and `llmem export --output` block access to protected system directories (e.g. `/etc`, `/bin`, `/usr/bin`, `/sbin`, `/usr/sbin`, `/dev`, `/proc`, `/sys`, `/var`, `/boot`, `/root`). These checks use prefix + `/` matching to avoid false positives (e.g. `/binary_search` is not blocked as `/bin`).
- **Import file size limit**: `llmem import` rejects files larger than 10 MiB.
- URL validation (`is_safe_url`) blocks private/reserved IPs and SSRF vectors, including percent-encoded IP hostnames (e.g. `%31%32%37%2e%30%2e%30%2e%31` is decoded before IP checks). When `allow_remote=False` (the default), only loopback addresses on the Ollama default port are permitted — all other IPs including public addresses are rejected. `safe_urlopen` enforces URL validation, blocks all redirects (via `_NoRedirectHandler`), mitigates DNS rebinding by re-resolving the hostname immediately before the request, strips credentials from error messages, and requires an explicit `allow_remote` parameter (defaults to `False`) for non-loopback addresses. It accepts both string URLs and `urllib.request.Request` objects, and applies a default 30-second timeout to prevent indefinite hangs.
- OpenCode session extraction validates the database path: rejects path traversal (`..`), system directories, symlinks, and URI injection (`?` and `#` characters). `get_opencode_db_path()` validates via `_validate_home_path` before returning.
- CopilotAdapter validates `state_dir` via `_validate_home_path` to reject path traversal and system directory attacks. `share_dir` is resolved but not validated (it points to user-controlled output).
- API keys are masked in `__repr__` on provider instances (`***masked***`).
- API keys are refused over plain HTTP to non-loopback hosts. `OpenAIProvider` and `AnthropicProvider` raise `ValueError` if `base_url` is `http://` and the hostname is not a loopback address (checked via exact string match and `ipaddress` for IPv6-mapped addresses like `::ffff:127.0.0.1`). Substring matches like `localhost.evil.com` are blocked.
- A warning is logged when API keys are sent to a non-default base URL to alert the user of potential credential exfiltration risk.
- Validation error messages use `_strip_credentials()` to remove userinfo from URLs — never embed user-supplied URL credentials in error messages or logs.
- `_strip_credentials()` is used consistently across `is_safe_url()`, `safe_urlopen()`, provider error messages, and config URL validation to prevent credential leaking.
- Embedding and generation inputs are validated against size limits: `MAX_TEXT_LENGTH` (100,000 characters per text) and `MAX_BATCH_SIZE` (2,048 texts per batch) to prevent OOM and resource exhaustion.
- Embedding dimension validation in `MemoryStore.add()` rejects vectors whose dimension doesn't match `vec_dimensions`, preventing dimension mismatch bugs from silently corrupting the vector index.
- All SQL queries use parameterized statements (no injection risk).
- **Code reference path validation**: `validate_code_ref_path()` rejects absolute paths (leading `/`) and directory traversal (`..`) in code ref target_ids. `resolve_code_ref()` enforces an `allowed_paths` allowlist (defaulting to `[Path.cwd()]`) and blocks resolved paths targeting system directories (`/etc`, `/var`, etc.). Code refs must use the relative format `path:start_line:end_line`.
- `add_relation()` validates code ref `target_id` paths at insertion time — unsafe paths are rejected with `ValueError`.
- SQLite extension loading is disabled immediately after `sqlite-vec` loads, preventing runtime loading of arbitrary shared libraries.
- Database files are created with `umask(0o177)` before creation, then `chmod(0o600)` applied to the DB file and its WAL/SHM sidecars (prevents a race window where sensitive memory content is world-readable on multi-user systems). Parent directories use `0o700`.
- `config.yaml` is written with `0o600` file permissions (owner-only read/write) to protect API keys and secrets from other users on shared systems.
- **Server auth token strength**: `server.auth_token` in `config.yaml` must be at least 16 characters. Short tokens are rejected with a hint to generate a strong token.
- `import_memories()` validates entry IDs (string, max 256 chars), embeddings (bytes, max 1 MB), and confidence (numeric) before insertion. Invalid entries are skipped with warnings rather than crashing.
- `export_all()` defaults to a limit of 10,000 memories to prevent unbounded memory consumption; pass `limit=None` to export all.
- `_search_by_embedding_brute()` uses a `LIMIT` clause (10,000 rows max) to prevent OOM on large databases.
- **Embedding metrics computation capping**: `get_embeddings_with_types(limit=)` applies a SQL `LIMIT` clause (default: 10,000) and `compute_metrics(max_embeddings=10000)` truncates input vectors. These caps prevent O(n²) pairwise metrics computations from causing OOM or CPU hangs on large stores.
- `process_transcript()` enforces the same size limit as `process_file()` to prevent OOM from large session transcripts.
- OpenCode tool invocations (`_llmem.ts`) prepend `--` before user arguments to prevent argparse flag injection.
- JavaScript hooks use `execFileSync` (not shell-based `execSync`) and `validateSessionId()` for path traversal protection, with `canSpawnProcess()` rate limiting and `MAX_CONCURRENT=3` process cap to prevent resource exhaustion.
- Prototype pollution protection in `_parseSimpleYaml`: keys `__proto__`, `constructor`, and `prototype` are filtered from parsed YAML to prevent Object prototype mutation.
- ProviderDetector.detect() only returns `provider` and `ollama_url` — no API key presence is exposed.
- Migration from `~/.lobsterdog/` skips symlinks (using `follow_symlinks=False`).

**Code indexing security:**

- `walk_code_files()` skips all symlinks (both file and directory) to prevent path traversal and data exposure.
- Default file size limit of 1 MiB (`--max-file-size`) prevents memory exhaustion from large files.
- Default directory depth limit of 50 (`--max-depth`) prevents stack overflow from deeply nested trees.
- Credential files are excluded from indexing: `.env`, `.env.*` variants, `.pem`, `.key`, SSH private keys (`id_rsa`, `id_dsa`, `id_ed25519`, `id_ecdsa`), `.netrc`, `.htpasswd`, `.npmrc`, `.pypirc`.
- `.gitignore` patterns are respected at every directory level, with correct handling of anchored patterns (leading `/`), negation patterns (`!`), and directory-only patterns (trailing `/`).

## Go Implementation Security

The Go implementation (`internal/store`) shares the same security posture as Python with some implementation-specific differences:

- **Database file permissions**: Parent directory created with `0700` (owner-only). On Unix (Linux/macOS), `syscall.Umask(0o177)` is set before DB file creation so the file is created with mode `0600` from the start — no TOCTOU window. On non-Unix platforms (Windows), the umask is a no-op and the `0700` parent directory serves as the primary defense. After creation, `chmodDBFiles()` applies `0600` to the DB file and its WAL/SHM sidecars as an additional layer.
- **SQL injection**: All queries use parameterized `?` placeholders via Go's `database/sql` package. The `placeholders()` function generates comma-separated `?` sequences for `IN` clauses. FTS5 MATCH queries are sanitized via `sanitizeFTSQuery()`. LIKE queries use `ESCAPE '\'` with `escapeLike()`.
- **Type validation**: Memory type names must match `^[a-z][a-z0-9_]*$` and be ≤64 characters (enforced by `RegisterMemoryType()`). Relation types must be in the `ValidRelationTypes()` set (`supersedes`, `related_to`, `derived_from`).
- **Embedding dimension validation**: `Add()` rejects embeddings whose byte length doesn't match `vec_dimensions × 4`. `ImportMemories()` skips entries with mismatched embedding dimensions.
- **Embedding size limit (DoS protection)**: `Add()`, `Update()`, and `ImportMemories()` enforce a 1 MB per-embedding limit (`maxEmbeddingBytes = 1048576`). `Add()` and `Update()` return errors for oversized embeddings. `ImportMemories()` skips oversized entries with a `slog.Warn` log message.
- **ID length cap**: `ImportMemories()` rejects IDs longer than 256 characters.
- **Import validation**: `ImportMemories()` validates that each entry has non-empty `Type` and `Content` string fields, `Confidence` is in [0, 1], and embedding dimensions match. Invalid entries are skipped with `slog.Warn` — no panics.
- **Traversal depth cap**: `TraverseRelations()` caps `maxDepth` at 5 (hard limit matching Python).
- **Brute-force limit**: `searchByEmbeddingBrute()` uses a `LIMIT` clause (default: 10000 rows) to prevent OOM on large databases.
- **Export limit**: `ExportAll()` defaults to 10000; pass `0` for no limit.
- **Vec dimension mismatch**: `initVecTable()` verifies that an existing `memories_vec` table matches the configured `vec_dimensions`. Mismatch returns an error.
- **FTS fallback**: If FTS5 search fails (e.g., malformed query), `Search()` silently falls back to LIKE search rather than returning an error.
- **Context support**: All public methods accept `context.Context` for cancellation and timeout, following Go best practices.
- **Error wrapping**: All errors are wrapped with the `"llmem: store: "` prefix using `fmtErr()`, providing domain context for debugging.

### Go URL Validation (internal/urlvalidate)

The `internal/urlvalidate` package mirrors the Python `url_validate` module's SSRF protections with Go-specific implementation details:

- **Private IP blocking**: `IsSafeURL` rejects private, link-local, multicast, and unspecified IPs. Loopback is only allowed on the Ollama default port (11434) when `allowRemote=false`.
- **Percent-decode bypass prevention**: Hostnames are percent-decoded via `url.PathUnescape` before IP and DNS checks, preventing SSRF bypass via encoded IP addresses (e.g. `%31%32%37%2e%30%2e%30%2e%31` → `127.0.0.1`).
- **Redirect blocking**: `SafeURLOpen` uses a custom `noRedirectTransport` that blocks all HTTP redirects (3xx responses). Redirect source and target URLs are logged via `slog.Warn` with credentials stripped.
- **DNS rebinding mitigation**: `SafeURLOpen` re-resolves the hostname immediately before the HTTP request to detect DNS rebinding TOCTOU attacks.
- **Credential stripping**: `stripCredentials()` removes userinfo from URLs in error messages and logs, preserving query strings and fragments.
- **Fail-closed defaults**: `IsSafeURL` with `allowRemote=false` only permits loopback addresses on the Ollama default port. `IsRemoteAllowed` returns `false` for hostnames that fail DNS resolution.
- **Local hostname recognition**: `IsRemoteAllowed` recognizes `localhost`, `localhost.localdomain`, and `localhost6` as local hostnames (not remote), consistent with Python's `_is_loopback_hostname()`.
- **EmbeddingEngine URL validation**: `NewEmbeddingEngine` validates BaseURL via `ValidateBaseURL` when creating a production HTTP client. Test clients (with `HTTPClient` provided) skip URL validation since the caller controls the transport.

### Go Embedding Engine (internal/embed)

- **LRU cache safety**: Cache access is protected by `sync.RWMutex`. Cache hits return a defensive copy to prevent mutation. Cache writes use exclusive lock.
- **Ollama availability check**: `CheckAvailable` returns `false` on any error (logs at Debug level). Never panics. Matches exact model name or model name with tag suffix (e.g. `"nomic-embed-text:latest"`), not prefix matching.
- **Timeout protection**: Production HTTP clients use a configurable timeout (default 30s). Context cancellation is respected in `Embed` and `CheckAvailable`.

### Go Metrics (internal/metrics)

- **Computation capping**: `ComputeMetrics` caps embedding count at `MetricsMaxEmbeddings` (10000) to prevent O(n²) CPU hangs. Labels are truncated to match. Logs a warning when capping occurs.
- **Edge case safety**: `Anisotropy` and `SimilarityRange` return 0.0 for empty/single-vector input. `DiscriminationGap` returns an error on label/embedding length mismatch.