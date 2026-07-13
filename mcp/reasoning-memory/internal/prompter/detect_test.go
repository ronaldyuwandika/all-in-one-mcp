package prompter

import (
	"testing"
)

func TestDetectTaskTypeCoding(t *testing.T) {
	cases := []string{
		"implement a new API endpoint in Go",
		"fix the bug in the auth middleware",
		"refactor the database layer",
		"write golang code for the MCP server",
		"add function to handle errors",
		"optimize the search query",
		"debug the race condition",
		"create a new Python script",
		"migrate the database schema",
	}
	for _, c := range cases {
		got := DetectTaskType(c)
		if got != "coding" {
			t.Errorf("for %q: expected coding, got %s", c, got)
		}
	}
}

func TestDetectTaskTypeAgentic(t *testing.T) {
	cases := []string{
		"orchestrate the deployment pipeline",
		"automate the CI/CD workflow",
		"deploy to production",
	}
	for _, c := range cases {
		got := DetectTaskType(c)
		if got != "agentic" {
			t.Errorf("for %q: expected agentic, got %s", c, got)
		}
	}
}

func TestDetectTaskTypeAnalysis(t *testing.T) {
	cases := []string{
		"analyze the performance bottleneck",
		"explain how the caching works",
		"why does this query return null?",
		"compare these two approaches",
		"audit the security configuration",
		"review the pull request",
	}
	for _, c := range cases {
		got := DetectTaskType(c)
		if got != "analysis" {
			t.Errorf("for %q: expected analysis, got %s", c, got)
		}
	}
}

func TestDetectTaskTypeGeneral(t *testing.T) {
	got := DetectTaskType("hello world")
	if got != "general" {
		t.Errorf("expected general, got %s", got)
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"write a golang server", "Go"},
		{"python script with def function", "Python"},
		{"bash shell script", "Bash"},
		{"typescript react component", "TypeScript"},
		{"javascript node.js app", "JavaScript"},
		{"rust cargo project", "Rust"},
		{"java spring boot service", "Java"},
		{"some random text", ""},
	}
	for _, tc := range cases {
		got := DetectLanguage(tc.input)
		if got != tc.expected {
			t.Errorf("for %q: expected %q, got %q", tc.input, tc.expected, got)
		}
	}
}
