package linkcontent

import "time"

const (
	StatusSummarized       = "summarized"
	StatusBlocked          = "blocked"
	StatusFetchFailed      = "fetch_failed"
	StatusUnsupported      = "unsupported_content"
	StatusExtractionFailed = "extraction_failed"
	StatusSummaryFailed    = "summary_failed"
)

const (
	SourceTypeJira      = "jira"
	SourceTypeWebPage   = "web_page"
	SourceTypePlainText = "plain_text"
)

const (
	FailurePolicyWarn = "warn"
	FailurePolicyFail = "fail"
)

type Config struct {
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

func DefaultConfig() Config {
	return Config{
		Enabled:                  true,
		MaxLinks:                 5,
		RequestTimeoutSeconds:    10,
		MaxRedirects:             3,
		MaxResponseBytes:         2 * 1024 * 1024,
		MaxExtractedChars:        50000,
		MaxSummaryChars:          4000,
		MaxConcurrency:           3,
		CacheTTLMinutes:          15,
		AllowedContentTypes:      []string{"text/html", "text/plain"},
		FailurePolicy:            FailurePolicyWarn,
		IncludeThinkingTrace:     false,
		RestRequirePreSummarized: true,
	}
}

func (c Config) WithDefaults() Config {
	d := DefaultConfig()
	if c.MaxLinks <= 0 {
		c.MaxLinks = d.MaxLinks
	}
	if c.RequestTimeoutSeconds <= 0 {
		c.RequestTimeoutSeconds = d.RequestTimeoutSeconds
	}
	if c.MaxRedirects < 0 {
		c.MaxRedirects = d.MaxRedirects
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = d.MaxResponseBytes
	}
	if c.MaxExtractedChars <= 0 {
		c.MaxExtractedChars = d.MaxExtractedChars
	}
	if c.MaxSummaryChars <= 0 {
		c.MaxSummaryChars = d.MaxSummaryChars
	}
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = d.MaxConcurrency
	}
	if c.CacheTTLMinutes < 0 {
		c.CacheTTLMinutes = d.CacheTTLMinutes
	}
	if len(c.AllowedContentTypes) == 0 {
		c.AllowedContentTypes = d.AllowedContentTypes
	}
	if c.FailurePolicy == "" {
		c.FailurePolicy = d.FailurePolicy
	}
	if c.FailurePolicy != FailurePolicyWarn && c.FailurePolicy != FailurePolicyFail {
		c.FailurePolicy = d.FailurePolicy
	}
	return c
}

type Source struct {
	SourceURL          string    `json:"source_url" yaml:"source_url"`
	SourceType         string    `json:"source_type,omitempty" yaml:"source_type,omitempty"`
	Title              string    `json:"title,omitempty" yaml:"title,omitempty"`
	Summary            string    `json:"summary,omitempty" yaml:"summary,omitempty"`
	Instructions       []string  `json:"instructions" yaml:"instructions"`
	AcceptanceCriteria []string  `json:"acceptance_criteria" yaml:"acceptance_criteria"`
	Constraints        []string  `json:"constraints" yaml:"constraints"`
	ContentHash        string    `json:"content_hash,omitempty" yaml:"content_hash,omitempty"`
	Status             string    `json:"status" yaml:"status"`
	Warning            string    `json:"warning,omitempty" yaml:"warning,omitempty"`
	FetchedAt          time.Time `json:"fetched_at,omitempty" yaml:"fetched_at,omitempty"`
	Truncated          bool      `json:"truncated,omitempty" yaml:"truncated,omitempty"`
}

type FailureReason string

const (
	ReasonBlocked          FailureReason = "blocked"
	ReasonFetchFailed      FailureReason = "fetch_failed"
	ReasonUnsupported      FailureReason = "unsupported_content"
	ReasonExtractionFailed FailureReason = "extraction_failed"
	ReasonSummaryFailed    FailureReason = "summary_failed"
)
