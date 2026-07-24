package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

var DefaultConfig = models.Config{
	Version:     "1",
	EpisodesDir: "episodes",
	IndexDir:    "index",
	PatternsDir: "patterns",
	VectorDir:   "vector",
	Embedding: models.EmbeddingConfig{
		Provider: "ollama",
		Model:    "nomic-embed-text",
		BaseURL:  "http://localhost:11434",
		Enabled:  false,
	},
	Retrieval: models.RetrievalConfig{
		TopKDefault:   3,
		MinSimilarity: 0.15,
		HybridWeight:  0.5,
	},
	Consolidation: models.ConsolidationConfig{
		PruneAfterDays:        90,
		MinEpisodesForPattern: 3,
		AutoRun:               true,
	},
	Security: models.SecurityConfig{
		RedactSecrets:         true,
		RedactBeforeEmbedding: true,
		RedactOnRetrieval:     true,
		RedactPolishedPrompts: true,
		Replacement:           "[REDACTED]",
		AuditDetection:        true,
	},
	PromptPolishing: models.PromptPolishingConfig{
		Enabled:                true,
		DefaultTargetAgent:     "generic",
		DefaultOutputFormat:    "markdown",
		IncludeMemoryByDefault: true,
		MaxMemories:            3,
		MaxPromptChars:         20000,
		IncludeFailureLessons:  true,
		IncludeFullTraces:      false,
		DeduplicateContext:     true,
	},
}

func Load(path string) (*models.Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is user-provided config path, caller controls it
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig
			return &cfg, nil
		}
		return nil, err
	}

	cfg := DefaultConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.EpisodesDir == "" {
		cfg.EpisodesDir = DefaultConfig.EpisodesDir
	}
	if cfg.IndexDir == "" {
		cfg.IndexDir = DefaultConfig.IndexDir
	}
	if cfg.PatternsDir == "" {
		cfg.PatternsDir = DefaultConfig.PatternsDir
	}
	if cfg.Retrieval.TopKDefault == 0 {
		cfg.Retrieval.TopKDefault = DefaultConfig.Retrieval.TopKDefault
	}
	if cfg.Retrieval.MinSimilarity == 0 {
		cfg.Retrieval.MinSimilarity = DefaultConfig.Retrieval.MinSimilarity
	}
	if cfg.Retrieval.HybridWeight == 0 {
		cfg.Retrieval.HybridWeight = DefaultConfig.Retrieval.HybridWeight
	}
	if cfg.Consolidation.PruneAfterDays == 0 {
		cfg.Consolidation.PruneAfterDays = DefaultConfig.Consolidation.PruneAfterDays
	}
	if cfg.Security.Replacement == "" {
		cfg.Security.Replacement = DefaultConfig.Security.Replacement
	}
	if cfg.PromptPolishing.DefaultTargetAgent == "" {
		cfg.PromptPolishing.DefaultTargetAgent = DefaultConfig.PromptPolishing.DefaultTargetAgent
	}
	if cfg.PromptPolishing.DefaultOutputFormat == "" {
		cfg.PromptPolishing.DefaultOutputFormat = DefaultConfig.PromptPolishing.DefaultOutputFormat
	}
	if cfg.PromptPolishing.MaxMemories <= 0 {
		cfg.PromptPolishing.MaxMemories = DefaultConfig.PromptPolishing.MaxMemories
	}
	if cfg.PromptPolishing.MaxPromptChars <= 0 {
		cfg.PromptPolishing.MaxPromptChars = DefaultConfig.PromptPolishing.MaxPromptChars
	}

	return &cfg, nil
}

func DirFor(baseDir, subDir string) string {
	return filepath.Join(baseDir, subDir)
}
