package security

import (
	"encoding/json"
	"sync/atomic"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/pkg/secretdetect"
)

var activeConfig atomic.Value

func init() {
	activeConfig.Store(secretdetect.DefaultConfig())
}

func Configure(replacement string) {
	config := secretdetect.DefaultConfig()
	if replacement != "" {
		config.Replacement = replacement
	}
	activeConfig.Store(config)
}

func Text(value string) string {
	return secretdetect.RedactWithConfig(value, activeConfig.Load().(secretdetect.Config)).Text
}

func Strings(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = Text(value)
	}
	return out
}

func Labels(values map[string][]string) map[string][]string {
	if values == nil {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, value := range values {
		out[Text(key)] = Strings(value)
	}
	return out
}

func Any(value any) any {
	switch v := value.(type) {
	case string:
		return Text(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = Any(v[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[Text(key)] = Any(item)
		}
		return out
	default:
		// Tool arguments can arrive as typed maps/slices. A JSON round-trip
		// normalizes them so every nested string is covered.
		raw, err := json.Marshal(value)
		if err != nil {
			return value
		}
		var normalized any
		if json.Unmarshal(raw, &normalized) != nil {
			return value
		}
		switch normalized.(type) {
		case map[string]any, []any, string:
			return Any(normalized)
		default:
			return value
		}
	}
}

func Episode(ep *models.Episode) {
	if ep == nil {
		return
	}
	ep.Domain = Text(ep.Domain)
	ep.Outcome = models.EpisodeOutcome(Text(string(ep.Outcome)))
	ep.Tags = Strings(ep.Tags)
	ep.Repo = Text(ep.Repo)
	ep.Project = Text(ep.Project)
	ep.Provenance = Text(ep.Provenance)
	ep.Labels = Labels(ep.Labels)
	ep.Problem = Text(ep.Problem)
	ep.Objectives = Strings(ep.Objectives)
	ep.Decisions = Strings(ep.Decisions)
	ep.Alternatives = Strings(ep.Alternatives)
	verification := ep.Verification
	ep.Verification = make([]models.VerificationRecord, len(verification))
	for i, v := range verification {
		ep.Verification[i] = models.VerificationRecord{
			Type:     models.VerificationType(Text(string(v.Type))),
			Command:  Text(v.Command),
			Result:   Text(v.Result),
			Success:  v.Success,
			Evidence: Text(v.Evidence),
		}
	}
	ep.Lessons = Strings(ep.Lessons)
	for i := range ep.FailedApproaches {
		ep.FailedApproaches[i].Approach = Text(ep.FailedApproaches[i].Approach)
		ep.FailedApproaches[i].FailureMode = Text(ep.FailedApproaches[i].FailureMode)
		ep.FailedApproaches[i].RootCause = Text(ep.FailedApproaches[i].RootCause)
		ep.FailedApproaches[i].Lesson = Text(ep.FailedApproaches[i].Lesson)
	}
	ep.ThinkingTrace = Text(ep.ThinkingTrace)
	ep.ModelID = Text(ep.ModelID)
	for i := range ep.Steps {
		ep.Steps[i].ID = Text(ep.Steps[i].ID)
		ep.Steps[i].Type = Text(ep.Steps[i].Type)
		ep.Steps[i].Content = Text(ep.Steps[i].Content)
	}
	for i := range ep.ToolCalls {
		ep.ToolCalls[i].Tool = Text(ep.ToolCalls[i].Tool)
		ep.ToolCalls[i].Args = Any(ep.ToolCalls[i].Args)
		ep.ToolCalls[i].ResultExcerpt = Text(ep.ToolCalls[i].ResultExcerpt)
		ep.ToolCalls[i].Outcome = Text(ep.ToolCalls[i].Outcome)
	}
}

func Summary(summary *models.EpisodeSummary) {
	if summary == nil {
		return
	}
	summary.Problem = Text(summary.Problem)
	summary.Domain = Text(summary.Domain)
	summary.Outcome = models.EpisodeOutcome(Text(string(summary.Outcome)))
	summary.Tags = Strings(summary.Tags)
	summary.Repo = Text(summary.Repo)
	summary.Project = Text(summary.Project)
	summary.Provenance = Text(summary.Provenance)
	summary.Labels = Labels(summary.Labels)
	summary.ModelID = Text(summary.ModelID)
	summary.StepTypes = Strings(summary.StepTypes)
	summary.VerificationTypes = Strings(summary.VerificationTypes)
	for i := range summary.VerificationSummaries {
		summary.VerificationSummaries[i].Type = Text(summary.VerificationSummaries[i].Type)
		summary.VerificationSummaries[i].ResultExcerpt = Text(summary.VerificationSummaries[i].ResultExcerpt)
		summary.VerificationSummaries[i].EvidenceExcerpt = Text(summary.VerificationSummaries[i].EvidenceExcerpt)
	}
}

func Pattern(pattern *models.Pattern) {
	if pattern == nil {
		return
	}
	pattern.Domain = Text(pattern.Domain)
	pattern.Sources = Strings(pattern.Sources)
	pattern.ConsolidatedPrompt = Text(pattern.ConsolidatedPrompt)
	pattern.MasterThinkingPath = Text(pattern.MasterThinkingPath)
	pattern.Tags = Strings(pattern.Tags)
	for i := range pattern.MasterToolCalls {
		pattern.MasterToolCalls[i].Tool = Text(pattern.MasterToolCalls[i].Tool)
		pattern.MasterToolCalls[i].Args = Any(pattern.MasterToolCalls[i].Args)
		pattern.MasterToolCalls[i].ResultExcerpt = Text(pattern.MasterToolCalls[i].ResultExcerpt)
		pattern.MasterToolCalls[i].Outcome = Text(pattern.MasterToolCalls[i].Outcome)
	}
}
