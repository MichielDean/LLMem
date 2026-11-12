package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichielDean/LLMem/internal/store"
)

func newTestStoreCmd(t *testing.T) *store.MemoryStore {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { dbPath = "" })
	dbPath = filepath.Join(dir, "memory.db")
	ms, err := store.NewMemoryStore(store.StoreConfig{
		DBPath:     dbPath,
		DisableVec: true,
	})
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	for _, typ := range store.DefaultRegisteredTypes() {
		_ = ms.RegisterMemoryType(typ)
	}
	t.Cleanup(func() { ms.Close() })
	return ms
}

func TestTokenOverlap(t *testing.T) {
	if got := tokenOverlap("always write commit messages in present tense", "always write commit messages in present tense"); got < 0.99 {
		t.Errorf("identical strings should overlap ~1.0, got %f", got)
	}
	if got := tokenOverlap("use tabs in go files", "deploy dashboard at midnight"); got > 0.1 {
		t.Errorf("unrelated strings should overlap low, got %f", got)
	}
	if got := tokenOverlap("Use, spaces. NOT tabs!", "use spaces not tabs"); got < 0.9 {
		t.Errorf("punctuation should be stripped, got %f", got)
	}
	if got := tokenOverlap("", "anything"); got != 0 {
		t.Errorf("empty side should give 0, got %f", got)
	}
}

func TestPromotionCandidatesRounds(t *testing.T) {
	// runs counting lives in metadata; verify the bump logic via a fake run sequence
	// using the real store
	ms := newTestStoreCmd(t)
	ctx := context.Background()

	id, _ := ms.Add(ctx, store.AddParams{Type: "stable", Content: "promotable preference"})
	ms.Pin(ctx, id)

	// Run 1+2: below threshold
	for i := 0; i < 2; i++ {
		cands, err := promotionCandidates(ctx, ms, 3)
		if err != nil {
			t.Fatalf("promotionCandidates run %d: %v", i+1, err)
		}
		if len(cands) != 0 {
			t.Fatalf("run %d: expected 0 candidates, got %d", i+1, len(cands))
		}
	}
	// Run 3: crosses threshold
	cands, err := promotionCandidates(ctx, ms, 3)
	if err != nil {
		t.Fatalf("promotionCandidates run 3: %v", err)
	}
	if len(cands) != 1 || cands[0] != id {
		t.Fatalf("run 3: expected candidate %s, got %v", id, cands)
	}
	// After promoted marker: no longer candidate
	if err := ms.UpdateMetadata(ctx, id, map[string]any{"promoted": true}); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	cands, err = promotionCandidates(ctx, ms, 3)
	if err != nil {
		t.Fatalf("promotionCandidates after promoted: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("promoted pin should not re-candidate, got %v", cands)
	}
}
