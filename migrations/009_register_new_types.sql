-- +goose Up
-- 009_register_new_types.sql — DML only
-- Registers the three new memory tiers alongside the existing 7 types.
-- This is the expand phase — both old and new types are accepted.
-- The old 7 types will be removed in the contract phase (migration 010).

INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('stable');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('volatile');
INSERT OR IGNORE INTO "memory_types" ("name") VALUES ('log');

-- +goose Down
DELETE FROM "memory_types" WHERE "name" IN ('stable', 'volatile', 'log');
