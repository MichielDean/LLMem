// Package dream provides the 3-phase dream consolidation system for LLMem.
// Phases: Light (deduplication), Deep (decay/boost/merge/auto-link), REM (themes).
package dream

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MichielDean/LLMem/internal/paths"
	"github.com/MichielDean/LLMem/internal/store"
)

// Default configuration values.
const (
	defaultSimilarityThreshold    = 0.92
	defaultDecayRate              = 0.05
	defaultDecayIntervalDays      = 30
	defaultDecayFloor             = 0.3
	defaultConfidenceFloor        = 0.3
	defaultBoostThreshold         = 5
	defaultBoostAmount            = 0.05
	defaultAutoLinkThreshold      = 0.85
	defaultStaleProcedureDays     = 30
)

// DreamerConfig contains the configuration for creating a Dreamer.
type DreamerConfig struct {
	// Store is required. Error if nil.
	Store *store.MemoryStore

	// SimilarityThreshold for duplicate detection. Defaults to 0.92.
	SimilarityThreshold float64

	// DecayRate for idle memories. Defaults to 0.05.
	DecayRate float64

	// DecayIntervalDays grace period before decay. Defaults to 30.
	DecayIntervalDays int

	// DecayFloor minimum confidence after decay. Defaults to 0.3.
	DecayFloor float64

	// ConfidenceFloor below this, invalidate. Defaults to 0.3.
	ConfidenceFloor float64

	// BoostThreshold access count for boost. Defaults to 5.
	BoostThreshold int

	// BoostAmount confidence boost per boost event. Defaults to 0.05.
	BoostAmount float64

	// AutoLinkThreshold for auto-linking. Defaults to 0.85.
	AutoLinkThreshold float64

	// StaleProcedureDays is the age threshold (in days) after which a procedure
	// memory with no recent access is considered stale and decays at double rate.
	// Defaults to 30. Zero value uses the default.
	StaleProcedureDays int

	// DiaryPath path for writing dream diary. Defaults from paths.GetDreamDiaryPath().
	DiaryPath string

	// ReportPath path for writing dream report. Defaults from paths.GetDreamReportPath().
	ReportPath string
}

// LightPhaseResult holds the results of the light (deduplication) phase.
type LightPhaseResult struct {
	DuplicatePairs  int
	MergeCandidates []*store.DuplicatePair
}

// DeepPhaseResult holds the results of the deep (decay/boost/merge) phase.
type DeepPhaseResult struct {
	DecayedCount               int
	StaleProcedureDecayedCount int
	BoostedCount               int
	InvalidatedCount          int
	MergedCount               int
	AutoLinkedCount           int
}

// RemPhaseResult holds the results of the REM (reflect) phase.
type RemPhaseResult struct {
	TotalMemories  int
	ActiveMemories int
	Themes         []string
}

// DreamResult holds the combined results of all dream phases.
type DreamResult struct {
	Light *LightPhaseResult
	Deep  *DeepPhaseResult
	Rem   *RemPhaseResult
}

// Dreamer performs 3-phase dream consolidation.
type Dreamer struct {
	store                  *store.MemoryStore
	similarityThreshold    float64
	decayRate              float64
	decayIntervalDays      int
	decayFloor             float64
	confidenceFloor        float64
	boostThreshold         int
	boostAmount            float64
	autoLinkThreshold      float64
	staleProcedureDays     int
	diaryPath              string
	reportPath             string
	mu                     sync.Mutex
}

// fmtErr wraps an error with the "llmem: dream:" domain prefix.
func fmtErr(format string, args ...any) error {
	return fmt.Errorf("llmem: dream: "+format, args...)
}

// NewDreamer creates and initializes a Dreamer.
// All config fields default to sensible values if zero.
// The constructor leaves the dreamer in a fully usable state.
func NewDreamer(cfg DreamerConfig) (*Dreamer, error) {
	if cfg.Store == nil {
		return nil, fmtErr("store is required")
	}

	similarityThreshold := cfg.SimilarityThreshold
	if similarityThreshold == 0 {
		similarityThreshold = defaultSimilarityThreshold
	}
	decayRate := cfg.DecayRate
	if decayRate == 0 {
		decayRate = defaultDecayRate
	}
	decayIntervalDays := cfg.DecayIntervalDays
	if decayIntervalDays == 0 {
		decayIntervalDays = defaultDecayIntervalDays
	}
	decayFloor := cfg.DecayFloor
	if decayFloor == 0 {
		decayFloor = defaultDecayFloor
	}
	confidenceFloor := cfg.ConfidenceFloor
	if confidenceFloor == 0 {
		confidenceFloor = defaultConfidenceFloor
	}
	boostThreshold := cfg.BoostThreshold
	if boostThreshold == 0 {
		boostThreshold = defaultBoostThreshold
	}
	boostAmount := cfg.BoostAmount
	if boostAmount == 0 {
		boostAmount = defaultBoostAmount
	}
	autoLinkThreshold := cfg.AutoLinkThreshold
	if autoLinkThreshold == 0 {
		autoLinkThreshold = defaultAutoLinkThreshold
	}
	staleProcedureDays := cfg.StaleProcedureDays
	if staleProcedureDays == 0 {
		staleProcedureDays = defaultStaleProcedureDays
	}
	diaryPath := cfg.DiaryPath
	if diaryPath == "" {
		diaryPath = paths.GetDreamDiaryPath()
	}
	reportPath := cfg.ReportPath
	if reportPath == "" {
		reportPath = paths.GetDreamReportPath()
	}

	return &Dreamer{
		store:                  cfg.Store,
		similarityThreshold:    similarityThreshold,
		decayRate:              decayRate,
		decayIntervalDays:      decayIntervalDays,
		decayFloor:             decayFloor,
		confidenceFloor:        confidenceFloor,
		boostThreshold:         boostThreshold,
		boostAmount:            boostAmount,
		autoLinkThreshold:      autoLinkThreshold,
		staleProcedureDays:     staleProcedureDays,
		diaryPath:              diaryPath,
		reportPath:             reportPath,
	}, nil
}

// Run executes the dream consolidation cycle.
// apply=false means dry run (count only, no mutations).
// phase can be "light", "deep", "rem", or "" for all.
func (d *Dreamer) Run(ctx context.Context, apply bool, phase string) (*DreamResult, error) {
	result := &DreamResult{}

	if phase == "" || phase == "light" {
		result.Light = d.lightPhase(ctx, apply)
	}

	if phase == "" || phase == "deep" {
		mergeCandidates := []*store.DuplicatePair{}
		if result.Light != nil && len(result.Light.MergeCandidates) > 0 {
			mergeCandidates = result.Light.MergeCandidates
		}
		result.Deep = d.deepPhase(ctx, apply, mergeCandidates)
	}

	if phase == "" || phase == "rem" {
		result.Rem = d.remPhase(ctx, apply)
	}

	// Write diary if applied and deep phase ran
	if apply && result.Deep != nil {
		if err := d.WriteDiary(result); err != nil {
			slog.Warn("llmem: dream: failed to write diary", "error", err)
		}
	}

	return result, nil
}

// lightPhase finds near-duplicate pairs by cosine similarity.
func (d *Dreamer) lightPhase(ctx context.Context, apply bool) *LightPhaseResult {
	pairs, err := d.store.ConsolidateDuplicates(ctx, d.similarityThreshold, 500)
	if err != nil {
		slog.Error("llmem: dream: light phase consolidation failed", "error", err)
		return &LightPhaseResult{DuplicatePairs: 0}
	}
	limit := 20
	if len(pairs) < limit {
		limit = len(pairs)
	}
	return &LightPhaseResult{
		DuplicatePairs:  len(pairs),
		MergeCandidates: pairs[:limit],
	}
}

// deepPhase performs decay, boost, merge, and auto-link.
func (d *Dreamer) deepPhase(ctx context.Context, apply bool, mergeCandidates []*store.DuplicatePair) *DeepPhaseResult {
	result := &DeepPhaseResult{}

	// Decay idle memories
	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -d.decayIntervalDays)
	staleCutoff := now.AddDate(0, 0, -d.staleProcedureDays)

	memories, err := d.store.Search(ctx, store.SearchParams{ValidOnly: true, Limit: 500})
	if err != nil {
		slog.Error("llmem: dream: deep phase search failed", "error", err)
		return result
	}

	for _, m := range memories {
		if m.CreatedAt == "" {
			continue
		}
		created, err := time.Parse(time.RFC3339, m.CreatedAt)
		if err != nil {
			continue
		}

		if created.Before(cutoff) {
			// Check if recently accessed
			if m.AccessedAt != "" {
				accessed, err := time.Parse(time.RFC3339, m.AccessedAt)
				if err == nil && accessed.After(cutoff) {
					continue // recently accessed, skip decay
				}
			}

			// Check if this is a stale procedure eligible for double-decay
			decayAmount := d.decayRate
			isStaleProcedure := m.Type == "procedure" && isStaleProcedure(m, staleCutoff)
			if isStaleProcedure {
				decayAmount = d.decayRate * 2
			}

			newConf := clampFloat(m.Confidence-decayAmount, d.decayFloor, 1.0)
			if newConf < d.confidenceFloor {
				if apply {
					_, err := d.store.Invalidate(ctx, m.ID, "Dream decay: confidence below floor")
					if err != nil {
						slog.Debug("llmem: dream: failed to invalidate memory", "id", m.ID, "error", err)
					}
				}
				result.InvalidatedCount++
			} else if apply {
				conf := newConf
				_, err := d.store.Update(ctx, store.UpdateParams{ID: m.ID, Confidence: &conf})
				if err != nil {
					slog.Debug("llmem: dream: failed to decay memory", "id", m.ID, "error", err)
				}
			}
			if isStaleProcedure {
				result.StaleProcedureDecayedCount++
			}
			result.DecayedCount++
		}
	}

	// Boost frequently accessed memories
	for _, m := range memories {
		if m.AccessCount >= d.boostThreshold {
			newConf := m.Confidence + d.boostAmount
			if newConf > 1.0 {
				newConf = 1.0
			}
			if apply {
				_, err := d.store.Update(ctx, store.UpdateParams{ID: m.ID, Confidence: &newConf})
				if err != nil {
					slog.Debug("llmem: dream: failed to boost memory", "id", m.ID, "error", err)
				}
			}
			result.BoostedCount++
		}
	}

	// Merge near-duplicate pairs
	for _, pair := range mergeCandidates {
		srcMem, err1 := d.store.Get(ctx, pair.SourceID, false)
		tgtMem, err2 := d.store.Get(ctx, pair.TargetID, false)
		if err1 != nil || err2 != nil || srcMem == nil || tgtMem == nil {
			continue
		}
		if srcMem.ValidUntil != "" || tgtMem.ValidUntil != "" {
			continue // skip invalidated
		}

		// Invalidate the lower-confidence memory
		if srcMem.Confidence >= tgtMem.Confidence {
			if apply {
				d.store.Invalidate(ctx, tgtMem.ID, fmt.Sprintf("Dream merge: superseded by %s", srcMem.ID))
				d.store.AddRelation(ctx, srcMem.ID, tgtMem.ID, "supersedes")
			}
		} else {
			if apply {
				d.store.Invalidate(ctx, srcMem.ID, fmt.Sprintf("Dream merge: superseded by %s", tgtMem.ID))
				d.store.AddRelation(ctx, tgtMem.ID, srcMem.ID, "supersedes")
			}
		}
		result.MergedCount++
	}

	// Auto-link similar memories
	if apply {
		pairs, err := d.store.ConsolidateDuplicates(ctx, d.autoLinkThreshold, 500)
		if err == nil && len(pairs) > 0 {
			// Get existing relations to avoid duplicates
			memIDs := make([]string, 0, len(pairs)*2)
			for _, p := range pairs {
				memIDs = append(memIDs, p.SourceID, p.TargetID)
			}
			existingRels, _ := d.store.GetRelationsBatch(ctx, memIDs)
			existingSet := make(map[string]bool)
			for _, r := range existingRels {
				if r.RelationType == "related_to" {
					key := sortPair(r.SourceID, r.TargetID)
					existingSet[key] = true
				}
			}
			for _, p := range pairs {
				key := sortPair(p.SourceID, p.TargetID)
				if !existingSet[key] {
					d.store.AddRelation(ctx, p.SourceID, p.TargetID, "related_to")
					result.AutoLinkedCount++
					existingSet[key] = true
				}
			}
		}
	}

	return result
}

// remPhase extracts themes from active memories.
func (d *Dreamer) remPhase(ctx context.Context, apply bool) *RemPhaseResult {
	result := &RemPhaseResult{}

	total, err := d.store.Count(ctx, false)
	if err != nil {
		slog.Error("llmem: dream: REM count failed", "error", err)
	}
	result.TotalMemories = total

	active, err := d.store.Count(ctx, true)
	if err != nil {
		slog.Error("llmem: dream: REM active count failed", "error", err)
	}
	result.ActiveMemories = active

	result.Themes = d.extractThemes(ctx)

	return result
}

// extractThemes extracts content-word themes from active memories.
func (d *Dreamer) extractThemes(ctx context.Context) []string {
	typeCounts, err := d.store.CountByType(ctx, true)
	if err != nil {
		slog.Error("llmem: dream: REM count by type failed", "error", err)
		return []string{}
	}

	var themes []string
	// Sort by count descending
	type countEntry struct {
		typ   string
		count int
	}
	var entries []countEntry
	for typ, cnt := range typeCounts {
		entries = append(entries, countEntry{typ, cnt})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].count > entries[j].count })

	limit := 8
	if len(entries) < limit {
		limit = len(entries)
	}
	for _, e := range entries[:limit] {
		themes = append(themes, fmt.Sprintf("%d memories about %s", e.count, e.typ))
	}

	// Extract content word themes
	memories, err := d.store.Search(ctx, store.SearchParams{ValidOnly: true, Limit: 200})
	if err != nil {
		return themes
	}

	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
		"were": true, "be": true, "been": true, "being": true, "have": true,
		"has": true, "had": true, "do": true, "does": true, "did": true,
		"will": true, "would": true, "could": true, "should": true, "may": true,
		"might": true, "must": true, "shall": true, "can": true, "need": true,
		"to": true, "of": true, "in": true, "for": true, "on": true,
		"with": true, "at": true, "by": true, "from": true, "as": true,
		"into": true, "and": true, "or": true, "but": true, "not": true,
		"no": true, "nor": true, "so": true, "yet": true, "this": true,
		"that": true, "these": true, "those": true, "it": true, "its": true,
		"we": true, "our": true, "us": true, "they": true, "them": true,
		"their": true, "i": true, "me": true, "my": true, "you": true, "your": true,
	}

	wordFreq := map[string]int{}
	wordRe := regexp.MustCompile(`[a-zA-Z_]{4,}`)
	for _, m := range memories {
		words := wordRe.FindAllString(strings.ToLower(m.Content), -1)
		for _, w := range words {
			if !stopWords[w] {
				wordFreq[w]++
			}
		}
	}

	type wordEntry struct {
		word  string
		count int
	}
	var wordEntries []wordEntry
	for w, c := range wordFreq {
		if c >= 2 {
			wordEntries = append(wordEntries, wordEntry{w, c})
		}
	}
	sort.Slice(wordEntries, func(i, j int) bool { return wordEntries[i].count > wordEntries[j].count })

	remaining := limit - len(themes)
	if remaining > 8 {
		remaining = 8
	}
	for i, we := range wordEntries {
		if i >= remaining {
			break
		}
		themes = append(themes, fmt.Sprintf("cluster: %d memories involve '%s'", we.count, we.word))
	}

	return themes
}

// WriteDiary writes the dream diary as markdown to the configured path.
// Uses sync.Mutex for concurrency safety within the process.
func (d *Dreamer) WriteDiary(result *DreamResult) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Validate write path
	diaryPath, err := paths.ValidateWritePath(d.diaryPath, "dream diary")
	if err != nil {
		return fmtErr("validate diary path: %w", err)
	}

	dir := filepath.Dir(diaryPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmtErr("create diary directory: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("# Dream Diary\n\n")
	sb.WriteString(fmt.Sprintf("**Date:** %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	if result.Light != nil {
		sb.WriteString("## Light Phase\n\n")
		sb.WriteString(fmt.Sprintf("- Duplicate pairs found: %d\n", result.Light.DuplicatePairs))
		if len(result.Light.MergeCandidates) > 0 {
			sb.WriteString(fmt.Sprintf("- Top merge candidates: %d\n", len(result.Light.MergeCandidates)))
		}
		sb.WriteString("\n")
	}

	if result.Deep != nil {
		sb.WriteString("## Deep Phase\n\n")
		sb.WriteString(fmt.Sprintf("- Decayed memories: %d\n", result.Deep.DecayedCount))
		sb.WriteString(fmt.Sprintf("- Stale procedures double-decayed: %d\n", result.Deep.StaleProcedureDecayedCount))
		sb.WriteString(fmt.Sprintf("- Boosted memories: %d\n", result.Deep.BoostedCount))
		sb.WriteString(fmt.Sprintf("- Invalidated memories: %d\n", result.Deep.InvalidatedCount))
		sb.WriteString(fmt.Sprintf("- Merged memories: %d\n", result.Deep.MergedCount))
		sb.WriteString(fmt.Sprintf("- Auto-linked memories: %d\n", result.Deep.AutoLinkedCount))
		sb.WriteString("\n")
	}

	if result.Rem != nil {
		sb.WriteString("## REM Phase\n\n")
		sb.WriteString(fmt.Sprintf("- Total memories: %d\n", result.Rem.TotalMemories))
		sb.WriteString(fmt.Sprintf("- Active memories: %d\n", result.Rem.ActiveMemories))
		if len(result.Rem.Themes) > 0 {
			sb.WriteString("### Themes\n\n")
			for _, theme := range result.Rem.Themes {
				sb.WriteString(fmt.Sprintf("- %s\n", theme))
			}
			sb.WriteString("\n")
		}
	}

	if err := os.WriteFile(diaryPath, []byte(sb.String()), 0600); err != nil {
		return fmtErr("write diary: %w", err)
	}

	return nil
}

// GenerateDreamReport generates an HTML dream report at the given path.
// Validates reportPath via paths.ValidateWritePath.
func (d *Dreamer) GenerateDreamReport(result *DreamResult, reportPath string) error {
	resolvedPath, err := paths.ValidateWritePath(reportPath, "dream report")
	if err != nil {
		return fmtErr("validate report path: %w", err)
	}

	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmtErr("create report directory: %w", err)
	}

	html := buildReportHTML(result)
	if err := os.WriteFile(resolvedPath, []byte(html), 0600); err != nil {
		return fmtErr("write report: %w", err)
	}
	return nil
}

// buildReportHTML generates the HTML content for the dream report.
func buildReportHTML(result *DreamResult) string {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html><head><title>Dream Report</title>\n")
	sb.WriteString("<style>body{font-family:sans-serif;margin:2em}table{border-collapse:collapse;width:100%}td,th{border:1px solid #ddd;padding:8px}th{background:#f4f4f4}</style>\n")
	sb.WriteString("</head><body>\n")
	sb.WriteString("<h1>Dream Report</h1>\n")
	sb.WriteString(fmt.Sprintf("<p>Generated: %s</p>\n", time.Now().UTC().Format(time.RFC3339)))

	if result.Light != nil {
		sb.WriteString("<h2>Light Phase</h2>\n")
		sb.WriteString(fmt.Sprintf("<p>Duplicate pairs: <strong>%d</strong></p>\n", result.Light.DuplicatePairs))
	}
	if result.Deep != nil {
		sb.WriteString("<h2>Deep Phase</h2>\n")
		sb.WriteString("<table><tr><th>Metric</th><th>Count</th></tr>\n")
		sb.WriteString(fmt.Sprintf("<tr><td>Decayed</td><td>%d</td></tr>\n", result.Deep.DecayedCount))
		sb.WriteString(fmt.Sprintf("<tr><td>Stale procedures double-decayed</td><td>%d</td></tr>\n", result.Deep.StaleProcedureDecayedCount))
		sb.WriteString(fmt.Sprintf("<tr><td>Boosted</td><td>%d</td></tr>\n", result.Deep.BoostedCount))
		sb.WriteString(fmt.Sprintf("<tr><td>Invalidated</td><td>%d</td></tr>\n", result.Deep.InvalidatedCount))
		sb.WriteString(fmt.Sprintf("<tr><td>Merged</td><td>%d</td></tr>\n", result.Deep.MergedCount))
		sb.WriteString(fmt.Sprintf("<tr><td>Auto-linked</td><td>%d</td></tr>\n", result.Deep.AutoLinkedCount))
		sb.WriteString("</table>\n")
	}
	if result.Rem != nil {
		sb.WriteString("<h2>REM Phase</h2>\n")
		sb.WriteString(fmt.Sprintf("<p>Total memories: %d, Active: %d</p>\n", result.Rem.TotalMemories, result.Rem.ActiveMemories))
		if len(result.Rem.Themes) > 0 {
			sb.WriteString("<h3>Themes</h3><ul>\n")
			for _, theme := range result.Rem.Themes {
				sb.WriteString(fmt.Sprintf("<li>%s</li>\n", theme))
			}
			sb.WriteString("</ul>\n")
		}
	}

	sb.WriteString("</body></html>\n")
	return sb.String()
}

// sortPair returns a deterministic key for a pair of IDs.
func sortPair(a, b string) string {
	if a < b {
		return a + ":" + b
	}
	return b + ":" + a
}

// isStaleProcedure checks whether a procedure memory is stale based on its
// AccessedAt timestamp. A procedure is stale if it was never accessed
// (AccessedAt is empty) or if its last access is older than the stale cutoff.
func isStaleProcedure(m *store.Memory, staleCutoff time.Time) bool {
	if m.AccessedAt == "" {
		return true // never accessed = stale
	}
	accessed, err := time.Parse(time.RFC3339, m.AccessedAt)
	if err != nil {
		return true // unparseable = treat as stale
	}
	return !accessed.After(staleCutoff)
}

// clampFloat returns val clamped between min and max.
func clampFloat(val, lo, hi float64) float64 {
	if val < lo {
		return lo
	}
	if val > hi {
		return hi
	}
	return val
}