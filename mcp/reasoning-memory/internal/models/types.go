package models

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

type ToolCall struct {
	Tool          string `json:"tool" yaml:"tool"`
	Args          any    `json:"args" yaml:"args"`
	ResultExcerpt string `json:"result_excerpt" yaml:"result_excerpt"`
	Outcome       string `json:"outcome" yaml:"outcome"`
}

type Step struct {
	ID      string `json:"id" yaml:"id"`
	Type    string `json:"type" yaml:"type"`
	Content string `json:"content" yaml:"content"`
}

type MemoryTier string

type EpisodeOutcome = string

type FailedApproach struct {
	Approach    string `json:"approach" yaml:"approach"`
	FailureMode string `json:"failure_mode" yaml:"failure_mode"`
	RootCause   string `json:"root_cause" yaml:"root_cause"`
	Lesson      string `json:"lesson" yaml:"lesson"`
}

type FailureMatch struct {
	Approach    string  `json:"approach" yaml:"approach"`
	FailureMode string  `json:"failure_mode" yaml:"failure_mode"`
	RootCause   string  `json:"root_cause" yaml:"root_cause"`
	Lesson      string  `json:"lesson" yaml:"lesson"`
	Score       float64 `json:"score" yaml:"score"`
}

const (
	TierEpisodic MemoryTier = "episodic"
	TierSemantic MemoryTier = "semantic"

	OutcomeVerifiedSuccess   EpisodeOutcome = "verified_success"
	OutcomeUnverifiedSuccess EpisodeOutcome = "unverified_success"
	OutcomePartialSuccess    EpisodeOutcome = "partial_success"
	OutcomeFailure           EpisodeOutcome = "failure"
	OutcomeAbandoned         EpisodeOutcome = "abandoned"
)

type Episode struct {
	ID               string              `json:"id" yaml:"id"`
	CreatedAt        time.Time           `json:"created_at" yaml:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at" yaml:"updated_at"`
	Domain           string              `json:"domain" yaml:"domain"`
	Outcome          EpisodeOutcome      `json:"outcome" yaml:"outcome"`
	Tier             MemoryTier          `json:"tier" yaml:"tier"`
	Tags             []string            `json:"tags" yaml:"tags"`
	Repo             string              `json:"repo,omitempty" yaml:"repo,omitempty"`
	Project          string              `json:"project,omitempty" yaml:"project,omitempty"`
	Provenance       string              `json:"provenance,omitempty" yaml:"provenance,omitempty"`
	Confidence       *float64            `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Labels           map[string][]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Problem          string              `json:"problem" yaml:"problem"`
	Objectives       []string            `json:"objectives,omitempty" yaml:"objectives,omitempty"`
	Decisions        []string            `json:"decisions,omitempty" yaml:"decisions,omitempty"`
	Alternatives     []string            `json:"alternatives,omitempty" yaml:"alternatives,omitempty"`
	Verification     []string            `json:"verification,omitempty" yaml:"verification,omitempty"`
	Lessons          []string            `json:"lessons,omitempty" yaml:"lessons,omitempty"`
	FailedApproaches []FailedApproach    `json:"failed_approaches,omitempty" yaml:"failed_approaches,omitempty"`
	ThinkingTrace    string              `json:"thinking_trace" yaml:"thinking_trace"`
	Steps            []Step              `json:"steps" yaml:"steps"`
	ToolCalls        []ToolCall          `json:"tool_calls" yaml:"tool_calls"`
	ModelID          string              `json:"model_id" yaml:"model_id"`
	DurationSeconds  int                 `json:"duration_seconds" yaml:"duration_seconds"`
}

type EpisodeSummary struct {
	ID              string              `json:"id" yaml:"id"`
	CreatedAt       string              `json:"created_at" yaml:"created_at"`
	UpdatedAt       string              `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	Problem         string              `json:"problem" yaml:"problem"`
	Domain          string              `json:"domain" yaml:"domain"`
	Outcome         EpisodeOutcome      `json:"outcome" yaml:"outcome"`
	Tier            MemoryTier          `json:"tier" yaml:"tier"`
	Tags            []string            `json:"tags" yaml:"tags"`
	Repo            string              `json:"repo,omitempty" yaml:"repo,omitempty"`
	Project         string              `json:"project,omitempty" yaml:"project,omitempty"`
	Provenance      string              `json:"provenance,omitempty" yaml:"provenance,omitempty"`
	Confidence      *float64            `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Labels          map[string][]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	StepCount       int                 `json:"step_count" yaml:"step_count"`
	ToolCount       int                 `json:"tool_count" yaml:"tool_count"`
	StepTypes       []string            `json:"step_types" yaml:"step_types"`
	ModelID         string              `json:"model_id" yaml:"model_id"`
	DurationSeconds int                 `json:"duration_seconds" yaml:"duration_seconds"`
	FailureMatches  []FailureMatch      `json:"failure_matches,omitempty" yaml:"failure_matches,omitempty"`
	LocalScore      float64             `json:"_local_score,omitempty" yaml:"_local_score,omitempty"`
	VectorScore     float64             `json:"_vector_score,omitempty" yaml:"_vector_score,omitempty"`
}

func NormalizeOutcome(outcome string) (EpisodeOutcome, bool) {
	switch outcome {
	case "success", "unverified_success":
		return OutcomeUnverifiedSuccess, true
	case "verified_success":
		return OutcomeVerifiedSuccess, true
	case "partial", "partial_success":
		return OutcomePartialSuccess, true
	case "failure":
		return OutcomeFailure, true
	case "abandoned":
		return OutcomeAbandoned, true
	default:
		return "", false
	}
}

func (e *Episode) Validate() error {
	if e == nil {
		return fmt.Errorf("episode is required")
	}
	if strings.TrimSpace(e.Problem) == "" {
		return fmt.Errorf("problem is required")
	}
	switch e.Outcome {
	case OutcomeVerifiedSuccess, OutcomeUnverifiedSuccess, OutcomePartialSuccess, OutcomeFailure, OutcomeAbandoned:
	default:
		return fmt.Errorf("invalid outcome %q", e.Outcome)
	}
	if e.Confidence != nil && (math.IsNaN(*e.Confidence) || math.IsInf(*e.Confidence, 0) || *e.Confidence < 0 || *e.Confidence > 1) {
		return fmt.Errorf("confidence must be a finite number between 0 and 1")
	}
	if e.Tier != "" && e.Tier != TierEpisodic && e.Tier != TierSemantic {
		return fmt.Errorf("invalid tier %q", e.Tier)
	}
	failed, err := NormalizeFailedApproaches(e.FailedApproaches)
	if err != nil {
		return err
	}
	e.FailedApproaches = failed
	return nil
}

func NormalizeFailedApproaches(values []FailedApproach) ([]FailedApproach, error) {
	if len(values) > 20 {
		return nil, fmt.Errorf("failed_approaches must contain at most 20 records")
	}
	out := make([]FailedApproach, 0, len(values))
	seen := make(map[FailedApproach]struct{}, len(values))
	for i, value := range values {
		value.Approach = strings.TrimSpace(value.Approach)
		value.FailureMode = strings.TrimSpace(value.FailureMode)
		value.RootCause = strings.TrimSpace(value.RootCause)
		value.Lesson = strings.TrimSpace(value.Lesson)
		fields := []struct {
			name  string
			value string
		}{{"approach", value.Approach}, {"failure_mode", value.FailureMode}, {"root_cause", value.RootCause}, {"lesson", value.Lesson}}
		for _, field := range fields {
			if !utf8.ValidString(field.value) {
				return nil, fmt.Errorf("failed_approaches[%d].%s must be valid UTF-8", i, field.name)
			}
			if field.value == "" {
				return nil, fmt.Errorf("failed_approaches[%d].%s is required", i, field.name)
			}
			if utf8.RuneCountInString(field.value) > 2000 {
				return nil, fmt.Errorf("failed_approaches[%d].%s must contain at most 2000 runes", i, field.name)
			}
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func (e *Episode) IsSemantic() bool {
	return e.Tier == TierSemantic
}

func (e *Episode) IsEpisodic() bool {
	return e.Tier == "" || e.Tier == TierEpisodic
}

type Pattern struct {
	ID                 string     `json:"id" yaml:"id"`
	CreatedAt          string     `json:"created_at" yaml:"created_at"`
	Domain             string     `json:"domain" yaml:"domain"`
	MergeScore         float64    `json:"merge_score" yaml:"merge_score"`
	Sources            []string   `json:"sources" yaml:"sources"`
	ConsolidatedPrompt string     `json:"consolidated_prompt" yaml:"consolidated_prompt"`
	MasterThinkingPath string     `json:"master_thinking_path" yaml:"master_thinking_path"`
	MasterToolCalls    []ToolCall `json:"master_tool_calls" yaml:"master_tool_calls"`
	Tags               []string   `json:"tags" yaml:"tags"`
}

type PatternContext struct {
	ID                 string   `json:"id" yaml:"id"`
	Domain             string   `json:"domain" yaml:"domain"`
	ConsolidatedPrompt string   `json:"consolidated_prompt" yaml:"consolidated_prompt"`
	MasterThinkingPath string   `json:"master_thinking_path" yaml:"master_thinking_path"`
	Tags               []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	MergeScore         float64  `json:"merge_score,omitempty" yaml:"merge_score,omitempty"`
}

type Config struct {
	Version         string                `yaml:"version"`
	EpisodesDir     string                `yaml:"episodes_dir"`
	IndexDir        string                `yaml:"index_dir"`
	PatternsDir     string                `yaml:"patterns_dir"`
	VectorDir       string                `yaml:"vector_dir"`
	Embedding       EmbeddingConfig       `yaml:"embedding"`
	Retrieval       RetrievalConfig       `yaml:"retrieval"`
	Consolidation   ConsolidationConfig   `yaml:"consolidation"`
	Security        SecurityConfig        `yaml:"security"`
	PromptPolishing PromptPolishingConfig `yaml:"prompt_polishing"`
	LinkIngestion   LinkIngestionConfig   `yaml:"link_ingestion"`
}

type LinkIngestionConfig struct {
	Enabled                  bool     `yaml:"enabled"`
	MaxLinks                 int      `yaml:"max_links"`
	RequestTimeoutSeconds    int      `yaml:"request_timeout_seconds"`
	MaxRedirects             int      `yaml:"max_redirects"`
	MaxResponseBytes         int      `yaml:"max_response_bytes"`
	MaxExtractedChars        int      `yaml:"max_extracted_chars"`
	MaxSummaryChars          int      `yaml:"max_summary_chars"`
	MaxConcurrency           int      `yaml:"max_concurrency"`
	CacheTTLMinutes          int      `yaml:"cache_ttl_minutes"`
	AllowedContentTypes      []string `yaml:"allowed_content_types"`
	FailurePolicy            string   `yaml:"failure_policy"`
	IncludeThinkingTrace     bool     `yaml:"include_thinking_trace"`
	RestRequirePreSummarized bool     `yaml:"rest_require_pre_summarized"`
}

type EmbeddingConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Enabled  bool   `yaml:"enabled"`
}

type RetrievalConfig struct {
	TopKDefault     int     `yaml:"top_k_default"`
	MinSimilarity   float64 `yaml:"min_similarity"`
	HybridWeight    float64 `yaml:"hybrid_weight"`
	IncludePatterns bool    `yaml:"include_patterns"`
	MaxPatterns     int     `yaml:"max_patterns"`
}

type ConsolidationConfig struct {
	PruneAfterDays        int  `yaml:"prune_after_days"`
	MinEpisodesForPattern int  `yaml:"min_episodes_for_pattern"`
	AutoRun               bool `yaml:"auto_run"`
	IntervalHours         int  `yaml:"interval_hours"`
	ArchiveAfterDays      int  `yaml:"archive_after_days"`
	MaxArchiveDays        int  `yaml:"max_archive_days"`
	SummarizeThreshold    int  `yaml:"summarize_threshold"`
	MaxSummaryLength      int  `yaml:"max_summary_length"`
}

type SecurityConfig struct {
	RedactSecrets         bool   `yaml:"redact_secrets"`
	RedactBeforeEmbedding bool   `yaml:"redact_before_embedding"`
	RedactOnRetrieval     bool   `yaml:"redact_on_retrieval"`
	RedactPolishedPrompts bool   `yaml:"redact_polished_prompts"`
	Replacement           string `yaml:"replacement"`
	AuditDetection        bool   `yaml:"audit_detection"`
}

type PromptPolishingConfig struct {
	Enabled                bool   `yaml:"enabled"`
	DefaultTargetAgent     string `yaml:"default_target_agent"`
	DefaultOutputFormat    string `yaml:"default_output_format"`
	IncludeMemoryByDefault bool   `yaml:"include_memory_by_default"`
	MaxMemories            int    `yaml:"max_memories"`
	MaxPromptChars         int    `yaml:"max_prompt_chars"`
	IncludeFailureLessons  bool   `yaml:"include_failure_lessons"`
	IncludeFullTraces      bool   `yaml:"include_full_traces"`
	DeduplicateContext     bool   `yaml:"deduplicate_context"`
	IncludePatterns        bool   `yaml:"include_patterns"`
	MaxPatterns            int    `yaml:"max_patterns"`
}

type PolishResult struct {
	PolishedPrompt string   `json:"polished_prompt"`
	TargetAgent    string   `json:"target_agent"`
	TaskType       string   `json:"task_type"`
	Language       string   `json:"language,omitempty"`
	Domain         string   `json:"domain"`
	SkillInjected  bool     `json:"skill_injected"`
	SkillName      string   `json:"skill_name,omitempty"`
	ContextCount   int      `json:"context_count"`
	OutputFormat   string   `json:"output_format"`
	Warnings       []string `json:"warnings,omitempty"`
	Truncated      bool     `json:"truncated,omitempty"`
}

type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

type DayBucket struct {
	Date        string  `json:"date"`
	Count       int     `json:"count"`
	Successes   int     `json:"successes"`
	AvgDuration float64 `json:"avg_duration_sec"`
	AvgTraceLen float64 `json:"avg_trace_len_chars"`
}

type SummaryStats struct {
	TotalEpisodes      int     `json:"total_episodes"`
	TotalPatterns      int     `json:"total_patterns"`
	SuccessRate        float64 `json:"success_rate"`
	AvgDurationSec     float64 `json:"avg_duration_sec"`
	AvgTraceLenChars   float64 `json:"avg_trace_len_chars"`
	ConsolidationRatio float64 `json:"consolidation_ratio"`
	TopDomain          string  `json:"top_domain"`
	TopRepo            string  `json:"top_repo"`
	TopLabelKey        string  `json:"top_label_key"`
	LabelCardinality   int     `json:"label_cardinality"`
	UnlabeledCount     int     `json:"unlabeled_count"`
	TotalArchived      int     `json:"total_archived"`
	TotalPruned        int     `json:"total_pruned"`
}

type LabelCount struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Count int    `json:"count"`
}

type StatsResult struct {
	EpisodesTotal         int            `json:"episodes_total"`
	PatternsTotal         int            `json:"patterns_total"`
	EpisodesByDomain      map[string]int `json:"episodes_by_domain"`
	EpisodesByOutcome     map[string]int `json:"episodes_by_outcome"`
	EpisodesByRepo        map[string]int `json:"episodes_by_repo"`
	EpisodesByLabel       []LabelCount   `json:"episodes_by_label,omitempty"`
	TopTags               []TagCount     `json:"top_tags"`
	VectorIndexSizeMB     float64        `json:"vector_index_size_mb"`
	VectorCount           int            `json:"vector_count"`
	FTSSizeMB             float64        `json:"fts5_size_mb"`
	DBSizeMB              float64        `json:"db_size_mb"`
	LastConsolidationTS   *string        `json:"last_consolidation_ts,omitempty"`
	ConsolidationsTotal   int            `json:"consolidations_total"`
	AvgEpisodeLenChars    float64        `json:"avg_episode_length_chars"`
	AvgThinkingTraceChars float64        `json:"avg_thinking_trace_chars"`

	SuccessRate        float64     `json:"success_rate"`
	ConsolidationRatio float64     `json:"consolidation_ratio"`
	TopDomain          string      `json:"top_domain"`
	TopRepo            string      `json:"top_repo"`
	TopLabelKey        string      `json:"top_label_key"`
	LabelCardinality   int         `json:"label_cardinality"`
	UnlabeledCount     int         `json:"unlabeled_count"`
	AvgDurationSec     float64     `json:"avg_duration_sec"`
	EpisodesByDay      []DayBucket `json:"episodes_by_day"`
	ArchivedTotal      int         `json:"archived_total"`
	PrunedTotal        int         `json:"pruned_total"`
}
