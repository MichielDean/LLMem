-- +goose Up
-- 003_register_default_types.sql — DML only
-- Registers all default memory types in the memory_types table.
-- The memory_types table was created in 001_initial_schema.sql.
-- Goose wraps this in a transaction automatically, so no explicit BEGIN/COMMIT.

INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('fact');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('decision');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('preference');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('event');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('project_state');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('procedure');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('conversation');

-- +goose Down
DELETE FROM "memory_types" WHERE "name" IN ('fact', 'decision', 'preference', 'event', 'project_state', 'procedure', 'conversation');