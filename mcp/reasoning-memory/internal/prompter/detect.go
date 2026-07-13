package prompter

import (
	"strings"
)

func DetectTaskType(rawPrompt string) string {
	lower := strings.ToLower(rawPrompt)

	codingIndicators := []string{
		"implement", "refactor", "write code", "fix", "debug",
		"add function", "create api", "optimize", "migrate",
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
