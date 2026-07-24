package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected default config, got nil")
	}
	if cfg.Version != "1" {
		t.Errorf("expected version 1, got %s", cfg.Version)
	}
	if cfg.Retrieval.TopKDefault != 3 {
		t.Errorf("expected top_k_default 3, got %d", cfg.Retrieval.TopKDefault)
	}
	if cfg.Consolidation.PruneAfterDays != 90 {
		t.Errorf("expected prune_after_days 90, got %d", cfg.Consolidation.PruneAfterDays)
	}
	if cfg.Consolidation.IntervalHours != 24 {
		t.Errorf("expected interval_hours 24, got %d", cfg.Consolidation.IntervalHours)
	}
	if cfg.Consolidation.ArchiveAfterDays != 30 {
		t.Errorf("expected archive_after_days 30, got %d", cfg.Consolidation.ArchiveAfterDays)
	}
	if cfg.Consolidation.MaxArchiveDays != 90 {
		t.Errorf("expected max_archive_days 90, got %d", cfg.Consolidation.MaxArchiveDays)
	}
	if cfg.Consolidation.SummarizeThreshold != 5 {
		t.Errorf("expected summarize_threshold 5, got %d", cfg.Consolidation.SummarizeThreshold)
	}
	if cfg.Consolidation.MaxSummaryLength != 500 {
		t.Errorf("expected max_summary_length 500, got %d", cfg.Consolidation.MaxSummaryLength)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := []byte(`version: 2
retrieval:
  top_k_default: 5
  min_similarity: 0.3
  hybrid_weight: 0.7
consolidation:
  prune_after_days: 30
  min_episodes_for_pattern: 5
  auto_run: false
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != "2" {
		t.Errorf("expected version 2, got %s", cfg.Version)
	}
	if cfg.Retrieval.TopKDefault != 5 {
		t.Errorf("expected top_k_default 5, got %d", cfg.Retrieval.TopKDefault)
	}
	if cfg.Retrieval.MinSimilarity != 0.3 {
		t.Errorf("expected min_similarity 0.3, got %f", cfg.Retrieval.MinSimilarity)
	}
	if cfg.Consolidation.PruneAfterDays != 30 {
		t.Errorf("expected prune_after_days 30, got %d", cfg.Consolidation.PruneAfterDays)
	}
	if cfg.Consolidation.AutoRun {
		t.Error("expected auto_run false")
	}
}

func TestLoadPartialYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := []byte(`version: 1
retrieval:
  top_k_default: 7
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Retrieval.TopKDefault != 7 {
		t.Errorf("expected top_k_default 7, got %d", cfg.Retrieval.TopKDefault)
	}
	if cfg.Consolidation.PruneAfterDays != 0 {
		cfg.Consolidation.PruneAfterDays = DefaultConfig.Consolidation.PruneAfterDays
	}
}
