-- +goose Up
-- 010_remove_legacy_types.sql — DML only
-- Contract phase: removes the 7 legacy memory types from memory_types.
-- Only the 3 new tier types (stable, volatile, log) remain.
-- Existing memories should have been migrated via `llmem migrate-types` first.

DELETE FROM "memory_types" WHERE "name" IN ('fact', 'decision', 'preference', 'event', 'project_state', 'procedure', 'conversation');

-- +goose Down
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('fact');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('decision');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('preference');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('event');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('project_state');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('procedure');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('conversation');
