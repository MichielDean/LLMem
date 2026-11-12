-- +goose Up
-- 008_add_pinned_column.sql — DDL only
-- Adds the "pinned" boolean column to the memories table.
-- Pinned memories are exempt from TTL-based auto-pruning.
-- Only stable-type memories can be pinned (enforced in application code).

ALTER TABLE "memories" ADD COLUMN "pinned" INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS "idx_memories_pinned" ON "memories"("pinned");

-- +goose Down
DROP INDEX IF EXISTS "idx_memories_pinned";

-- SQLite does not support DROP COLUMN before 3.35.0.
-- To remove the column, we would need to recreate the table.
-- For the down migration, we leave the column in place (harmless).
