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
	if !cfg.Security.RedactSecrets || !cfg.Security.RedactBeforeEmbedding ||
		!cfg.Security.RedactOnRetrieval || !cfg.Security.RedactPolishedPrompts {
		t.Fatal("expected secure redaction defaults")
	}
	if cfg.PromptPolishing.DefaultTargetAgent != "generic" ||
		cfg.PromptPolishing.DefaultOutputFormat != "markdown" ||
		cfg.PromptPolishing.IncludeFullTraces {
		t.Fatalf("unexpected prompt-polishing defaults: %#v", cfg.PromptPolishing)
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
