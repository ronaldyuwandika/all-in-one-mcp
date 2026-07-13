package prompter

import (
	"fmt"
	"strings"
)

func PolishCodingPrompt(raw, language, context, skillContext string) string {
	var buf strings.Builder

	buf.WriteString("# Coding Task\n\n")
	buf.WriteString("## Task\n")
	buf.WriteString(raw)
	buf.WriteString("\n\n")

	if language != "" {
		buf.WriteString("## Language\n")
		buf.WriteString(language)
		buf.WriteString("\n\n")
	}

	if skillContext != "" {
		buf.WriteString("## Skill Rules\n")
		buf.WriteString(skillContext)
		buf.WriteString("\n\n")
	}

	buf.WriteString("## Execution Protocol\n")
	buf.WriteString("1. Understand the codebase and conventions\n")
	buf.WriteString("2. Plan the implementation with error handling\n")
	buf.WriteString("3. Implement following idiomatic patterns\n")
	buf.WriteString("4. Verify with tests and linting\n")
	buf.WriteString("5. Only commit when explicitly requested\n")

	if context != "" {
		buf.WriteString("\n## Relevant Past Reasoning\n")
		buf.WriteString(context)
	}

	return buf.String()
}

func PolishAgenticPrompt(raw, context, skillContext string) string {
	var buf strings.Builder

	buf.WriteString("# Agentic Task\n\n")
	buf.WriteString("## Goal\n")
	buf.WriteString(raw)
	buf.WriteString("\n\n")

	if skillContext != "" {
		buf.WriteString("## Domain Constraints\n")
		buf.WriteString(skillContext)
		buf.WriteString("\n\n")
	}

	buf.WriteString("## Autonomy Rules\n")
	buf.WriteString("- Decide on sub-tasks independently\n")
	buf.WriteString("- Report blockers immediately\n")
	buf.WriteString("- Verify each step before proceeding\n")
	buf.WriteString("- Handle errors gracefully with fallbacks\n")

	if context != "" {
		buf.WriteString("\n## Relevant Past Context\n")
		buf.WriteString(context)
	}

	return buf.String()
}

func PolishAnalysisPrompt(raw, context, skillContext string) string {
	var buf strings.Builder

	buf.WriteString("# Analysis Task\n\n")
	buf.WriteString("## Question\n")
	buf.WriteString(raw)
	buf.WriteString("\n\n")

	if skillContext != "" {
		buf.WriteString("## Domain Knowledge\n")
		buf.WriteString(skillContext)
		buf.WriteString("\n\n")
	}

	buf.WriteString("## Analysis Framework\n")
	buf.WriteString("1. Define scope and assumptions\n")
	buf.WriteString("2. Gather relevant data\n")
	buf.WriteString("3. Analyze with evidence\n")
	buf.WriteString("4. Present findings with confidence levels\n")

	if context != "" {
		buf.WriteString("\n## Related Analysis\n")
		buf.WriteString(context)
	}

	return buf.String()
}

func PolishGeneralPrompt(raw, context, skillContext string) string {
	var buf strings.Builder

	buf.WriteString("# Task\n\n")
	buf.WriteString(raw)
	buf.WriteString("\n\n")

	if skillContext != "" {
		buf.WriteString("## Guidelines\n")
		buf.WriteString(skillContext)
		buf.WriteString("\n\n")
	}

	if context != "" {
		buf.WriteString("## Relevant Context\n")
		buf.WriteString(context)
	}

	return buf.String()
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

func PolishPrompt(rawPrompt, domain, context, skillName string, compact bool) (*PolishResult, error) {
	result := &PolishResult{}

	if domain == "" {
		domain = DetectTaskType(rawPrompt)
	}
	result.Domain = domain

	result.TaskType = domain
	result.Language = DetectLanguage(rawPrompt)

	var skillContext string
	if skillName != "" {
		data, _ := LoadSkill(skillName)
		if data != nil {
			result.SkillInjected = true
			result.SkillName = skillName
			if compact {
				skillContext = BuildCompactSkillContext(data)
			} else {
				skillContext = BuildSkillContext(data)
			}
		}
	}

	switch domain {
	case "coding":
		result.PolishedPrompt = PolishCodingPrompt(rawPrompt, result.Language, context, skillContext)
	case "agentic":
		result.PolishedPrompt = PolishAgenticPrompt(rawPrompt, context, skillContext)
	case "analysis":
		result.PolishedPrompt = PolishAnalysisPrompt(rawPrompt, context, skillContext)
	default:
		result.PolishedPrompt = PolishGeneralPrompt(rawPrompt, context, skillContext)
	}

	if context != "" {
		result.ContextCount = 1
	}

	return result, nil
}

func BuildXMLEpisodeBlock(episodes []EpisodeContext) string {
	if len(episodes) == 0 {
		return ""
	}

	var buf strings.Builder
	buf.WriteString("<reasoning_memory>\n")

	for i, ep := range episodes {
		fmt.Fprintf(&buf, "  <episode id=\"%d\">\n", i+1)
		fmt.Fprintf(&buf, "    <problem>%s</problem>\n", strings.TrimSpace(ep.Problem))
		fmt.Fprintf(&buf, "    <domain>%s</domain>\n", ep.Domain)
		fmt.Fprintf(&buf, "    <outcome>%s</outcome>\n", ep.Outcome)
		if len(ep.Tags) > 0 {
			fmt.Fprintf(&buf, "    <tags>%s</tags>\n", strings.Join(ep.Tags, ","))
		}
		fmt.Fprintf(&buf, "    <thinking_trace>%s</thinking_trace>\n", strings.TrimSpace(ep.ThinkingTrace))
		buf.WriteString("  </episode>\n")
	}

	buf.WriteString("</reasoning_memory>")
	return buf.String()
}

type EpisodeContext struct {
	Problem       string
	Domain        string
	Outcome       string
	Tags          []string
	ThinkingTrace string
}
