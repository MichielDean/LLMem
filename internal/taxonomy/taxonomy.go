// Package taxonomy provides error taxonomy constants for the LLMem project.
package taxonomy

// ErrorTaxonomy maps error category keys to their descriptions.
// This is a constant map — it should never be modified at runtime.
var ErrorTaxonomy = map[string]string{
	"NULL_SAFETY":        "Missing null/None/undefined checks before property access or method calls",
	"ERROR_HANDLING":     "Missing try/except, bare except, swallowed errors, unhandled promise rejections",
	"OFF_BY_ONE":         "Boundary errors, wrong loop bounds, fencepost errors",
	"RACE_CONDITION":     "Concurrency issues, async/await problems, missing locks",
	"AUTH_BYPASS":        "Missing auth checks, SSRF, injection vulnerabilities, security oversights",
	"DATA_INTEGRITY":     "Stale derived fields, out-of-sync caches/embeddings/indexes, source-of-truth divergence",
	"MISSING_VERIFICATION": "Skipped test steps, unverified outputs, assumed-it-works",
	"EDGE_CASE":          "Unhandled empty input, unexpected types, boundary values",
	"PERFORMANCE":        "N+1 queries, unnecessary recomputation, memory leaks",
	"DESIGN":             "Architectural issues, wrong abstraction level, coupling problems",
	"REVIEW_PASSED":      "Clean review with no findings — positive outcome for tracking purposes",
}

// ErrorTaxonomyKeys returns the ordered list of error taxonomy category keys.
// Returns a new slice each time (defensive copy).
func ErrorTaxonomyKeys() []string {
	return []string{
		"NULL_SAFETY",
		"ERROR_HANDLING",
		"OFF_BY_ONE",
		"RACE_CONDITION",
		"AUTH_BYPASS",
		"DATA_INTEGRITY",
		"MISSING_VERIFICATION",
		"EDGE_CASE",
		"PERFORMANCE",
		"DESIGN",
		"REVIEW_PASSED",
	}
}

// ReviewSeverityTaxonomy maps severity levels to their associated error categories.
// This maps human-facing severity labels to the taxonomy categories they encompass.
var ReviewSeverityTaxonomy = map[string][]string{
	"Blocking":          {"AUTH_BYPASS", "RACE_CONDITION", "DATA_INTEGRITY"},
	"Required":          {"NULL_SAFETY", "ERROR_HANDLING", "MISSING_VERIFICATION", "EDGE_CASE"},
	"Strong Suggestions": {"PERFORMANCE", "DESIGN"},
	"Noted":             {"OFF_BY_ONE"},
	"Passed":            {"REVIEW_PASSED"},
}