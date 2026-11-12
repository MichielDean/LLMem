// Package store provides a SQLite-backed memory store with FTS5 full-text
// search and vector search via sqlite-vec for the LLMem project.
package store

// Memory represents a single memory record in the store.
type Memory struct {
	ID          string
	Type        string
	Content     string
	Summary     string
	Hints       []string
	Source      string
	Confidence  float64
	ValidFrom   string
	ValidUntil  string
	CreatedAt   string
	UpdatedAt   string
	AccessedAt  string
	AccessCount int
	Metadata    map[string]any
	Embedding   []byte
	Pinned      bool
}

// Relation represents a relationship between two memories.
type Relation struct {
	ID           string
	SourceID     string
	TargetID     string
	RelationType string
	CreatedAt    string
}

// ExtractionLog represents an extraction log entry.
type ExtractionLog struct {
	ID             int64
	SourceType     string
	SourceID       string
	RawText        string
	ExtractedCount int
	CreatedAt      string
}

// AddParams contains the parameters for adding a new memory.
type AddParams struct {
	ID         string
	Type       string
	Content    string
	Summary    string
	Source     string
	Confidence float64
	ValidUntil string
	Metadata   map[string]any
	Embedding  []byte
	Hints      []string
}

// UpdateParams contains the parameters for updating a memory.
type UpdateParams struct {
	ID             string
	Content        *string
	Summary        *string
	Confidence     *float64
	ValidUntil     *string
	Metadata       map[string]any
	Embedding      []byte
	ClearEmbedding bool
	Hints          []string
}

// SearchParams contains the parameters for searching memories.
type SearchParams struct {
	Query       string
	Type        string
	ValidOnly   bool
	Limit       int
	Offset      int
	FTSOnly     bool
	SemanticOnly bool
}

// SearchCountParams contains the parameters for counting search results.
type SearchCountParams struct {
	Query     string
	Type      string
	ValidOnly bool
}

// ListParams contains the parameters for listing memories.
type ListParams struct {
	Type      string
	ValidOnly bool
	Limit     int
}

// FindSimilarParams contains the parameters for finding similar memories.
type FindSimilarParams struct {
	QueryVec  []float32
	Content   string
	Threshold float64
	Limit     int
}

// ScoredMemory represents a memory with a similarity score.
type ScoredMemory struct {
	Memory *Memory
	Score  float64
}

// DuplicatePair represents a pair of similar memories.
type DuplicatePair struct {
	SourceID string
	TargetID string
	Score    float64
}

// TraversedRelation represents a relation reached during traversal.
type TraversedRelation struct {
	TargetID      string
	RelationType  string
	Distance      int
	RelationScore float64
}

// ImportMemory represents a memory to be imported.
type ImportMemory struct {
	ID         string
	Type       string
	Content    string
	Summary    string
	Source     string
	Confidence float64
	Metadata   map[string]any
	Embedding  []byte
	Hints      []string
}

// EmbeddingWithType pairs an embedding with its memory type.
type EmbeddingWithType struct {
	Embedding []byte
	Type      string
}

// ValidRelationTypes returns the set of allowed relation types.
func ValidRelationTypes() []string {
	return []string{"supersedes", "related_to", "derived_from"}
}

// DefaultRegisteredTypes returns the three memory tier types.
// The legacy 7 types have been removed in the contract phase.
// Returns a new slice each time (defensive copy).
// Must match the types registered in migration 009.
func DefaultRegisteredTypes() []string {
	return []string{
		"stable",
		"volatile",
		"log",
	}
}

// NewTierTypes returns the three new memory tier types.
func NewTierTypes() []string {
	return []string{"stable", "volatile", "log"}
}

// IsNewTierType returns true if the given type is one of the three new tiers.
func IsNewTierType(typ string) bool {
	for _, t := range NewTierTypes() {
		if t == typ {
			return true
		}
	}
	return false
}

// TTL constants for the new tier types.
const (
	TTLStableDays   = 90
	TTLVolatileDays = 14
	TTLLLogDays     = 90
)

// DefaultTTLForType returns the default TTL in days for a given memory type.
// Returns 0 for legacy types (no auto-TTL) and for unrecognized types.
func DefaultTTLForType(typ string) int {
	switch typ {
	case "stable":
		return TTLStableDays
	case "volatile":
		return TTLVolatileDays
	case "log":
		return TTLLLogDays
	default:
		return 0
	}
}

// StoreConfig contains the configuration for creating a MemoryStore.
type StoreConfig struct {
	// DBPath is the path to the SQLite database file.
	// If empty, defaults to ~/.config/llmem/memory.db.
	DBPath string

	// VecDimensions is the dimensionality for the embedding index.
	// Defaults to 768 if zero. Must be positive.
	VecDimensions int

	// DisableVec skips vec virtual table creation and vector search.
	DisableVec bool

	// RegisteredTypes lists the memory types to register at construction.
	// If empty, defaults to the 7 standard types from DefaultRegisteredTypes().
	RegisteredTypes []string
}