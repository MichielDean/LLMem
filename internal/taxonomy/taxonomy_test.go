package taxonomy

import (
	"testing"
)

func TestErrorTaxonomy_KeysMatch(t *testing.T) {
	keys := ErrorTaxonomyKeys()
	for _, k := range keys {
		if _, ok := ErrorTaxonomy[k]; !ok {
			t.Errorf("ErrorTaxonomyKeys contains %q but ErrorTaxonomy does not", k)
		}
	}
	keySet := map[string]bool{}
	for _, k := range keys {
		keySet[k] = true
	}
	for k := range ErrorTaxonomy {
		if !keySet[k] {
			t.Errorf("ErrorTaxonomy contains %q but ErrorTaxonomyKeys does not", k)
		}
	}
}

func TestErrorTaxonomy_HasAllCategories(t *testing.T) {
	expected := []string{
		"NULL_SAFETY", "ERROR_HANDLING", "OFF_BY_ONE", "RACE_CONDITION",
		"AUTH_BYPASS", "DATA_INTEGRITY", "MISSING_VERIFICATION", "EDGE_CASE",
		"PERFORMANCE", "DESIGN", "REVIEW_PASSED",
	}
	if len(ErrorTaxonomy) != len(expected) {
		t.Errorf("expected %d categories, got %d", len(expected), len(ErrorTaxonomy))
	}
	for _, cat := range expected {
		if _, ok := ErrorTaxonomy[cat]; !ok {
			t.Errorf("missing category %q", cat)
		}
	}
}

func TestErrorTaxonomyKeys_ReturnsDefensiveCopy(t *testing.T) {
	keys1 := ErrorTaxonomyKeys()
	keys2 := ErrorTaxonomyKeys()
	if len(keys1) != len(keys2) {
		t.Fatal("keys lengths differ")
	}
	keys1[0] = "MODIFIED"
	if keys2[0] == "MODIFIED" {
		t.Error("ErrorTaxonomyKeys should return defensive copies")
	}
}

func TestReviewSeverityTaxonomy_Blocking(t *testing.T) {
	blocking := ReviewSeverityTaxonomy["Blocking"]
	expected := []string{"AUTH_BYPASS", "RACE_CONDITION", "DATA_INTEGRITY"}
	if len(blocking) != len(expected) {
		t.Fatalf("Blocking: expected %d categories, got %d", len(expected), len(blocking))
	}
	for i, exp := range expected {
		if blocking[i] != exp {
			t.Errorf("Blocking[%d]: expected %q, got %q", i, exp, blocking[i])
		}
	}
}

