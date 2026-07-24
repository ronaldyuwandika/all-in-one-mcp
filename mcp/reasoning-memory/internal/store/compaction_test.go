package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func TestCompaction(t *testing.T) {
	es := testStore(t)
	ctx := context.Background()

	// 1. Create a normal episode, and an old episode to be archived (older than 30 days)
	_, _ = es.CreateEpisode(&models.Episode{
		ID:            "ep-current",
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"go"},
		Problem:       "current problem",
		ThinkingTrace: "current trace step 1\nstep 2",
		Tier:          "episodic",
	})

	// Insert directly to bypass CreateEpisode created_at overwrite
	oldTime := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339)
	_, err := es.db.Exec(`
		INSERT INTO episodes (id, created_at, domain, outcome, tags, problem, thinking_trace, tier)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "ep-old", oldTime, "coding", "success", `["go"]`, "old problem", "old trace step 1\nstep 2\nstep 3\nstep 4", "episodic")
	if err != nil {
		t.Fatalf("failed to insert old episode: %v", err)
	}

	// Insert another archived episode to test pruning (older than 90 days)
	veryOldTime := time.Now().UTC().AddDate(0, 0, -100).Format(time.RFC3339)
	_, err = es.db.Exec(`
		INSERT INTO episodes_archive (id, created_at, domain, outcome, tags, problem, thinking_trace, tier)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "ep-very-old", veryOldTime, "coding", "success", `["go"]`, "very old problem", "very old trace", "episodic")
	if err != nil {
		t.Fatalf("failed to insert very old archived episode: %v", err)
	}

	// Create a pattern with 5 sources to test summarization
	sources := []string{"ep-current", "ep-old", "ep-3", "ep-4", "ep-5"}
	sourcesJSON, _ := json.Marshal(sources)
	_, err = es.db.Exec(`
		INSERT INTO patterns (id, created_at, domain, sources, consolidated_prompt, master_thinking_path)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "pat-1", time.Now().UTC().Format(time.RFC3339), "coding", string(sourcesJSON), "consolidated prompt", "master path")
	if err != nil {
		t.Fatalf("failed to insert pattern: %v", err)
	}

	// Let's create ep-3, ep-4, ep-5 with long thinking traces (exceeds MaxSummaryLength 50)
	longTrace := "long trace line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9"
	for _, id := range []string{"ep-3", "ep-4", "ep-5"} {
		_, _ = es.CreateEpisode(&models.Episode{
			ID:            id,
			Domain:        "coding",
			Outcome:       "success",
			Tags:          []string{"go"},
			Problem:       "problem " + id,
			ThinkingTrace: longTrace,
			Tier:          "episodic",
		})
	}

	cfg := models.ConsolidationConfig{
		ArchiveAfterDays:   30,
		MaxArchiveDays:     90,
		SummarizeThreshold: 5,
		MaxSummaryLength:   50,
		IntervalHours:      24,
		AutoRun:            false,
	}

	// Dry run first
	report, err := es.Compact(ctx, cfg, true)
	if err != nil {
		t.Fatalf("dry-run compact failed: %v", err)
	}

	if report.ArchivedCount != 1 {
		t.Errorf("expected dry-run to archive 1 episode, got %d", report.ArchivedCount)
	}
	if report.PrunedCount != 1 {
		t.Errorf("expected dry-run to prune 1 episode, got %d", report.PrunedCount)
	}
	// ep-current is not long, ep-old is not long, but ep-3, ep-4, ep-5 are long.
	// So 3 traces should be summarized.
	if report.SummarizedCount != 3 {
		t.Errorf("expected dry-run to summarize 3 episodes, got %d", report.SummarizedCount)
	}

	// Now run real compaction
	report, err = es.Compact(ctx, cfg, false)
	if err != nil {
		t.Fatalf("real compact failed: %v", err)
	}

	if report.ArchivedCount != 1 {
		t.Errorf("expected to archive 1 episode, got %d", report.ArchivedCount)
	}
	if report.PrunedCount != 1 {
		t.Errorf("expected to prune 1 episode, got %d", report.PrunedCount)
	}
	if report.SummarizedCount != 3 {
		t.Errorf("expected to summarize 3 episodes, got %d", report.SummarizedCount)
	}
	if !report.RebuiltFTS {
		t.Errorf("expected FTS index to be rebuilt")
	}

	// Verify ep-old is archived and not in active episodes table
	epCurrent, err := es.GetEpisode("ep-current")
	if err != nil || epCurrent == nil {
		t.Fatalf("ep-current should still exist")
	}

	epOld, err := es.GetEpisode("ep-old")
	if err != nil {
		t.Fatal(err)
	}
	if epOld != nil {
		t.Errorf("ep-old should have been removed from active episodes table")
	}

	var existsInArchive int
	_ = es.db.QueryRow("SELECT COUNT(*) FROM episodes_archive WHERE id = 'ep-old'").Scan(&existsInArchive)
	if existsInArchive != 1 {
		t.Errorf("ep-old should exist in episodes_archive")
	}

	// Verify ep-very-old was permanently pruned
	var existsVeryOld int
	_ = es.db.QueryRow("SELECT COUNT(*) FROM episodes_archive WHERE id = 'ep-very-old'").Scan(&existsVeryOld)
	if existsVeryOld != 0 {
		t.Errorf("ep-very-old should have been permanently deleted")
	}

	// Verify traces of ep-3, ep-4, ep-5 are summarized (shorter than longTrace)
	for _, id := range []string{"ep-3", "ep-4", "ep-5"} {
		ep, err := es.GetEpisode(id)
		if err != nil || ep == nil {
			t.Fatalf("%s should exist", id)
		}
		if len(ep.ThinkingTrace) >= len(longTrace) {
			t.Errorf("thinking trace for %s should have been summarized", id)
		}
		if len(ep.ThinkingTrace) > cfg.MaxSummaryLength {
			t.Errorf("summarized trace length %d exceeds max %d", len(ep.ThinkingTrace), cfg.MaxSummaryLength)
		}
	}

	// Verify Stats
	stats, err := es.SummaryStats()
	if err != nil {
		t.Fatalf("failed to get summary stats: %v", err)
	}
	if stats.TotalArchived != 1 {
		t.Errorf("expected TotalArchived 1, got %d", stats.TotalArchived)
	}
	if stats.TotalPruned != 1 {
		t.Errorf("expected TotalPruned 1, got %d", stats.TotalPruned)
	}
}
