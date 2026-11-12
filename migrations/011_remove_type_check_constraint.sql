-- +goose Up
-- 011_remove_type_check_constraint.sql — DDL only
-- The live database has a CHECK constraint on the "type" column from the
-- legacy Python LLMem version: CHECK(type IN ('fact','decision',...,'self_assessment')).
-- The Go migration 001 intentionally has NO CHECK constraint (type validation
-- is in application code). But databases created by the Python version
-- retain the old constraint, blocking migration to new tier types.
-- SQLite cannot ALTER TABLE to remove a CHECK constraint, so we recreate
-- the table without it.

CREATE TABLE "memories_new" (
    "id" TEXT PRIMARY KEY,
    "type" TEXT NOT NULL,
    "content" TEXT NOT NULL,
    "summary" TEXT,
    "hints" TEXT NOT NULL DEFAULT '[]',
    "source" TEXT NOT NULL DEFAULT 'manual',
    "confidence" REAL NOT NULL DEFAULT 0.8 CHECK("confidence" >= 0 AND "confidence" <= 1),
    "valid_from" TEXT NOT NULL DEFAULT (datetime('now')),
    "valid_until" TEXT,
    "created_at" TEXT NOT NULL DEFAULT (datetime('now')),
    "updated_at" TEXT NOT NULL DEFAULT (datetime('now')),
    "accessed_at" TEXT,
    "access_count" INTEGER NOT NULL DEFAULT 0,
    "metadata" TEXT NOT NULL DEFAULT '{}',
    "embedding" BLOB,
    "pinned" INTEGER NOT NULL DEFAULT 0
);

INSERT INTO "memories_new" SELECT * FROM "memories";

DROP TABLE "memories";

ALTER TABLE "memories_new" RENAME TO "memories";

-- Recreate indexes (DROP TABLE drops indexes)
CREATE INDEX IF NOT EXISTS "idx_memories_type" ON "memories"("type");
CREATE INDEX IF NOT EXISTS "idx_memories_valid" ON "memories"("valid_until");
CREATE INDEX IF NOT EXISTS "idx_memories_source" ON "memories"("source");
CREATE INDEX IF NOT EXISTS "idx_memories_updated_at" ON "memories"("updated_at");
CREATE INDEX IF NOT EXISTS "idx_memories_pinned" ON "memories"("pinned");

-- Recreate FTS5 table and triggers
CREATE VIRTUAL TABLE IF NOT EXISTS "memories_fts" USING fts5("content", "summary", "hints", content="memories", content_rowid="rowid");

INSERT INTO "memories_fts"("rowid", "content", "summary", "hints")
    SELECT "rowid", "content", "summary", "hints" FROM "memories";

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS "memories_fts_insert" AFTER INSERT ON "memories" BEGIN
    INSERT INTO "memories_fts"("rowid", "content", "summary", "hints") VALUES (new."rowid", new."content", new."summary", new."hints");
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS "memories_fts_update" AFTER UPDATE ON "memories" BEGIN
    INSERT INTO "memories_fts"("memories_fts", "rowid", "content", "summary", "hints") VALUES ('delete', old."rowid", old."content", old."summary", old."hints");
    INSERT INTO "memories_fts"("rowid", "content", "summary", "hints") VALUES (new."rowid", new."content", new."summary", new."hints");
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS "memories_fts_delete" AFTER DELETE ON "memories" BEGIN
    INSERT INTO "memories_fts"("memories_fts", "rowid", "content", "summary", "hints") VALUES ('delete', old."rowid", old."content", old."summary", old."hints");
END;
-- +goose StatementEnd

-- +goose Down
-- Restoring the CHECK constraint is not practical (the whole point is to remove it).
-- If needed, the table could be recreated with the constraint, but that would
-- block new tier types again.
DROP TRIGGER IF EXISTS "memories_fts_delete";
DROP TRIGGER IF EXISTS "memories_fts_update";
DROP TRIGGER IF EXISTS "memories_fts_insert";
DROP TABLE IF EXISTS "memories_fts";
DROP INDEX IF EXISTS "idx_memories_pinned";
DROP INDEX IF EXISTS "idx_memories_updated_at";
DROP INDEX IF EXISTS "idx_memories_source";
DROP INDEX IF EXISTS "idx_memories_valid";
DROP INDEX IF EXISTS "idx_memories_type";
-- Cannot easily restore the old CHECK constraint via ALTER TABLE.
-- The table stays without it.
