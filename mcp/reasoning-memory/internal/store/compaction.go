package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

type CompactionReport struct {
	ArchivedCount   int
	PrunedCount     int
	SummarizedCount int
	RebuiltFTS      bool
}

func (cr CompactionReport) String() string {
	return fmt.Sprintf("Archived: %d, Pruned: %d, Summarized: %d, Rebuilt FTS: %t",
		cr.ArchivedCount, cr.PrunedCount, cr.SummarizedCount, cr.RebuiltFTS)
}

func (es *EpisodeStore) Compact(ctx context.Context, cfg models.ConsolidationConfig, dryRun bool) (CompactionReport, error) {
	var report CompactionReport

	// 1. Archive episodes older than ArchiveAfterDays (default 30 days)
	// Only archive episodic tier episodes
	archiveCutoff := time.Now().UTC().Add(time.Duration(-cfg.ArchiveAfterDays*24) * time.Hour).Format(time.RFC3339)

	// Let's query matching episodic IDs
	rows, err := es.db.Query("SELECT id FROM episodes WHERE created_at < ? AND tier = 'episodic'", archiveCutoff)
	if err != nil {
		return report, fmt.Errorf("query archive candidates: %w", err)
	}

	var archiveIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			archiveIDs = append(archiveIDs, id)
		}
	}
	_ = rows.Close()

	if dryRun {
		report.ArchivedCount = len(archiveIDs)
	} else {
		for _, id := range archiveIDs {
			// We do a transaction for move
			tx, err := es.db.Begin()
			if err != nil {
				slog.Error("Failed to begin transaction for archive", "id", id, "error", err)
				continue
			}

			_, err = tx.Exec(`
				INSERT OR REPLACE INTO episodes_archive (
					id, created_at, domain, outcome, tags, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds, repo, labels, tier
				)
				SELECT id, created_at, domain, outcome, tags, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds, repo, labels, tier
				FROM episodes WHERE id = ?
			`, id)
			if err != nil {
				_ = tx.Rollback()
				slog.Error("Failed to copy episode to archive", "id", id, "error", err)
				continue
			}

			_, err = tx.Exec("DELETE FROM episodes WHERE id = ?", id)
			if err != nil {
				_ = tx.Rollback()
				slog.Error("Failed to delete episode from active store", "id", id, "error", err)
				continue
			}

			if err := tx.Commit(); err != nil {
				slog.Error("Failed to commit archive transaction", "id", id, "error", err)
				continue
			}

			// Delete from vector DB if enabled
			if es.vec != nil && es.vec.Enabled() {
				_ = es.vec.DeleteEpisode(ctx, id)
			}

			report.ArchivedCount++
		}
	}

	// 2. Prune permanently archived episodes older than MaxArchiveDays (default 90 days)
	pruneCutoff := time.Now().UTC().Add(time.Duration(-cfg.MaxArchiveDays*24) * time.Hour).Format(time.RFC3339)
	var pruneCount int
	err = es.db.QueryRow("SELECT COUNT(*) FROM episodes_archive WHERE created_at < ?", pruneCutoff).Scan(&pruneCount)
	if err != nil {
		slog.Error("Failed to count prune candidates", "error", err)
	} else {
		if dryRun {
			report.PrunedCount = pruneCount
		} else {
			res, err := es.db.Exec("DELETE FROM episodes_archive WHERE created_at < ?", pruneCutoff)
			if err != nil {
				slog.Error("Failed to prune archived episodes", "error", err)
			} else {
				deleted, _ := res.RowsAffected()
				report.PrunedCount = int(deleted)
				if deleted > 0 {
					_, _ = es.db.Exec(`
						INSERT INTO compaction_stats (key, value)
						VALUES ('pruned_count', ?)
						ON CONFLICT(key) DO UPDATE SET value = value + ?
					`, deleted, deleted)
				}
			}
		}
	}

	// 3. Summarize episode clusters identified during consolidation (pattern sources length >= SummarizeThreshold)
	// We read patterns
	rowsPat, err := es.db.Query("SELECT sources FROM patterns")
	if err != nil {
		slog.Error("Failed to query patterns for summarization", "error", err)
	} else {
		var targets []string
		seenTargets := make(map[string]bool)

		for rowsPat.Next() {
			var sourcesJSON string
			if err := rowsPat.Scan(&sourcesJSON); err == nil {
				var sources []string
				if err := json.Unmarshal([]byte(sourcesJSON), &sources); err == nil {
					if len(sources) >= cfg.SummarizeThreshold {
						for _, srcID := range sources {
							if !seenTargets[srcID] {
								seenTargets[srcID] = true
								targets = append(targets, srcID)
							}
						}
					}
				}
			}
		}
		_ = rowsPat.Close()

		// For each target episode, summarize if it exceeds MaxSummaryLength
		for _, epID := range targets {
			ep, err := es.GetEpisode(epID)
			if err != nil || ep == nil {
				continue
			}
			if len(ep.ThinkingTrace) > cfg.MaxSummaryLength {
				if dryRun {
					report.SummarizedCount++
				} else {
					summary := summarizeTrace(ep.ThinkingTrace, cfg.MaxSummaryLength)
					_, err = es.db.Exec("UPDATE episodes SET thinking_trace = ? WHERE id = ?", summary, epID)
					if err != nil {
						slog.Error("Failed to update episode thinking trace with summary", "id", epID, "error", err)
					} else {
						report.SummarizedCount++
						// Update in vector store if active
						if es.vec != nil && es.vec.Enabled() {
							content := ep.Problem + "\n" + summary
							_ = es.vec.AddEpisodes(ctx, []EpisodeContent{{ID: epID, Content: content}})
						}
					}
				}
			}
		}
	}

	// 4. Rebuild FTS5 index
	if !dryRun {
		if err := es.ReindexFTS5(); err != nil {
			slog.Error("Failed to rebuild FTS5 index after compaction", "error", err)
		} else {
			report.RebuiltFTS = true
		}
	}

	return report, nil
}

func (es *EpisodeStore) StartCompactionLoop(ctx context.Context, cfg models.ConsolidationConfig) {
	if !cfg.AutoRun {
		return
	}
	slog.Info("Starting background compaction loop", "interval_hours", cfg.IntervalHours)
	go func() {
		// Run first compaction on startup after a small delay
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return
		}

		runCompaction := func() {
			slog.Info("Running scheduled database compaction")
			report, err := es.Compact(ctx, cfg, false)
			if err != nil {
				slog.Error("Compaction failed", "error", err)
			} else {
				slog.Info("Compaction finished", "report", report.String())
			}
		}

		runCompaction()

		ticker := time.NewTicker(time.Duration(cfg.IntervalHours) * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				runCompaction()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func summarizeTrace(trace string, maxLength int) string {
	if len(trace) <= maxLength {
		return trace
	}
	lines := strings.Split(trace, "\n")
	var summaryLines []string
	currentLen := 0

	// Always try to include important lines
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		isHeader := strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "Step") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*")
		if isHeader || len(summaryLines) < 5 {
			if currentLen+len(trimmed)+1 > maxLength-50 {
				break
			}
			summaryLines = append(summaryLines, trimmed)
			currentLen += len(trimmed) + 1
		}
	}

	summary := strings.Join(summaryLines, "\n")
	if len(summary) > maxLength {
		summary = summary[:maxLength-3] + "..."
	} else if len(summary) == 0 {
		summary = trace[:maxLength-3] + "..."
	} else {
		summary = summary + "\n... [truncated trace]"
		if len(summary) > maxLength {
			summary = summary[:maxLength-3] + "..."
		}
	}
	return summary
}
