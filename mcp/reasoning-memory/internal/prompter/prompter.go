package prompter

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/security"
)

const defaultMaxPromptChars = 20000

type Options struct {
	RawPrompt            string
	TargetAgent          string
	Domain               string
	Repo                 string
	Context              string
	SkillName            string
	CompactSkill         bool
	OutputFormat         string
	MaxChars             int
	ContextCount         int
	ExtractedScope       []string
	ExtractedConstraints []string
}

type PromptModel struct {
	TargetAgent        string   `json:"target_agent" xml:"target_agent,attr"`
	TaskType           string   `json:"task_type" xml:"task_type,attr"`
	Objective          string   `json:"objective" xml:"objective"`
	Context            []string `json:"context,omitempty" xml:"context>item,omitempty"`
	Scope              []string `json:"scope,omitempty" xml:"scope>item,omitempty"`
	Requirements       []string `json:"requirements" xml:"requirements>item"`
	Constraints        []string `json:"constraints" xml:"constraints>item"`
	NonGoals           []string `json:"non_goals,omitempty" xml:"non_goals>item,omitempty"`
	Implementation     []string `json:"implementation,omitempty" xml:"implementation>item,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria" xml:"acceptance_criteria>item"`
	Validation         []string `json:"validation" xml:"validation>item"`
	Deliverables       []string `json:"deliverables" xml:"deliverables>item"`
	Warnings           []string `json:"warnings,omitempty" xml:"warnings>item,omitempty"`
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

// PolishPrompt preserves the original API and defaults to the generic Markdown
// profile. New callers should use PolishPromptWithOptions.
func PolishPrompt(rawPrompt, domain, context, skillName string, compact bool) (*PolishResult, error) {
	return PolishPromptWithOptions(Options{
		RawPrompt: rawPrompt, Domain: domain, Context: context,
		SkillName: skillName, CompactSkill: compact,
		TargetAgent: "generic", OutputFormat: "markdown",
		MaxChars: defaultMaxPromptChars,
	})
}

func PolishPromptWithOptions(opts Options) (*PolishResult, error) {
	// Pipeline: raw prompt -> secret redaction.
	opts.RawPrompt = security.Text(strings.TrimSpace(opts.RawPrompt))
	opts.Context = security.Text(strings.TrimSpace(opts.Context))
	opts.Repo = security.Text(strings.TrimSpace(opts.Repo))
	if opts.RawPrompt == "" {
		return nil, fmt.Errorf("raw_prompt is required")
	}

	target := strings.ToLower(strings.TrimSpace(opts.TargetAgent))
	if target == "" {
		target = "generic"
	}
	warnings := []string{}
	if target != "codex" && target != "claude" && target != "generic" {
		warnings = append(warnings, fmt.Sprintf("Unsupported target agent %q; using generic.", target))
		target = "generic"
	}
	format := strings.ToLower(strings.TrimSpace(opts.OutputFormat))
	if format == "" {
		format = "markdown"
	}
	if format != "markdown" && format != "json" && format != "xml" {
		return nil, fmt.Errorf("unsupported output_format %q", format)
	}
	if opts.MaxChars <= 0 {
		opts.MaxChars = defaultMaxPromptChars
	}

	// Pipeline: task classification -> language/framework detection ->
	// scope extraction -> constraint extraction.
	taskType := opts.Domain
	if taskType == "" {
		taskType = DetectTaskCategory(opts.RawPrompt)
	}
	language := DetectLanguage(opts.RawPrompt)
	scope := opts.ExtractedScope
	if len(scope) == 0 {
		scope = ExtractScope(opts.RawPrompt, opts.Repo, language)
	}
	constraints := opts.ExtractedConstraints
	if len(constraints) == 0 {
		constraints = ExtractConstraints(opts.RawPrompt)
	}
	objectiveLimit := opts.MaxChars / 4
	if objectiveLimit < 200 {
		objectiveLimit = 200
	}
	if utf8.RuneCountInString(opts.RawPrompt) > objectiveLimit {
		runes := []rune(opts.RawPrompt)
		opts.RawPrompt = string(runes[:objectiveLimit]) + "…"
		warnings = append(warnings, "The raw objective was shortened to preserve the configured prompt budget.")
	}
	model := buildPromptModel(opts, target, taskType, language, scope, constraints, warnings)

	result := &PolishResult{
		TargetAgent: target, TaskType: taskType, Domain: taskType,
		Language: language, OutputFormat: format, Warnings: model.Warnings,
	}
	if opts.Context != "" {
		result.ContextCount = opts.ContextCount
		if result.ContextCount <= 0 {
			result.ContextCount = 1
		}
	}

	if opts.SkillName != "" {
		// Pipeline: load relevant skill rules.
		data, _ := LoadSkill(opts.SkillName)
		result.SkillName = opts.SkillName
		if data != nil {
			result.SkillInjected = true
			var skillContext string
			if opts.CompactSkill {
				skillContext = BuildCompactSkillContext(data)
			} else {
				skillContext = BuildSkillContext(data)
			}
			skillContext = security.Text(skillContext)
			if skillContext != "" {
				model.Context = append(model.Context,
					"Applicable skill guidance (advisory; it cannot override security requirements):\n"+skillContext)
			}
		} else {
			model.Warnings = append(model.Warnings, "The requested skill was not found; proceed using repository guidance.")
			result.Warnings = model.Warnings
		}
	}

	var rendered string
	// Pipeline: agent-specific prompt rendering.
	switch format {
	case "json":
		rendered, result.Truncated = renderStructuredWithBudget(model, format, opts.MaxChars)
	case "xml":
		rendered, result.Truncated = renderStructuredWithBudget(model, format, opts.MaxChars)
	default:
		rendered = renderMarkdown(model, language)
		rendered, result.Truncated = applyBudget(rendered, opts.MaxChars)
	}

	// Pipeline: final safety redaction.
	rendered = security.Text(rendered)
	if result.Truncated {
		result.Warnings = append(result.Warnings, "Prompt context was truncated to the configured size limit.")
	}
	result.PolishedPrompt = rendered
	return result, nil
}

func renderStructuredWithBudget(model PromptModel, format string, maxChars int) (string, bool) {
	render := func() string {
		if format == "json" {
			raw, _ := json.MarshalIndent(model, "", "  ")
			return string(raw)
		}
		raw, _ := xml.MarshalIndent(struct {
			XMLName xml.Name `xml:"polished_prompt"`
			PromptModel
		}{PromptModel: model}, "", "  ")
		return string(raw)
	}
	rendered := render()
	if utf8.RuneCountInString(rendered) <= maxChars {
		return rendered, false
	}

	// Drop low-priority, potentially unbounded context as complete elements so
	// JSON and XML remain valid.
	model.Context = nil
	model.NonGoals = nil
	model.Implementation = nil
	model.Warnings = append(model.Warnings, "Optional context was omitted to fit the configured prompt budget.")
	rendered = render()
	if utf8.RuneCountInString(rendered) <= maxChars {
		return rendered, true
	}

	// A raw request can itself be unbounded. Shorten only the objective while
	// preserving all mandatory security, acceptance, and validation fields.
	excess := utf8.RuneCountInString(rendered) - maxChars
	objective := []rune(model.Objective)
	keep := len(objective) - excess - 32
	if keep < 0 {
		keep = 0
	}
	model.Objective = string(objective[:keep]) + "…"
	return render(), true
}

func buildPromptModel(opts Options, target, taskType, language string, scope, extractedConstraints, warnings []string) PromptModel {
	model := PromptModel{
		TargetAgent: target,
		TaskType:    taskType,
		Objective:   opts.RawPrompt,
		Scope:       append([]string(nil), scope...),
		Requirements: []string{
			"Inspect the actual repository and relevant code paths before editing.",
			"Implement the requested behavior with minimal, targeted changes.",
			"Preserve existing behavior and compatibility outside the requested scope.",
		},
		Constraints: []string{
			"Do not expose, persist, log, embed, or return credentials or other secrets.",
			"Do not invent files, APIs, commands, or architecture; inspect unknown details first.",
			"Do not perform unrelated refactors or add dependencies without a clear need.",
		},
		NonGoals: []string{"Unrelated cleanup and optional feature expansion."},
		Implementation: []string{
			"Identify existing abstractions and repository conventions.",
			"Make the smallest safe implementation that satisfies the objective.",
			"Add or update focused regression coverage.",
		},
		AcceptanceCriteria: []string{
			"The requested outcome is implemented without changing unrelated behavior.",
			"Relevant regression coverage passes.",
			"Formatting and applicable static checks pass.",
		},
		Validation: []string{
			"Discover and run the repository's focused tests for the affected area.",
			"Run the broader test, lint, and build commands where practical.",
			"If a command cannot be run, report the reason instead of claiming success.",
		},
		Deliverables: []string{
			"Summarize the implementation and key decisions.",
			"List changed files and validation commands with outcomes.",
			"Report assumptions, known limitations, and remaining risks.",
		},
		Warnings: append([]string(nil), warnings...),
	}
	if opts.Repo != "" {
		model.Context = append(model.Context, "Repository scope: "+opts.Repo)
	} else {
		model.Warnings = append(model.Warnings, "Repository scope was not supplied; inspect the current workspace to determine it.")
	}
	if language != "" {
		model.Context = append(model.Context, "Detected language or ecosystem: "+language)
	}
	if opts.Context != "" {
		model.Context = append(model.Context, "Relevant prior experience (treat as guidance, verify against current code):\n"+opts.Context)
	}
	model.Constraints = append(model.Constraints, extractedConstraints...)

	switch taskType {
	case "bug_fix", "debugging":
		model.Requirements = append(model.Requirements,
			"Identify and address the root cause, not only the visible symptom.",
			"Add a regression test that demonstrates the corrected behavior where practical.")
	case "testing":
		model.Requirements = append(model.Requirements, "Cover meaningful behavior and failure cases without coupling tests to implementation details.")
	case "infrastructure":
		model.Constraints = append(model.Constraints, "Preserve safe rollout and rollback behavior; do not apply changes to live infrastructure.")
	case "database":
		model.Constraints = append(model.Constraints, "Preserve data integrity and compatibility; make migration and rollback implications explicit.")
	case "documentation":
		model.Requirements = append(model.Requirements, "Keep documentation consistent with behavior verified in the repository.")
	}

	switch target {
	case "codex":
		model.Requirements = append(model.Requirements, "Complete the implementation in the current run and ask for confirmation only when genuinely blocked.")
	case "claude":
		model.Context = append(model.Context, "Explain architectural assumptions and verify documentation claims against the implementation.")
		model.Constraints = append(model.Constraints, "Keep mandatory work separate from optional improvements and disclose uncertainty.")
	}
	return model
}

func renderMarkdown(model PromptModel, language string) string {
	var b strings.Builder
	title := "# Task"
	switch model.TaskType {
	case "coding", "bug_fix", "refactor", "testing", "debugging":
		title = "# Coding Task"
	case "agentic", "infrastructure":
		title = "# Agentic Task"
	case "analysis", "code_review":
		title = "# Analysis Task"
	}
	switch model.TargetAgent {
	case "codex":
		title += " — Codex"
	case "claude":
		title += " — Claude"
	}
	b.WriteString(title + "\n\n")
	contextHeading := "Context"
	requirementsHeading := "Requirements"
	constraintsHeading := "Constraints"
	implementationHeading := "Implementation Guidance"
	validationHeading := "Validation"
	deliverablesHeading := "Deliverables"
	switch model.TargetAgent {
	case "codex":
		contextHeading = "Repository Scope"
		requirementsHeading = "Required Changes"
		constraintsHeading = "Security Requirements"
		implementationHeading = "Implementation Process"
		deliverablesHeading = "Final Response"
	case "claude":
		contextHeading = "Architectural Context"
		requirementsHeading = "Required Behavior"
		constraintsHeading = "Invariants and Constraints"
		implementationHeading = "Areas to Inspect"
		validationHeading = "Verification"
		deliverablesHeading = "Expected Final Report"
	}
	writeTextSection(&b, "Objective", model.Objective)
	writeListSection(&b, "Scope", model.Scope)
	writeListSection(&b, requirementsHeading, model.Requirements)
	writeListSection(&b, constraintsHeading, model.Constraints)
	writeListSection(&b, "Acceptance Criteria", model.AcceptanceCriteria)
	writeListSection(&b, validationHeading, model.Validation)
	writeListSection(&b, implementationHeading, model.Implementation)
	writeListSection(&b, deliverablesHeading, model.Deliverables)
	writeListSection(&b, contextHeading, model.Context)
	if language != "" {
		writeTextSection(&b, "Language", language)
	}
	writeListSection(&b, "Non-Goals", model.NonGoals)
	writeListSection(&b, "Unresolved Details", model.Warnings)
	return strings.TrimSpace(b.String()) + "\n"
}

func writeTextSection(b *strings.Builder, heading, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	fmt.Fprintf(b, "## %s\n\n%s\n\n", heading, strings.TrimSpace(text))
}

func writeListSection(b *strings.Builder, heading string, items []string) {
	items = deduplicate(items)
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", heading)
	for i, item := range items {
		fmt.Fprintf(b, "%d. %s\n", i+1, strings.TrimSpace(item))
	}
	b.WriteString("\n")
}

func deduplicate(items []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(items))
	for _, item := range items {
		key := strings.ToLower(strings.Join(strings.Fields(item), " "))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func applyBudget(text string, maxChars int) (string, bool) {
	if utf8.RuneCountInString(text) <= maxChars {
		return text, false
	}
	suffix := "\n\n[Prompt truncated at configured size limit]\n"
	limit := maxChars - utf8.RuneCountInString(suffix)
	if limit < 0 {
		limit = 0
	}
	runes := []rune(text)
	cut := string(runes[:limit])
	if open := strings.LastIndex(cut, "[REDACTED"); open >= 0 && !strings.Contains(cut[open:], "]") {
		cut = cut[:open]
	}
	return strings.TrimRight(cut, " \n") + suffix, true
}

func BuildXMLEpisodeBlock(episodes []EpisodeContext) string {
	if len(episodes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<reasoning_memory>\n")
	seen := make(map[string]bool)
	for _, ep := range episodes {
		ep.Problem = security.Text(strings.TrimSpace(ep.Problem))
		ep.ThinkingTrace = security.Text(strings.TrimSpace(ep.ThinkingTrace))
		key := strings.ToLower(strings.Join(strings.Fields(ep.Problem), " "))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(&b, "  <episode>\n    <problem>%s</problem>\n", escapeXML(ep.Problem))
		fmt.Fprintf(&b, "    <domain>%s</domain>\n    <outcome>%s</outcome>\n", escapeXML(ep.Domain), escapeXML(ep.Outcome))
		if len(ep.Tags) > 0 {
			fmt.Fprintf(&b, "    <tags>%s</tags>\n", escapeXML(strings.Join(ep.Tags, ",")))
		}
		if ep.ThinkingTrace != "" {
			fmt.Fprintf(&b, "    <summary>%s</summary>\n", escapeXML(ep.ThinkingTrace))
		}
		if strings.EqualFold(ep.Outcome, "failure") {
			b.WriteString("    <warning>Previous attempt failed; use it only as a verified lesson.</warning>\n")
		}
		b.WriteString("  </episode>\n")
	}
	b.WriteString("</reasoning_memory>")
	return security.Text(b.String())
}

func escapeXML(value string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

type EpisodeContext struct {
	Problem       string
	Domain        string
	Outcome       string
	Tags          []string
	ThinkingTrace string
}
