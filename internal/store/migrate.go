package store

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// MigrationResult summarizes the changes made by MigrateTypes.
type MigrationResult struct {
	Retyped   int
	Merged    int
	Pruned    int
	Compressed int
	Extracted  int
}

// TypeMigration maps legacy types to new tier types.
var typeMigration = map[string]string{
	"decision":        "stable",
	"preference":      "stable",
	"procedure":       "stable",
	"fact":            "stable",
	"conversation":    "stable",
	"self_assessment":  "stable",
	"project_state":    "volatile",
	// event is handled separately (log or drop)
}

// MigrateTypes performs the one-time migration from the 7 legacy types
// to the 3 new tier types (stable/volatile/log).
//
// Steps:
// 1. Type remapping: legacy types → new tiers (event → log or drop)
// 2. Semantic dedup: merge near-duplicate stable memories (text-based Jaccard ≥ 0.85)
// 3. Retroactive TTL: compute valid_until for all memories, prune expired
// 4. Log compression: truncate log entries exceeding 200 words
//
// If dryRun is true, no changes are committed — the result reflects what would happen.
// The migration is idempotent: running it twice produces the same state.
func (ms *MemoryStore) MigrateTypes(ctx context.Context, dryRun bool) (*MigrationResult, error) {
	result := &MigrationResult{}

	// 1. Type remapping
	retyped, dropped, err := ms.migrateTypes_step1(ctx, dryRun)
	if err != nil {
		return nil, fmtErr("migrate: step1 retyping: %w", err)
	}
	result.Retyped = retyped
	result.Pruned = dropped // dropped events count as pruned

	// 2. Semantic dedup (stable only)
	merged, err := ms.migrateTypes_step2(ctx, dryRun)
	if err != nil {
		return nil, fmtErr("migrate: step2 dedup: %w", err)
	}
	result.Merged = merged

	// 3. Retroactive TTL + prune
	pruned, err := ms.migrateTypes_step3(ctx, dryRun)
	if err != nil {
		return nil, fmtErr("migrate: step3 ttl: %w", err)
	}
	result.Pruned += pruned

	// 4. Log compression
	compressed, err := ms.migrateTypes_step4(ctx, dryRun)
	if err != nil {
		return nil, fmtErr("migrate: step4 compress: %w", err)
	}
	result.Compressed = compressed

	return result, nil
}

// migrateTypes_step1 remaps legacy types to new tiers.
// Events with audit value (URL or "app-" pattern) become log; others are dropped.
func (ms *MemoryStore) migrateTypes_step1(ctx context.Context, dryRun bool) (retyped int, dropped int, err error) {
	// Remap non-event types
	for oldType, newType := range typeMigration {
		if dryRun {
			var count int
			err = ms.db.QueryRowContext(ctx,
				`SELECT count(*) FROM "memories" WHERE "type" = ?`, oldType,
			).Scan(&count)
			if err != nil {
				return 0, 0, err
			}
			retyped += count
		} else {
			result, execErr := ms.db.ExecContext(ctx,
				`UPDATE "memories" SET "type" = ? WHERE "type" = ?`,
				newType, oldType,
			)
			if execErr != nil {
				return 0, 0, execErr
			}
			n, _ := result.RowsAffected()
			retyped += int(n)
		}
	}

	// Handle event type: audit records → log, diary entries → drop
	eventRows, err := ms.db.QueryContext(ctx,
		`SELECT "id", "content" FROM "memories" WHERE "type" = 'event'`,
	)
	if err != nil {
		return retyped, 0, err
	}
	defer eventRows.Close()

	var toLog []string
	var toDrop []string
	for eventRows.Next() {
		var id, content string
		if err = eventRows.Scan(&id, &content); err != nil {
			return retyped, 0, err
		}
		if hasAuditValue(content) {
			toLog = append(toLog, id)
		} else {
			toDrop = append(toDrop, id)
		}
	}
	if err = eventRows.Err(); err != nil {
		return retyped, 0, err
	}

	if !dryRun {
		for _, id := range toLog {
			if _, execErr := ms.db.ExecContext(ctx,
				`UPDATE "memories" SET "type" = 'log' WHERE "id" = ?`, id,
			); execErr != nil {
				slog.Warn("llmem: migrate: failed to retype event to log", "id", id, "error", execErr)
			}
		}
		for _, id := range toDrop {
			if _, execErr := ms.db.ExecContext(ctx,
				`DELETE FROM "memories" WHERE "id" = ?`, id,
			); execErr != nil {
				slog.Warn("llmem: migrate: failed to drop diary event", "id", id, "error", execErr)
			}
		}
	}
	retyped += len(toLog)
	dropped += len(toDrop)

	return retyped, dropped, nil
}

// migrateTypes_step2 merges near-duplicate stable memories using text-based Jaccard similarity.
func (ms *MemoryStore) migrateTypes_step2(ctx context.Context, dryRun bool) (int, error) {
	const similarityThreshold = 0.85

	rows, err := ms.db.QueryContext(ctx,
		`SELECT "id", "content" FROM "memories" WHERE "type" = 'stable' ORDER BY "created_at" ASC`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type memEntry struct {
		id      string
		content string
		words   map[string]bool
	}
	var entries []memEntry
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return 0, err
		}
		entries = append(entries, memEntry{
			id:      id,
			content:  content,
			words:   tokenizeToSet(content),
		})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	merged := 0
	mergedIDs := make(map[string]bool)

	for i := 0; i < len(entries); i++ {
		if mergedIDs[entries[i].id] {
			continue
		}
		for j := i + 1; j < len(entries); j++ {
			if mergedIDs[entries[j].id] {
				continue
			}
			sim := jaccardSimilarity(entries[i].words, entries[j].words)
			if sim >= similarityThreshold {
				if !dryRun {
					// Merge j into i: keep the one with more content
					keepContent := entries[i].content
					if len(entries[j].content) > len(entries[i].content) {
						keepContent = entries[j].content
					}
					_, execErr := ms.db.ExecContext(ctx,
						`UPDATE "memories" SET "content" = ? WHERE "id" = ?`,
						keepContent, entries[i].id,
					)
					if execErr != nil {
						slog.Warn("llmem: migrate: merge: update keeper failed", "id", entries[i].id, "error", execErr)
						continue
					}
					// Delete the duplicate
					// Clean up relations first
					ms.db.ExecContext(ctx, `DELETE FROM "relations" WHERE "target_id" = ?`, entries[j].id)
					_, execErr = ms.db.ExecContext(ctx,
						`DELETE FROM "memories" WHERE "id" = ?`, entries[j].id,
					)
					if execErr != nil {
						slog.Warn("llmem: migrate: merge: delete duplicate failed", "id", entries[j].id, "error", execErr)
						continue
					}
				}
				mergedIDs[entries[j].id] = true
				merged++
			}
		}
	}

	return merged, nil
}

// migrateTypes_step3 computes valid_until for all memories based on their type
// and prunes memories that are already past their new TTL.
func (ms *MemoryStore) migrateTypes_step3(ctx context.Context, dryRun bool) (int, error) {
	now := time.Now().UTC()
	stableExpiry := now.AddDate(0, 0, TTLStableDays).Format(time.RFC3339)
	volatileExpiry := now.AddDate(0, 0, TTLVolatileDays).Format(time.RFC3339)
	logExpiry := now.AddDate(0, 0, TTLLLogDays).Format(time.RFC3339)

	type ttlEntry struct {
		date string
	}
	ttlMap := map[string]ttlEntry{
		"stable":   {date: stableExpiry},
		"volatile": {date: volatileExpiry},
		"log":      {date: logExpiry},
	}

	// Set valid_until for new tier types (if not pinned)
	for typ, ttl := range ttlMap {
		if dryRun {
			var count int
			err := ms.db.QueryRowContext(ctx,
				`SELECT count(*) FROM "memories" WHERE "type" = ? AND "pinned" = 0 AND ("valid_until" IS NULL OR "valid_until" > ?)`,
				typ, ttl.date,
			).Scan(&count)
			if err != nil {
				return 0, err
			}
			_ = count // not counting separately, just checking
		} else {
			_, execErr := ms.db.ExecContext(ctx,
				`UPDATE "memories" SET "valid_until" = ? WHERE "type" = ? AND "pinned" = 0`,
				ttl.date, typ,
			)
			if execErr != nil {
				return 0, execErr
			}
		}
	}

	// Prune expired
	if dryRun {
		var count int
		err := ms.db.QueryRowContext(ctx,
			`SELECT count(*) FROM "memories" WHERE "valid_until" IS NOT NULL AND "valid_until" < ? AND "pinned" = 0`,
			now.Format(time.RFC3339),
		).Scan(&count)
		if err != nil {
			return 0, err
		}
		return count, nil
	}

	return ms.PruneExpired(ctx)
}

// migrateTypes_step4 compresses log entries exceeding 200 words.
// Truncates to the first ~200 words with an ellipsis.
func (ms *MemoryStore) migrateTypes_step4(ctx context.Context, dryRun bool) (int, error) {
	const maxWords = 200

	rows, err := ms.db.QueryContext(ctx,
		`SELECT "id", "content" FROM "memories" WHERE "type" = 'log'`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	compressed := 0
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return 0, err
		}
		words := strings.Fields(content)
		if len(words) <= maxWords {
			continue
		}

		if !dryRun {
			truncated := strings.Join(words[:maxWords], " ") + "..."
			_, execErr := ms.db.ExecContext(ctx,
				`UPDATE "memories" SET "content" = ? WHERE "id" = ?`,
				truncated, id,
			)
			if execErr != nil {
				slog.Warn("llmem: migrate: compress failed", "id", id, "error", execErr)
				continue
			}
		}
		compressed++
	}

	return compressed, nil
}

// hasAuditValue determines if an event memory contains audit/dedup value.
// Heuristic: contains a URL (http/https) or an application ID pattern (app-NNNN).
func hasAuditValue(content string) bool {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") {
		return true
	}
	if strings.Contains(lower, "app-") {
		return true
	}
	// Check for common application tracking patterns
	if strings.Contains(lower, "req id") || strings.Contains(lower, "req-id") {
		return true
	}
	return false
}

// tokenizeToSet splits content into a set of lowercase word tokens.
func tokenizeToSet(content string) map[string]bool {
	words := strings.Fields(strings.ToLower(content))
	set := make(map[string]bool, len(words))
	for _, w := range words {
		// Strip basic punctuation
		w = strings.Trim(w, ".,;:!?\"'()[]{}")
		if w != "" {
			set[w] = true
		}
	}
	return set
}

// jaccardSimilarity computes the Jaccard similarity between two word sets.
// J(A,B) = |A ∩ B| / |A ∪ B|
func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	intersection := 0
	union := make(map[string]bool)
	for w := range a {
		union[w] = true
		if b[w] {
			intersection++
		}
	}
	for w := range b {
		union[w] = true
	}
	if len(union) == 0 {
		return 0.0
	}
	return float64(intersection) / float64(len(union))
}
