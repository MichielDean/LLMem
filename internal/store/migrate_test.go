package store

import (
	"context"
	"strings"
	"testing"
)

// newTestStoreWithLegacyTypes creates a store with both legacy and new types registered,
// simulating a pre-migration database that hasn't run migrate-types yet.
func newTestStoreWithLegacyTypes(t *testing.T) *MemoryStore {
	t.Helper()
	ms := newTestStore(t)
	for _, typ := range []string{"fact", "decision", "preference", "event", "project_state", "procedure", "conversation", "self_assessment"} {
		if err := ms.RegisterMemoryType(typ); err != nil {
			// Type may already be registered — that's fine
		}
	}
	return ms
}

func TestMigrateTypes_RetypeLegacyTypes(t *testing.T) {
	ms := newTestStoreWithLegacyTypes(t)
	ctx := context.Background()

	// Add memories with each legacy type
	legacyTypes := []string{"fact", "decision", "preference", "procedure", "conversation", "project_state"}
	for _, typ := range legacyTypes {
		_, err := ms.Add(ctx, AddParams{Type: typ, Content: "test " + typ})
		if err != nil {
			t.Fatalf("Add %s: %v", typ, err)
		}
	}

	result, err := ms.MigrateTypes(ctx, false)
	if err != nil {
		t.Fatalf("MigrateTypes: %v", err)
	}

	if result.Retyped != len(legacyTypes) {
		t.Errorf("expected %d retyped, got %d", len(legacyTypes), result.Retyped)
	}

	// Verify all are now new tier types
	for _, typ := range legacyTypes {
		var count int
		err := ms.db.QueryRowContext(ctx, `SELECT count(*) FROM "memories" WHERE "type" = ?`, typ).Scan(&count)
		if err != nil {
			t.Fatalf("count %s: %v", typ, err)
		}
		if count != 0 {
			t.Errorf("expected 0 memories with legacy type %q after migration, got %d", typ, count)
		}
	}
}

func TestMigrateTypes_EventToLogOrDrop(t *testing.T) {
	ms := newTestStoreWithLegacyTypes(t)
	ctx := context.Background()

	// Event with audit value (URL)
	_, _ = ms.Add(ctx, AddParams{Type: "event", Content: "Applied to Acme Corp https://example.com/jobs/123"})

	// Event with app ID
	_, _ = ms.Add(ctx, AddParams{Type: "event", Content: "Submitted app-0708 via Greenhouse"})

	// Event without audit value (diary)
	_, _ = ms.Add(ctx, AddParams{Type: "event", Content: "Worked on the dashboard today"})

	_, err := ms.MigrateTypes(ctx, false)
	if err != nil {
		t.Fatalf("MigrateTypes: %v", err)
	}

	// Should have retyped 2 to log and dropped 1
	var logCount int
	ms.db.QueryRowContext(ctx, `SELECT count(*) FROM "memories" WHERE "type" = 'log'`).Scan(&logCount)
	if logCount != 2 {
		t.Errorf("expected 2 log memories from events with audit value, got %d", logCount)
	}

	// Diary event should be gone
	var totalAfter int
	ms.db.QueryRowContext(ctx, `SELECT count(*) FROM "memories"`).Scan(&totalAfter)
	if totalAfter != 2 {
		t.Errorf("expected 2 memories after migration (2 log, 1 dropped), got %d", totalAfter)
	}
}

func TestMigrateTypes_Dedup(t *testing.T) {
	ms := newTestStoreWithLegacyTypes(t)
	ctx := context.Background()

	// Add near-duplicate stable memories (longer content for reliable Jaccard similarity)
	dupContents := []string{
		"Verify assumptions before committing to any code changes: deletions must be searched globally, data transforms need unit tests, UI events need manual click testing, trust boundaries require explicit verification",
		"Verify assumptions before committing to any code changes: deletions must be searched globally, data transforms need unit tests, UI events need manual click testing, trust boundaries require explicit verification",
		"Verify assumptions before committing to any code changes: deletions must be searched globally, data transforms need unit tests, UI interactions need manual click testing, trust boundaries require explicit verification",
	}
	for _, c := range dupContents {
		_, _ = ms.Add(ctx, AddParams{Type: "procedure", Content: c})
	}

	// Add a unique one
	uniqueID, _ := ms.Add(ctx, AddParams{Type: "procedure", Content: "Completely different content about gardening"})

	result, err := ms.MigrateTypes(ctx, false)
	if err != nil {
		t.Fatalf("MigrateTypes: %v", err)
	}

	if result.Merged < 2 {
		t.Errorf("expected at least 2 merged (3 near-duplicates), got %d", result.Merged)
	}

	// The unique one should survive
	m, _ := ms.Get(ctx, uniqueID, false)
	if m == nil {
		t.Error("expected unique memory to survive dedup")
	}
}

func TestMigrateTypes_RetroactiveTTL(t *testing.T) {
	ms := newTestStoreWithLegacyTypes(t)
	ctx := context.Background()

	// Add memories with old types (no TTL)
	id1, _ := ms.Add(ctx, AddParams{Type: "fact", Content: "test fact"})
	id2, _ := ms.Add(ctx, AddParams{Type: "project_state", Content: "test state"})

	// Verify no TTL before migration
	m1, _ := ms.Get(ctx, id1, false)
	if m1.ValidUntil != "" {
		t.Error("expected legacy memory to have no TTL before migration")
	}

	_, err := ms.MigrateTypes(ctx, false)
	if err != nil {
		t.Fatalf("MigrateTypes: %v", err)
	}

	// After migration, stable should have TTL
	m1After, _ := ms.Get(ctx, id1, false)
	if m1After.ValidUntil == "" {
		t.Error("expected migrated stable memory to have auto-TTL")
	}
	if m1After.Type != "stable" {
		t.Errorf("expected type 'stable', got %q", m1After.Type)
	}

	// Volatile should have TTL
	m2After, _ := ms.Get(ctx, id2, false)
	if m2After.ValidUntil == "" {
		t.Error("expected migrated volatile memory to have auto-TTL")
	}
	if m2After.Type != "volatile" {
		t.Errorf("expected type 'volatile', got %q", m2After.Type)
	}
}

func TestMigrateTypes_LogCompression(t *testing.T) {
	ms := newTestStoreWithLegacyTypes(t)
	ctx := context.Background()

	// Add an event with > 200 words
	var words []string
	for i := 0; i < 250; i++ {
		words = append(words, "word")
	}
	longContent := strings.Join(words, " ")
	id, _ := ms.Add(ctx, AddParams{Type: "event", Content: longContent + " https://example.com"})

	_, err := ms.MigrateTypes(ctx, false)
	if err != nil {
		t.Fatalf("MigrateTypes: %v", err)
	}

	m, _ := ms.Get(ctx, id, false)
	if m == nil {
		t.Fatal("expected log memory to survive compression")
	}
	if m.Type != "log" {
		t.Errorf("expected type 'log', got %q", m.Type)
	}
	// Should be truncated
	wordCount := len(strings.Fields(m.Content))
	if wordCount > 205 { // 200 + "..." allowance
		t.Errorf("expected content to be compressed to ~200 words, got %d", wordCount)
	}
}

func TestMigrateTypes_Idempotent(t *testing.T) {
	ms := newTestStoreWithLegacyTypes(t)
	ctx := context.Background()

	// Add some memories
	ms.Add(ctx, AddParams{Type: "fact", Content: "test fact"})
	ms.Add(ctx, AddParams{Type: "decision", Content: "test decision"})
	ms.Add(ctx, AddParams{Type: "project_state", Content: "test state"})

	// Run once
	_, err := ms.MigrateTypes(ctx, false)
	if err != nil {
		t.Fatalf("First MigrateTypes: %v", err)
	}

	// Capture state after first migration
	var countAfterFirst int
	ms.db.QueryRowContext(ctx, `SELECT count(*) FROM "memories"`).Scan(&countAfterFirst)

	// Run again
	result2, err := ms.MigrateTypes(ctx, false)
	if err != nil {
		t.Fatalf("Second MigrateTypes: %v", err)
	}

	// Should be no-op: no retypes, no merges, no prunes
	if result2.Retyped != 0 {
		t.Errorf("expected 0 retyped on second run, got %d", result2.Retyped)
	}
	if result2.Merged != 0 {
		t.Errorf("expected 0 merged on second run, got %d", result2.Merged)
	}

	// Count should be the same
	var countAfterSecond int
	ms.db.QueryRowContext(ctx, `SELECT count(*) FROM "memories"`).Scan(&countAfterSecond)
	if countAfterFirst != countAfterSecond {
		t.Errorf("expected same count after idempotent run, first=%d second=%d", countAfterFirst, countAfterSecond)
	}
}

func TestMigrateTypes_DryRun(t *testing.T) {
	ms := newTestStoreWithLegacyTypes(t)
	ctx := context.Background()

	// Add legacy type
	_, _ = ms.Add(ctx, AddParams{Type: "fact", Content: "test fact"})

	// Dry run should not modify
	result, err := ms.MigrateTypes(ctx, true)
	if err != nil {
		t.Fatalf("MigrateTypes dry-run: %v", err)
	}
	if result.Retyped == 0 {
		t.Error("expected dry-run to report retyped count > 0")
	}

	// Verify store is unchanged
	var factCount int
	ms.db.QueryRowContext(ctx, `SELECT count(*) FROM "memories" WHERE "type" = 'fact'`).Scan(&factCount)
	if factCount != 1 {
		t.Error("expected dry-run to leave 'fact' type unchanged")
	}
}

func TestMigrateTypes_AlreadyMigrated(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add memories already with new types
	_, _ = ms.Add(ctx, AddParams{Type: "stable", Content: "already stable"})
	_, _ = ms.Add(ctx, AddParams{Type: "volatile", Content: "already volatile"})

	result, err := ms.MigrateTypes(ctx, false)
	if err != nil {
		t.Fatalf("MigrateTypes: %v", err)
	}

	// Should be no-op
	if result.Retyped != 0 {
		t.Errorf("expected 0 retyped for already-migrated store, got %d", result.Retyped)
	}
}

func TestHasAuditValue(t *testing.T) {
	tests := []struct {
		content  string
		expected bool
	}{
		{"Applied to Acme https://example.com", true},
		{"Submitted app-0708", true},
		{"Worked on dashboard today", false},
		{"req id 12345", true},
		{"", false},
	}
	for _, tt := range tests {
		got := hasAuditValue(tt.content)
		if got != tt.expected {
			t.Errorf("hasAuditValue(%q) = %v, want %v", tt.content, got, tt.expected)
		}
	}
}

func TestJaccardSimilarity(t *testing.T) {
	a := tokenizeToSet("the quick brown fox jumps")
	b := tokenizeToSet("the quick brown fox leaps")
	// 4 common words, 6 total unique → 4/6 = 0.667
	sim := jaccardSimilarity(a, b)
	if sim < 0.6 || sim > 0.7 {
		t.Errorf("expected jaccard ~0.667, got %f", sim)
	}

	// Identical sets → 1.0
	identical := jaccardSimilarity(a, a)
	if identical != 1.0 {
		t.Errorf("expected jaccard 1.0 for identical sets, got %f", identical)
	}

	// Disjoint sets → 0.0
	c := tokenizeToSet("completely different words here")
	disjoint := jaccardSimilarity(a, c)
	if disjoint > 0.1 {
		t.Errorf("expected jaccard ~0.0 for disjoint sets, got %f", disjoint)
	}
}
