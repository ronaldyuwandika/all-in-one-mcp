package prompter

import (
	"strings"
)

func DetectTaskCategory(rawPrompt string) string {
	lower := strings.ToLower(rawPrompt)

	// High-confidence explicit signals — these fire unconditionally because
	// they uniquely identify intent even when other category keywords are present.

	// Agentic: orchestration / automation keywords that don't appear in queries or code tasks.
	if containsAny(lower, []string{
		"orchestration script", "orchestrate", "automate", "workflow",
		"multi-agent", "container deployments", "on-call", "coordinate",
	}) {
		return "agentic"
	}

	// General: concept-explanation forms that look like analysis but are not.
	if strings.Contains(lower, "explain the concept") || strings.Contains(lower, "explain a concept") {
		return "general"
	}

	// Analysis: interrogative stems that unambiguously express an investigation goal.
	if containsAny(lower, []string{
		"analyze", "analyse", "investigate",
		"explain how", "explain",
		"compare", "evaluate", "audit", "assess",
		"how does", "root cause", "what explains", "trade-off", "tradeoff",
	}) {
		return "analysis"
	}

	// For everything else, use the broad scorer which weighs signals across all
	// categories simultaneously. This prevents any single sub-category keyword
	// (e.g. "postgres" in a scheduling prompt) from hijacking the result.
	return detectBroadTask(lower)
}

// detectBroadTask scores a prompt across coding/agentic/analysis using a
// weighted signal table and returns the winner, or "general" if no category
// reaches a minimum confidence threshold.
func detectBroadTask(prompt string) string {
	if isSimpleQuestion(prompt) {
		return "general"
	}

	scores := map[string]int{
		"coding":   0,
		"agentic":  0,
		"analysis": 0,
	}

	// ── Coding signals ───────────────────────────────────────────────────────
	// High-confidence: strong action verbs targeting code or a code artifact.
	addScore(prompt, scores, "coding", 3, []string{
		"fix bug", "bug fix", "regression", "incorrect behavior",
		"doesn't work", "does not work",
		"refactor", "restructure",
		"add test", "write test", "unit test", "integration test", "e2e test",
		"code review",
		"implement", "write code", "create api", "add function",
		"debug", "trace error",
	})
	// Medium: describing a broken state or direct coding actions.
	addScore(prompt, scores, "coding", 2, []string{
		"crash", "flaky", "race condition", "stale data", "truncat",
		"disconnect", "doesn't account", "vulnerability",
		"edge case", "broken", "incorrect",
		"fix", "patch ", "port ", "wire up",
		"add ", "change ", "split this",
		"reduce ", "bump ", "handle ",
		"convert the", "update the", "make the",
		"response time", "health check", "paginated",
		// verbs that on their own strongly imply a code task
		"optimize", "migrate", "implement", "serialize", "parse",
		"write golang", "write python", "write javascript", "write typescript", "write rust", "write java",
	})
	// Low: technical objects and language names.
	addScore(prompt, scores, "coding", 1, []string{
		"endpoint", "handler", "route", "middleware",
		"grpc", "websocket", "sdk", "orm",
		"docker image", "docker",
		"module", "toolchain", "header", "retry", "log",
		"golang", "python", "javascript", "typescript", "rust", "java",
		"sql", "bash", "script", "function ", "api",
		"field", "test", "version",
		// schema/database mentions in context of a change task
		"schema", "migration", "postgres", "sqlite", "mysql", "mongodb",
	})

	// ── Agentic signals ──────────────────────────────────────────────────────
	// High-confidence: temporal or event-conditional automation patterns.
	addScore(prompt, scores, "agentic", 3, []string{
		"nightly", "hourly", "every minute", "every sunday", "every day",
		"recurring", "automatically", "auto-",
		"blue-green", "canary", "zero-downtime",
		"fan out", "fan-out", "chatops",
		"trigger", "schedule", "monitor",
	})
	// Medium: coordination / event-driven / multi-step patterns.
	addScore(prompt, scores, "agentic", 2, []string{
		"when a pr", "whenever ", "after the canary", "after the deploy",
		"if error", "if cpu", "if the health", "if memory",
		"notify", "alert", "page the", "scale the", "spin up",
		"promote", "rotate", "gate merge", "open a jira", "restarts the",
		"deploy", "ci/cd", "pipeline", "chain the", "etl steps",
		"across", "regions", "replicas",
	})
	// Low: operational nouns that appear in automation tasks.
	addScore(prompt, scores, "agentic", 1, []string{
		"production", "staging", "worker pool", "queue",
		"pod", "node", "registry", "job", "deployment",
		"terraform", "kubernetes", "helm", "infrastructure",
	})

	// ── Analysis signals ─────────────────────────────────────────────────────
	// High-confidence: question stems expressing investigation intent.
	addScore(prompt, scores, "analysis", 3, []string{
		"why ", "what would", "how much", "how do ", "how would",
		"what fraction", "what is the blast", "pros and cons",
		"what are the pro", "what are the con",
	})
	// Medium: investigative action verbs.
	addScore(prompt, scores, "analysis", 2, []string{
		"break down", "correlate", "profile", "identify", "benchmark",
		"determine", "examine", "calculate", "map out", "figure out",
		"trace the", "check if", "review the security",
		"review pr", "review pull request", "review the pull request",
		// question forms that require a technical subject to be meaningful
		"is the ", "does ", "which ",
	})
	// Low: analytical nouns and concepts.
	addScore(prompt, scores, "analysis", 1, []string{
		"latency", "throughput", "ratio", "distribution",
		"cost increase", "error budget", "memory", "load",
		"contention", "traffic", "complexity", "blast radius",
		"hot path", "affected", "spike", "p99",
		"database load", "caching layer",
	})

	// Pick the category with the highest score; require at least 2 to avoid
	// noise from a single weak signal.
	best := "general"
	bestScore := 0
	// Iterate in deterministic priority order: ties go to coding > agentic > analysis.
	for _, category := range []string{"analysis", "agentic", "coding"} {
		if scores[category] >= bestScore {
			best = category
			bestScore = scores[category]
		}
	}
	if bestScore < 2 {
		return "general"
	}
	return best
}

func addScore(prompt string, scores map[string]int, category string, weight int, signals []string) {
	for _, signal := range signals {
		if strings.Contains(prompt, signal) {
			scores[category] += weight
		}
	}
}

func containsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// isSimpleQuestion detects brief, general-knowledge questions that don't carry
// technical task intent — e.g. "what does X stand for", "who is the CEO of Y".
func isSimpleQuestion(prompt string) bool {
	if containsAny(prompt, []string{
		"what does ", "what is ", "who is ", "who are ", "when is ",
		"where is ", "how to say ", "what time ",
	}) && !containsAny(prompt, []string{
		"latency", "throughput", "memory", "query", "cache", "service",
		"deployment", "cluster", "database", "error", "request", "load",
		"cpu", "spike", "traffic", "budget", "build", "pipeline", "api", "ratio", "sessions",
	}) {
		return true
	}
	return false
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
