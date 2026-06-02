package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichielDean/LLMem/migrations"
	"github.com/pressly/goose/v3"
)

func TestMigration_AllApplied(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_schema.db")

	// Use NewMemoryStore which runs all migrations
	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	// Access the db directly for verification
	db := ms.db

	// Verify memories table has all required columns
	requiredColumns := []string{
		"id", "type", "content", "summary", "hints", "source", "confidence",
		"valid_from", "valid_until", "created_at", "updated_at", "accessed_at",
		"access_count", "metadata", "embedding",
	}
	rows, err := db.Query(`SELECT "name" FROM pragma_table_info('memories')`)
	if err != nil {
		t.Fatalf("query memories columns: %v", err)
	}
	defer rows.Close()

	var foundColumns []string
	for rows.Next() {
		var colName string
		if err := rows.Scan(&colName); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		foundColumns = append(foundColumns, colName)
	}
	for _, req := range requiredColumns {
		found := false
		for _, col := range foundColumns {
			if col == req {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected column %q in memories table, not found", req)
		}
	}

	// Verify relations table schema (post-007: no target_type)
	relRows, err := db.Query(`SELECT "name" FROM pragma_table_info('relations')`)
	if err != nil {
		t.Fatalf("query relations columns: %v", err)
	}
	defer relRows.Close()

	var relColumns []string
	for relRows.Next() {
		var colName string
		if err := relRows.Scan(&colName); err != nil {
			t.Fatalf("scan relation column: %v", err)
		}
		relColumns = append(relColumns, colName)
	}

	for _, col := range relColumns {
		if col == "target_type" {
			t.Error("target_type column should not exist in relations after migration 007")
		}
	}

	// Verify extraction_log table exists
	var elCount int
	err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('extraction_log')`).Scan(&elCount)
	if err != nil {
		t.Fatalf("extraction_log table check: %v", err)
	}
	if elCount == 0 {
		t.Error("expected extraction_log table to exist")
	}

	// Verify memory_types table has 7 types
	var mtCount int
	err = db.QueryRow(`SELECT count(*) FROM "memory_types"`).Scan(&mtCount)
	if err != nil {
		t.Fatalf("memory_types count: %v", err)
	}
	if mtCount != 7 {
		t.Errorf("expected 7 memory types, got %d", mtCount)
	}

	// Verify code_chunks table exists
	var ccCount int
	err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('code_chunks')`).Scan(&ccCount)
	if err != nil {
		t.Fatalf("code_chunks table check: %v", err)
	}
	if ccCount == 0 {
		t.Error("expected code_chunks table to exist")
	}

	// Verify memories_fts FTS5 table exists
	var ftsExists bool
	err = db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='memories_fts'`).Scan(&ftsExists)
	if err != nil {
		if err == sql.ErrNoRows {
			t.Error("expected memories_fts virtual table to exist")
		} else {
			t.Fatalf("check FTS table: %v", err)
		}
	}

	// Verify required indexes exist
	requiredIndexes := []string{
		"idx_memories_type", "idx_memories_valid", "idx_memories_source",
		"idx_memories_updated_at", "idx_relations_source", "idx_relations_target",
		"idx_code_chunks_file_path", "idx_code_chunks_language", "idx_code_chunks_chunk_type",
	}
	allTables := []string{"memories", "relations", "code_chunks"}
	var foundIndexes []string
	for _, table := range allTables {
		idxRows, err := db.Query(`SELECT name FROM pragma_index_list(?)`, table)
		if err != nil {
			t.Fatalf("query indexes for %s: %v", table, err)
		}
		for idxRows.Next() {
			var idxName string
			if err := idxRows.Scan(&idxName); err != nil {
				idxRows.Close()
				t.Fatalf("scan index name: %v", err)
			}
			foundIndexes = append(foundIndexes, idxName)
		}
		idxRows.Close()
	}
	for _, req := range requiredIndexes {
		found := false
		for _, idx := range foundIndexes {
			if idx == req {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected index %q, not found (found: %v)", req, foundIndexes)
		}
	}

	// Verify inbox table does NOT exist (dropped by migration 007)
	var inboxExists bool
	err = db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='inbox'`).Scan(&inboxExists)
	if err == nil {
		t.Error("expected inbox table to NOT exist after migration 007")
	}

	// Verify FTS trigger exists
	var insertTriggerExists bool
	err = db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='trigger' AND name='memories_fts_insert'`).Scan(&insertTriggerExists)
	if err != nil {
		if err == sql.ErrNoRows {
			t.Error("expected memories_fts_insert trigger to exist")
		}
	}

	// Close the store
	ms.Close()
}

func TestMigration_Numbering(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_numbering.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("PRAGMA journal_mode=WAL")
	if err != nil {
		t.Fatalf("WAL: %v", err)
	}
	_, err = db.Exec("PRAGMA foreign_keys=ON")
	if err != nil {
		t.Fatalf("FK: %v", err)
	}

	goose.SetDialect("sqlite3")
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.FS)
	if err != nil {
		t.Fatalf("goose.NewProvider: %v", err)
	}

	res, err := provider.Up(context.Background())
	if err != nil {
		t.Fatalf("goose.Up: %v", err)
	}

	if len(res) < 7 {
		t.Errorf("expected at least 7 migration results, got %d", len(res))
	}
}

func TestMigration_FTS5Search(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_fts.db")

	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer ms.Close()

	ctx := context.Background()

	// Add memories with distinct content
	ms.Add(ctx, AddParams{Type: "fact", Content: "golang is a programming language"})
	ms.Add(ctx, AddParams{Type: "fact", Content: "python is also a programming language"})
	ms.Add(ctx, AddParams{Type: "fact", Content: "rust is a systems programming language"})

	// Search for "golang"
	results, err := ms.Search(ctx, SearchParams{Query: "golang", ValidOnly: false, Limit: 10})
	if err != nil {
		t.Fatalf("FTS search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected FTS search results for 'golang'")
	}

	found := false
	for _, r := range results {
		if r.Content == "golang is a programming language" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'golang is a programming language' in FTS search results")
	}
}

func TestNewMemoryStore_DirectoryPermissions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "subdir", "test.db")

	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	ms.Close()

	subdirInfo, err := os.Stat(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("stat subdir: %v", err)
	}
	perms := subdirInfo.Mode().Perm()
	if perms&0077 != 0 {
		t.Errorf("expected directory permissions to be 0700 or stricter, got %o", perms)
	}
}

func TestNewMemoryStore_DBFilePermissions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	ms.Close()

	// Check that the DB file has 0600 permissions (owner-only read/write).
	// On Unix, the umask is set to 0o177 before creation, so the file
	// should be created with mode 0600. On other platforms, chmodDBFiles
	// applies 0600 after creation.
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	perms := info.Mode().Perm()
	if perms&0077 != 0 {
		t.Errorf("expected DB file permissions to be 0600 or stricter, got %o", perms)
	}

	// Check WAL and SHM sidecars if they exist (WAL mode always creates them)
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecarPath := dbPath + suffix
		sidecarInfo, err := os.Stat(sidecarPath)
		if err != nil {
			// Sidecar may not exist yet (created on first write), skip
			continue
		}
		sidecarPerms := sidecarInfo.Mode().Perm()
		if sidecarPerms&0077 != 0 {
			t.Errorf("expected %s permissions to be 0600 or stricter, got %o", suffix, sidecarPerms)
		}
	}
}

func TestSetResetUmask(t *testing.T) {
	// Verify setUmask actually changes the umask on Unix and resetUmask
	// restores it. On non-Unix platforms these are no-ops, so we only
	// test the round-trip, not the file permission effect.
	origMask := setUmask(0o022) // Save whatever the current mask is
	resetUmask(origMask)         // Restore it

	// Verify round-trip: set a known mask, read it back
	saved := setUmask(0o177)
	second := setUmask(saved)
	if second != 0o177 {
		t.Errorf("expected umask round-trip to return 0o177, got 0o%o", second)
	}
	resetUmask(saved) // Clean up: restore original

	// On Unix, verify that a file created while umask is 0o177 gets
	// mode 0600. On other platforms, this test still passes because
	// chmodDBFiles applies 0600 after creation.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "umask_test.txt")

	old := setUmask(0o177)
	f, err := os.Create(filePath)
	if err != nil {
		resetUmask(old)
		t.Fatalf("create test file: %v", err)
	}
	f.Close()
	resetUmask(old)

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	perms := info.Mode().Perm()
	if perms&0077 != 0 {
		t.Errorf("expected file created under umask 0o177 to have no group/other bits, got 0o%o", perms)
	}
	os.Remove(filePath)
}

func TestMigration_MemoryTypesRegistered(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_types.db")

	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer ms.Close()

	defaultTypes := DefaultRegisteredTypes()
	for _, typeName := range defaultTypes {
		if _, ok := ms.registeredTypes[typeName]; !ok {
			t.Errorf("expected type %q to be registered", typeName)
		}
	}
}

func TestMigration_RelationCheckConstraint(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_constraints.db")

	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer ms.Close()

	ctx := context.Background()
	id1, _ := ms.Add(ctx, AddParams{Type: "fact", Content: "source"})
	id2, _ := ms.Add(ctx, AddParams{Type: "fact", Content: "target"})

	for _, rt := range []string{"supersedes", "related_to", "derived_from"} {
		_, err := ms.AddRelation(ctx, id1, id2, rt)
		if err != nil {
			t.Errorf("expected relation type %q to work, got error: %v", rt, err)
		}
	}
}

func TestMigration_DBCreatedSuccessfully(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_create.db")

	_, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected DB file to be created")
	}
}

func TestMigration_Vec0ObjectsDroppedBeforeMigration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_vec0_migration.db")

	ctx := context.Background()

	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}

	_, err = ms.Add(ctx, AddParams{Type: "fact", Content: "test memory for vec0 migration"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	ms.Close()

	rawDB, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}

	_, err = rawDB.Exec(`CREATE TABLE IF NOT EXISTS "memories_vec_rowids" (rowid INTEGER PRIMARY KEY)`)
	if err != nil {
		t.Fatalf("create memories_vec_rowids: %v", err)
	}
	_, err = rawDB.Exec(`CREATE TABLE IF NOT EXISTS "memories_vec_chunks" (rowid INTEGER PRIMARY KEY)`)
	if err != nil {
		t.Fatalf("create memories_vec_chunks: %v", err)
	}
	_, err = rawDB.Exec(`CREATE TRIGGER IF NOT EXISTS "memories_vec_insert" AFTER INSERT ON "memories" WHEN new."embedding" IS NOT NULL BEGIN INSERT INTO "memories_vec_rowids"(rowid) VALUES(new."rowid"); END`)
	if err != nil {
		t.Fatalf("create vec0 insert trigger: %v", err)
	}
	rawDB.Close()

	ms2, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore with vec0 artifacts: %v", err)
	}
	defer ms2.Close()

	var count int
	err = ms2.db.QueryRow(`SELECT count(*) FROM "memories" WHERE type = 'fact'`).Scan(&count)
	if err != nil {
		t.Fatalf("query after migration: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 fact memory after migration, got %d", count)
	}

	var triggerCount int
	err = ms2.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='memories_vec_insert'`).Scan(&triggerCount)
	if err != nil {
		t.Fatalf("query trigger count: %v", err)
	}
	if triggerCount != 0 {
		t.Errorf("expected vec0 insert trigger to be dropped, found %d", triggerCount)
	}
}

func TestHelpers_NowUTC(t *testing.T) {
	result := nowUTC()
	if result == "" {
		t.Error("expected non-empty UTC timestamp")
	}
	_, err := time.Parse(time.RFC3339, result)
	if err != nil {
		t.Errorf("expected RFC3339 format, got %q: %v", result, err)
	}
}