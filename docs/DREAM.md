# LLMem Dream Cycle & Extraction

Automated memory maintenance and session extraction pipelines. [Back to README](../README.md)

## Dream Cycle

The dream cycle performs automated memory maintenance during idle periods. It can be invoked manually via `llmem dream` (see [CLI Reference](CLI.md#llmem-dream)) or run automatically by a systemd timer.

- **Light phase:** Sort and deduplicate near-duplicate memories (cosine similarity ≥ threshold).
- **Deep phase:** Score, promote, decay, and merge memories. Decays confidence on idle memories. Boosts frequently accessed memories. Auto-links memories with high cosine similarity (≥ `dream.auto_link_threshold`, default 0.85) by creating `related_to` relations between them. Procedure memories older than `dream.stale_procedure_days` (default 30 days) with no recent access decay at double the normal rate — proposed-but-never-adopted procedures fade faster than confirmed ones.
- **REM phase:** Extract themes from memory clusters and write a dream diary (read-only reflection). Produces type counts, word clusters, and total/active memory counts.

Configuration is under the `dream:` key in `config.yaml`. See [Configuration](CONFIGURATION.md) for all dream settings.

### Systemd Timer

The Go implementation includes systemd unit generation for running the dream cycle on a schedule:

```bash
# Generate service and timer unit files
llmem dream --apply  # manual run
```

The `internal/systemd` package provides `GenerateServiceUnit()` and `GenerateTimerUnit()` to produce systemd `.service` and `.timer` unit files from embedded templates. Timer schedules are validated via `ValidateSchedule()` to prevent shell metacharacter injection.

### Go API — Dream Package

The `internal/dream` package provides the Go dream cycle implementation:

```go
import "github.com/MichielDean/LLMem/internal/dream"

dreamer, err := dream.NewDreamer(dream.DreamerConfig{
    Store:                 ms,
    SimilarityThreshold:   0.92,   // default
    DecayRate:             0.05,    // default
    DecayIntervalDays:     30,     // default
    DecayFloor:            0.3,    // default
    ConfidenceFloor:       0.3,    // default
    BoostThreshold:        5,      // default
    BoostAmount:           0.05,   // default
    AutoLinkThreshold:     0.85,   // default
    StaleProcedureDays:    30,     // default — procedure memories older than this decay at 2x
    DiaryPath:             "",     // defaults from paths.GetDreamDiaryPath()
    ReportPath:            "",     // defaults from paths.GetDreamReportPath()
})
if err != nil {
    log.Fatal(err)
}

// Run all phases (dry run)
result, err := dreamer.Run(ctx, false, "")

// Run a specific phase
result, err := dreamer.Run(ctx, true, "deep")

// Write dream diary (markdown with sync.Mutex for in-process concurrency)
err = dreamer.WriteDiary(result)

// Generate HTML dream report
err = dreamer.GenerateDreamReport(result, "/path/to/report.html")
```

#### DreamerConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| Store | *store.MemoryStore | (required) | Memory store instance |
| SimilarityThreshold | float64 | 0.92 | Cosine similarity threshold for duplicate detection |
| DecayRate | float64 | 0.05 | Confidence decay rate for idle memories |
| DecayIntervalDays | int | 30 | Grace period before decay starts |
| DecayFloor | float64 | 0.3 | Minimum confidence after decay |
| ConfidenceFloor | float64 | 0.3 | Memories below this are invalidated |
| BoostThreshold | int | 5 | Access count threshold for boosting |
| BoostAmount | float64 | 0.05 | Confidence boost per event |
| AutoLinkThreshold | float64 | 0.85 | Cosine similarity threshold for auto-linking |
| StaleProcedureDays | int | 30 | Age threshold (days) for double-decay of procedure memories |
| DiaryPath | string | paths.GetDreamDiaryPath() | Path for dream diary markdown |
| ReportPath | string | paths.GetDreamReportPath() | Path for HTML dream report |

#### DreamResult

```go
type DreamResult struct {
    Light *LightPhaseResult
    Deep  *DeepPhaseResult
    Rem   *RemPhaseResult
}

type LightPhaseResult struct {
    DuplicatePairs  int
    MergeCandidates []*store.DuplicatePair
}

type DeepPhaseResult struct {
    DecayedCount               int
    StaleProcedureDecayedCount int
    BoostedCount               int
    InvalidatedCount           int
    MergedCount                int
    AutoLinkedCount            int
}

type RemPhaseResult struct {
    TotalMemories  int
    ActiveMemories int
    Themes         []string
}
```

## Extraction and Hooks

### Python

The `hooks` module provides automatic extraction from session transcripts:

- `process_file()`: Extract memories from a transcript file.
- `process_session()`: Extract from an OpenCode session ID.
- `process_all_session_sources()`: Process all session sources (delegates to `session_hooks.process_opencode_sessions`).

The `session_hooks` module provides `process_opencode_sessions()` — the full pipeline that reads OpenCode sessions from the SQLite database, chunks them by message boundaries, and runs extraction and embedding.

The `extract` module uses Ollama (default: `qwen2.5:1.5b`) to extract structured memories from text. The `embed` module generates embeddings using Ollama (default: `nomic-embed-text`).

### Go

The `internal/extract` package provides LLM-based memory extraction via Ollama:

```go
import "github.com/MichielDean/LLMem/internal/extract"

engine, err := extract.NewExtractionEngine(extract.ExtractionConfig{
    Model:   "glm-5.1:cloud",     // defaults to "glm-5.1:cloud"
    BaseURL: "http://localhost:11434",  // defaults to localhost
})
if err != nil {
    log.Fatal(err)
}

// Extract memories from text. Returns empty slice if Ollama is unavailable (graceful degradation).
memories := engine.Extract(ctx, text)

// Check if the extraction model is available
available := engine.CheckAvailable(ctx)
```
