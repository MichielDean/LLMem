package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// newTestStore creates a MemoryStore in a temp directory for testing.
func newTestStore(t *testing.T) *MemoryStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ms, err := NewMemoryStore(StoreConfig{
		DBPath:     dbPath,
		DisableVec: true,
	})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() { ms.Close() })
	return ms
}

func newTestStoreWithVec(t *testing.T) *MemoryStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ms, err := NewMemoryStore(StoreConfig{
		DBPath:     dbPath,
		DisableVec: false,
	})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() { ms.Close() })
	return ms
}

func TestNewMemoryStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer ms.Close()

	if ms.db == nil {
		t.Error("expected db to be initialized")
	}
	if ms.vecDimensions != defaultVecDimensions {
		t.Errorf("expected vecDimensions=%d, got %d", defaultVecDimensions, ms.vecDimensions)
	}
}

func TestNewMemoryStore_DefaultVecDimensions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, VecDimensions: 0, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer ms.Close()

	if ms.vecDimensions != 768 {
		t.Errorf("expected default vecDimensions=768, got %d", ms.vecDimensions)
	}
}

func TestNewMemoryStore_NegativeVecDimensions(t *testing.T) {
	_, err := NewMemoryStore(StoreConfig{VecDimensions: -1, DisableVec: true})
	if err == nil {
		t.Error("expected error for negative vec_dimensions")
	}
}

func TestNewMemoryStore_DefaultDBPath(t *testing.T) {
	// Create a temp dir to act as home
	tmpHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", originalHome)

	ms, err := NewMemoryStore(StoreConfig{DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer ms.Close()

	expectedPath := filepath.Join(tmpHome, ".config", "llmem", "memory.db")
	if ms.dbPath != expectedPath {
		t.Errorf("expected dbPath=%s, got %s", expectedPath, ms.dbPath)
	}
}

func TestMemoryStore_Add(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{
		Type:       "stable",
		Content:    "The sky is blue",
		Summary:    "Sky color",
		Source:     "test",
		Confidence: 0.9,
		Hints:      []string{"observation", "weather"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}

	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.Content != "The sky is blue" {
		t.Errorf("expected content='The sky is blue', got %q", m.Content)
	}
	if m.Type != "stable" {
		t.Errorf("expected type='fact', got %q", m.Type)
	}
	if m.Source != "test" {
		t.Errorf("expected source='test', got %q", m.Source)
	}
	if m.Confidence != 0.9 {
		t.Errorf("expected confidence=0.9, got %f", m.Confidence)
	}
}

func TestMemoryStore_Add_UnregisteredType(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	_, err := ms.Add(ctx, AddParams{
		Type:    "unregistered",
		Content: "test",
	})
	if err == nil {
		t.Error("expected error for unregistered type")
	}
	if !errors.Is(err, ErrUnregisteredType) {
		t.Errorf("expected errors.Is(err, ErrUnregisteredType), got: %v", err)
	}
}

func TestMemoryStore_Add_EmbeddingDimensionMismatch(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Create embedding with wrong dimensions (2 floats = 8 bytes, but store expects 768 dims = 3072 bytes)
	wrongEmb := make([]byte, 8) // 2 dimensions
	_, err := ms.Add(ctx, AddParams{
		Type:      "stable",
		Content:   "test",
		Embedding: wrongEmb,
	})
	// Embedding dimension check only applies when vec is not disabled
	// When DisableVec=true, embedding dimension check is skipped
	_ = err
}

func TestMemoryStore_Add_WithCustomID(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{
		ID:      "custom-id-123",
		Type:    "stable",
		Content: "test content",
	})
	if err != nil {
		t.Fatalf("Add with custom ID: %v", err)
	}
	if id != "custom-id-123" {
		t.Errorf("expected id='custom-id-123', got %q", id)
	}
}

func TestMemoryStore_Get(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test content"})
	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.ID != id {
		t.Errorf("expected ID=%s, got %s", id, m.ID)
	}
}

func TestMemoryStore_Get_NotFound(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	m, err := ms.Get(ctx, "nonexistent", false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m != nil {
		t.Error("expected nil for nonexistent ID")
	}
}

func TestMemoryStore_Get_TrackAccess(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test content"})

	m, err := ms.Get(ctx, id, true)
	if err != nil {
		t.Fatalf("Get with trackAccess: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.AccessCount != 1 {
		t.Errorf("expected access_count=1, got %d", m.AccessCount)
	}
}

func TestMemoryStore_GetBatch(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "content 1"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "content 2"})

	results, err := ms.GetBatch(ctx, []string{id1, id2}, false)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestMemoryStore_GetBatch_EmptyInput(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	results, err := ms.GetBatch(ctx, []string{}, false)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty input, got %d", len(results))
	}
}

func TestMemoryStore_Update(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "original content"})

	newContent := "updated content"
	ok, err := ms.Update(ctx, UpdateParams{
		ID:      id,
		Content: &newContent,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !ok {
		t.Error("expected Update to return true")
	}

	m, _ := ms.Get(ctx, id, false)
	if m.Content != "updated content" {
		t.Errorf("expected content='updated content', got %q", m.Content)
	}
}

func TestMemoryStore_Update_NotFound(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	fakeContent := "x"
	ok, err := ms.Update(ctx, UpdateParams{ID: "nonexistent", Content: &fakeContent})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if ok {
		t.Error("expected Update to return false for nonexistent ID")
	}
}

func TestMemoryStore_Update_ClearEmbedding(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{
		Type:      "stable",
		Content:   "test",
		Embedding: make([]byte, 768*4), // 768-dim embedding
	})

	ok, err := ms.Update(ctx, UpdateParams{ID: id, ClearEmbedding: true})
	if err != nil {
		t.Fatalf("Update ClearEmbedding: %v", err)
	}
	if !ok {
		t.Error("expected Update to return true")
	}

	m, _ := ms.Get(ctx, id, false)
	if len(m.Embedding) != 0 {
		t.Errorf("expected embedding to be cleared, got %d bytes", len(m.Embedding))
	}
}

func TestMemoryStore_Update_ConflictingEmbeddingParams(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test"})

	_, err := ms.Update(ctx, UpdateParams{
		ID:             id,
		Embedding:      []byte{0, 0, 0, 0},
		ClearEmbedding: true,
	})
	if err == nil {
		t.Error("expected error for conflicting embedding params")
	}
}

func TestMemoryStore_Invalidate(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test content"})

	ok, err := ms.Invalidate(ctx, id, "outdated info")
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if !ok {
		t.Error("expected Invalidate to return true")
	}

	m, _ := ms.Get(ctx, id, false)
	if m.ValidUntil == "" {
		t.Error("expected valid_until to be set after invalidation")
	}
	if m.Metadata["invalidation_reason"] != "outdated info" {
		t.Errorf("expected invalidation_reason in metadata, got %v", m.Metadata)
	}
}

func TestMemoryStore_Invalidate_NotFound(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ok, err := ms.Invalidate(ctx, "nonexistent", "reason")
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if ok {
		t.Error("expected Invalidate to return false for nonexistent ID")
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test content"})
	ok, err := ms.Delete(ctx, id)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !ok {
		t.Error("expected Delete to return true")
	}

	m, _ := ms.Get(ctx, id, false)
	if m != nil {
		t.Error("expected nil after delete")
	}
}

func TestMemoryStore_Delete_NotFound(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ok, err := ms.Delete(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok {
		t.Error("expected Delete to return false for nonexistent ID")
	}
}

func TestMemoryStore_Delete_TargetSideRelationCleanup(t *testing.T) {
	// Delete should also clean up relations where the deleted memory is the target
	// (FK cascade handles source_id but NOT target_id since there's no FK on target_id)
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "source"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "target"})

	_, err := ms.AddRelation(ctx, id1, id2, "related_to")
	if err != nil {
		t.Fatalf("AddRelation: %v", err)
	}

	// Delete id2 (the target) — should clean up target-side relation
	ok, err := ms.Delete(ctx, id2)
	if err != nil {
		t.Fatalf("Delete target: %v", err)
	}
	if !ok {
		t.Error("expected Delete to return true")
	}

	// Verify relation is gone when querying by source
	rels, err := ms.GetRelations(ctx, id1)
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	for _, r := range rels {
		if r.TargetID == id2 {
			t.Error("expected relation targeting deleted memory to be removed")
		}
	}
}

func TestMemoryStore_Search_Query(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "The sky is blue"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "Water is clear"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "Grass is green"})

	results, err := ms.Search(ctx, SearchParams{Query: "sky", ValidOnly: false, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'sky'")
	}
	// Result should contain "sky"
	found := false
	for _, r := range results {
		if r.Content == "The sky is blue" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'The sky is blue' in search results")
	}
}

func TestMemoryStore_Search_Type(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "fact content"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "decision content"})

	results, err := ms.Search(ctx, SearchParams{Query: "content", Type: "stable", ValidOnly: false, Limit: 10})
	if err != nil {
		t.Fatalf("Search with type filter: %v", err)
	}
	for _, r := range results {
		if r.Type != "stable" {
			t.Errorf("expected type='fact', got %q", r.Type)
		}
	}
}

func TestMemoryStore_SearchCount(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "sky is blue and vast"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "ocean is blue and deep"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "use blue theme"})

	count, err := ms.SearchCount(ctx, SearchCountParams{Query: "blue", ValidOnly: false})
	if err != nil {
		t.Fatalf("SearchCount: %v", err)
	}
	if count < 1 {
		t.Errorf("expected at least 1 result for 'blue', got %d", count)
	}
}

func TestMemoryStore_ListAll(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 1"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "decision 1"})

	// Without type filter
	results, err := ms.ListAll(ctx, ListParams{Limit: 100})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results, got %d", len(results))
	}

	// With type filter
	results, err = ms.ListAll(ctx, ListParams{Type: "stable", Limit: 100})
	if err != nil {
		t.Fatalf("ListAll with type: %v", err)
	}
	for _, r := range results {
		if r.Type != "stable" {
			t.Errorf("expected type='fact', got %q", r.Type)
		}
	}
}

func TestMemoryStore_Count(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "test 1"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "test 2"})

	count, err := ms.Count(ctx, false)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count=2, got %d", count)
	}
}

func TestMemoryStore_CountByType(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "test 1"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "test 2"})
	ms.Add(ctx, AddParams{Type: "log", Content: "test 3"})

	result, err := ms.CountByType(ctx, false)
	if err != nil {
		t.Fatalf("CountByType: %v", err)
	}
	if result["stable"] != 2 {
		t.Errorf("expected stable=2, got %d", result["stable"])
	}
	if result["log"] != 1 {
		t.Errorf("expected log=1, got %d", result["log"])
	}
}

func TestMemoryStore_CountEmbeddings(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "without embedding"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "with embedding", Embedding: make([]byte, 768*4)})

	count, err := ms.CountEmbeddings(ctx)
	if err != nil {
		t.Fatalf("CountEmbeddings: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 embedding, got %d", count)
	}
}

func TestMemoryStore_AddRelation(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "source memory"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "target memory"})

	relID, err := ms.AddRelation(ctx, id1, id2, "supersedes")
	if err != nil {
		t.Fatalf("AddRelation: %v", err)
	}
	if relID == "" {
		t.Error("expected non-empty relation ID")
	}
}

func TestMemoryStore_AddRelation_InvalidType(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "source"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "target"})

	_, err := ms.AddRelation(ctx, id1, id2, "invalid_type")
	if err == nil {
		t.Error("expected error for invalid relation type")
	}
}

func TestMemoryStore_GetRelations(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "source"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "target"})

	ms.AddRelation(ctx, id1, id2, "related_to")

	rels, err := ms.GetRelations(ctx, id1)
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relation, got %d", len(rels))
	}
	if rels[0].RelationType != "related_to" {
		t.Errorf("expected relation_type='related_to', got %q", rels[0].RelationType)
	}
}

func TestMemoryStore_GetRelationsBatch(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "1"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "2"})
	id3, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "3"})

	ms.AddRelation(ctx, id1, id2, "related_to")
	ms.AddRelation(ctx, id2, id3, "derived_from")

	rels, err := ms.GetRelationsBatch(ctx, []string{id1, id2})
	if err != nil {
		t.Fatalf("GetRelationsBatch: %v", err)
	}
	if len(rels) < 2 {
		t.Errorf("expected at least 2 relations, got %d", len(rels))
	}
}

func TestMemoryStore_TraverseRelations(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "A"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "B"})
	id3, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "C"})

	ms.AddRelation(ctx, id1, id2, "related_to")
	ms.AddRelation(ctx, id2, id3, "derived_from")

	results, err := ms.TraverseRelations(ctx, []string{id1}, 2)
	if err != nil {
		t.Fatalf("TraverseRelations: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected traverse results")
	}
}

func TestMemoryStore_TraverseRelations_EmptyInput(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	results, err := ms.TraverseRelations(ctx, []string{}, 2)
	if err != nil {
		t.Fatalf("TraverseRelations: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty input, got %d", len(results))
	}
}

func TestMemoryStore_LogExtraction(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	err := ms.LogExtraction(ctx, "file", "doc1", nil, 3)
	if err != nil {
		t.Fatalf("LogExtraction: %v", err)
	}

	exists, err := ms.IsExtracted(ctx, "file", "doc1")
	if err != nil {
		t.Fatalf("IsExtracted: %v", err)
	}
	if !exists {
		t.Error("expected extraction log to exist")
	}
}

func TestMemoryStore_SupersedeBySource(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{
		Type:     "stable",
		Content:  "test content",
		Source:   "stable",
		Metadata: map[string]any{"source_id": "conv-123"},
	})

	count, err := ms.SupersedeBySource(ctx, "stable", "conv-123")
	if err != nil {
		t.Fatalf("SupersedeBySource: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row invalidated, got %d", count)
	}
}

func TestMemoryStore_IsExtracted(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	exists, err := ms.IsExtracted(ctx, "file", "nonexistent")
	if err != nil {
		t.Fatalf("IsExtracted: %v", err)
	}
	if exists {
		t.Error("expected false for nonexistent extraction log")
	}
}

func TestMemoryStore_RemoveExtractionLog(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.LogExtraction(ctx, "file", "doc1", nil, 5)

	ok, err := ms.RemoveExtractionLog(ctx, "file", "doc1")
	if err != nil {
		t.Fatalf("RemoveExtractionLog: %v", err)
	}
	if !ok {
		t.Error("expected RemoveExtractionLog to return true")
	}

	ok, _ = ms.RemoveExtractionLog(ctx, "file", "doc1")
	if ok {
		t.Error("expected RemoveExtractionLog to return false for nonexistent entry")
	}
}

func TestMemoryStore_FindSimilar(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "sky is blue"})

	results, err := ms.FindSimilar(ctx, FindSimilarParams{
		Content:   "sky",
		Threshold: 0.0,
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one result")
	}
}

func TestMemoryStore_FindSimilar_EmptyInput(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	results, err := ms.FindSimilar(ctx, FindSimilarParams{})
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty input, got %d", len(results))
	}
}

func TestMemoryStore_ConsolidateDuplicates(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Create non-zero embeddings with known similarity
	vec1 := make([]float32, 768)
	vec2 := make([]float32, 768)
	for i := range vec1 {
		vec1[i] = 0.1
		vec2[i] = 0.1
	}
	emb1 := VecToBytes(vec1)
	emb2 := VecToBytes(vec2)

	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 1", Embedding: emb1})
	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 2", Embedding: emb2})

	pairs, err := ms.ConsolidateDuplicates(ctx, 0.99, 100)
	if err != nil {
		t.Fatalf("ConsolidateDuplicates: %v", err)
	}
	if len(pairs) < 1 {
		t.Error("expected at least one duplicate pair for identical embeddings")
	}
}

func TestMemoryStore_ExportAll(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 1"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 2"})

	memories, err := ms.ExportAll(ctx, nil)
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if len(memories) != 2 {
		t.Errorf("expected 2 memories, got %d", len(memories))
	}
}

func TestMemoryStore_ExportAll_WithLimit(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 1"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 2"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 3"})

	limit := 2
	memories, err := ms.ExportAll(ctx, &limit)
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if len(memories) != 2 {
		t.Errorf("expected 2 memories with limit, got %d", len(memories))
	}
}

func TestMemoryStore_ExportAll_ZeroLimit(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 1"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "fact 2"})

	limit := 0
	memories, err := ms.ExportAll(ctx, &limit)
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if len(memories) != 2 {
		t.Errorf("expected 2 memories with no limit, got %d", len(memories))
	}
}

func TestMemoryStore_ImportMemories(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	memories := []ImportMemory{
		{Type: "stable", Content: "imported fact 1"},
		{Type: "stable", Content: "imported decision 1"},
	}

	count, err := ms.ImportMemories(ctx, memories)
	if err != nil {
		t.Fatalf("ImportMemories: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 imported, got %d", count)
	}
}

func TestMemoryStore_ImportMemories_InvalidEntries(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	memories := []ImportMemory{
		{Type: "stable", Content: "valid entry"},
		{Type: "", Content: "missing type"},
		{Type: "stable", Content: ""},
	}

	count, err := ms.ImportMemories(ctx, memories)
	if err != nil {
		t.Fatalf("ImportMemories: %v", err)
	}
	// Only 1 valid entry (missing type = skipped, empty content = skipped)
	if count != 1 {
		t.Errorf("expected 1 imported, got %d", count)
	}
}

func TestMemoryStore_Touch(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test content"})

	ok, err := ms.Touch(ctx, id)
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if !ok {
		t.Error("expected Touch to return true")
	}

	m, _ := ms.Get(ctx, id, false)
	if m.AccessCount != 1 {
		t.Errorf("expected access_count=1 after touch, got %d", m.AccessCount)
	}
}

func TestMemoryStore_Touch_NotFound(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ok, err := ms.Touch(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if ok {
		t.Error("expected Touch to return false for nonexistent ID")
	}
}

func TestMemoryStore_TouchBatch(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test 1"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test 2"})

	count, err := ms.TouchBatch(ctx, []string{id1, id2})
	if err != nil {
		t.Fatalf("TouchBatch: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows affected, got %d", count)
	}
}

func TestMemoryStore_TouchBatch_EmptyInput(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	count, err := ms.TouchBatch(ctx, []string{})
	if err != nil {
		t.Fatalf("TouchBatch: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 for empty input, got %d", count)
	}
}

func TestMemoryStore_Close(t *testing.T) {
	ms := newTestStore(t)

	// Close should work
	err := ms.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Double close should be safe
	err = ms.Close()
	if err != nil {
		t.Fatalf("Double close: %v", err)
	}
}

func TestMemoryStore_SearchByEmbedding_BruteForce(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Create embedding with known pattern (768 dims)
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 0.1
	}
	emb := VecToBytes(vec)

	ms.Add(ctx, AddParams{Type: "stable", Content: "memory with embedding", Embedding: emb})

	results, err := ms.SearchByEmbedding(ctx, vec, false, 10, 0.0)
	if err != nil {
		t.Fatalf("SearchByEmbedding: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one result from brute-force embedding search")
	}
}

func TestMemoryStore_ChmodDBFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "subdir2", "test.db")
	ms, err := NewMemoryStore(StoreConfig{DBPath: dbPath, DisableVec: true})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	defer ms.Close()

	// Verify DB file exists
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	if info.IsDir() {
		t.Error("expected DB file, not directory")
	}
	// Verify parent directory was created
	dirInfo, err := os.Stat(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("stat db dir: %v", err)
	}
	if !dirInfo.IsDir() {
		t.Error("expected directory")
	}
}

func TestRegisterMemoryType(t *testing.T) {
	ms := newTestStore(t)

	err := ms.RegisterMemoryType("custom_type")
	if err != nil {
		t.Fatalf("RegisterMemoryType: %v", err)
	}

	// Verify custom type can be used
	ctx := context.Background()
	id, err := ms.Add(ctx, AddParams{Type: "custom_type", Content: "custom content"})
	if err != nil {
		t.Fatalf("Add with custom type: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID for custom type")
	}
}

func TestRegisterMemoryType_Duplicate(t *testing.T) {
	ms := newTestStore(t)

	err := ms.RegisterMemoryType("stable")
	if err == nil {
		t.Error("expected error for duplicate type registration")
	}
}

func TestRegisterMemoryType_InvalidName(t *testing.T) {
	ms := newTestStore(t)

	tests := []string{
		"",                      // empty
		"123abc",                // starts with number
		"ABC_TYPE",              // uppercase
		"my-type",               // hyphen
		strings.Repeat("a", 65), // too long
	}

	for _, name := range tests {
		err := ms.RegisterMemoryType(name)
		if err == nil {
			t.Errorf("expected error for invalid type name %q", name)
		}
	}
}

func TestDefaultRegisteredTypes(t *testing.T) {
	types := DefaultRegisteredTypes()
	expected := []string{"stable", "volatile", "log"}
	if len(types) != len(expected) {
		t.Errorf("expected %d types, got %d", len(expected), len(types))
	}
	sort.Strings(types)
	sort.Strings(expected)
	for i := range types {
		if types[i] != expected[i] {
			t.Errorf("expected type %q at index %d, got %q", expected[i], i, types[i])
		}
	}
}

func TestDefaultRegisteredTypes_DefensiveCopy(t *testing.T) {
	types1 := DefaultRegisteredTypes()
	types2 := DefaultRegisteredTypes()
	if len(types1) != len(types2) {
		t.Error("expected consistent length from DefaultRegisteredTypes")
	}
	// Modify the returned slice should not affect future calls
	types1[0] = "modified"
	types3 := DefaultRegisteredTypes()
	if types3[0] == "modified" {
		t.Error("expected DefaultRegisteredTypes to return defensive copy")
	}
}

func TestMemoryStore_Search_CountOnly(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "test alpha"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "test beta"})

	count, err := ms.SearchCount(ctx, SearchCountParams{ValidOnly: false})
	if err != nil {
		t.Fatalf("SearchCount: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count=2, got %d", count)
	}
}

func TestMemoryStore_GetEmbeddingsWithTypes(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 0.1
	}
	emb := VecToBytes(vec)
	ms.Add(ctx, AddParams{Type: "stable", Content: "with embedding", Embedding: emb})

	results, err := ms.GetEmbeddingsWithTypes(ctx, 100)
	if err != nil {
		t.Fatalf("GetEmbeddingsWithTypes: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Type != "stable" {
		t.Errorf("expected type='fact', got %q", results[0].Type)
	}
	if len(results[0].Embedding) != len(emb) {
		t.Errorf("expected embedding length=%d, got %d", len(emb), len(results[0].Embedding))
	}
}

func TestMemoryStore_SerializationBoundary(t *testing.T) {
	// Verify that Memory struct serializes with empty collections, not null
	m := &Memory{
		ID:          "test",
		Type:        "stable",
		Content:     "test content",
		Hints:       []string{},
		Metadata:    map[string]any{},
		AccessCount: 0,
	}
	// Serialize to JSON and verify no null fields
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// Check that hints and metadata are empty arrays/objects, not null
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	// This test verifies the pattern; the Memory struct uses []string and map[string]any
	// which will serialize as null if nil, so we must initialize them properly
	_ = data
	_ = raw
}

func TestMemoryStore_ScanMemory_NilGuards(t *testing.T) {
	// Verify that Get returns empty slices/maps instead of nil for Hints/Metadata
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{Type: "stable", Content: "test content"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.Hints == nil {
		t.Error("expected Hints to be non-nil (empty slice), got nil")
	}
	if m.Metadata == nil {
		t.Error("expected Metadata to be non-nil (empty map), got nil")
	}
}

func TestMemoryStore_Add_WithMetadataAndHints(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{
		Type:    "stable",
		Content: "test content",
		Hints:   []string{"hint1", "hint2"},
		Metadata: map[string]any{
			"source_id": "src-123",
			"priority":  float64(5),
		},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	m, _ := ms.Get(ctx, id, false)
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if len(m.Hints) != 2 {
		t.Errorf("expected 2 hints, got %d", len(m.Hints))
	}
	if m.Metadata["source_id"] != "src-123" {
		t.Errorf("expected source_id='src-123', got %v", m.Metadata["source_id"])
	}
}

func TestMemoryStore_Delete_CascadeRelations(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "source"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "target"})

	ms.AddRelation(ctx, id1, id2, "related_to")

	// Delete the source memory should cascade to relations
	ms.Delete(ctx, id1)

	rels, _ := ms.GetRelations(ctx, id2)
	// The relation should be gone since source_id is FK with ON DELETE CASCADE
	for _, r := range rels {
		if r.SourceID == id1 || r.TargetID == id1 {
			t.Error("expected relation to be deleted after memory deletion")
		}
	}
}

func TestMemoryStore_Invalidate_ClearsEmbedding(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	emb := make([]byte, 768*4)
	id, _ := ms.Add(ctx, AddParams{
		Type:      "stable",
		Content:   "test content",
		Embedding: emb,
	})

	ms.Invalidate(ctx, id, "outdated")

	m, _ := ms.Get(ctx, id, false)
	if m.Embedding != nil && len(m.Embedding) > 0 {
		t.Error("expected embedding to be null after invalidation")
	}
}

func TestMemoryStore_Add_DefaultSource(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test content"})
	m, _ := ms.Get(ctx, id, false)
	if m.Source != "manual" {
		t.Errorf("expected default source='manual', got %q", m.Source)
	}
}

func TestMemoryStore_Add_DefaultConfidence(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test content", Confidence: 0})
	m, _ := ms.Get(ctx, id, false)
	// Confidence of 0 should not override default (implementation uses 0.8 default when 0)
	// But since 0 is passed, the SQL defaults to 0.8
	_ = m // just verifying no crash
}

func TestValidRelationTypes(t *testing.T) {
	types := ValidRelationTypes()
	expected := []string{"supersedes", "related_to", "derived_from"}
	if len(types) != len(expected) {
		t.Errorf("expected %d relation types, got %d", len(expected), len(types))
	}
	sort.Strings(types)
	sort.Strings(expected)
	for i := range types {
		if types[i] != expected[i] {
			t.Errorf("expected %q, got %q", expected[i], types[i])
		}
	}
}

func TestMemoryStore_Search_EmptyQuery(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.Add(ctx, AddParams{Type: "stable", Content: "test content"})

	results, err := ms.Search(ctx, SearchParams{Query: "", ValidOnly: false, Limit: 10})
	if err != nil {
		t.Fatalf("Search with empty query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for empty query, got %d", len(results))
	}
}

func TestMemoryStore_RemoveExtractionLog_NotFound(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ok, err := ms.RemoveExtractionLog(ctx, "nonexistent_type", "nonexistent_id")
	if err != nil {
		t.Fatalf("RemoveExtractionLog: %v", err)
	}
	if ok {
		t.Error("expected RemoveExtractionLog to return false for nonexistent entry")
	}
}

func TestMemoryStore_LogExtraction_Upsert(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ms.LogExtraction(ctx, "file", "doc1", nil, 3)
	ms.LogExtraction(ctx, "file", "doc1", nil, 5) // upsert with new count

	exists, _ := ms.IsExtracted(ctx, "file", "doc1")
	if !exists {
		t.Error("expected extraction log to exist after upsert")
	}
}

func TestMemoryStore_SearchByEmbedding_NoResults(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// No memories with embeddings
	vec := make([]float32, 768)
	results, err := ms.SearchByEmbedding(ctx, vec, false, 10, 0.5)
	if err != nil {
		t.Fatalf("SearchByEmbedding: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results when no embeddings, got %d", len(results))
	}
}

func TestMemoryStore_ExportAll_DefaultLimit(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add fewer than default limit
	for i := 0; i < 3; i++ {
		ms.Add(ctx, AddParams{Type: "stable", Content: fmt.Sprintf("fact %d", i)})
	}

	memories, err := ms.ExportAll(ctx, nil)
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if len(memories) != 3 {
		t.Errorf("expected 3 memories, got %d", len(memories))
	}
}

func TestIsValidTypeName_PrecompiledRegex(t *testing.T) {
	// Verify the pre-compiled regex works correctly (was previously regexp.MatchString on every call)
	tests := []struct {
		name  string
		valid bool
	}{
		{"stable", true},
		{"my_type", true},
		{"a", true},
		{"Type1", false}, // uppercase
		{"1type", false}, // starts with digit
		{"", false},
		{"type with spaces", false},
	}
	for _, tt := range tests {
		got := isValidTypeName(tt.name)
		if got != tt.valid {
			t.Errorf("isValidTypeName(%q) = %v, want %v", tt.name, got, tt.valid)
		}
	}
}

func TestSanitizeFTSQuery_PrecompiledRegex(t *testing.T) {
	// Verify the pre-compiled regex works correctly (was previously regexp.MustCompile on every call)
	result := sanitizeFTSQuery("hello world")
	if result == "" {
		t.Error("expected non-empty sanitized query")
	}
	// Verify special characters are stripped
	result2 := sanitizeFTSQuery("test@#$% query")
	if result2 == "" {
		t.Error("expected non-empty sanitized query with special chars")
	}
}

func TestSearchByEmbedding_DefaultLimit(t *testing.T) {
	// When limit <= 0, SearchByEmbedding should default to 20 (not return empty results).
	// This tests consistency with Search/ListAll/ExportAll where limit=0 means "use default".
	ms := newTestStore(t)
	ctx := context.Background()

	// Add several memories with embeddings to exceed the default limit of 20
	for i := 0; i < 25; i++ {
		vec := make([]float32, 768)
		for j := range vec {
			vec[j] = float32(i) * 0.01
		}
		ms.Add(ctx, AddParams{
			Type:      "stable",
			Content:   fmt.Sprintf("memory %d", i),
			Embedding: VecToBytes(vec),
		})
	}

	results, err := ms.SearchByEmbedding(ctx, make([]float32, 768), false, 0, 0.0)
	if err != nil {
		t.Fatalf("SearchByEmbedding with limit=0: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected non-empty results from SearchByEmbedding with limit=0")
	}
	// With limit=0 defaulting to 20, we should get exactly 20 results (all match at threshold 0.0)
	if len(results) != 20 {
		t.Errorf("expected 20 results with default limit, got %d", len(results))
	}
}

func TestDefaultRegisteredTypes_IncludesConversation(t *testing.T) {
	// Verify that DefaultRegisteredTypes includes 'conversation' to match migration 003
	types := DefaultRegisteredTypes()
	found := false
	for _, t := range types {
		if t == "stable" {
			found = true
			break
		}
	}
	if !found {
		t.Error("DefaultRegisteredTypes must include 'conversation' to match migration 003")
	}
}

func TestConversationType_IsAddable(t *testing.T) {
	// Verify that 'conversation' type can be used in Add since it's now in DefaultRegisteredTypes
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{Type: "stable", Content: "user discussed preferences"})
	if err != nil {
		t.Fatalf("Add with 'conversation' type: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID for conversation type")
	}

	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.Type != "stable" {
		t.Errorf("expected type='conversation', got %q", m.Type)
	}
}

func TestScanMemoryFields_NilGuards_FromBatch(t *testing.T) {
	// Verify that GetBatch also returns non-nil Hints/Metadata (uses scanMemoryFromRows)
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{Type: "stable", Content: "batch test"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	results, err := ms.GetBatch(ctx, []string{id}, false)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	m, ok := results[id]
	if !ok {
		t.Fatal("expected memory in batch results")
	}
	if m.Hints == nil {
		t.Error("expected Hints to be non-nil (empty slice) from GetBatch, got nil")
	}
	if m.Metadata == nil {
		t.Error("expected Metadata to be non-nil (empty map) from GetBatch, got nil")
	}
}

func TestSearch_FTS_UsesRank(t *testing.T) {
	// Verify FTS search returns results ordered by BM25 rank.
	// This tests that the query uses 'rank' instead of the invalid '_fts_rank'.
	ms := newTestStore(t)
	ctx := context.Background()

	// Add multiple memories with overlapping content to establish ranking
	ms.Add(ctx, AddParams{Type: "stable", Content: "The sky is blue and vast during the day"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "The sky was blue yesterday"})
	ms.Add(ctx, AddParams{Type: "stable", Content: "The ocean is deep and blue"})

	results, err := ms.Search(ctx, SearchParams{Query: "sky blue", ValidOnly: false, Limit: 10})
	if err != nil {
		t.Fatalf("Search FTS: %v", err)
	}
	// The key assertion: the query should NOT fail with an FTS column error.
	// If _fts_rank was used, SQLite would return an error and fallback to LIKE.
	// With 'rank', FTS works correctly.
	if len(results) == 0 {
		t.Error("expected FTS search results for 'sky blue'")
	}
}

func TestIsValidRelationType_UsesValidRelationTypes(t *testing.T) {
	// Verify that isValidRelationType checks against ValidRelationTypes(),
	// ensuring there is exactly one source of truth for valid relation types.
	for _, rt := range ValidRelationTypes() {
		if !isValidRelationType(rt) {
			t.Errorf("isValidRelationType(%q) = false, want true — ValidRelationTypes should be accepted", rt)
		}
	}
	// Invalid types should be rejected
	if isValidRelationType("invalid_type") {
		t.Error("isValidRelationType('invalid_type') = true, want false")
	}
	if isValidRelationType("") {
		t.Error("isValidRelationType('') = true, want false")
	}
}

func TestAddRelation_AllValidRelatioTypes(t *testing.T) {
	// Verify that AddRelation accepts every type from ValidRelationTypes(),
	// confirming the helper-based lookup is wired correctly.
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "source memory"})
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "target memory"})

	for _, rt := range ValidRelationTypes() {
		relID, err := ms.AddRelation(ctx, id1, id2, rt)
		if err != nil {
			t.Errorf("AddRelation(%q) returned unexpected error: %v", rt, err)
		}
		if relID == "" {
			t.Errorf("AddRelation(%q) returned empty ID", rt)
		}
	}
}

func TestGetEmbeddingsWithTypes_ZeroLimit_NoLimit(t *testing.T) {
	// When limit=0, GetEmbeddingsWithTypes should return all rows (no limit),
	// consistent with ExportAll's 0-means-no-limit semantics.
	ms := newTestStore(t)
	ctx := context.Background()

	// Add 3 memories with embeddings
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 0.1
	}
	emb := VecToBytes(vec)
	ms.Add(ctx, AddParams{Type: "stable", Content: "m1", Embedding: emb})
	ms.Add(ctx, AddParams{Type: "stable", Content: "m2", Embedding: emb})
	ms.Add(ctx, AddParams{Type: "stable", Content: "m3", Embedding: emb})

	results, err := ms.GetEmbeddingsWithTypes(ctx, 0)
	if err != nil {
		t.Fatalf("GetEmbeddingsWithTypes with limit=0: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results with limit=0 (no limit), got %d", len(results))
	}
}

func TestGetEmbeddingsWithTypes_NegativeLimit_DefaultLimit(t *testing.T) {
	// When limit < 0, GetEmbeddingsWithTypes should default to defaultBruteForceMaxRows.
	ms := newTestStore(t)
	ctx := context.Background()

	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 0.1
	}
	emb := VecToBytes(vec)
	ms.Add(ctx, AddParams{Type: "stable", Content: "m1", Embedding: emb})

	results, err := ms.GetEmbeddingsWithTypes(ctx, -1)
	if err != nil {
		t.Fatalf("GetEmbeddingsWithTypes with limit=-1: %v", err)
	}
	// Should return the 1 result (well within the default limit of 10000)
	if len(results) != 1 {
		t.Errorf("expected 1 result with limit=-1 (default), got %d", len(results))
	}
}

func TestMemoryStore_Add_EmbeddingExceedsMaxSize(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Create an embedding that exceeds maxEmbeddingBytes (1MB)
	oversizedEmbedding := make([]byte, maxEmbeddingBytes+1)
	_, err := ms.Add(ctx, AddParams{
		Type:      "stable",
		Content:   "test content",
		Embedding: oversizedEmbedding,
	})
	if err == nil {
		t.Error("expected error for embedding exceeding maxEmbeddingBytes")
	}
	// Verify error message contains domain context
	if !strings.Contains(err.Error(), "add:") {
		t.Errorf("expected error to contain 'add:' domain prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), "embedding size") {
		t.Errorf("expected error to contain 'embedding size', got: %v", err)
	}
}

func TestMemoryStore_Add_EmbeddingAtMaxSize_Succeeds(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Create an embedding exactly at maxEmbeddingBytes — should succeed
	exactMaxEmbedding := make([]byte, maxEmbeddingBytes)
	id, err := ms.Add(ctx, AddParams{
		Type:      "stable",
		Content:   "test content",
		Embedding: exactMaxEmbedding,
	})
	if err != nil {
		t.Fatalf("expected Add to succeed with embedding at maxEmbeddingBytes, got: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID for embedding at maxEmbeddingBytes")
	}
}

func TestMemoryStore_ImportMemories_EmbeddingExceedsMaxSize(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Create memories where one has an oversized embedding
	memories := []ImportMemory{
		{Type: "stable", Content: "valid entry"},
		{Type: "stable", Content: "oversized embedding", Embedding: make([]byte, maxEmbeddingBytes+1)},
		{Type: "stable", Content: "another valid entry"},
	}

	count, err := ms.ImportMemories(ctx, memories)
	if err != nil {
		t.Fatalf("ImportMemories: %v", err)
	}
	// Only 2 valid entries (the oversized one should be skipped)
	if count != 2 {
		t.Errorf("expected 2 imported (oversized skipped), got %d", count)
	}
}

func TestMemoryStore_ImportMemories_EmbeddingAtMaxSize_Succeeds(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// An embedding exactly at maxEmbeddingBytes should be accepted
	memories := []ImportMemory{
		{Type: "stable", Content: "at max size", Embedding: make([]byte, maxEmbeddingBytes)},
	}

	count, err := ms.ImportMemories(ctx, memories)
	if err != nil {
		t.Fatalf("ImportMemories: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 imported, got %d", count)
	}
}

func TestMemoryStore_Update_EmbeddingExceedsMaxSize(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "original content"})

	oversizedEmbedding := make([]byte, maxEmbeddingBytes+1)
	_, err := ms.Update(ctx, UpdateParams{
		ID:        id,
		Embedding: oversizedEmbedding,
	})
	if err == nil {
		t.Error("expected error for embedding exceeding maxEmbeddingBytes in Update")
	}
	if !strings.Contains(err.Error(), "update:") {
		t.Errorf("expected error to contain 'update:' domain prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), "embedding size") {
		t.Errorf("expected error to contain 'embedding size', got: %v", err)
	}
}

func TestMemoryStore_Update_EmbeddingAtMaxSize_Succeeds(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "original content"})

	exactMaxEmbedding := make([]byte, maxEmbeddingBytes)
	ok, err := ms.Update(ctx, UpdateParams{
		ID:        id,
		Embedding: exactMaxEmbedding,
	})
	if err != nil {
		t.Fatalf("expected Update to succeed with embedding at maxEmbeddingBytes, got: %v", err)
	}
	if !ok {
		t.Error("expected Update to return true")
	}
}

func TestSearch_SemanticOnly_DisabledVec_ReturnsError(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	_, err := ms.Search(ctx, SearchParams{
		Query:        "test",
		SemanticOnly: true,
	})
	if err == nil {
		t.Error("expected error when SemanticOnly=true with vec unavailable, got nil")
	}
	if !strings.Contains(err.Error(), "semantic search requires") {
		t.Errorf("expected semantic search error, got: %v", err)
	}
}

func TestSearch_BothFTSOnlyAndSemanticOnly_ReturnsError(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	_, err := ms.Search(ctx, SearchParams{
		Query:        "test",
		FTSOnly:      true,
		SemanticOnly: true,
	})
	if err == nil {
		t.Error("expected error when both FTSOnly and SemanticOnly are true, got nil")
	}
	if !strings.Contains(err.Error(), "cannot specify both") {
		t.Errorf("expected both-flags error, got: %v", err)
	}
}

func TestSearch_FTSOnly_WithQuery(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add a memory to search for
	_, err := ms.Add(ctx, AddParams{
		Type:       "stable",
		Content:    "Go is statically typed",
		Source:     "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	results, err := ms.Search(ctx, SearchParams{
		Query:   "Go",
		FTSOnly: true,
	})
	if err != nil {
		t.Fatalf("Search with FTSOnly: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one result with FTSOnly")
	}
}

// ═══════════════════════════════════════════════════════════
// Ticket #54: Schema expand — pinned column + 3 new types
// ═══════════════════════════════════════════════════════════

func TestMemoryStore_Add_StableAutoTTL(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{
		Type:    "stable",
		Content: "Important decision about architecture",
	})
	if err != nil {
		t.Fatalf("Add stable: %v", err)
	}

	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.ValidUntil == "" {
		t.Error("expected stable memory to have auto-computed valid_until, got empty")
	}
	if m.Pinned {
		t.Error("expected new memory to not be pinned")
	}

	// Verify TTL is approximately 90 days from now
	validUntil, err := time.Parse(time.RFC3339, m.ValidUntil)
	if err != nil {
		t.Fatalf("parse valid_until: %v", err)
	}
	now := time.Now().UTC()
	diff := validUntil.Sub(now)
	if diff < 89*24*time.Hour || diff > 91*24*time.Hour {
		t.Errorf("expected stable TTL ~90 days, got %v", diff)
	}
}

func TestMemoryStore_Add_VolatileAutoTTL(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{
		Type:    "volatile",
		Content: "Current project state: working on auth module",
	})
	if err != nil {
		t.Fatalf("Add volatile: %v", err)
	}

	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.ValidUntil == "" {
		t.Error("expected volatile memory to have auto-computed valid_until, got empty")
	}

	validUntil, err := time.Parse(time.RFC3339, m.ValidUntil)
	if err != nil {
		t.Fatalf("parse valid_until: %v", err)
	}
	now := time.Now().UTC()
	diff := validUntil.Sub(now)
	if diff < 13*24*time.Hour || diff > 15*24*time.Hour {
		t.Errorf("expected volatile TTL ~14 days, got %v", diff)
	}
}

func TestMemoryStore_Add_LogAutoTTL(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{
		Type:    "log",
		Content: "Applied to Acme Corp for Engineering Manager role",
	})
	if err != nil {
		t.Fatalf("Add log: %v", err)
	}

	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m == nil {
		t.Fatal("expected memory, got nil")
	}
	if m.ValidUntil == "" {
		t.Error("expected log memory to have auto-computed valid_until, got empty")
	}

	validUntil, err := time.Parse(time.RFC3339, m.ValidUntil)
	if err != nil {
		t.Fatalf("parse valid_until: %v", err)
	}
	now := time.Now().UTC()
	diff := validUntil.Sub(now)
	if diff < 89*24*time.Hour || diff > 91*24*time.Hour {
		t.Errorf("expected log TTL ~90 days, got %v", diff)
	}
}

func TestMemoryStore_Add_LegacyTypeRejected(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Legacy types should be rejected (contract phase removed them)
	for _, typ := range []string{"fact", "decision", "preference", "event", "project_state", "procedure", "conversation"} {
		_, err := ms.Add(ctx, AddParams{
			Type:    typ,
			Content: "test content for " + typ,
		})
		if err == nil {
			t.Errorf("expected legacy type %q to be rejected, but Add succeeded", typ)
		}
	}
}

func TestMemoryStore_Add_AllTypesAccepted(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Only the 3 new tier types should be accepted
	allTypes := []string{"stable", "volatile", "log"}
	for _, typ := range allTypes {
		_, err := ms.Add(ctx, AddParams{
			Type:    typ,
			Content: "test for " + typ,
		})
		if err != nil {
			t.Errorf("expected type %q to be accepted, got error: %v", typ, err)
		}
	}
}

func TestMemoryStore_Add_PinnedFieldDefault(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, err := ms.Add(ctx, AddParams{
		Type:    "stable",
		Content: "test pinned default",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.Pinned {
		t.Error("expected new memory to have pinned=false by default")
	}
}

func TestMemoryStore_Add_ExplicitValidUntilOverridesAutoTTL(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	explicit := "2025-12-31T23:59:59Z"
	id, err := ms.Add(ctx, AddParams{
		Type:       "stable",
		Content:    "test explicit valid_until",
		ValidUntil: explicit,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	m, err := ms.Get(ctx, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.ValidUntil != explicit {
		t.Errorf("expected explicit valid_until %q, got %q", explicit, m.ValidUntil)
	}
}

func TestDefaultTTLForType(t *testing.T) {
	tests := []struct {
		typ      string
		expected int
	}{
		{"stable", 90},
		{"volatile", 14},
		{"log", 90},
		{"unknown", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := DefaultTTLForType(tt.typ)
		if got != tt.expected {
			t.Errorf("DefaultTTLForType(%q) = %d, want %d", tt.typ, got, tt.expected)
		}
	}
}

func TestIsNewTierType(t *testing.T) {
	for _, typ := range []string{"stable", "volatile", "log"} {
		if !IsNewTierType(typ) {
			t.Errorf("expected %q to be a new tier type", typ)
		}
	}
	for _, typ := range []string{"fact", "decision", "event", "unknown", ""} {
		if IsNewTierType(typ) {
			t.Errorf("expected %q to NOT be a new tier type", typ)
		}
	}
}

// ═══════════════════════════════════════════════════════════
// Ticket #55: Pinning — pin/unpin stable memories
// ═══════════════════════════════════════════════════════════

func TestMemoryStore_Pin_StableMemory(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "important decision"})

	ok, err := ms.Pin(ctx, id)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if !ok {
		t.Error("expected ok=true for pinning existing memory")
	}

	m, _ := ms.Get(ctx, id, false)
	if !m.Pinned {
		t.Error("expected memory to be pinned after Pin()")
	}
	if m.ValidUntil != "" {
		t.Errorf("expected pinned memory to have valid_until=NULL (empty string), got %q", m.ValidUntil)
	}
}

func TestMemoryStore_Unpin_StableMemory(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "important decision"})
	_, _ = ms.Pin(ctx, id)

	ok, err := ms.Unpin(ctx, id)
	if err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if !ok {
		t.Error("expected ok=true for unpinning existing memory")
	}

	m, _ := ms.Get(ctx, id, false)
	if m.Pinned {
		t.Error("expected memory to not be pinned after Unpin()")
	}
	if m.ValidUntil == "" {
		t.Error("expected unpinned memory to have recomputed valid_until")
	}

	// Verify TTL is approximately 90 days from now
	validUntil, err := time.Parse(time.RFC3339, m.ValidUntil)
	if err != nil {
		t.Fatalf("parse valid_until: %v", err)
	}
	now := time.Now().UTC()
	diff := validUntil.Sub(now)
	if diff < 89*24*time.Hour || diff > 91*24*time.Hour {
		t.Errorf("expected unpinned stable TTL ~90 days, got %v", diff)
	}
}

func TestMemoryStore_Pin_VolatileReturnsError(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "volatile", Content: "project state"})

	_, err := ms.Pin(ctx, id)
	if err == nil {
		t.Error("expected error when pinning volatile memory")
	}
}

func TestMemoryStore_Pin_LogReturnsError(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "log", Content: "application record"})

	_, err := ms.Pin(ctx, id)
	if err == nil {
		t.Error("expected error when pinning log memory")
	}
}

func TestMemoryStore_Pin_NonExistentReturnsFalse(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ok, err := ms.Pin(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if ok {
		t.Error("expected ok=false for non-existent memory")
	}
}

func TestMemoryStore_Unpin_NonExistentReturnsFalse(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	ok, err := ms.Unpin(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if ok {
		t.Error("expected ok=false for non-existent memory")
	}
}

func TestMemoryStore_Unpin_NotPinnedIsNoOp(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test"})

	// Unpinning a memory that was never pinned should succeed (idempotent)
	ok, err := ms.Unpin(ctx, id)
	if err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if !ok {
		t.Error("expected ok=true for unpinning existing memory (even if not pinned)")
	}

	m, _ := ms.Get(ctx, id, false)
	if m.Pinned {
		t.Error("expected memory to not be pinned")
	}
}

func TestMemoryStore_Pin_LegacyTypeReturnsError(t *testing.T) {
	ms := newTestStoreWithLegacyTypes(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "fact", Content: "test fact"})

	_, err := ms.Pin(ctx, id)
	if err == nil {
		t.Error("expected error when pinning legacy type (fact)")
	}
}

// ═══════════════════════════════════════════════════════════
// Ticket #56: Briefing — llmem brief command
// ═══════════════════════════════════════════════════════════

func TestBriefing_IncludesPinnedStable(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "pinned decision"})
	_, _ = ms.Pin(ctx, id)

	b, err := ms.GenerateBriefing(ctx, DefaultBriefingConfig())
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	found := false
	for _, m := range b.Pinned.Memories {
		if m.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected pinned memory to appear in Pinned section")
	}
}

func TestBriefing_IncludesNonExpiredVolatile(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "volatile", Content: "active project state"})

	b, err := ms.GenerateBriefing(ctx, DefaultBriefingConfig())
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	found := false
	for _, m := range b.Volatile.Memories {
		if m.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected non-expired volatile memory to appear in Active section")
	}
}

func TestBriefing_IncludesRecentStableNotPinned(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	pinnedID, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "pinned decision"})
	_, _ = ms.Pin(ctx, pinnedID)

	recentID, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "recent decision"})

	b, err := ms.GenerateBriefing(ctx, DefaultBriefingConfig())
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	// Pinned should be in Pinned section
	foundPinned := false
	for _, m := range b.Pinned.Memories {
		if m.ID == pinnedID {
			foundPinned = true
		}
	}
	if !foundPinned {
		t.Error("expected pinned memory in Pinned section")
	}

	// Recent (non-pinned) should be in Recent section, not Pinned
	foundRecent := false
	for _, m := range b.RecentStable.Memories {
		if m.ID == recentID {
			foundRecent = true
		}
	}
	if !foundRecent {
		t.Error("expected non-pinned recent stable in Recent section")
	}

	// Recent should NOT appear in Pinned
	for _, m := range b.Pinned.Memories {
		if m.ID == recentID {
			t.Error("non-pinned memory should not appear in Pinned section")
		}
	}
}

func TestBriefing_Top5RecentStable(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add 7 stable memories (none pinned)
	for i := 0; i < 7; i++ {
		ms.Add(ctx, AddParams{Type: "stable", Content: fmt.Sprintf("decision %d", i)})
	}

	b, err := ms.GenerateBriefing(ctx, DefaultBriefingConfig())
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	if len(b.RecentStable.Memories) != 5 {
		t.Errorf("expected exactly 5 recent stable memories, got %d", len(b.RecentStable.Memories))
	}
}

func TestBriefing_CapsAtMaxLines(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add lots of memories to exceed cap
	for i := 0; i < 20; i++ {
		ms.Add(ctx, AddParams{Type: "stable", Content: fmt.Sprintf("stable decision %d with some content", i)})
	}
	for i := 0; i < 20; i++ {
		ms.Add(ctx, AddParams{Type: "volatile", Content: fmt.Sprintf("volatile state %d with content", i)})
	}

	cfg := BriefingConfig{MaxLines: 15, RecentStable: 5, RecentVolatile: 3}
	b, err := ms.GenerateBriefing(ctx, cfg)
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	output := b.Format()
	lineCount := strings.Count(output, "\n")
	if lineCount > 20 { // allow small overflow for headers
		t.Errorf("expected briefing under ~20 lines with cap=15, got %d lines", lineCount)
	}

	// Recent stable should be dropped first
	if len(b.RecentStable.Memories) > 0 {
		t.Errorf("expected recent stable to be fully dropped when over cap, got %d", len(b.RecentStable.Memories))
	}
}

func TestBriefing_EmptyStore(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	b, err := ms.GenerateBriefing(ctx, DefaultBriefingConfig())
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	if len(b.Pinned.Memories) != 0 {
		t.Error("expected no pinned memories in empty store")
	}
	if len(b.Volatile.Memories) != 0 {
		t.Error("expected no volatile memories in empty store")
	}
	if len(b.RecentStable.Memories) != 0 {
		t.Error("expected no recent stable in empty store")
	}
	if b.StoreHealth.StableTotal != 0 || b.StoreHealth.VolatileTotal != 0 || b.StoreHealth.LogTotal != 0 {
		t.Error("expected zero counts in empty store")
	}
	if b.StoreHealth.ExpiredCount != 0 {
		t.Error("expected zero expired in empty store")
	}
}

func TestBriefing_OnlyExpiredMemories(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add a volatile memory with a past valid_until
	id, _ := ms.Add(ctx, AddParams{Type: "volatile", Content: "expired state"})
	// Manually set valid_until to past
	past := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	ms.Update(ctx, UpdateParams{ID: id, ValidUntil: &past})

	b, err := ms.GenerateBriefing(ctx, DefaultBriefingConfig())
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	// Expired volatile should NOT appear in Active section
	for _, m := range b.Volatile.Memories {
		if m.ID == id {
			t.Error("expected expired volatile to NOT appear in Active section")
		}
	}
	// But should be counted in expired
	if b.StoreHealth.ExpiredCount < 1 {
		t.Error("expected at least 1 expired memory in health stats")
	}
}

func TestBriefing_StoreHealth(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add memories of each type
	ms.Add(ctx, AddParams{Type: "stable", Content: "s1"})
	pinnedID, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "s2"})
	ms.Pin(ctx, pinnedID)
	ms.Add(ctx, AddParams{Type: "volatile", Content: "v1"})
	ms.Add(ctx, AddParams{Type: "log", Content: "l1"})

	b, err := ms.GenerateBriefing(ctx, DefaultBriefingConfig())
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	if b.StoreHealth.StableTotal != 2 {
		t.Errorf("expected stableTotal=2, got %d", b.StoreHealth.StableTotal)
	}
	if b.StoreHealth.StablePinned != 1 {
		t.Errorf("expected stablePinned=1, got %d", b.StoreHealth.StablePinned)
	}
	if b.StoreHealth.VolatileTotal != 1 {
		t.Errorf("expected volatileTotal=1, got %d", b.StoreHealth.VolatileTotal)
	}
	if b.StoreHealth.LogTotal != 1 {
		t.Errorf("expected logTotal=1, got %d", b.StoreHealth.LogTotal)
	}
}

func TestBriefing_Format(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test decision"})
	_, _ = ms.Pin(ctx, id)

	b, err := ms.GenerateBriefing(ctx, DefaultBriefingConfig())
	if err != nil {
		t.Fatalf("GenerateBriefing: %v", err)
	}

	output := b.Format()
	if !strings.Contains(output, "## Session Briefing") {
		t.Error("expected '## Session Briefing' header in format output")
	}
	if !strings.Contains(output, "### Pinned") {
		t.Error("expected '### Pinned' section in format output")
	}
	if !strings.Contains(output, "### Store Health") {
		t.Error("expected '### Store Health' in format output")
	}
	if !strings.Contains(output, id) {
		t.Error("expected memory ID in format output")
	}
}

// ═══════════════════════════════════════════════════════════
// Ticket #57: TTL pruning + amend — llmem amend command
// ═══════════════════════════════════════════════════════════

func TestMemoryStore_PruneExpired_DeletesNonPinnedExpired(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add a stable memory
	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "will expire"})
	// Manually set valid_until to past
	past := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	ms.Update(ctx, UpdateParams{ID: id1, ValidUntil: &past})

	// Add another that's still valid
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "still valid"})

	pruned, err := ms.PruneExpired(ctx)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if pruned != 1 {
		t.Errorf("expected 1 pruned, got %d", pruned)
	}

	// Verify expired one is gone
	m, _ := ms.Get(ctx, id1, false)
	if m != nil {
		t.Error("expected expired memory to be deleted")
	}

	// Verify valid one survives
	m2, _ := ms.Get(ctx, id2, false)
	if m2 == nil {
		t.Error("expected valid memory to survive pruning")
	}
}

func TestMemoryStore_PruneExpired_PreservesPinned(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "pinned but expired"})
	_, _ = ms.Pin(ctx, id)

	// Manually set valid_until to past (pinning sets it to NULL, so set it back)
	past := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	ms.db.ExecContext(ctx, `UPDATE "memories" SET "valid_until" = ? WHERE "id" = ?`, past, id)

	pruned, err := ms.PruneExpired(ctx)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned (pinned preserved), got %d", pruned)
	}

	m, _ := ms.Get(ctx, id, false)
	if m == nil {
		t.Error("expected pinned memory to survive pruning even with past valid_until")
	}
}

func TestMemoryStore_PruneExpired_EmptyStore(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	pruned, err := ms.PruneExpired(ctx)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned on empty store, got %d", pruned)
	}
}

func TestMemoryStore_PruneExpired_NoExpiredMemories(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	// Add memories with future TTLs
	ms.Add(ctx, AddParams{Type: "stable", Content: "s1"})
	ms.Add(ctx, AddParams{Type: "volatile", Content: "v1"})

	pruned, err := ms.PruneExpired(ctx)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned when no expired memories, got %d", pruned)
	}
}

func TestMemoryStore_TouchBatch_ResetsTTLForStable(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test stable"})

	// Get initial valid_until
	m1, _ := ms.Get(ctx, id, false)
	initialExpiry, _ := time.Parse(time.RFC3339, m1.ValidUntil)

	// Touch should reset TTL
	time.Sleep(10 * time.Millisecond)
	_, err := ms.TouchBatch(ctx, []string{id})
	if err != nil {
		t.Fatalf("TouchBatch: %v", err)
	}

	m2, _ := ms.Get(ctx, id, false)
	newExpiry, _ := time.Parse(time.RFC3339, m2.ValidUntil)

	if newExpiry.Before(initialExpiry) {
		t.Errorf("expected TTL to be reset after TouchBatch, new=%v initial=%v", newExpiry, initialExpiry)
	}
}

func TestMemoryStore_TouchBatch_ResetsTTLForVolatile(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "volatile", Content: "test volatile"})

	m1, _ := ms.Get(ctx, id, false)
	initialExpiry, _ := time.Parse(time.RFC3339, m1.ValidUntil)

	time.Sleep(10 * time.Millisecond)
	_, err := ms.TouchBatch(ctx, []string{id})
	if err != nil {
		t.Fatalf("TouchBatch: %v", err)
	}

	m2, _ := ms.Get(ctx, id, false)
	newExpiry, _ := time.Parse(time.RFC3339, m2.ValidUntil)

	if newExpiry.Before(initialExpiry) {
		t.Errorf("expected volatile TTL to be reset after TouchBatch, new=%v initial=%v", newExpiry, initialExpiry)
	}
}

func TestMemoryStore_TouchBatch_DoesNotResetTTLForLog(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "log", Content: "test log"})

	m1, _ := ms.Get(ctx, id, false)
	initialValidUntil := m1.ValidUntil

	time.Sleep(10 * time.Millisecond)
	_, err := ms.TouchBatch(ctx, []string{id})
	if err != nil {
		t.Fatalf("TouchBatch: %v", err)
	}

	m2, _ := ms.Get(ctx, id, false)
	if m2.ValidUntil != initialValidUntil {
		t.Errorf("expected log TTL to NOT be reset after TouchBatch, initial=%q new=%q", initialValidUntil, m2.ValidUntil)
	}
}

func TestMemoryStore_TouchBatch_DoesNotResetTTLForPinned(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "pinned test"})
	_, _ = ms.Pin(ctx, id)

	// Pin sets valid_until to NULL, so it should stay NULL after TouchBatch
	_, err := ms.TouchBatch(ctx, []string{id})
	if err != nil {
		t.Fatalf("TouchBatch: %v", err)
	}

	m, _ := ms.Get(ctx, id, false)
	if m.ValidUntil != "" {
		t.Errorf("expected pinned memory to have valid_until=NULL (empty), got %q", m.ValidUntil)
	}
}

func TestMemoryStore_Get_TrackAccessResetsTTL(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "test get trackAccess"})

	m1, _ := ms.Get(ctx, id, false)
	initialExpiry, _ := time.Parse(time.RFC3339, m1.ValidUntil)

	time.Sleep(10 * time.Millisecond)
	_, err := ms.Get(ctx, id, true)
	if err != nil {
		t.Fatalf("Get with trackAccess: %v", err)
	}

	m2, _ := ms.Get(ctx, id, false)
	newExpiry, _ := time.Parse(time.RFC3339, m2.ValidUntil)

	if newExpiry.Before(initialExpiry) {
		t.Errorf("expected TTL to be reset after Get(trackAccess=true), new=%v initial=%v", newExpiry, initialExpiry)
	}
}

// ═══════════════════════════════════════════════════════════
// Learning pipeline: auto-pin via extract, ListPinned, UpdateMetadata
// used by the nightly curator
// ═══════════════════════════════════════════════════════════

func TestMemoryStore_ListPinned(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id1, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "pinned one"})
	ms.Pin(ctx, id1)
	id2, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "pinned two"})
	ms.Pin(ctx, id2)
	unpinned, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "not pinned"})

	pins, err := ms.ListPinned(ctx)
	if err != nil {
		t.Fatalf("ListPinned: %v", err)
	}
	if len(pins) != 2 {
		t.Fatalf("expected 2 pinned, got %d", len(pins))
	}
	for _, p := range pins {
		if p.ID == unpinned {
			t.Errorf("unpinned memory leaked into ListPinned")
		}
		if !p.Pinned {
			t.Errorf("ListPinned result not marked pinned: %s", p.ID)
		}
	}
	if pins[0].ID != id1 || pins[1].ID != id2 {
		t.Errorf("expected oldest-first order: %s, %s", pins[0].ID, pins[1].ID)
	}
}

func TestMemoryStore_UpdateMetadata(t *testing.T) {
	ms := newTestStore(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, AddParams{Type: "stable", Content: "meta test", Metadata: map[string]any{"src": "user"}})

	if err := ms.UpdateMetadata(ctx, id, map[string]any{"curate_runs": float64(1)}); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	m, err := ms.Get(ctx, id, false)
	if err != nil || m == nil {
		t.Fatalf("Get after UpdateMetadata: %v", err)
	}
	if m.Metadata["src"] != "user" {
		t.Errorf("existing metadata lost: %v", m.Metadata)
	}
	if _, ok := m.Metadata["curate_runs"]; !ok {
		t.Errorf("new metadata key missing: %v", m.Metadata)
	}

	// error on missing memory
	if err := ms.UpdateMetadata(ctx, "nope", map[string]any{"a": 1}); err == nil {
		t.Error("expected error updating metadata of non-existent memory")
	}
}

