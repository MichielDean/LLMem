package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BriefingConfig controls briefing generation parameters.
type BriefingConfig struct {
	MaxLines       int // Total line cap (default 25)
	RecentStable   int // Top-K recent stable entries (default 5)
	RecentVolatile int // Max volatile entries when over cap (default 3)
}

// DefaultBriefingConfig returns the default briefing configuration.
func DefaultBriefingConfig() BriefingConfig {
	return BriefingConfig{
		MaxLines:       25,
		RecentStable:   5,
		RecentVolatile: 3,
	}
}

// BriefingSection is a labeled group of memories in the briefing.
type BriefingSection struct {
	Title    string
	Memories []*Memory
}

// Briefing is the assembled session briefing.
type Briefing struct {
	Pinned      BriefingSection
	Volatile    BriefingSection
	RecentStable BriefingSection
	StoreHealth  BriefingHealth
}

// BriefingHealth summarizes the store state.
type BriefingHealth struct {
	StableTotal   int
	StablePinned  int
	VolatileTotal int
	LogTotal      int
	ExpiredCount  int
}

// GenerateBriefing assembles the session briefing from the memory store.
// Includes pinned stable memories, recent non-expired volatile memories,
// and the top-K most recently updated stable memories (not already pinned).
// The total content is capped at MaxLines lines.
func (ms *MemoryStore) GenerateBriefing(ctx context.Context, cfg BriefingConfig) (*Briefing, error) {
	if cfg.MaxLines <= 0 {
		cfg = DefaultBriefingConfig()
	}

	// 1. Pinned stable memories
	pinned, err := ms.queryMemoriesByType(ctx, "stable", `AND "pinned" = 1 ORDER BY "updated_at" DESC`)
	if err != nil {
		return nil, fmtErr("briefing: pinned: %w", err)
	}

	// 2. Recent volatile memories (non-expired)
	now := time.Now().UTC().Format(time.RFC3339)
	volatile, err := ms.queryMemoriesByType(ctx, "volatile",
		fmt.Sprintf(`AND ("valid_until" IS NULL OR "valid_until" > '%s') ORDER BY "updated_at" DESC`, now))
	if err != nil {
		return nil, fmtErr("briefing: volatile: %w", err)
	}

	// 3. Top-K recent stable memories (not pinned)
	recentStable, err := ms.queryMemoriesByType(ctx, "stable",
		fmt.Sprintf(`AND "pinned" = 0 ORDER BY "updated_at" DESC LIMIT %d`, cfg.RecentStable))
	if err != nil {
		return nil, fmtErr("briefing: recent stable: %w", err)
	}

	// 4. Store health
	health, err := ms.briefingHealth(ctx)
	if err != nil {
		return nil, fmtErr("briefing: health: %w", err)
	}

	b := &Briefing{
		Pinned:       BriefingSection{Title: "Pinned", Memories: pinned},
		Volatile:     BriefingSection{Title: "Active (volatile)", Memories: volatile},
		RecentStable: BriefingSection{Title: "Recent (stable)", Memories: recentStable},
		StoreHealth:  *health,
	}

	// 5. Apply line cap
	b.applyCap(cfg)

	return b, nil
}

// applyCap trims sections to fit within the line cap.
// Drop recent stable first, then volatile beyond RecentVolatile.
func (b *Briefing) applyCap(cfg BriefingConfig) {
	const headerOverhead = 9 // ## header + 3 section headers + blank lines + health block

	available := cfg.MaxLines - headerOverhead
	if available <= 0 {
		b.RecentStable.Memories = nil
		b.Volatile.Memories = nil
		b.Pinned.Memories = nil
		return
	}

	// Pinned always survives — count them
	pinnedCount := len(b.Pinned.Memories)
	remaining := available - pinnedCount
	if remaining <= 0 {
		// Even pinned exceeds cap — trim pinned to fit
		if available < 0 {
			available = 0
		}
		b.Pinned.Memories = trimSlice(b.Pinned.Memories, available)
		b.Volatile.Memories = nil
		b.RecentStable.Memories = nil
		return
	}

	// Try volatile + recent stable
	volatileCount := len(b.Volatile.Memories)
	recentCount := len(b.RecentStable.Memories)

	if volatileCount+recentCount <= remaining {
		return // fits
	}

	// Drop recent stable first
	if recentCount > 0 {
		needed := remaining - volatileCount
		if needed < 0 {
			needed = 0
		}
		b.RecentStable.Memories = trimSlice(b.RecentStable.Memories, needed)
		remaining = available - pinnedCount - len(b.RecentStable.Memories)
	}

	// If still over, trim volatile beyond RecentVolatile
	if len(b.Volatile.Memories) > remaining {
		maxVolatile := cfg.RecentVolatile
		if remaining < maxVolatile {
			maxVolatile = remaining
		}
		b.Volatile.Memories = trimSlice(b.Volatile.Memories, maxVolatile)
	}
}

// Format renders the briefing as structured markdown.
func (b *Briefing) Format() string {
	var sb strings.Builder

	sb.WriteString("## Session Briefing\n\n")

	if len(b.Pinned.Memories) > 0 {
		sb.WriteString(fmt.Sprintf("### %s\n", b.Pinned.Title))
		for _, m := range b.Pinned.Memories {
			sb.WriteString(formatBriefingLine(m))
		}
		sb.WriteString("\n")
	}

	if len(b.Volatile.Memories) > 0 {
		sb.WriteString(fmt.Sprintf("### %s\n", b.Volatile.Title))
		for _, m := range b.Volatile.Memories {
			sb.WriteString(formatBriefingLine(m))
		}
		sb.WriteString("\n")
	}

	if len(b.RecentStable.Memories) > 0 {
		sb.WriteString(fmt.Sprintf("### %s\n", b.RecentStable.Title))
		for _, m := range b.RecentStable.Memories {
			sb.WriteString(formatBriefingLine(m))
		}
		sb.WriteString("\n")
	}

	h := b.StoreHealth
	sb.WriteString("### Store Health\n")
	sb.WriteString(fmt.Sprintf("- stable: %d (%d pinned)\n", h.StableTotal, h.StablePinned))
	sb.WriteString(fmt.Sprintf("- volatile: %d\n", h.VolatileTotal))
	sb.WriteString(fmt.Sprintf("- log: %d\n", h.LogTotal))
	sb.WriteString(fmt.Sprintf("- expired: %d\n", h.ExpiredCount))

	return sb.String()
}

// formatBriefingLine renders a single memory as a one-line entry.
func formatBriefingLine(m *Memory) string {
	preview := m.Content
	const maxPreview = 80
	if len(preview) > maxPreview {
		preview = preview[:maxPreview] + "..."
	}
	preview = strings.ReplaceAll(preview, "\n", " ")
	return fmt.Sprintf("- [%s] %s\n", m.ID, preview)
}

// queryMemoriesByType queries memories of a given type with an optional extra WHERE clause.
func (ms *MemoryStore) queryMemoriesByType(ctx context.Context, typ string, extraWhere string) ([]*Memory, error) {
	query := fmt.Sprintf(
		`SELECT "id", "type", "content", "summary", "hints", "source", "confidence", "valid_from", "valid_until", "created_at", "updated_at", "accessed_at", "access_count", "metadata", "embedding", "pinned" FROM "memories" WHERE "type" = ? %s`,
		extraWhere,
	)
	rows, err := ms.db.QueryContext(ctx, query, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []*Memory
	for rows.Next() {
		m, err := scanMemoryFromRows(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// briefingHealth computes store health statistics for the briefing.
func (ms *MemoryStore) briefingHealth(ctx context.Context) (*BriefingHealth, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	countType := func(typ string) (int, error) {
		var count int
		err := ms.db.QueryRowContext(ctx,
			`SELECT count(*) FROM "memories" WHERE "type" = ?`, typ,
		).Scan(&count)
		return count, err
	}

	countExpired := func() (int, error) {
		var count int
		err := ms.db.QueryRowContext(ctx,
			`SELECT count(*) FROM "memories" WHERE "valid_until" IS NOT NULL AND "valid_until" < ?`,
			now,
		).Scan(&count)
		return count, err
	}

	countPinnedStable := func() (int, error) {
		var count int
		err := ms.db.QueryRowContext(ctx,
			`SELECT count(*) FROM "memories" WHERE "type" = 'stable' AND "pinned" = 1`,
		).Scan(&count)
		return count, err
	}

	stableTotal, err := countType("stable")
	if err != nil {
		return nil, err
	}
	volatileTotal, err := countType("volatile")
	if err != nil {
		return nil, err
	}
	logTotal, err := countType("log")
	if err != nil {
		return nil, err
	}
	expired, err := countExpired()
	if err != nil {
		return nil, err
	}
	stablePinned, err := countPinnedStable()
	if err != nil {
		return nil, err
	}

	return &BriefingHealth{
		StableTotal:   stableTotal,
		StablePinned:  stablePinned,
		VolatileTotal: volatileTotal,
		LogTotal:      logTotal,
		ExpiredCount:  expired,
	}, nil
}

// trimSlice returns the first n elements of the slice (or the whole slice if n >= len).
func trimSlice(s []*Memory, n int) []*Memory {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return nil
	}
	return s[:n]
}
