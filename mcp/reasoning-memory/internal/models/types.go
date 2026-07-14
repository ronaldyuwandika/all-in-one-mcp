package models

import "time"

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

type Episode struct {
	ID              string              `json:"id" yaml:"id"`
	CreatedAt       time.Time           `json:"created_at" yaml:"created_at"`
	Domain          string              `json:"domain" yaml:"domain"`
	Outcome         string              `json:"outcome" yaml:"outcome"`
	Tags            []string            `json:"tags" yaml:"tags"`
	Repo            string              `json:"repo,omitempty" yaml:"repo,omitempty"`
	Labels          map[string][]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Problem         string              `json:"problem" yaml:"problem"`
	ThinkingTrace   string              `json:"thinking_trace" yaml:"thinking_trace"`
	Steps           []Step              `json:"steps" yaml:"steps"`
	ToolCalls       []ToolCall          `json:"tool_calls" yaml:"tool_calls"`
	ModelID         string              `json:"model_id" yaml:"model_id"`
	DurationSeconds int                 `json:"duration_seconds" yaml:"duration_seconds"`
}

type EpisodeSummary struct {
	ID              string              `json:"id" yaml:"id"`
	CreatedAt       string              `json:"created_at" yaml:"created_at"`
	Problem         string              `json:"problem" yaml:"problem"`
	Domain          string              `json:"domain" yaml:"domain"`
	Outcome         string              `json:"outcome" yaml:"outcome"`
	Tags            []string            `json:"tags" yaml:"tags"`
	Repo            string              `json:"repo,omitempty" yaml:"repo,omitempty"`
	Labels          map[string][]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	StepCount       int                 `json:"step_count" yaml:"step_count"`
	ToolCount       int                 `json:"tool_count" yaml:"tool_count"`
	StepTypes       []string            `json:"step_types" yaml:"step_types"`
	ModelID         string              `json:"model_id" yaml:"model_id"`
	DurationSeconds int                 `json:"duration_seconds" yaml:"duration_seconds"`
	LocalScore      float64             `json:"_local_score,omitempty" yaml:"_local_score,omitempty"`
	VectorScore     float64             `json:"_vector_score,omitempty" yaml:"_vector_score,omitempty"`
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

type Config struct {
	Version       string              `yaml:"version"`
	EpisodesDir   string              `yaml:"episodes_dir"`
	IndexDir      string              `yaml:"index_dir"`
	PatternsDir   string              `yaml:"patterns_dir"`
	VectorDir     string              `yaml:"vector_dir"`
	Embedding     EmbeddingConfig     `yaml:"embedding"`
	Retrieval     RetrievalConfig     `yaml:"retrieval"`
	Consolidation ConsolidationConfig `yaml:"consolidation"`
}

type EmbeddingConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Enabled  bool   `yaml:"enabled"`
}

type RetrievalConfig struct {
	TopKDefault   int     `yaml:"top_k_default"`
	MinSimilarity float64 `yaml:"min_similarity"`
	HybridWeight  float64 `yaml:"hybrid_weight"`
}

type ConsolidationConfig struct {
	PruneAfterDays        int  `yaml:"prune_after_days"`
	MinEpisodesForPattern int  `yaml:"min_episodes_for_pattern"`
	AutoRun               bool `yaml:"auto_run"`
}

type PolishResult struct {
	PolishedPrompt string `json:"polished_prompt"`
	TaskType       string `json:"task_type"`
	Language       string `json:"language,omitempty"`
	Domain         string `json:"domain"`
	SkillInjected  bool   `json:"skill_injected"`
	SkillName      string `json:"skill_name,omitempty"`
	ContextCount   int    `json:"context_count"`
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
}
