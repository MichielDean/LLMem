// Package retriever provides hybrid search combining FTS5 and vector cosine
// similarity via Reciprocal Rank Fusion (RRF) with multi-signal reranking.
// This is specific to LLMem retrieval and will not be reused.
package retriever

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/MichielDean/LLMem/internal/embed"
	"github.com/MichielDean/LLMem/internal/store"
)

const (
	defaultAlpha   = 0.7
	defaultBlend   = 0.3
	defaultRRF_K   = 60
	confidenceWeight = 0.4
	recencyWeight  = 0.3
	accessWeight   = 0.2
	typeWeight     = 0.1
	defaultBudget  = 4000
)

// defaultTypePriorityMap holds the default type priority weights.
// This is an unexported package-level immutable map — it is never modified
// after initialization and is only read via DefaultTypePriority() which
// returns defensive copies.
var defaultTypePriorityMap = map[string]float64{
	"decision":      1.2,
	"preference":    1.1,
	"procedure":     1.1,
	"fact":          1.0,
	"project_state": 1.0,
	"event":         0.9,
	"conversation":  0.7,
}

// RerankSignals holds per-memory reranking signal values.
type RerankSignals struct {
	Confidence float64
	Recency    float64
	Access     float64
	Type       float64
}

// RRFResult holds an ID and its RRF score.
type RRFResult struct {
	ID    string
	Score float64
}

// ScoredMemory extends store.Memory with RRF and reranking scores.
type ScoredMemory struct {
	Memory     *store.Memory
	RRFScore   float64
	RerankScore float64
}

// RetrieverConfig contains the configuration for creating a Retriever.
type RetrieverConfig struct {
	// Store is required. Error if nil.
	Store *store.MemoryStore

	// Embedder is optional. Nil means FTS-only mode.
	Embedder *embed.EmbeddingEngine

	// Blend is the reranking blend factor. 0.0 = pure RRF, 1.0 = pure signals.
	// Use a pointer to distinguish "use default" (nil → 0.3) from "explicitly 0.0" (pure RRF).
	// Defaults to 0.3 if nil.
	Blend *float64

	// Alpha is the RRF semantic weight. 0.0 = pure FTS, 1.0 = pure semantic.
	// Use a pointer to distinguish "use default" (nil → 0.7) from "explicitly 0.0" (pure FTS).
	// Defaults to 0.7 if nil.
	Alpha *float64

	// RRF_K is the RRF constant. Defaults to 60 if zero.
	RRF_K int

	// TypePriority is the type priority map for reranking. If nil, defaults to DefaultTypePriority().
	TypePriority map[string]float64
}

// Retriever performs hybrid search combining FTS5 and semantic vector search.
// This is specific to LLMem retrieval and will not be reused.
type Retriever struct {
	store         *store.MemoryStore
	embedder      *embed.EmbeddingEngine
	blend         float64
	alpha         float64
	rrfK          int
	typePriority  map[string]float64
}

// fmtErr wraps an error with the "llmem: retriever:" domain prefix.
func fmtErr(format string, args ...any) error {
	return fmt.Errorf("llmem: retriever: "+format, args...)
}

// truncateToValidUTF8 truncates s to at most maxBytes bytes, ensuring the
// result is a valid UTF-8 string. If truncation would split a multi-byte
// sequence, it backs up to the previous valid rune boundary.
func truncateToValidUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backward from maxBytes to find a valid rune start boundary.
	for i := maxBytes; i > 0; i-- {
		// In UTF-8, continuation bytes have the pattern 10xxxxxx (0x80-0xBF).
		// A start byte has either 0xxxxxxx (ASCII), 110xxxxx, 1110xxxx, or 11110xxx.
		if s[i]&0xC0 != 0x80 {
			return s[:i]
		}
	}
	// Fallback: maxBytes itself should be valid since s[0] is a start byte.
	return s[:maxBytes]
}

// NewRetriever creates and initializes a Retriever.
// Blend and Alpha use pointer values: nil means "apply default", while a pointer
// to 0.0 means "explicitly set to 0.0". This makes both 0.0 (pure FTS/pure RRF)
// and the defaults (0.7/0.3) expressible.
// If cfg.Blend is not nil and outside [0.0, 1.0], returns error.
// If cfg.Alpha is not nil and outside [0.0, 1.0], returns error.
// If cfg.Store == nil, returns error.
// Embedder may be nil (FTS-only mode).
// The constructor leaves the retriever in a fully usable state.
func NewRetriever(cfg RetrieverConfig) (*Retriever, error) {
	if cfg.Store == nil {
		return nil, fmtErr("store is required")
	}

	blend := defaultBlend
	if cfg.Blend != nil {
		blend = *cfg.Blend
	}
	if blend < 0.0 || blend > 1.0 {
		return nil, fmtErr("blend factor %v out of range [0.0, 1.0]", blend)
	}

	alpha := defaultAlpha
	if cfg.Alpha != nil {
		alpha = *cfg.Alpha
	}
	if alpha < 0.0 || alpha > 1.0 {
		return nil, fmtErr("alpha %v out of range [0.0, 1.0]", alpha)
	}

	rrfK := cfg.RRF_K
	if rrfK == 0 {
		rrfK = defaultRRF_K
	}

	typePriority := cfg.TypePriority
	if typePriority == nil {
		typePriority = DefaultTypePriority()
	} else {
		// Defensive copy to prevent caller from mutating retriever state
		cp := make(map[string]float64, len(typePriority))
		for k, v := range typePriority {
			cp[k] = v
		}
		typePriority = cp
	}

	return &Retriever{
		store:        cfg.Store,
		embedder:     cfg.Embedder,
		blend:        blend,
		alpha:        alpha,
		rrfK:         rrfK,
		typePriority:  typePriority,
	}, nil
}

// Search performs basic FTS5 search and optionally traverses relations.
// If trackAccess is true, calls store.TouchBatch on result IDs.
// When no results are found, returns nil.
func (r *Retriever) Search(ctx context.Context, query string, limit int, typeFilter string, traverseRelations bool, relationDepth int, trackAccess bool) ([]*store.Memory, error) {
	if limit <= 0 {
		limit = 20
	}

	results, err := r.store.Search(ctx, store.SearchParams{
		Query: query,
		Type:  typeFilter,
		Limit: limit,
	})
	if err != nil {
		return nil, fmtErr("search: %w", err)
	}

	if trackAccess && len(results) > 0 {
		ids := make([]string, len(results))
		for i, m := range results {
			ids[i] = m.ID
		}
		// Best-effort, never propagates errors
		if _, err := r.store.TouchBatch(ctx, ids); err != nil {
			slog.Debug("llmem: retriever: failed to track access", "error", err)
		}
	}

	if traverseRelations && len(results) > 0 {
		memIDs := make([]string, len(results))
		for i, m := range results {
			memIDs[i] = m.ID
		}
		related, err := r.store.TraverseRelations(ctx, memIDs, relationDepth)
		if err != nil {
			slog.Debug("llmem: retriever: failed to traverse relations", "error", err)
		} else if len(related) > 0 {
			resultIDs := make(map[string]bool)
			for _, m := range results {
				resultIDs[m.ID] = true
			}
			relatedIDs := make([]string, 0)
			for _, rel := range related {
				if !resultIDs[rel.TargetID] {
					relatedIDs = append(relatedIDs, rel.TargetID)
				}
			}
			if len(relatedIDs) > 0 {
				relatedMems, err := r.store.GetBatch(ctx, relatedIDs, false)
				if err != nil {
					slog.Debug("llmem: retriever: failed to fetch related memories", "error", err)
				} else {
					remaining := limit - len(resultIDs)
					if remaining < 0 {
						remaining = 0
					}
					added := 0
					for _, id := range relatedIDs {
						if added >= remaining {
							break
						}
						if m, ok := relatedMems[id]; ok {
							results = append(results, m)
							added++
						}
					}
				}
			}
		}
	}

	return results, nil
}

// HybridSearch performs hybrid RRF fusion search combining FTS5 and semantic results.
// searchMode must be one of "hybrid", "fts", "semantic".
// alpha controls the semantic/FTS blend: 0.0 = pure FTS, 1.0 = pure semantic.
// Use nil for alpha to use the retriever's default alpha (0.7).
// Use ptrFloat64(0.0) to explicitly request pure FTS mode.
// Returns an error for invalid searchMode.
// When searchMode="hybrid" and embedder is nil, logs slog.Warn and falls back to FTS-only.
// When searchMode="semantic" and embedder is nil, returns error.
func (r *Retriever) HybridSearch(ctx context.Context, query string, limit int, typeFilter string, alpha *float64, searchMode string, trackAccess bool) ([]*ScoredMemory, error) {
	if query == "" {
		return []*ScoredMemory{}, nil
	}

	validModes := map[string]bool{"hybrid": true, "fts": true, "semantic": true}
	if !validModes[searchMode] {
		return nil, fmtErr("invalid search_mode %q, must be one of [fts, hybrid, semantic]", searchMode)
	}

	if limit <= 0 {
		limit = 20
	}

	effectiveAlpha := r.alpha
	if alpha != nil {
		effectiveAlpha = *alpha
	}

	switch searchMode {
	case "fts":
		return r.hybridSearchFTS(ctx, query, limit, typeFilter, trackAccess)
	case "semantic":
		return r.hybridSearchSemantic(ctx, query, limit, typeFilter, effectiveAlpha, trackAccess)
	default: // "hybrid"
		return r.hybridSearchFull(ctx, query, limit, typeFilter, effectiveAlpha, trackAccess)
	}
}

func (r *Retriever) hybridSearchFTS(ctx context.Context, query string, limit int, typeFilter string, trackAccess bool) ([]*ScoredMemory, error) {
	ftsResults, err := r.store.Search(ctx, store.SearchParams{
		Query: query,
		Type:  typeFilter,
		Limit: limit,
	})
	if err != nil {
		return nil, fmtErr("fts search: %w", err)
	}

	ftsRanks := make(map[string]int)
	for i, m := range ftsResults {
		ftsRanks[m.ID] = i + 1
	}

	scored := RRFScore(map[string]int{}, ftsRanks, 0.0, r.rrfK)
	scoreByID := make(map[string]float64)
	for _, s := range scored {
		scoreByID[s.ID] = s.Score
	}

	var results []*ScoredMemory
	for _, m := range ftsResults {
		sm := &ScoredMemory{
			Memory:   m,
			RRFScore: scoreByID[m.ID],
		}
		results = append(results, sm)
	}

	results = r.applyReranking(results)

	if trackAccess && len(results) > 0 {
		r.trackAccess(ctx, results)
	}

	return results, nil
}

func (r *Retriever) hybridSearchSemantic(ctx context.Context, query string, limit int, typeFilter string, alpha float64, trackAccess bool) ([]*ScoredMemory, error) {
	if r.embedder == nil {
		return nil, fmtErr("semantic search requires an embedder")
	}

	queryVec, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmtErr("embed query: %w", err)
	}

	semanticResults, err := r.store.SearchByEmbedding(ctx, queryVec, false, limit, 0.0)
	if err != nil {
		return nil, fmtErr("semantic search: %w", err)
	}

	semanticRanks := make(map[string]int)
	for i, sm := range semanticResults {
		semanticRanks[sm.Memory.ID] = i + 1
	}

	scored := RRFScore(semanticRanks, map[string]int{}, 1.0, r.rrfK)
	scoreByID := make(map[string]float64)
	for _, s := range scored {
		scoreByID[s.ID] = s.Score
	}

	var results []*ScoredMemory
	for _, sm := range semanticResults {
		result := &ScoredMemory{
			Memory:   sm.Memory,
			RRFScore: scoreByID[sm.Memory.ID],
		}
		if typeFilter == "" || sm.Memory.Type == typeFilter {
			results = append(results, result)
		}
	}

	results = r.applyReranking(results)

	if trackAccess && len(results) > 0 {
		r.trackAccess(ctx, results)
	}

	return results, nil
}

func (r *Retriever) hybridSearchFull(ctx context.Context, query string, limit int, typeFilter string, alpha float64, trackAccess bool) ([]*ScoredMemory, error) {
	if r.embedder == nil {
		slog.Warn("llmem: retriever: embedder not configured, falling back to FTS5-only")
		return r.hybridSearchFTS(ctx, query, limit, typeFilter, trackAccess)
	}

	ftsResults, err := r.store.Search(ctx, store.SearchParams{
		Query: query,
		Type:  typeFilter,
		Limit: limit,
	})
	if err != nil {
		return nil, fmtErr("fts search: %w", err)
	}

	ftsRanks := make(map[string]int)
	for i, m := range ftsResults {
		ftsRanks[m.ID] = i + 1
	}

	var semanticResults []*store.ScoredMemory
	queryVec, embedErr := r.embedder.Embed(ctx, query)
	if embedErr != nil {
		slog.Warn("llmem: retriever: semantic search failed, falling back to FTS5-only", "error", embedErr)
		return r.hybridSearchFTS(ctx, query, limit, typeFilter, trackAccess)
	}

	semanticResults, err = r.store.SearchByEmbedding(ctx, queryVec, false, limit, 0.0)
	if err != nil {
		slog.Warn("llmem: retriever: semantic search failed, falling back to FTS5-only", "error", err)
		return r.hybridSearchFTS(ctx, query, limit, typeFilter, trackAccess)
	}

	semanticRanks := make(map[string]int)
	for i, sm := range semanticResults {
		semanticRanks[sm.Memory.ID] = i + 1
	}

	// Compute RRF scores
	scored := RRFScore(semanticRanks, ftsRanks, alpha, r.rrfK)

	// Merge result dicts, deduplicating by memory ID
	allResults := make(map[string]*store.Memory)
	for _, m := range ftsResults {
		allResults[m.ID] = m
	}
	for _, sm := range semanticResults {
		if _, ok := allResults[sm.Memory.ID]; !ok {
			allResults[sm.Memory.ID] = sm.Memory
		}
	}

	// Build final sorted list with RRF scores
	var results []*ScoredMemory
	for _, s := range scored {
		if m, ok := allResults[s.ID]; ok {
			results = append(results, &ScoredMemory{
				Memory:   m,
				RRFScore: s.Score,
			})
		}
	}

	results = r.applyReranking(results)

	if trackAccess && len(results) > 0 {
		r.trackAccess(ctx, results)
	}

	return results, nil
}

// FormatContext formats hybrid search results as an LLM context string.
// Truncates to budget characters (default 4000). Returns empty string when no results.
func (r *Retriever) FormatContext(ctx context.Context, query string, budget int, typeFilter string) (string, error) {
	if budget <= 0 {
		budget = defaultBudget
	}

	results, err := r.HybridSearch(ctx, query, 20, typeFilter, nil, "hybrid", false)
	if err != nil {
		return "", fmtErr("format context: %w", err)
	}

	if len(results) == 0 {
		return "", nil
	}

	var lines []string
	for _, sm := range results {
		line := fmt.Sprintf("- [%s] %s", sm.Memory.Type, sm.Memory.Content)
		if sm.Memory.Summary != "" {
			line += fmt.Sprintf(" (summary: %s)", sm.Memory.Summary)
		}
		lines = append(lines, line)
	}

	context := strings.Join(lines, "\n")
	if len(context) > budget {
		// Truncate at a valid UTF-8 boundary to avoid splitting multi-byte characters.
		context = truncateToValidUTF8(context, budget)
	}
	return context, nil
}

// RRFScore computes Reciprocal Rank Fusion scores from semantic and FTS rank maps.
// k defaults to 60 if 0. Returns sorted by score descending, ties broken by ascending ID.
// Empty inputs return nil.
func RRFScore(semanticRanks map[string]int, ftsRanks map[string]int, alpha float64, k int) []RRFResult {
	if len(semanticRanks) == 0 && len(ftsRanks) == 0 {
		return nil
	}

	if k <= 0 {
		k = defaultRRF_K
	}

	allIDs := make(map[string]bool)
	for id := range semanticRanks {
		allIDs[id] = true
	}
	for id := range ftsRanks {
		allIDs[id] = true
	}

	nSemantic := len(semanticRanks)
	nFTS := len(ftsRanks)

	var results []RRFResult
	for id := range allIDs {
		semanticRank := semanticRanks[id]
		if semanticRank == 0 {
			semanticRank = nSemantic + 1
		}
		ftsRank := ftsRanks[id]
		if ftsRank == 0 {
			ftsRank = nFTS + 1
		}
		score := alpha*float64(1)/(float64(k)+float64(semanticRank)) + (1-alpha)*float64(1)/(float64(k)+float64(ftsRank))
		results = append(results, RRFResult{ID: id, Score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})

	return results
}

// ComputeRerankSignals computes per-memory reranking signals from memory fields.
// Returns a RerankSignals struct with Confidence, Recency, Access, and Type values.
func ComputeRerankSignals(memory *store.Memory, typePriority map[string]float64, now time.Time) RerankSignals {
	confidence := memory.Confidence

	// Recency signal: exp(-0.01 * days_since_access)
	recency := 0.0
	if memory.AccessedAt != "" {
		accessedAt, err := time.Parse(time.RFC3339, memory.AccessedAt)
		if err == nil {
			daysSince := now.Sub(accessedAt).Hours() / 24
			recency = math.Exp(-0.01 * daysSince)
		} else {
			slog.Debug("llmem: retriever: unparseable accessed_at", "value", memory.AccessedAt)
		}
	}

	// Access frequency signal: log(1 + access_count / max(age_days, 1))
	accessCount := memory.AccessCount
	ageDays := 1
	if memory.CreatedAt != "" {
		createdAt, err := time.Parse(time.RFC3339, memory.CreatedAt)
		if err == nil {
			days := int(now.Sub(createdAt).Hours() / 24)
			if days > 0 {
				ageDays = days
			}
		} else {
			slog.Debug("llmem: retriever: unparseable created_at", "value", memory.CreatedAt)
		}
	}

	access := 0.0
	if accessCount > 0 {
		access = math.Log(1 + float64(accessCount)/float64(ageDays))
	}

	// Type priority signal
	typeSignal := 1.0
	if prio, ok := typePriority[memory.Type]; ok {
		typeSignal = prio
	}

	return RerankSignals{
		Confidence: confidence,
		Recency:    recency,
		Access:     access,
		Type:       typeSignal,
	}
}

// ComputeWeightedSignal combines confidence, recency, access, and type signals using weights.
// Returns 0.4*Confidence + 0.3*Recency + 0.2*Access + 0.1*Type.
func ComputeWeightedSignal(signals RerankSignals) float64 {
	return confidenceWeight*signals.Confidence +
		recencyWeight*signals.Recency +
		accessWeight*signals.Access +
		typeWeight*signals.Type
}

// DefaultTypePriority returns the default type priority weights.
// Returns a new map each time (defensive copy).
func DefaultTypePriority() map[string]float64 {
	result := make(map[string]float64, len(defaultTypePriorityMap))
	for k, v := range defaultTypePriorityMap {
		result[k] = v
	}
	return result
}

// applyReranking applies multi-signal reranking to search results.
// Modifies each result's RerankScore and re-sorts by RerankScore descending.
func (r *Retriever) applyReranking(results []*ScoredMemory) []*ScoredMemory {
	if len(results) == 0 {
		return results
	}

	now := time.Now().UTC()

	for _, sm := range results {
		if r.blend == 0.0 {
			sm.RerankScore = sm.RRFScore
			continue
		}
		signals := ComputeRerankSignals(sm.Memory, r.typePriority, now)
		weighted := ComputeWeightedSignal(signals)
		sm.RerankScore = sm.RRFScore*(1-r.blend) + weighted*r.blend
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].RerankScore != results[j].RerankScore {
			return results[i].RerankScore > results[j].RerankScore
		}
		return results[i].Memory.ID < results[j].Memory.ID
	})

	return results
}

// trackAccess calls store.TouchBatch on result IDs. Best-effort, never propagates errors.
func (r *Retriever) trackAccess(ctx context.Context, results []*ScoredMemory) {
	if len(results) == 0 {
		return
	}
	ids := make([]string, len(results))
	for i, sm := range results {
		ids[i] = sm.Memory.ID
	}
	if _, err := r.store.TouchBatch(ctx, ids); err != nil {
		slog.Debug("llmem: retriever: failed to track access", "count", len(results), "error", err)
	}
}