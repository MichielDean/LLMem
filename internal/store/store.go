package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/google/uuid"
	"github.com/viant/sqlite-vec/vec"
)

const (
	defaultVecDimensions     = 768
	defaultBruteForceMaxRows = 10000
	defaultConfidence        = 0.8
	defaultExportLimit       = 10000
	maxIDLength              = 256
	maxEmbeddingBytes        = 1024 * 1024 // 1 MB per embedding vector
)

// ErrUnregisteredType indicates an Add was attempted with a memory type not
// registered via RegisterMemoryType. Use errors.Is to detect it; the returned
// error additionally names the offending type.
var ErrUnregisteredType = errors.New("unregistered type")

// MemoryStore is a SQLite-backed memory store with FTS5 full-text search
// and vector search via sqlite-vec.
type MemoryStore struct {
	dbPath          string
	vecDimensions   int
	vecAvailable    bool
	registeredTypes map[string]struct{}
	db              *sql.DB
	mu              sync.RWMutex
	closed          bool
}

// NewMemoryStore creates and initializes a MemoryStore.
// It opens the database, applies migrations, and optionally sets up vec0.
// If cfg.DBPath is empty, defaults to ~/.config/llmem/memory.db.
// If cfg.VecDimensions is 0, defaults to 768.
// The constructor leaves the store in a fully usable state.
func NewMemoryStore(cfg StoreConfig) (*MemoryStore, error) {
	if cfg.VecDimensions < 0 {
		return nil, fmtErr("vec_dimensions must be non-negative, got %d", cfg.VecDimensions)
	}
	if cfg.VecDimensions == 0 {
		cfg.VecDimensions = defaultVecDimensions
	}

	dbPath := cfg.DBPath
	if dbPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmtErr("default db path: %w", err)
		}
		dbPath = filepath.Join(homeDir, ".config", "llmem", "memory.db")
	}

	// Create parent directory with 0700 permissions
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmtErr("create db directory: %w", err)
		}
	}

	// Build registered types set with defensive copy
	registeredTypes := make(map[string]struct{})
	types := cfg.RegisteredTypes
	if len(types) == 0 {
		types = DefaultRegisteredTypes()
	}
	for _, t := range types {
		registeredTypes[t] = struct{}{}
	}

	// Set restrictive umask (0o177) before creating the DB file so it
	// is created with owner-only permissions (0600). On Unix this uses
	// syscall.Umask; on other platforms it's a no-op and the 0700 parent
	// directory serves as the primary access control.
	var oldMask int
	if dbPath != ":memory:" {
		oldMask = setUmask(0o177)
	}

	db, err := openDB(dbPath)
	if err != nil {
		if dbPath != ":memory:" {
			resetUmask(oldMask)
		}
		return nil, fmtErr("open db: %w", err)
	}

	if dbPath != ":memory:" {
		resetUmask(oldMask)
	}

	ms := &MemoryStore{
		dbPath:          dbPath,
		vecDimensions:   cfg.VecDimensions,
		vecAvailable:    false,
		registeredTypes: registeredTypes,
		db:              db,
	}

	// Initialize vector search: register the vec module early so we can
	// drop stale vec0 virtual tables (they require the vec0 module for DROP
	// TABLE, which isn't available — we need writable_schema instead,
	// but having vec registered helps with cleanup).
	vecRegistered := false
	if !cfg.DisableVec {
		if err := vec.Register(db); err != nil {
			slog.Warn("llmem: store: failed to register sqlite-vec module, vector search disabled", "error", err)
		} else {
			vecRegistered = true
			// vec.Register adds the module to the driver's global registry, but it
			// only takes effect on NEW connections. The openDB PRAGMAs (WAL mode,
			// foreign keys) already created a warm connection in the pool that lacks
			// the vec module. Close and reopen the DB so all pool connections pick up
			// the module registration.
			if dbPath != ":memory:" {
				if err := db.Close(); err != nil {
					return nil, fmtErr("close db after vec register: %w", err)
				}
				db, err = openDB(dbPath)
				if err != nil {
					return nil, fmtErr("reopen db after vec register: %w", err)
				}
				ms.db = db
			}
		}
	}

	// Drop legacy vec0 virtual tables, triggers, and auxiliary tables
	// from earlier Python LLMem or C extension versions.
	if err := dropLegacyVec0Objects(db); err != nil {
		slog.Warn("llmem: store: failed to drop legacy vec0 objects before migration", "error", err)
	}

	// Run migrations
	if err := runMigrationsFromDB(db); err != nil {
		db.Close()
		return nil, fmtErr("run migrations: %w", err)
	}

	// Fix FTS index if empty but memories exist
	if err := ms.rebuildFTSIfEmpty(); err != nil {
		slog.Warn("llmem: store: failed to rebuild FTS index", "error", err)
	}

	// Create the vec virtual table now that migrations are done and the
	// stale vec0 table has been cleaned up.
	if vecRegistered {
		if err := ms.initVecTable(); err != nil {
			slog.Warn("llmem: store: failed to initialize vec virtual table, vector search disabled", "error", err)
		} else {
			ms.vecAvailable = true
		}
	}

	// Chmod DB files for security
	if dbPath != ":memory:" {
		if err := chmodDBFiles(dbPath); err != nil {
			slog.Warn("llmem: store: failed to set db file permissions", "error", err)
		}
	}

	return ms, nil
}

// DB returns the underlying database connection for advanced queries.
// Most callers should use the higher-level methods instead.
func (ms *MemoryStore) DB() *sql.DB {
	return ms.db
}

// Close closes the database connection. Safe to call multiple times.
func (ms *MemoryStore) Close() error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.closed {
		return nil
	}
	ms.closed = true
	return ms.db.Close()
}

// RegisterMemoryType adds a custom memory type to the instance's type registry.
// The type name must match ^[a-z][a-z0-9_]*$ and be at most 64 characters.
// Returns an error for duplicate registration or invalid names.
func (ms *MemoryStore) RegisterMemoryType(typeName string) error {
	if !isValidTypeName(typeName) {
		return fmtErr("register_memory_type: invalid type name %q: must match ^[a-z][a-z0-9_]*$ and be at most 64 chars", typeName)
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if _, exists := ms.registeredTypes[typeName]; exists {
		return fmtErr("register_memory_type: type %q is already registered", typeName)
	}
	ms.registeredTypes[typeName] = struct{}{}
	return nil
}

// Add creates a new memory and returns its ID.
// Returns an error if the type is not registered or embedding dimensions don't match.
func (ms *MemoryStore) Add(ctx context.Context, params AddParams) (string, error) {
	ms.mu.RLock()
	_, typeOK := ms.registeredTypes[params.Type]
	ms.mu.RUnlock()
	if !typeOK {
		return "", fmtErr("add: %w %q: register it with RegisterMemoryType first", ErrUnregisteredType, params.Type)
	}

	// Validate embedding size limit (DoS protection)
	if len(params.Embedding) > maxEmbeddingBytes {
		return "", fmtErr("add: embedding size %d bytes exceeds maximum %d bytes", len(params.Embedding), maxEmbeddingBytes)
	}

	// Validate embedding dimensions only when vec is available.
	// Without vec, embeddings are stored as opaque blobs and dimension
	// doesn't matter for correctness — only the vec index requires matching dims.
	if len(params.Embedding) > 0 && ms.vecAvailable {
		actualDim := len(params.Embedding) / 4
		if actualDim != ms.vecDimensions {
			return "", fmtErr("add: embedding dimension %d does not match vec_dimensions %d — use %d-dimensional embeddings", actualDim, ms.vecDimensions, ms.vecDimensions)
		}
	}

	memID := params.ID
	if memID == "" {
		memID = uuid.New().String()
	}

	now := nowUTC()
	confidence := params.Confidence
	if confidence == 0 {
		confidence = defaultConfidence
	}

	hintsJSON, err := json.Marshal(params.Hints)
	if err != nil {
		return "", fmtErr("add: marshal hints: %w", err)
	}
	if string(hintsJSON) == "null" {
		hintsJSON = []byte("[]")
	}

	metadataJSON, err := json.Marshal(params.Metadata)
	if err != nil {
		return "", fmtErr("add: marshal metadata: %w", err)
	}
	if string(metadataJSON) == "null" {
		metadataJSON = []byte("{}")
	}

	source := params.Source
	if source == "" {
		source = "manual"
	}

	// Convert empty strings to nil for nullable columns
	var validUntil any
	if params.ValidUntil != "" {
		validUntil = params.ValidUntil
	} else {
		// Auto-compute TTL for new tier types if not explicitly set
		ttlDays := DefaultTTLForType(params.Type)
		if ttlDays > 0 {
			expiry := time.Now().UTC().AddDate(0, 0, ttlDays).Format(time.RFC3339)
			validUntil = expiry
		}
	}
	var summary any
	if params.Summary != "" {
		summary = params.Summary
	}
	var embedding any
	if params.Embedding != nil {
		embedding = params.Embedding
	}

	_, err = ms.db.ExecContext(ctx,
		`INSERT INTO "memories" ("id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "metadata", "embedding", "pinned") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
		memID, params.Type, params.Content, summary, string(hintsJSON), source, confidence, now, validUntil, now, now, string(metadataJSON), embedding,
	)
	if err != nil {
		return "", fmtErr("add: insert memory: %w", err)
	}

	// Sync embedding to vec shadow table
	if params.Embedding != nil && ms.vecAvailable {
		if _, syncErr := ms.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO "_vec_memories_vec"("dataset_id", "id", "content", "meta", "embedding") VALUES ('memories', ?, ?, ?, ?)`,
			memID, params.Content, string(metadataJSON), params.Embedding,
		); syncErr != nil {
			slog.Debug("llmem: store: add: failed to sync embedding to vec shadow", "id", memID, "error", syncErr)
		}
	}

	return memID, nil
}

// Get retrieves a memory by ID. Returns nil if not found.
// If trackAccess is true, increments access_count and updates accessed_at.
func (ms *MemoryStore) Get(ctx context.Context, id string, trackAccess bool) (*Memory, error) {
	row := ms.db.QueryRowContext(ctx,
		`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" WHERE "id" = ?`,
		id,
	)
	m, err := scanMemory(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmtErr("get: query: %w", err)
	}

	if trackAccess {
		now := nowUTC()
		stableExpiry := time.Now().UTC().AddDate(0, 0, TTLStableDays).Format(time.RFC3339)
		volatileExpiry := time.Now().UTC().AddDate(0, 0, TTLVolatileDays).Format(time.RFC3339)
		_, err = ms.db.ExecContext(ctx,
			`UPDATE "memories" SET "accessed_at" = ?, "valid_until" = CASE WHEN "type" = 'stable' AND "pinned" = 0 THEN ? WHEN "type" = 'volatile' AND "pinned" = 0 THEN ? ELSE "valid_until" END, "access_count" = "access_count" + 1 WHERE "id" = ?`,
			now, stableExpiry, volatileExpiry, id,
		)
		if err != nil {
			slog.Debug("llmem: store: get: failed to update access count", "error", err)
		} else {
			m.AccessCount++
			m.AccessedAt = now
		}
	}

	return m, nil
}

// GetBatch retrieves multiple memories by their IDs.
// Returns a map keyed by memory ID. Returns empty map for empty input.
// If validOnly is true, only returns memories with valid_until IS NULL.
func (ms *MemoryStore) GetBatch(ctx context.Context, ids []string, validOnly bool) (map[string]*Memory, error) {
	if len(ids) == 0 {
		return map[string]*Memory{}, nil
	}

	ph := placeholders(len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	query := fmt.Sprintf(`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" WHERE "id" IN (%s)`, ph)
	if validOnly {
		query += ` AND ("valid_until" IS NULL OR "valid_until" > datetime('now'))`
	}

	rows, err := ms.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmtErr("get_batch: query: %w", err)
	}
	defer rows.Close()

	result := make(map[string]*Memory)
	for rows.Next() {
		m, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmtErr("get_batch: scan: %w", err)
		}
		result[m.ID] = m
	}
	return result, rows.Err()
}

// Update modifies a memory's fields. Returns false if not found, true on success.
// Rejects ClearEmbedding=true AND Embedding != nil (conflict).
func (ms *MemoryStore) Update(ctx context.Context, params UpdateParams) (bool, error) {
	if params.ClearEmbedding && params.Embedding != nil {
		return false, fmtErr("update: cannot specify both embedding and clear_embedding=true")
	}

	// Validate embedding size limit (DoS protection)
	if len(params.Embedding) > maxEmbeddingBytes {
		return false, fmtErr("update: embedding size %d bytes exceeds maximum %d bytes", len(params.Embedding), maxEmbeddingBytes)
	}

	// Check existence
	var exists bool
	err := ms.db.QueryRowContext(ctx, `SELECT 1 FROM "memories" WHERE "id" = ?`, params.ID).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmtErr("update: check existence: %w", err)
	}
	if !exists {
		return false, nil
	}

	now := nowUTC()
	sets := []string{`"updated_at" = ?`}
	vals := []any{now}

	if params.Content != nil {
		sets = append(sets, `"content" = ?`)
		vals = append(vals, *params.Content)
		// Clear embedding when content changes unless explicitly providing new embedding
		if !params.ClearEmbedding && params.Embedding == nil {
			sets = append(sets, `"embedding" = NULL`)
		}
	}
	if params.Summary != nil {
		sets = append(sets, `"summary" = ?`)
		vals = append(vals, *params.Summary)
	}
	if params.Confidence != nil {
		sets = append(sets, `"confidence" = ?`)
		vals = append(vals, *params.Confidence)
	}
	if params.ValidUntil != nil {
		sets = append(sets, `"valid_until" = ?`)
		vals = append(vals, *params.ValidUntil)
	}
	if params.Metadata != nil {
		metadataJSON, err := json.Marshal(params.Metadata)
		if err != nil {
			return false, fmtErr("update: marshal metadata: %w", err)
		}
		sets = append(sets, `"metadata" = ?`)
		vals = append(vals, string(metadataJSON))
	}
	if params.Hints != nil {
		hintsJSON, err := json.Marshal(params.Hints)
		if err != nil {
			return false, fmtErr("update: marshal hints: %w", err)
		}
		sets = append(sets, `"hints" = ?`)
		vals = append(vals, string(hintsJSON))
	}
	if params.ClearEmbedding {
		sets = append(sets, `"embedding" = NULL`)
	} else if params.Embedding != nil {
		sets = append(sets, `"embedding" = ?`)
		vals = append(vals, params.Embedding)
	}

	vals = append(vals, params.ID)
	query := fmt.Sprintf(`UPDATE "memories" SET %s WHERE "id" = ?`, strings.Join(sets, ", "))
	result, err := ms.db.ExecContext(ctx, query, vals...)
	if err != nil {
		return false, fmtErr("update: %w", err)
	}
	n, _ := result.RowsAffected()

	// Sync embedding changes to vec shadow table
	if n > 0 && ms.vecAvailable {
		if params.ClearEmbedding {
			// Embedding was explicitly cleared — remove from shadow table
			ms.db.ExecContext(ctx, `DELETE FROM "_vec_memories_vec" WHERE dataset_id = 'memories' AND id = ?`, params.ID)
		} else if params.Embedding != nil {
			// Embedding was updated — upsert with new embedding.
			// Content is available if Content pointer is non-nil.
			content := ""
			if params.Content != nil {
				content = *params.Content
			}
			ms.db.ExecContext(ctx, `INSERT OR REPLACE INTO "_vec_memories_vec"("dataset_id", "id", "content", "meta", "embedding") VALUES ('memories', ?, ?, '', ?)`, params.ID, content, params.Embedding)
		} else if params.Content != nil {
			// Content changed without embedding change — update content in shadow table,
			// preserve the existing embedding
			ms.db.ExecContext(ctx, `UPDATE "_vec_memories_vec" SET content = ? WHERE dataset_id = 'memories' AND id = ?`, *params.Content, params.ID)
		}
	}

	return n > 0, nil
}

// Invalidate sets valid_until on a memory, effectively expiring it.
// Also clears the embedding and updates metadata with invalidation_reason.
// Returns false if not found.
func (ms *MemoryStore) Invalidate(ctx context.Context, id string, reason string) (bool, error) {
	// Fetch current metadata
	var metadataStr string
	err := ms.db.QueryRowContext(ctx, `SELECT "metadata" FROM "memories" WHERE "id" = ?`, id).Scan(&metadataStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmtErr("invalidate: fetch metadata: %w", err)
	}

	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
		metadata = map[string]any{}
	}
	if reason != "" {
		metadata["invalidation_reason"] = reason
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return false, fmtErr("invalidate: marshal metadata: %w", err)
	}

	now := nowUTC()
	result, err := ms.db.ExecContext(ctx,
		`UPDATE "memories" SET "valid_until" = ?, "metadata" = ?, "embedding" = NULL, "updated_at" = ? WHERE "id" = ?`,
		now, string(metadataJSON), now, id,
	)
	if err != nil {
		return false, fmtErr("invalidate: %w", err)
	}
	n, _ := result.RowsAffected()

	// Remove from vec shadow table since embedding is cleared
	if n > 0 && ms.vecAvailable {
		ms.db.ExecContext(ctx, `DELETE FROM "_vec_memories_vec" WHERE dataset_id = 'memories' AND id = ?`, id)
	}

	return n > 0, nil
}

// Pin marks a stable memory as pinned, exempting it from TTL-based pruning.
// Sets pinned=true and valid_until=NULL. Returns false if not found.
// Returns an error if the memory is not of type "stable".
func (ms *MemoryStore) Pin(ctx context.Context, id string) (bool, error) {
	var typ string
	err := ms.db.QueryRowContext(ctx, `SELECT "type" FROM "memories" WHERE "id" = ?`, id).Scan(&typ)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmtErr("pin: check type: %w", err)
	}
	if typ != "stable" {
		return false, fmtErr("pin: only stable memories can be pinned, got type %q", typ)
	}

	now := nowUTC()
	result, err := ms.db.ExecContext(ctx,
		`UPDATE "memories" SET "pinned" = 1, "valid_until" = NULL, "updated_at" = ? WHERE "id" = ?`,
		now, id,
	)
	if err != nil {
		return false, fmtErr("pin: %w", err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// Unpin removes the pinned status from a memory and recomputes its TTL.
// Sets pinned=false and valid_until = accessed_at + TTL (or created_at + TTL if never accessed).
// Returns false if not found. Only works on stable memories.
func (ms *MemoryStore) Unpin(ctx context.Context, id string) (bool, error) {
	var typ string
	var accessedAt, createdAt sql.NullString
	err := ms.db.QueryRowContext(ctx, `SELECT "type", "accessed_at", "created_at" FROM "memories" WHERE "id" = ?`, id).Scan(&typ, &accessedAt, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmtErr("unpin: check: %w", err)
	}
	if typ != "stable" {
		return false, fmtErr("unpin: only stable memories can be unpinned, got type %q", typ)
	}

	// Compute new valid_until from accessed_at (or created_at if never accessed) + TTL
	baseTime := accessedAt.String
	if baseTime == "" {
		baseTime = createdAt.String
	}

	ttlDays := DefaultTTLForType(typ)
	var validUntil any
	if ttlDays > 0 && baseTime != "" {
		base, parseErr := time.Parse(time.RFC3339, baseTime)
		if parseErr != nil {
			slog.Debug("llmem: store: unpin: failed to parse base time, leaving valid_until NULL", "id", id, "error", parseErr)
		} else {
			validUntil = base.UTC().AddDate(0, 0, ttlDays).Format(time.RFC3339)
		}
	}

	now := nowUTC()
	result, err := ms.db.ExecContext(ctx,
		`UPDATE "memories" SET "pinned" = 0, "valid_until" = ?, "updated_at" = ? WHERE "id" = ?`,
		validUntil, now, id,
	)
	if err != nil {
		return false, fmtErr("unpin: %w", err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// PruneExpired deletes all non-pinned memories where valid_until is in the past.
// Pinned memories are always preserved. Returns the number of memories deleted.
func (ms *MemoryStore) PruneExpired(ctx context.Context) (int, error) {
	now := nowUTC()
	result, err := ms.db.ExecContext(ctx,
		`DELETE FROM "memories" WHERE "valid_until" IS NOT NULL AND "valid_until" < ? AND "pinned" = 0`,
		now,
	)
	if err != nil {
		return 0, fmtErr("prune_expired: %w", err)
	}
	n, _ := result.RowsAffected()

	// Clean up vec shadow table for deleted memories (best-effort)
	if n > 0 && ms.vecAvailable {
		// The vec shadow table rows are cascaded via the memories table's
		// DELETE trigger, but the vec shadow table is not a real FK table.
		// Clean up manually for safety.
		ms.db.ExecContext(ctx,
			`DELETE FROM "_vec_memories_vec" WHERE dataset_id = 'memories' AND id NOT IN (SELECT "id" FROM "memories")`,
		)
	}

	return int(n), nil
}

// Delete removes a memory by ID and cascades to its relations.
// Returns false if not found.
func (ms *MemoryStore) Delete(ctx context.Context, id string) (bool, error) {
	// Delete relations where target_id matches (no FK cascade on target_id)
	if _, err := ms.db.ExecContext(ctx, `DELETE FROM "relations" WHERE "target_id" = ?`, id); err != nil {
		return false, fmtErr("delete: cleanup target relations: %w", err)
	}

	result, err := ms.db.ExecContext(ctx, `DELETE FROM "memories" WHERE "id" = ?`, id)
	if err != nil {
		return false, fmtErr("delete: %w", err)
	}
	n, _ := result.RowsAffected()

	// Remove from vec shadow table
	if n > 0 && ms.vecAvailable {
		ms.db.ExecContext(ctx, `DELETE FROM "_vec_memories_vec" WHERE dataset_id = 'memories' AND id = ?`, id)
	}

	return n > 0, nil
}

// Search performs FTS5 full-text search with fallback to LIKE.
// Returns memories ranked by BM25 (FTS) or updated_at DESC (LIKE).
func (ms *MemoryStore) Search(ctx context.Context, params SearchParams) ([]*Memory, error) {
	if params.SemanticOnly && params.FTSOnly {
		return nil, fmtErr("search: cannot specify both fts-only and semantic-only")
	}
	if params.SemanticOnly && !ms.vecAvailable {
		return nil, fmtErr("search: semantic search requires vector index (store opened with DisableVec=false and sqlite-vec available)")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	if params.Query != "" {
		return ms.searchFTS(ctx, params, limit, offset)
	}
	return ms.searchNoQuery(ctx, params, limit, offset)
}

func (ms *MemoryStore) searchFTS(ctx context.Context, params SearchParams, limit, offset int) ([]*Memory, error) {
	ftsQuery := sanitizeFTSQuery(params.Query)

	var clauses []string
	var ftsVals []any
	if params.ValidOnly {
		clauses = append(clauses, `(m."valid_until" IS NULL OR m."valid_until" > datetime('now'))`)
	}
	if params.Type != "" {
		clauses = append(clauses, `m."type" = ?`)
		ftsVals = append(ftsVals, params.Type)
	}
	where := ""
	if len(clauses) > 0 {
		where = " AND " + strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT m."id", m."type", m."content", m."summary", m."hints", m."source", m."confidence", m."valid_from", m."valid_until", m."created_at", m."updated_at", m."accessed_at", m."access_count", m."metadata", m."embedding", m."pinned" FROM "memories_fts" AS fts JOIN "memories" AS m ON m."rowid" = fts."rowid" WHERE "memories_fts" MATCH ?%s ORDER BY rank DESC LIMIT ? OFFSET ?`,
		where,
	)
	args := append([]any{ftsQuery}, ftsVals...)
	args = append(args, limit, offset)

	rows, err := ms.db.QueryContext(ctx, query, args...)
	if err != nil {
		// Fallback to LIKE search
		return ms.fallbackLikeSearch(ctx, params, limit, offset)
	}
	defer rows.Close()

	var results []*Memory
	for rows.Next() {
		m, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmtErr("search: scan: %w", err)
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

func (ms *MemoryStore) searchNoQuery(ctx context.Context, params SearchParams, limit, offset int) ([]*Memory, error) {
	var clauses []string
	var vals []any
	if params.ValidOnly {
		clauses = append(clauses, `("valid_until" IS NULL OR "valid_until" > datetime('now'))`)
	}
	if params.Type != "" {
		clauses = append(clauses, `"type" = ?`)
		vals = append(vals, params.Type)
	}
	where := "1=1"
	if len(clauses) > 0 {
		where = strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" WHERE %s ORDER BY "updated_at" DESC LIMIT ? OFFSET ?`,
		where,
	)
	vals = append(vals, limit, offset)

	rows, err := ms.db.QueryContext(ctx, query, vals...)
	if err != nil {
		return nil, fmtErr("search: query: %w", err)
	}
	defer rows.Close()

	var results []*Memory
	for rows.Next() {
		m, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmtErr("search: scan: %w", err)
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

func (ms *MemoryStore) fallbackLikeSearch(ctx context.Context, params SearchParams, limit, offset int) ([]*Memory, error) {
	clauses, vals := ms.buildLikeClauses(params.Query, params.Type, params.ValidOnly)

	rows, err := ms.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" AS m WHERE %s ORDER BY m."updated_at" DESC LIMIT ? OFFSET ?`, clauses),
		append(vals, limit, offset)...,
	)
	if err != nil {
		return nil, fmtErr("search: like fallback: %w", err)
	}
	defer rows.Close()

	var results []*Memory
	for rows.Next() {
		m, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmtErr("search: like scan: %w", err)
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

func (ms *MemoryStore) buildLikeClauses(query, typeFilter string, validOnly bool) (string, []any) {
	var clauses []string
	var vals []any

	escaped := escapeLike(query)
	clauses = append(clauses, `(m."content" LIKE ? ESCAPE '\' OR m."summary" LIKE ? ESCAPE '\' OR m."hints" LIKE ? ESCAPE '\')`)
	vals = append(vals, "%"+escaped+"%", "%"+escaped+"%", "%"+escaped+"%")

	if validOnly {
		clauses = append(clauses, `(m."valid_until" IS NULL OR m."valid_until" > datetime('now'))`)
	}
	if typeFilter != "" {
		clauses = append(clauses, `m."type" = ?`)
		vals = append(vals, typeFilter)
	}
	return strings.Join(clauses, " AND "), vals
}

// SearchCount returns the number of matching memories.
func (ms *MemoryStore) SearchCount(ctx context.Context, params SearchCountParams) (int, error) {
	if params.Query != "" {
		return ms.searchCountFTS(ctx, params)
	}
	return ms.searchCountNoQuery(ctx, params)
}

func (ms *MemoryStore) searchCountFTS(ctx context.Context, params SearchCountParams) (int, error) {
	ftsQuery := sanitizeFTSQuery(params.Query)

	var clauses []string
	var ftsVals []any
	if params.ValidOnly {
		clauses = append(clauses, `(m."valid_until" IS NULL OR m."valid_until" > datetime('now'))`)
	}
	if params.Type != "" {
		clauses = append(clauses, `m."type" = ?`)
		ftsVals = append(ftsVals, params.Type)
	}
	where := ""
	if len(clauses) > 0 {
		where = " AND " + strings.Join(clauses, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT COUNT(*) FROM "memories_fts" AS fts JOIN "memories" AS m ON m."rowid" = fts."rowid" WHERE "memories_fts" MATCH ?%s`,
		where,
	)
	args := append([]any{ftsQuery}, ftsVals...)

	var count int
	err := ms.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		// Fallback to LIKE count
		clauses, vals := ms.buildLikeClauses(params.Query, params.Type, params.ValidOnly)
		err = ms.db.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT COUNT(*) FROM "memories" AS m WHERE %s`, clauses),
			vals...,
		).Scan(&count)
		if err != nil {
			return 0, fmtErr("search_count: like fallback: %w", err)
		}
	}
	return count, nil
}

func (ms *MemoryStore) searchCountNoQuery(ctx context.Context, params SearchCountParams) (int, error) {
	var clauses []string
	var vals []any
	if params.ValidOnly {
		clauses = append(clauses, `("valid_until" IS NULL OR "valid_until" > datetime('now'))`)
	}
	if params.Type != "" {
		clauses = append(clauses, `"type" = ?`)
		vals = append(vals, params.Type)
	}
	where := "1=1"
	if len(clauses) > 0 {
		where = strings.Join(clauses, " AND ")
	}

	var count int
	err := ms.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM "memories" WHERE %s`, where),
		vals...,
	).Scan(&count)
	if err != nil {
		return 0, fmtErr("search_count: %w", err)
	}
	return count, nil
}

// SearchByEmbedding searches memories by vector similarity.
// Uses the vec virtual table if available, brute-force fallback otherwise.
// If limit <= 0, defaults to 20.
func (ms *MemoryStore) SearchByEmbedding(ctx context.Context, queryVec []float32, validOnly bool, limit int, threshold float64) ([]*ScoredMemory, error) {
	if limit <= 0 {
		limit = 20
	}
	if ms.vecAvailable {
		return ms.searchByEmbeddingVec(ctx, queryVec, validOnly, limit, threshold)
	}
	return ms.searchByEmbeddingBrute(ctx, queryVec, validOnly, limit, threshold)
}

func (ms *MemoryStore) searchByEmbeddingVec(ctx context.Context, queryVec []float32, validOnly bool, limit int, threshold float64) ([]*ScoredMemory, error) {
	queryBytes := VecToBytes(queryVec)

	// Query the vec virtual table for k-NN search.
	// The vec vtab schema is (dataset_id, doc_id, match_score HIDDEN).
	// MATCH is applied to the doc_id column, which maps to our memory IDs.
	// viant/sqlite-vec returns match_score as cosine similarity (0-1).
	// The shadow table _vec_memories_vec stores (dataset_id, id, content, meta, embedding).
	rows, err := ms.db.QueryContext(ctx,
		`SELECT doc_id, match_score FROM memories_vec WHERE dataset_id = 'memories' AND doc_id MATCH ? AND match_score >= ?`,
		queryBytes, threshold,
	)
	if err != nil {
		slog.Debug("llmem: store: search_by_embedding: vec query failed, falling back", "error", err)
		return ms.searchByEmbeddingBrute(ctx, queryVec, validOnly, limit, threshold)
	}
	defer rows.Close()

	type vecResult struct {
		id    string
		score float64
	}
	var results []vecResult
	for rows.Next() {
		var r vecResult
		if err := rows.Scan(&r.id, &r.score); err != nil {
			return nil, fmtErr("search_by_embedding: vec scan: %w", err)
		}
		if r.score >= threshold {
			results = append(results, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmtErr("search_by_embedding: vec rows: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	// Cap at limit
	if len(results) > limit {
		results = results[:limit]
	}

	// Fetch full memories by ID
	topIDs := make([]any, len(results))
	for i, r := range results {
		topIDs[i] = r.id
	}
	ph := placeholders(len(topIDs))
	var where string
	if validOnly {
		where = ` AND ("valid_until" IS NULL OR "valid_until" > datetime('now'))`
	}
	memRows, err := ms.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" WHERE "id" IN (%s)%s`, ph, where),
		topIDs...,
	)
	if err != nil {
		return nil, fmtErr("search_by_embedding: fetch memories: %w", err)
	}
	defer memRows.Close()

	memMap := make(map[string]*Memory)
	for memRows.Next() {
		m, err := scanMemoryFromRows(memRows)
		if err != nil {
			return nil, fmtErr("search_by_embedding: scan mem: %w", err)
		}
		memMap[m.ID] = m
	}

	var scored []*ScoredMemory
	for _, r := range results {
		if m, ok := memMap[r.id]; ok {
			scored = append(scored, &ScoredMemory{Memory: m, Score: r.score})
		}
	}
	return scored, nil
}

func (ms *MemoryStore) searchByEmbeddingBrute(ctx context.Context, queryVec []float32, validOnly bool, limit int, threshold float64) ([]*ScoredMemory, error) {
	where := `WHERE "embedding" IS NOT NULL`
	args := []any{}
	if validOnly {
		where += ` AND ("valid_until" IS NULL OR "valid_until" > datetime('now'))`
	}
	where += ` LIMIT ?`
	args = append(args, defaultBruteForceMaxRows)

	rows, err := ms.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT "id", "embedding" FROM "memories" %s`, where),
		args...,
	)
	if err != nil {
		return nil, fmtErr("search_by_embedding: brute query: %w", err)
	}
	defer rows.Close()

	type scoredEntry struct {
		id    string
		score float64
	}
	var entries []scoredEntry

	for rows.Next() {
		var id string
		var embBytes []byte
		if err := rows.Scan(&id, &embBytes); err != nil {
			return nil, fmtErr("search_by_embedding: brute scan: %w", err)
		}
		vec := BytesToVec(embBytes)
		if len(vec) != len(queryVec) {
			continue
		}
		score := cosineSimilarity(queryVec, vec)
		if score >= threshold {
			entries = append(entries, scoredEntry{id: id, score: score})
		}
	}
	if rows.Err() != nil {
		return nil, fmtErr("search_by_embedding: brute rows: %w", rows.Err())
	}

	// Sort descending by score
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].score > entries[j].score
	})

	if len(entries) == 0 {
		return nil, nil
	}

	// Cap at limit
	if len(entries) > limit {
		entries = entries[:limit]
	}

	// Fetch full memories
	topIDs := make([]any, len(entries))
	for i, e := range entries {
		topIDs[i] = e.id
	}
	ph := placeholders(len(topIDs))
	memRows, err := ms.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" WHERE "id" IN (%s)`, ph),
		topIDs...,
	)
	if err != nil {
		return nil, fmtErr("search_by_embedding: brute fetch: %w", err)
	}
	defer memRows.Close()

	memMap := make(map[string]*Memory)
	for memRows.Next() {
		m, err := scanMemoryFromRows(memRows)
		if err != nil {
			return nil, fmtErr("search_by_embedding: brute scan mem: %w", err)
		}
		memMap[m.ID] = m
	}

	var results []*ScoredMemory
	for _, e := range entries {
		if m, ok := memMap[e.id]; ok {
			results = append(results, &ScoredMemory{Memory: m, Score: e.score})
		}
	}
	return results, nil
}

// ListAll lists memories with optional type filter. Delegates to Search with no query.
func (ms *MemoryStore) ListAll(ctx context.Context, params ListParams) ([]*Memory, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	return ms.Search(ctx, SearchParams{
		Type:      params.Type,
		ValidOnly: params.ValidOnly,
		Limit:     limit,
	})
}

// Count returns total number of memories, optionally limited to valid only.
func (ms *MemoryStore) Count(ctx context.Context, validOnly bool) (int, error) {
	clause := ""
	if validOnly {
		clause = `WHERE ("valid_until" IS NULL OR "valid_until" > datetime('now'))`
	}
	var count int
	err := ms.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM "memories" %s`, clause),
	).Scan(&count)
	if err != nil {
		return 0, fmtErr("count: %w", err)
	}
	return count, nil
}

// CountByType returns a map of type to count, sorted by count descending.
func (ms *MemoryStore) CountByType(ctx context.Context, validOnly bool) (map[string]int, error) {
	clause := ""
	if validOnly {
		clause = `WHERE ("valid_until" IS NULL OR "valid_until" > datetime('now'))`
	}
	rows, err := ms.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT "type", COUNT(*) as cnt FROM "memories" %s GROUP BY "type" ORDER BY cnt DESC`, clause),
	)
	if err != nil {
		return nil, fmtErr("count_by_type: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var mtype string
		var cnt int
		if err := rows.Scan(&mtype, &cnt); err != nil {
			return nil, fmtErr("count_by_type: scan: %w", err)
		}
		result[mtype] = cnt
	}
	return result, rows.Err()
}

// CountEmbeddings returns the count of valid memories with non-null embeddings.
func (ms *MemoryStore) CountEmbeddings(ctx context.Context) (int, error) {
	var count int
	err := ms.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM "memories" WHERE "embedding" IS NOT NULL AND ("valid_until" IS NULL OR "valid_until" > datetime('now'))`,
	).Scan(&count)
	if err != nil {
		return 0, fmtErr("count_embeddings: %w", err)
	}
	return count, nil
}

// AddRelation adds a relation between two memories.
// Returns the relation UUID. Validates relation_type against allowed types.
func (ms *MemoryStore) AddRelation(ctx context.Context, sourceID, targetID, relationType string) (string, error) {
	if !isValidRelationType(relationType) {
		return "", fmtErr("add_relation: invalid relation_type %q, must be one of %v", relationType, ValidRelationTypes())
	}

	relID := uuid.New().String()
	now := nowUTC()

	_, err := ms.db.ExecContext(ctx,
		`INSERT INTO "relations" ("id", "source_id", "target_id", "relation_type", "created_at") VALUES (?,?,?,?,?)`,
		relID, sourceID, targetID, relationType, now,
	)
	if err != nil {
		return "", fmtErr("add_relation: insert: %w", err)
	}
	return relID, nil
}

// GetRelations returns all relations for a memory (both source and target).
func (ms *MemoryStore) GetRelations(ctx context.Context, memID string) ([]*Relation, error) {
	rows, err := ms.db.QueryContext(ctx,
		`SELECT "id", "source_id", "target_id", "relation_type", "created_at" FROM "relations" WHERE "source_id" = ? OR "target_id" = ?`,
		memID, memID,
	)
	if err != nil {
		return nil, fmtErr("get_relations: %w", err)
	}
	defer rows.Close()

	var relations []*Relation
	for rows.Next() {
		r, err := scanRelation(rows)
		if err != nil {
			return nil, fmtErr("get_relations: scan: %w", err)
		}
		relations = append(relations, r)
	}
	return relations, rows.Err()
}

// GetRelationsBatch retrieves relations for multiple memory IDs.
func (ms *MemoryStore) GetRelationsBatch(ctx context.Context, memIDs []string) ([]*Relation, error) {
	if len(memIDs) == 0 {
		return []*Relation{}, nil
	}
	ph := placeholders(len(memIDs))
	args := make([]any, len(memIDs)*2)
	for i, id := range memIDs {
		args[i] = id
		args[len(memIDs)+i] = id
	}

	rows, err := ms.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT "id", "source_id", "target_id", "relation_type", "created_at" FROM "relations" WHERE "source_id" IN (%s) OR "target_id" IN (%s)`, ph, ph),
		args...,
	)
	if err != nil {
		return nil, fmtErr("get_relations_batch: %w", err)
	}
	defer rows.Close()

	var relations []*Relation
	for rows.Next() {
		r, err := scanRelation(rows)
		if err != nil {
			return nil, fmtErr("get_relations_batch: scan: %w", err)
		}
		relations = append(relations, r)
	}
	return relations, rows.Err()
}

// TraverseRelations performs bidirectional relation traversal using recursive CTE.
// Caps maxDepth at 5. Returns deduplicated results with distance and relationScore.
func (ms *MemoryStore) TraverseRelations(ctx context.Context, startIDs []string, maxDepth int) ([]*TraversedRelation, error) {
	if len(startIDs) == 0 || maxDepth < 1 {
		return []*TraversedRelation{}, nil
	}
	if maxDepth > 5 {
		maxDepth = 5
	}

	ph := placeholders(len(startIDs))
	args := make([]any, len(startIDs)*2+2+len(startIDs))
	for i, id := range startIDs {
		args[i] = id
		args[len(startIDs)+i] = id
	}
	args[len(startIDs)*2] = maxDepth
	args[len(startIDs)*2+1] = maxDepth
	for i, id := range startIDs {
		args[len(startIDs)*2+2+i] = id
	}

	cteSQL := fmt.Sprintf(
		`WITH RECURSIVE "rel_traverse"("node_id", "reached_id", "rel_type", "dist") AS (
			SELECT "source_id", "target_id", "relation_type", 1 FROM "relations" WHERE "source_id" IN (%s)
			UNION ALL
			SELECT "target_id", "source_id", "relation_type", 1 FROM "relations" WHERE "target_id" IN (%s)
			UNION ALL
			SELECT r."source_id", r."target_id", r."relation_type", rt."dist" + 1 FROM "relations" r
			INNER JOIN "rel_traverse" rt ON rt."reached_id" = r."source_id"
			WHERE rt."dist" < ?
			UNION ALL
			SELECT r."target_id", r."source_id", r."relation_type", rt."dist" + 1 FROM "relations" r
			INNER JOIN "rel_traverse" rt ON rt."reached_id" = r."target_id"
			WHERE rt."dist" < ?
		)
		SELECT "reached_id", "rel_type", MIN("dist") as "dist" FROM "rel_traverse"
		WHERE "reached_id" NOT IN (%s)
		GROUP BY "reached_id", "rel_type"
		ORDER BY "dist"`,
		ph, ph, ph,
	)

	rows, err := ms.db.QueryContext(ctx, cteSQL, args...)
	if err != nil {
		return nil, fmtErr("traverse_relations: %w", err)
	}
	defer rows.Close()

	var results []*TraversedRelation
	seen := make(map[string]bool)
	for rows.Next() {
		var reachedID, relType string
		var dist int
		if err := rows.Scan(&reachedID, &relType, &dist); err != nil {
			return nil, fmtErr("traverse_relations: scan: %w", err)
		}
		key := fmt.Sprintf("%s:%s", reachedID, relType)
		if seen[key] {
			continue
		}
		seen[key] = true

		// relationScore decays as 0.5^distance (matches Python)
		relationScore := 0.5
		for i := 1; i < dist; i++ {
			relationScore *= 0.5
		}

		results = append(results, &TraversedRelation{
			TargetID:      reachedID,
			RelationType:  relType,
			Distance:      dist,
			RelationScore: relationScore,
		})
	}
	return results, rows.Err()
}

// LogExtraction upserts an extraction log entry.
func (ms *MemoryStore) LogExtraction(ctx context.Context, sourceType, sourceID string, rawText *string, extractedCount int) error {
	_, err := ms.db.ExecContext(ctx,
		`INSERT INTO "extraction_log" ("source_type", "source_id", "raw_text", "extracted_count", "created_at") VALUES (?,?,?,?,datetime('now')) ON CONFLICT("source_type", "source_id") DO UPDATE SET "raw_text"=excluded."raw_text", "extracted_count"=excluded."extracted_count"`,
		sourceType, sourceID, rawText, extractedCount,
	)
	if err != nil {
		return fmtErr("log_extraction: %w", err)
	}
	return nil
}

// SupersedeBySource invalidates all memories matching source metadata.
func (ms *MemoryStore) SupersedeBySource(ctx context.Context, sourceType, sourceID string) (int, error) {
	now := nowUTC()
	result, err := ms.db.ExecContext(ctx,
		`UPDATE "memories" SET "valid_until" = ?, "updated_at" = ? WHERE "source" = ? AND ("valid_until" IS NULL OR "valid_until" > datetime('now')) AND json_extract("metadata", '$.source_id') = ?`,
		now, now, sourceType, sourceID,
	)
	if err != nil {
		return 0, fmtErr("supersede_by_source: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// IsExtracted checks whether a source has been extracted.
func (ms *MemoryStore) IsExtracted(ctx context.Context, sourceType, sourceID string) (bool, error) {
	var exists bool
	err := ms.db.QueryRowContext(ctx,
		`SELECT 1 FROM "extraction_log" WHERE "source_type" = ? AND "source_id" = ?`,
		sourceType, sourceID,
	).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmtErr("is_extracted: %w", err)
	}
	return true, nil
}

// RemoveExtractionLog removes an extraction log entry. Returns false if not found.
func (ms *MemoryStore) RemoveExtractionLog(ctx context.Context, sourceType, sourceID string) (bool, error) {
	result, err := ms.db.ExecContext(ctx,
		`DELETE FROM "extraction_log" WHERE "source_type" = ? AND "source_id" = ?`,
		sourceType, sourceID,
	)
	if err != nil {
		return false, fmtErr("remove_extraction_log: %w", err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// FindSimilar finds similar memories using vector or text search.
func (ms *MemoryStore) FindSimilar(ctx context.Context, params FindSimilarParams) ([]*ScoredMemory, error) {
	if len(params.QueryVec) > 0 {
		return ms.SearchByEmbedding(ctx, params.QueryVec, false, params.Limit, params.Threshold)
	}
	if params.Content != "" {
		results, err := ms.Search(ctx, SearchParams{
			Query:     params.Content,
			ValidOnly: false,
			Limit:     params.Limit,
		})
		if err != nil {
			return nil, err
		}
		var scored []*ScoredMemory
		for _, m := range results {
			scored = append(scored, &ScoredMemory{Memory: m, Score: 0.0})
		}
		return scored, nil
	}
	return []*ScoredMemory{}, nil
}

// ConsolidateDuplicates finds near-duplicate pairs by cosine similarity.
func (ms *MemoryStore) ConsolidateDuplicates(ctx context.Context, threshold float64, limit int) ([]*DuplicatePair, error) {
	if limit <= 0 {
		limit = 500
	}

	rows, err := ms.db.QueryContext(ctx,
		`SELECT "id", "content", "embedding" FROM "memories" WHERE "embedding" IS NOT NULL AND ("valid_until" IS NULL OR "valid_until" > datetime('now')) LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmtErr("consolidate_duplicates: %w", err)
	}
	defer rows.Close()

	type entry struct {
		id      string
		content string
		emb     []float32
	}
	var entries []entry
	for rows.Next() {
		var id, content string
		var embBytes []byte
		if err := rows.Scan(&id, &content, &embBytes); err != nil {
			return nil, fmtErr("consolidate_duplicates: scan: %w", err)
		}
		vec := BytesToVec(embBytes)
		entries = append(entries, entry{id: id, content: content, emb: vec})
	}
	if rows.Err() != nil {
		return nil, fmtErr("consolidate_duplicates: rows: %w", rows.Err())
	}

	if len(entries) == 0 {
		return []*DuplicatePair{}, nil
	}

	var pairs []*DuplicatePair
	seen := make(map[string]bool)
	for i, e1 := range entries {
		if seen[e1.id] {
			continue
		}
		for j := i + 1; j < len(entries); j++ {
			e2 := entries[j]
			if seen[e2.id] || e2.id == e1.id {
				continue
			}
			if len(e1.emb) != len(e2.emb) {
				continue
			}
			score := cosineSimilarity(e1.emb, e2.emb)
			if score >= threshold {
				pairs = append(pairs, &DuplicatePair{
					SourceID: e1.id,
					TargetID: e2.id,
					Score:    score,
				})
				seen[e1.id] = true
				seen[e2.id] = true
			}
		}
	}
	return pairs, nil
}

// ExportAll exports all memories ordered by created_at.
// If limit is nil, defaults to 10000. Pass 0 for no limit.
func (ms *MemoryStore) ExportAll(ctx context.Context, limit *int) ([]*Memory, error) {
	effectiveLimit := defaultExportLimit
	if limit != nil {
		effectiveLimit = *limit
	}

	var rows *sql.Rows
	var err error
	if effectiveLimit == 0 {
		rows, err = ms.db.QueryContext(ctx,
			`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" ORDER BY "created_at"`,
		)
	} else {
		rows, err = ms.db.QueryContext(ctx,
			`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" ORDER BY "created_at" LIMIT ?`,
			effectiveLimit,
		)
	}
	if err != nil {
		return nil, fmtErr("export_all: %w", err)
	}
	defer rows.Close()

	var memories []*Memory
	for rows.Next() {
		m, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmtErr("export_all: scan: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// ImportMemories imports a list of memories with per-entry validation.
// Skips invalid entries with slog.Warn. Returns count of successfully imported memories.
func (ms *MemoryStore) ImportMemories(ctx context.Context, memories []ImportMemory) (int, error) {
	count := 0
	for i, m := range memories {
		// Validate type and content are strings
		if m.Type == "" {
			slog.Warn("llmem: store: import: skipping entry: missing type", "index", i)
			continue
		}
		if m.Content == "" {
			slog.Warn("llmem: store: import: skipping entry: missing content", "index", i)
			continue
		}

		// Validate ID length
		if len(m.ID) > maxIDLength {
			slog.Warn("llmem: store: import: skipping entry: id too long", "index", i, "id_length", len(m.ID))
			continue
		}

		// Validate embedding size limit (DoS protection)
		if len(m.Embedding) > maxEmbeddingBytes {
			slog.Warn("llmem: store: import: skipping entry: embedding exceeds size limit", "index", i, "got", len(m.Embedding), "max", maxEmbeddingBytes)
			continue
		}

		// Validate embedding dimensions only when vec is available
		if len(m.Embedding) > 0 && ms.vecAvailable {
			expectedLen := ms.vecDimensions * 4
			if len(m.Embedding) != expectedLen {
				slog.Warn("llmem: store: import: skipping entry: embedding dimension mismatch", "index", i, "got", len(m.Embedding), "expected", expectedLen)
				continue
			}
		}

		confidence := m.Confidence
		if confidence == 0 {
			confidence = defaultConfidence
		}
		if confidence < 0 || confidence > 1 {
			slog.Warn("llmem: store: import: skipping entry: confidence out of range", "index", i, "confidence", confidence)
			continue
		}

		_, err := ms.Add(ctx, AddParams{
			ID:         m.ID,
			Type:       m.Type,
			Content:    m.Content,
			Summary:    m.Summary,
			Source:     m.Source,
			Confidence: confidence,
			Metadata:   m.Metadata,
			Embedding:  m.Embedding,
			Hints:      m.Hints,
		})
		if err != nil {
			slog.Warn("llmem: store: import: skipping entry", "index", i, "error", err)
			continue
		}
		count++
	}
	return count, nil
}

// Touch increments access_count and updates accessed_at for a memory.
// Returns false if not found.
func (ms *MemoryStore) Touch(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := ms.db.QueryRowContext(ctx, `SELECT 1 FROM "memories" WHERE "id" = ?`, id).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmtErr("touch: check: %w", err)
	}
	if !exists {
		return false, nil
	}

	now := nowUTC()
	_, err = ms.db.ExecContext(ctx,
		`UPDATE "memories" SET "accessed_at" = ?, "access_count" = "access_count" + 1 WHERE "id" = ?`,
		now, id,
	)
	if err != nil {
		return false, fmtErr("touch: update: %w", err)
	}
	return true, nil
}

// TouchBatch increments access_count, updates accessed_at, and resets TTL for
// stable and volatile (non-pinned) memories. Log memories get access tracking
// but NO TTL reset. Returns the number of rows affected.
func (ms *MemoryStore) TouchBatch(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := nowUTC()
	stableExpiry := time.Now().UTC().AddDate(0, 0, TTLStableDays).Format(time.RFC3339)
	volatileExpiry := time.Now().UTC().AddDate(0, 0, TTLVolatileDays).Format(time.RFC3339)
	ph := placeholders(len(ids))
	args := []any{now, stableExpiry, volatileExpiry}
	for _, id := range ids {
		args = append(args, id)
	}
	result, err := ms.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE "memories" SET "accessed_at" = ?, "valid_until" = CASE WHEN "type" = 'stable' AND "pinned" = 0 THEN ? WHEN "type" = 'volatile' AND "pinned" = 0 THEN ? ELSE "valid_until" END, "access_count" = "access_count" + 1 WHERE "id" IN (%s)`, ph),
		args...,
	)
	if err != nil {
		return 0, fmtErr("touch_batch: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// GetEmbeddingsWithTypes returns (embedding_bytes, type) tuples for valid memories with embeddings.
// If limit is 0, returns all rows (no limit). If limit < 0, defaults to defaultBruteForceMaxRows.
func (ms *MemoryStore) GetEmbeddingsWithTypes(ctx context.Context, limit int) ([]*EmbeddingWithType, error) {
	effectiveLimit := limit
	if effectiveLimit < 0 {
		effectiveLimit = defaultBruteForceMaxRows
	}

	var rows *sql.Rows
	var err error
	if effectiveLimit > 0 {
		rows, err = ms.db.QueryContext(ctx,
			`SELECT "embedding", "type" FROM "memories" WHERE "embedding" IS NOT NULL AND ("valid_until" IS NULL OR "valid_until" > datetime('now')) LIMIT ?`,
			effectiveLimit,
		)
	} else {
		rows, err = ms.db.QueryContext(ctx,
			`SELECT "embedding", "type" FROM "memories" WHERE "embedding" IS NOT NULL AND ("valid_until" IS NULL OR "valid_until" > datetime('now'))`,
		)
	}
	if err != nil {
		return nil, fmtErr("get_embeddings_with_types: %w", err)
	}
	defer rows.Close()

	var result []*EmbeddingWithType
	for rows.Next() {
		var emb []byte
		var mtype string
		if err := rows.Scan(&emb, &mtype); err != nil {
			return nil, fmtErr("get_embeddings_with_types: scan: %w", err)
		}
		result = append(result, &EmbeddingWithType{Embedding: emb, Type: mtype})
	}
	return result, rows.Err()
}

// openDB opens a SQLite database connection using the modernc.org/sqlite driver.
func openDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmtErr("open db: %w", err)
	}
	// Enable WAL mode and foreign keys
	_, err = db.Exec("PRAGMA journal_mode=WAL")
	if err != nil {
		db.Close()
		return nil, fmtErr("enable WAL: %w", err)
	}
	_, err = db.Exec("PRAGMA foreign_keys=ON")
	if err != nil {
		db.Close()
		return nil, fmtErr("enable foreign keys: %w", err)
	}
	return db, nil
}

// rebuildFTSIfEmpty rebuilds the FTS index if it's empty but memories exist.
func (ms *MemoryStore) rebuildFTSIfEmpty() error {
	var ftsCount, memCount int
	err := ms.db.QueryRow(`SELECT count(*) FROM "memories_fts"`).Scan(&ftsCount)
	if err != nil {
		return fmtErr("rebuild FTS: count fts: %w", err)
	}
	err = ms.db.QueryRow(`SELECT count(*) FROM "memories"`).Scan(&memCount)
	if err != nil {
		return fmtErr("rebuild FTS: count memories: %w", err)
	}
	if ftsCount == 0 && memCount > 0 {
		_, err = ms.db.Exec(
			`INSERT INTO "memories_fts"("rowid", "content", "summary", "hints") SELECT "rowid", "content", "summary", "hints" FROM "memories" WHERE "content" IS NOT NULL`,
		)
		if err != nil {
			return fmtErr("rebuild FTS: insert: %w", err)
		}
	}
	return nil
}

// dropLegacyVec0Objects removes legacy vec0 virtual tables, auxiliary tables,
// and triggers from earlier Python LLMem or C extension versions.
// The Go version uses viant/sqlite-vec which has a different schema.
//
// Key challenge: if the database was created with vec0 (Python LLMem or C extension),
// the memories_vec virtual table is registered with the vec0 module. Since vec0
// isn't available in the Go version, DROP TABLE on that virtual table returns
// "no such module: vec0". We work around this by:
//  1. Dropping the auxiliary chunk/rowid/info tables (these are real tables, not virtual)
//  2. Dropping the triggers (they reference vec0, causing "no such module" errors)
//  3. Trying DROP TABLE on the vtab; if it fails (missing module), using
//     PRAGMA writable_schema to delete the stale entry from sqlite_master
//  4. Dropping any viant shadow/storage tables from prior runs
func dropLegacyVec0Objects(db *sql.DB) error {
	// Drop triggers first (they reference the vec0 virtual table)
	for _, name := range []string{"memories_vec_insert", "memories_vec_update", "memories_vec_update_null", "memories_vec_delete"} {
		_, _ = db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS "%s"`, name))
	}
	// Drop vec0 auxiliary tables (real tables, not virtual)
	for _, name := range []string{"memories_vec_chunks", "memories_vec_rowids", "memories_vec_vector_chunks00", "memories_vec_info"} {
		_, _ = db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, name))
	}
	// Drop viant/sqlite-vec shadow and storage tables from prior runs.
	// These are real tables and can always be dropped.
	for _, name := range []string{"_vec_memories_vec", "vector_storage", "vector_storage_locks"} {
		_, _ = db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, name))
	}
	// Drop the vec0 virtual table. If the database was created with the old
	// sqlite-vec C extension or Python LLMem, the vtab is registered under
	// the "vec0" module. Since vec0 isn't loaded in Go, DROP TABLE fails with
	// "no such module: vec0". Use PRAGMA writable_schema to remove the stale
	// entry from sqlite_master directly.
	_, err := db.Exec(`DROP TABLE IF EXISTS "memories_vec"`)
	if err != nil {
		slog.Debug("llmem: store: DROP TABLE memories_vec failed, using writable_schema workaround", "error", err)
		if dropErr := dropStaleVirtualTable(db, "memories_vec"); dropErr != nil {
			slog.Warn("llmem: store: failed to remove stale memories_vec vtab", "error", dropErr)
		}
	}
	return nil
}

// dropStaleVirtualTable removes a virtual table entry from sqlite_master when
// the module that created it is unavailable (e.g., vec0 in a Go-only build).
// Uses PRAGMA writable_schema to allow direct modification of sqlite_master.
func dropStaleVirtualTable(db *sql.DB, tableName string) error {
	// Enable writable_schema to allow modification of sqlite_master
	if _, err := db.Exec("PRAGMA writable_schema=ON"); err != nil {
		return fmtErr("enable writable_schema: %w", err)
	}
	// Remove the stale virtual table entry
	result, err := db.Exec(
		"DELETE FROM sqlite_master WHERE type='table' AND name=?",
		tableName,
	)
	if err != nil {
		// Always try to restore writable_schema=OFF even on error
		_, _ = db.Exec("PRAGMA writable_schema=OFF")
		return fmtErr("delete stale vtab from sqlite_master: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	slog.Info("llmem: store: removed stale virtual table from sqlite_master", "table", tableName, "rows_removed", rowsAffected)
	// Disable writable_schema and verify integrity
	if _, err := db.Exec("PRAGMA writable_schema=OFF"); err != nil {
		return fmtErr("disable writable_schema: %w", err)
	}
	// Verify database integrity after modifying sqlite_master
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return fmtErr("integrity check failed after removing stale vtab: status=%s err=%w", integrity, err)
	}
	return nil
}

// initVecTable creates the vec virtual table and populates it from memories.embedding.
// Uses viant/sqlite-vec which provides a pure Go vector search implementation
// compatible with modernc.org/sqlite. No CGO or shared libraries required.
//
// The vec virtual table uses a shadow table (_vec_memories_vec) with columns:
// dataset_id, id, content, meta, embedding. We use dataset_id='memories'
// as a fixed partition since LLMem doesn't have multi-tenancy.
//
// Sync between memories.embedding and the shadow table is handled in Go code
// (Add/Update/Invalidate/Delete), not via SQL triggers.
func (ms *MemoryStore) initVecTable() error {
	// Create the vec virtual table. viant/sqlite-vec uses 'vec' module (not vec0).
	_, err := ms.db.Exec(fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS "memories_vec" USING vec(doc_id, embedding float[%d] distance_metric=cosine)`,
		ms.vecDimensions,
	))
	if err != nil {
		return fmtErr("init_vec: create vec virtual table: %w", err)
	}

	// Eagerly create the shadow table, vector_storage, and vector_storage_locks.
	// viant/sqlite-vec creates these lazily on first query, but that causes
	// deadlocks with database/sql connection pooling (the SELECT holds the
	// connection, and CREATE TABLE needs another). Create them upfront.
	for _, ddl := range []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			dataset_id TEXT NOT NULL,
			id TEXT NOT NULL,
			content TEXT,
			meta TEXT,
			embedding BLOB,
			PRIMARY KEY(dataset_id, id)
		)`, "_vec_memories_vec"),
		`CREATE TABLE IF NOT EXISTS vector_storage (
			shadow_table_name TEXT NOT NULL,
			dataset_id TEXT NOT NULL DEFAULT '',
			"index" BLOB,
			PRIMARY KEY (shadow_table_name, dataset_id)
		)`,
		`CREATE TABLE IF NOT EXISTS vector_storage_locks (
			shadow_table_name TEXT NOT NULL,
			dataset_id TEXT NOT NULL DEFAULT '',
			owner TEXT NOT NULL,
			locked_at INTEGER NOT NULL,
			PRIMARY KEY (shadow_table_name, dataset_id)
		)`,
	} {
		if _, err := ms.db.Exec(ddl); err != nil {
			return fmtErr("init_vec: create vector infrastructure: %w", err)
		}
	}

	// Populate shadow table from existing embeddings in memories table.
	// This syncs any embeddings that were added before vec was initialized.
	var embCount int
	if err := ms.db.QueryRow(`SELECT count(*) FROM "memories" WHERE "embedding" IS NOT NULL AND ("valid_until" IS NULL OR "valid_until" > datetime('now'))`).Scan(&embCount); err != nil {
		return fmtErr("init_vec: count embeddings: %w", err)
	}

	if embCount > 0 {
		rows, err := ms.db.Query(`SELECT "id", "content", "metadata", "embedding" FROM "memories" WHERE "embedding" IS NOT NULL AND ("valid_until" IS NULL OR "valid_until" > datetime('now'))`)
		if err != nil {
			return fmtErr("init_vec: fetch embeddings: %w", err)
		}
		synced := 0
		for rows.Next() {
			var id, content string
			var metadataJSON, emb []byte
			if err := rows.Scan(&id, &content, &metadataJSON, &emb); err != nil {
				rows.Close()
				return fmtErr("init_vec: scan embedding: %w", err)
			}
			meta := ""
			if len(metadataJSON) > 0 && string(metadataJSON) != "null" {
				meta = string(metadataJSON)
			}
			if _, err := ms.db.Exec(
				`INSERT OR REPLACE INTO "_vec_memories_vec"("dataset_id", "id", "content", "meta", "embedding") VALUES ('memories', ?, ?, ?, ?)`,
				id, content, meta, emb,
			); err != nil {
				slog.Debug("llmem: store: init_vec: failed to sync embedding", "id", id, "error", err)
				continue
			}
			synced++
		}
		rows.Close()
		slog.Info("llmem: store: vec shadow table populated", "embeddings", synced, "total", embCount)
	}

	return nil
}

// chmodDBFiles sets 0600 permissions on the DB file and its WAL/SHM sidecars.
func chmodDBFiles(dbPath string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := dbPath + suffix
		if _, err := os.Stat(path); err == nil {
			if err := os.Chmod(path, 0600); err != nil {
				slog.Warn("llmem: store: failed to set permissions on db file", "path", path, "error", err)
			}
		}
	}
	return nil
}

// scanFunc is a function type that scans row columns into dest pointers.
type scanFunc func(dest ...any) error

// scanMemoryFields scans columns from a row into a Memory struct using the provided scan function.
// This is the shared implementation for both scanMemory (single row) and scanMemoryFromRows (iterator).
func scanMemoryFields(scan scanFunc) (*Memory, error) {
	m := &Memory{}
	var hintsJSON, metadataJSON string
	var summary, validUntil, accessedAt sql.NullString
	var embedding []byte
	var pinned int

	err := scan(
		&m.ID, &m.Type, &m.Content, &summary, &hintsJSON, &m.Source, &m.Confidence,
		&m.ValidFrom, &validUntil, &m.CreatedAt, &m.UpdatedAt, &accessedAt,
		&m.AccessCount, &metadataJSON, &embedding, &pinned,
	)
	if err != nil {
		return nil, err
	}

	m.Summary = summary.String
	m.ValidUntil = validUntil.String
	m.AccessedAt = accessedAt.String
	m.Embedding = embedding
	m.Pinned = pinned != 0

	if err := json.Unmarshal([]byte(hintsJSON), &m.Hints); err != nil {
		m.Hints = []string{}
	}
	if m.Hints == nil {
		m.Hints = []string{}
	}
	if err := json.Unmarshal([]byte(metadataJSON), &m.Metadata); err != nil {
		m.Metadata = map[string]any{}
	}
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}

	return m, nil
}

// scanMemory scans a single row from a *sql.Row into a Memory struct.
func scanMemory(row *sql.Row) (*Memory, error) {
	return scanMemoryFields(row.Scan)
}

// scanMemoryFromRows scans a single row from sql.Rows into a Memory struct.
func scanMemoryFromRows(rows *sql.Rows) (*Memory, error) {
	return scanMemoryFields(rows.Scan)
}

// scanRelation scans a single row from sql.Rows into a Relation struct.
func scanRelation(rows *sql.Rows) (*Relation, error) {
	r := &Relation{}
	err := rows.Scan(&r.ID, &r.SourceID, &r.TargetID, &r.RelationType, &r.CreatedAt)
	return r, err
}
