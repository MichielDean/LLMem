# LLMem Search Reranking

Multi-signal reranking for hybrid search results. [Back to README](../README.md)

## Multi-Signal Reranking

After RRF fusion, search results are automatically reranked using a blend of the RRF score and four weighted signals:

```
final_score = rrf_score * (1 - blend) + weighted_signal * blend
```

**Default blend factor: 0.3** (70% RRF, 30% signals). Configure via `Retriever(store, embedder, blend=...)`. Range: 0.0 (pure RRF) to 1.0 (pure signals). Out-of-range values raise `ValueError`.

### Signals and Weights

| Signal | Weight | Formula |
|--------|--------|---------|
| Confidence | 0.4 | Direct use of `confidence` field (0.0–1.0, default 0.0 for missing) |
| Recency | 0.3 | `exp(-0.01 * days_since_access)` (0.0 if never accessed) |
| Access frequency | 0.2 | `log(1 + access_count / max(age_days, 1))` (0.0 if never accessed) |
| Type priority | 0.1 | Lookup in `TYPE_PRIORITY` dict (default 1.0 for unknown types) |

### Type Priority

| Type | Priority | | Type | Priority |
|------|----------|-|------|----------|
| decision | 1.2 | | fact | 1.0 |
| preference | 1.1 | | project_state | 1.0 |
| procedure | 1.1 | | event | 0.9 |
| | | | conversation | 0.7 |

Search results include both `_rrf_score` (raw RRF fusion score) and `_rerank_score` (blended final score). Results are sorted by `_rerank_score` descending, with ties broken by ascending memory ID. Search operations (`Retriever.search()` and `Retriever.hybrid_search()`) automatically track access — each returned result's `access_count` and `accessed_at` are updated (best-effort), keeping the recency and access frequency signals current. This Hebbian reinforcement is on by default (`track_access=True`); pass `track_access=False` to skip access tracking (useful for analytics queries that shouldn't inflate counts).

## Go API (internal/retriever)

The Go implementation in `internal/retriever` provides the same hybrid search and reranking pipeline:

```go
import (
    "github.com/MichielDean/LLMem/internal/embed"
    "github.com/MichielDean/LLMem/internal/retriever"
    "github.com/MichielDean/LLMem/internal/store"
)

ms, _ := store.NewMemoryStore(store.StoreConfig{})
eng, _ := embed.NewEmbeddingEngine(embed.EmbeddingConfig{})

// Create a retriever (embedder may be nil for FTS-only mode)
r, _ := retriever.NewRetriever(retriever.RetrieverConfig{
    Store:    ms,
    Embedder: eng,
})

// Hybrid search (default alpha=0.7, blend=0.3)
scored, err := r.HybridSearch(ctx, "query", 20, "fact", nil, "hybrid", true)

// FTS-only search (no embedder needed)
results, err := r.Search(ctx, "query", 20, "fact", false, 0, true)

// Format results as LLM context string
context, err := r.FormatContext(ctx, "query", 4000, "fact")
```

### Alpha and Blend (Pointer Semantics)

`Alpha` and `Blend` in `RetrieverConfig` use `*float64` to distinguish "use the default" (nil) from "explicitly set to 0.0":

```go
// nil → use default (alpha=0.7, blend=0.3)
retriever.NewRetriever(retriever.RetrieverConfig{Store: ms})

// *float64(0.0) → explicitly set to 0.0 (pure FTS for alpha, pure RRF for blend)
alpha := 0.0
blend := 0.0
retriever.NewRetriever(retriever.RetrieverConfig{
    Store: ms, Alpha: &alpha, Blend: &blend,
})
```

Out-of-range values (outside [0.0, 1.0]) return an error from `NewRetriever`.

### Search Modes

| Mode | `searchMode` | Embedder Required | Behavior |
|------|-------------|-------------------|----------|
| Hybrid | `"hybrid"` | Optional | RRF fusion of FTS + semantic. Falls back to FTS-only if embedder is nil (logs `slog.Warn`). |
| FTS-only | `"fts"` | No | BM25-ranked FTS5 search only. |
| Semantic-only | `"semantic"` | Yes | Vector cosine similarity only. Returns error if embedder is nil. |

### RRF Score Computation

```go
// Compute RRF scores directly
results := retriever.RRFScore(semanticRanks, ftsRanks, 0.7, 60)
```

### Reranking Signals

The Go implementation exports the same signals and weights as Python:

```go
// Compute reranking signals for a single memory
signals := retriever.ComputeRerankSignals(memory, typePriority, time.Now().UTC())
// signals.Confidence, signals.Recency, signals.Access, signals.Type

// Compute weighted signal (0.4*Conf + 0.3*Recency + 0.2*Access + 0.1*Type)
weighted := retriever.ComputeWeightedSignal(signals)

// Get default type priority map (returns defensive copy)
priorities := retriever.DefaultTypePriority()
```

The type priority weights are identical to Python (`decision: 1.2`, `preference: 1.1`, `procedure: 1.1`, `fact: 1.0`, `project_state: 1.0`, `event: 0.9`, `conversation: 0.7`). `NewRetriever` makes a defensive copy of the input map to prevent caller mutation.

### Access Tracking

`HybridSearch` and `Search` accept a `trackAccess` parameter. When `true`, calls `store.TouchBatch` on result IDs (best-effort, never propagates errors). This keeps the recency and access frequency signals current.