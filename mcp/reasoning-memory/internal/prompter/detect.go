package prompter

import (
	"strings"
)

func DetectTaskCategory(rawPrompt string) string {
	lower := strings.ToLower(rawPrompt)

	categories := []struct {
		name       string
		indicators []string
	}{
		{"bug_fix", []string{"fix bug", "bug fix", "regression", "broken", "incorrect behavior", "doesn't work", "does not work"}},
		{"debugging", []string{"debug", "investigate failure", "root cause", "trace error"}},
		{"testing", []string{"add test", "write test", "test coverage", "unit test", "integration test", "e2e test"}},
		{"refactor", []string{"refactor", "restructure", "cleanup code", "simplify code"}},
		{"code_review", []string{"code review", "review pr", "review pull request", "review this code"}},
		{"infrastructure", []string{"terraform", "kubernetes", "helm", "deploy", "ci/cd", "pipeline", "infrastructure"}},
		{"database", []string{"database", "schema", "migration", "postgres", "mysql", "mongodb", "sqlite", "sql query"}},
		{"documentation", []string{"documentation", "readme", "docs", "document this", "write guide"}},
	}
	for _, category := range categories {
		for _, indicator := range category.indicators {
			if strings.Contains(lower, indicator) {
				return category.name
			}
		}
	}

	codingIndicators := []string{
		"implement", "write code", "fix", "add function", "create api", "optimize", "migrate",
		"golang", "python", "javascript", "typescript", "rust",
		"sql", "bash", "script", "testing", "function ",
	}
	for _, kw := range codingIndicators {
		if strings.Contains(lower, kw) {
			return "coding"
		}
	}

	agenticIndicators := []string{
		"orchestrat", "agent", "workflow", "pipeline", "automate",
		"deploy", "ci/cd", "trigger", "schedule", "monitor",
	}
	for _, kw := range agenticIndicators {
		if strings.Contains(lower, kw) {
			return "agentic"
		}
	}

	analysisIndicators := []string{
		"analyze", "investigate", "explain", "why", "how does",
		"compare", "evaluate", "audit", "review", "assess",
	}
	for _, kw := range analysisIndicators {
		if strings.Contains(lower, kw) {
			return "analysis"
		}
	}

	return "general"
}

// DetectTaskType preserves the original broad-domain API. Prompt polishing uses
// DetectTaskCategory for the more actionable classification.
func DetectTaskType(rawPrompt string) string {
	category := DetectTaskCategory(rawPrompt)
	switch category {
	case "bug_fix", "debugging", "testing", "refactor", "code_review", "database", "documentation":
		return "coding"
	case "infrastructure":
		return "agentic"
	default:
		return category
	}
}

var languagePatterns = []struct {
	Name     string
	Patterns []string
}{
	{"Go", []string{"golang", ".go", "go mod", "go build", "goroutine", "go func", "defer "}},
	{"Python", []string{"python", ".py", "python3", "def ", "import ", "pytest", "pip ", "venv", "django", "flask", "fastapi"}},
	{"Bash", []string{"bash", ".sh", "#!/bin/bash", "shell script"}},
	{"TypeScript", []string{"typescript", ".ts", ".tsx", "interface ", "type ", "react", "angular"}},
	{"JavaScript", []string{"javascript", ".js", ".jsx", "node.js", "npm ", "yarn "}},
	{"Rust", []string{"rust", ".rs", "cargo", "fn ", "struct ", "impl "}},
	{"Java", []string{"java", ".java", "maven", "gradle", "spring", "class "}},
}

func DetectLanguage(rawPrompt string) string {
	lower := strings.ToLower(rawPrompt)
	for _, lp := range languagePatterns {
		for _, pat := range lp.Patterns {
			if strings.Contains(lower, pat) {
				return lp.Name
			}
		}
	}
	return ""
}
