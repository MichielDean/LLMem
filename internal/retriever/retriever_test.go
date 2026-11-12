package retriever

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MichielDean/LLMem/internal/store"
)

// ptrFloat64 is a helper to create a *float64 from a literal.
func ptrFloat64(v float64) *float64 { return &v }

// newTestStore creates a MemoryStore in a temp directory for testing.
func newTestStore(t *testing.T) *store.MemoryStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	ms, err := store.NewMemoryStore(store.StoreConfig{
		DBPath:     dbPath,
		DisableVec: true,
	})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	t.Cleanup(func() { ms.Close() })
	return ms
}

func TestRetriever_Constructor_InvalidBlend(t *testing.T) {
	ms := newTestStore(t)
	_, err := NewRetriever(RetrieverConfig{
		Store: ms,
		Blend: ptrFloat64(-0.1),
	})
	if err == nil {
		t.Error("expected error for invalid blend")
	}

	_, err = NewRetriever(RetrieverConfig{
		Store: ms,
		Blend: ptrFloat64(1.1),
	})
	if err == nil {
		t.Error("expected error for invalid blend > 1.0")
	}
}

func TestRetriever_Constructor_InvalidAlpha(t *testing.T) {
	ms := newTestStore(t)
	_, err := NewRetriever(RetrieverConfig{
		Store: ms,
		Alpha: ptrFloat64(-0.1),
	})
	if err == nil {
		t.Error("expected error for invalid alpha")
	}

	_, err = NewRetriever(RetrieverConfig{
		Store: ms,
		Alpha: ptrFloat64(1.1),
	})
	if err == nil {
		t.Error("expected error for invalid alpha > 1.0")
	}
}

func TestRetriever_Constructor_NilStore(t *testing.T) {
	_, err := NewRetriever(RetrieverConfig{
		Store: nil,
	})
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestRetriever_HybridSearch_FTSOnlyMode(t *testing.T) {
	ms := newTestStore(t)
	// Create retriever with nil embedder (FTS-only mode), defaults for Blend and Alpha.
	r, err := NewRetriever(RetrieverConfig{
		Store:    ms,
		Embedder: nil,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	// Add a memory
	ctx := context.Background()
	_, err = ms.Add(ctx, store.AddParams{
		ID:       "test-1",
		Type:     "stable",
		Content:  "Go is a programming language",
		Source:   "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	results, err := r.HybridSearch(ctx, "Go programming", 10, "", nil, "fts", false)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from FTS search")
	}
}

func TestRetriever_HybridSearch_EmptyQuery(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	results, err := r.HybridSearch(ctx, "", 10, "", nil, "hybrid", false)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for empty query, got %d", len(results))
	}
}

func TestRetriever_InvalidSearchMode(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	_, err = r.HybridSearch(ctx, "test", 10, "", nil, "invalid", false)
	if err == nil {
		t.Error("expected error for invalid search_mode")
	}
}

func TestRetriever_SemanticWithoutEmbedder(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store:    ms,
		Embedder: nil,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	_, err = r.HybridSearch(ctx, "test", 10, "", nil, "semantic", false)
	if err == nil {
		t.Error("expected error for semantic search without embedder")
	}
}

func TestRetriever_RRF_Score(t *testing.T) {
	semanticRanks := map[string]int{
		"id1": 1,
		"id2": 2,
		"id3": 3,
	}
	ftsRanks := map[string]int{
		"id2": 1,
		"id4": 2,
	}
	results := RRFScore(semanticRanks, ftsRanks, 0.7, 60)

	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}

	// Verify scores are sorted descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted by score descending: %f > %f", results[i].Score, results[i-1].Score)
		}
	}
}

func TestRetriever_RRF_EmptyInputs(t *testing.T) {
	results := RRFScore(nil, nil, 0.7, 60)
	if len(results) != 0 {
		t.Errorf("expected empty results for empty inputs, got %d", len(results))
	}
	// Verify contract: empty inputs return nil (not an empty slice)
	if results != nil {
		t.Errorf("expected nil for empty inputs, got non-nil slice with length %d", len(results))
	}
}

func TestRetriever_RRF_MissingRankDefault(t *testing.T) {
	semanticRanks := map[string]int{
		"id1": 1,
	}
	ftsRanks := map[string]int{
		"id1": 2,
		"id2": 1,
	}
	results := RRFScore(semanticRanks, ftsRanks, 0.7, 60)

	// id2 should get a default semantic rank of len(semanticRanks)+1=2
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Verify id1 and id2 are both present
	foundIDs := map[string]bool{}
	for _, r := range results {
		foundIDs[r.ID] = true
	}
	if !foundIDs["id1"] || !foundIDs["id2"] {
		t.Error("expected both id1 and id2 in results")
	}
}

func TestRetriever_ComputeRerankSignals(t *testing.T) {
	now := time.Now().UTC()
	mem := &store.Memory{
		ID:          "test-1",
		Type:        "stable",
		Confidence:  0.9,
		AccessCount:  5,
		CreatedAt:   now.Add(-72 * time.Hour).Format(time.RFC3339),
		AccessedAt:  now.Add(-24 * time.Hour).Format(time.RFC3339),
	}

	signals := ComputeRerankSignals(mem, DefaultTypePriority(), now)

	// Confidence should be 0.9
	if math.Abs(signals.Confidence-0.9) > 1e-6 {
		t.Errorf("expected confidence 0.9, got %f", signals.Confidence)
	}

	// Type priority for "stable" should be 1.1
	if math.Abs(signals.Type-1.1) > 1e-6 {
		t.Errorf("expected type 1.1, got %f", signals.Type)
	}

	// Recency should be positive (was accessed yesterday)
	if signals.Recency <= 0 {
		t.Errorf("expected positive recency, got %f", signals.Recency)
	}

	// Access should be positive (5 accesses over ~3 days)
	if signals.Access <= 0 {
		t.Errorf("expected positive access, got %f", signals.Access)
	}
}

func TestRetriever_ComputeWeightedSignal(t *testing.T) {
	signals := RerankSignals{
		Confidence: 1.0,
		Recency:    1.0,
		Access:     1.0,
		Type:       1.0,
	}

	result := ComputeWeightedSignal(signals)
	expected := 0.4*1.0 + 0.3*1.0 + 0.2*1.0 + 0.1*1.0
	if math.Abs(result-expected) > 1e-6 {
		t.Errorf("expected %f, got %f", expected, result)
	}
}

func TestRetriever_ApplyReranking_BlendZero(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
		Blend: ptrFloat64(0.0), // explicit pure RRF
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	results := []*ScoredMemory{
		{
			Memory:   &store.Memory{ID: "1", Type: "stable", Confidence: 0.5},
			RRFScore: 0.8,
		},
	}

	results = r.applyReranking(results)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// With blend=0.0, RerankScore should equal RRFScore
	if math.Abs(results[0].RerankScore-0.8) > 1e-6 {
		t.Errorf("expected RerankScore=0.8 with blend=0, got %f", results[0].RerankScore)
	}
}

func TestRetriever_Search_WithRelations(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	_, err = ms.Add(ctx, store.AddParams{
		ID:         "mem-1",
		Type:       "stable",
		Content:    "Go is a programming language",
		Source:     "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	results, err := r.Search(ctx, "Go", 10, "", true, 1, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from search")
	}
}

func TestRetriever_Search_EmptyResultIsNil(t *testing.T) {
	// Verify that Search returns nil (not an empty slice) when no results found,
	// matching the updated docstring contract.
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	results, err := r.Search(ctx, "nonexistentquerythatmatchesnothing", 10, "", false, 0, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty search results, got slice with length %d", len(results))
	}
}

func TestRetriever_Search_DisabledTrackAccess(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	memID, err := ms.Add(ctx, store.AddParams{
		ID:         "mem-1",
		Type:       "stable",
		Content:    "Go is a programming language",
		Source:     "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Get initial access count
	memBefore, _ := ms.Get(ctx, memID, false)
	beforeCount := memBefore.AccessCount

	_, err = r.Search(ctx, "Go", 10, "", false, 1, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Access count should NOT change when trackAccess=false
	memAfter, _ := ms.Get(ctx, memID, false)
	if memAfter.AccessCount != beforeCount {
		t.Errorf("expected access count unchanged (%d), got %d", beforeCount, memAfter.AccessCount)
	}
}

func TestRetriever_TrackAccess(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	memID, err := ms.Add(ctx, store.AddParams{
		ID:         "mem-1",
		Type:       "stable",
		Content:    "Go is a programming language",
		Source:     "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Get initial access count
	memBefore, _ := ms.Get(ctx, memID, false)
	beforeCount := memBefore.AccessCount

	_, err = r.Search(ctx, "Go", 10, "", false, 1, true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Access count should increment when trackAccess=true
	memAfter, _ := ms.Get(ctx, memID, false)
	if memAfter.AccessCount <= beforeCount {
		t.Errorf("expected access count > %d after trackAccess=true, got %d", beforeCount, memAfter.AccessCount)
	}
}

func TestRetriever_FormatContext(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	_, err = ms.Add(ctx, store.AddParams{
		ID:         "mem-1",
		Type:       "stable",
		Content:    "Go is a programming language",
		Summary:    "Go overview",
		Source:     "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// FTS-only search since no embedder
	result, err := r.FormatContext(ctx, "Go", 4000, "")
	if err != nil {
		t.Fatalf("FormatContext: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty context string")
	}
}

func TestTruncateToValidUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		expected string
	}{
		{"short_enough", "hello", 10, "hello"},
		{"exact_truncate_ascii", "hello", 5, "hello"},
		{"truncate_ascii", "hello world", 5, "hello"},
		{"truncate_within_multibyte", "café世界", 4, "caf"},
		{"truncate_at_boundary", "café世界", 5, "café"},
		{"empty_input", "", 5, ""},
		{"zero_max", "hello", 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := truncateToValidUTF8(tc.input, tc.maxBytes)
			if result != tc.expected {
				t.Errorf("truncateToValidUTF8(%q, %d) = %q, want %q", tc.input, tc.maxBytes, result, tc.expected)
			}
			// Verify result is valid UTF-8
			for _, r := range result {
				if r == utf8.RuneError {
					t.Errorf("result contains invalid UTF-8 rune: %q", result)
				}
			}
		})
	}
}

func TestRetriever_FormatContext_UTF8Truncation(t *testing.T) {
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	// Add a memory with Chinese characters
	_, err = ms.Add(ctx, store.AddParams{
		ID:         "mem-1",
		Type:       "stable",
		Content:    "这是一个中文测试内容",
		Source:     "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Use a small budget that would truncate in the middle of a multi-byte sequence
	result, err := r.FormatContext(ctx, "中文", 20, "")
	if err != nil {
		t.Fatalf("FormatContext: %v", err)
	}
	// Verify the result is valid UTF-8 (no partial sequences)
	for _, ch := range result {
		if ch == utf8.RuneError {
			t.Errorf("FormatContext produced invalid UTF-8: %q", result)
		}
	}
}

func TestRetriever_HybridSearch_InvalidModeReturnsError(t *testing.T) {
	// Verify that HybridSearch returns an error for invalid search modes,
	// not nil error (docstring fix).
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	_, err = r.HybridSearch(ctx, "test", 10, "", nil, "badmode", false)
	if err == nil {
		t.Error("expected error for invalid search mode, got nil")
	}
}

func TestNewRetriever_DefensiveCopyTypePriority(t *testing.T) {
	// Verify that modifying the original TypePriority map after construction
	// does not affect the retriever's internal state.
	ms := newTestStore(t)

	customPrio := map[string]float64{
		"stable":   1.5,
		"volatile": 1.0,
	}

	r, err := NewRetriever(RetrieverConfig{
		Store:        ms,
		TypePriority: customPrio,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	// Mutate the original map
	customPrio["stable"] = 999.0

	// Verify retriever has its own copy — it should use the original values
	signals := ComputeRerankSignals(&store.Memory{
		ID:         "test-1",
		Type:       "stable",
		Confidence: 0.5,
	}, r.typePriority, time.Now().UTC())

	if signals.Type == 999.0 {
		t.Error("TypePriority was not defensively copied — mutation leaked into retriever")
	}
	if signals.Type != 1.5 {
		t.Errorf("expected Type=1.5, got %f", signals.Type)
	}
}

func TestDefaultTypePriority(t *testing.T) {
	prio := DefaultTypePriority()
	if prio["stable"] != 1.1 {
		t.Errorf("expected stable=1.1, got %f", prio["stable"])
	}
	if prio["volatile"] != 1.2 {
		t.Errorf("expected volatile=1.2, got %f", prio["volatile"])
	}
	if prio["log"] != 0.7 {
		t.Errorf("expected log=0.7, got %f", prio["log"])
	}
}

// Test embedder fallback when Ollama is not available
func TestRetriever_HybridSearch_EmbedderUnavailableFallsBackToFTS(t *testing.T) {
	ms := newTestStore(t)
	// Create retriever with nil embedder but hybrid mode
	r, err := NewRetriever(RetrieverConfig{
		Store:    ms,
		Embedder: nil,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	_, err = ms.Add(ctx, store.AddParams{
		ID:         "mem-1",
		Type:       "stable",
		Content:    "Go is a programming language",
		Source:     "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Hybrid search with nil embedder should fall back to FTS
	results, err := r.HybridSearch(ctx, "Go programming", 10, "", nil, "hybrid", false)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from FTS fallback")
	}
}

func TestNewRetriever_DefaultBlendAndAlpha(t *testing.T) {
	// Verify that nil Blend and nil Alpha result in the defaults (0.3 and 0.7).
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	const tolerance = 1e-9
	if math.Abs(r.blend-defaultBlend) > tolerance {
		t.Errorf("expected default blend %f, got %f", defaultBlend, r.blend)
	}
	if math.Abs(r.alpha-defaultAlpha) > tolerance {
		t.Errorf("expected default alpha %f, got %f", defaultAlpha, r.alpha)
	}
}

func TestNewRetriever_ExplicitZeroBlend(t *testing.T) {
	// Verify that *float64(0.0) sets blend to 0.0 (pure RRF), not the default.
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
		Blend: ptrFloat64(0.0),
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	if r.blend != 0.0 {
		t.Errorf("expected blend=0.0 (pure RRF), got %f", r.blend)
	}
}

func TestNewRetriever_ExplicitZeroAlpha(t *testing.T) {
	// Verify that *float64(0.0) sets alpha to 0.0 (pure FTS), not the default.
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
		Alpha: ptrFloat64(0.0),
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	if r.alpha != 0.0 {
		t.Errorf("expected alpha=0.0 (pure FTS), got %f", r.alpha)
	}
}

func TestRetriever_HybridSearch_ExplicitZeroAlpha_PureFTS(t *testing.T) {
	// Verify that passing ptrFloat64(0.0) as alpha to HybridSearch results in
	// pure FTS mode (alpha=0.0 means zero semantic weight).
	ms := newTestStore(t)
	r, err := NewRetriever(RetrieverConfig{
		Store: ms,
	})
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	ctx := context.Background()
	_, err = ms.Add(ctx, store.AddParams{
		ID:         "mem-1",
		Type:       "stable",
		Content:    "Go is a programming language",
		Source:     "test",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// HybridSearch with explicit alpha=0.0 should use pure FTS
	results, err := r.HybridSearch(ctx, "Go", 10, "", ptrFloat64(0.0), "hybrid", false)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from hybrid search with alpha=0.0 (pure FTS)")
	}
}