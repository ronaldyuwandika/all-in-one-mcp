package prompter

import (
	"strings"
	"testing"
)

func TestPolishCodingPrompt(t *testing.T) {
	result, err := PolishPrompt("Implement a Go function to parse JSON", "", "", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TaskType != "coding" {
		t.Errorf("expected coding task type, got %s", result.TaskType)
	}
	if result.Language != "Go" {
		t.Errorf("expected Go language, got %s", result.Language)
	}
	if !strings.Contains(result.PolishedPrompt, "Go function to parse JSON") {
		t.Error("expected original prompt in polished output")
	}
	if !strings.Contains(result.PolishedPrompt, "Coding Task") {
		t.Error("expected Coding Task header")
	}
}

func TestPolishAgenticPrompt(t *testing.T) {
	result, err := PolishPrompt("Orchestrate the CI/CD pipeline", "agentic", "", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Domain != "agentic" {
		t.Errorf("expected domain agentic, got %s", result.Domain)
	}
	if !strings.Contains(result.PolishedPrompt, "Agentic Task") {
		t.Error("expected Agentic Task header")
	}
}

func TestPolishAnalysisPrompt(t *testing.T) {
	result, err := PolishPrompt("Analyze why the cache is slow", "", "", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TaskType != "analysis" {
		t.Errorf("expected analysis task type, got %s", result.TaskType)
	}
	if !strings.Contains(result.PolishedPrompt, "Analysis Task") {
		t.Error("expected Analysis Task header")
	}
}

func TestPolishWithSkillInjection(t *testing.T) {
	result, err := PolishPrompt("Build a Docker image", "", "", "docker-expert", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SkillInjected {
		t.Log("skill injected (expected when SKILL.md exists)")
	}
	if result.SkillName != "docker-expert" {
		t.Errorf("expected skill name docker-expert, got %s", result.SkillName)
	}
}

func TestPolishDomainOverride(t *testing.T) {
	result, err := PolishPrompt("Write a poem", "coding", "", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Domain != "coding" {
		t.Errorf("expected overridden domain coding, got %s", result.Domain)
	}
}

func TestBuildXMLEpisodeBlock(t *testing.T) {
	episodes := []EpisodeContext{
		{
			Problem:       "Test problem",
			Domain:        "coding",
			Outcome:       "success",
			Tags:          []string{"go", "test"},
			ThinkingTrace: "1. Write test\n2. Verify",
		},
	}

	xml := BuildXMLEpisodeBlock(episodes)
	if !strings.Contains(xml, "<reasoning_memory>") {
		t.Error("expected <reasoning_memory> wrapper")
	}
	if !strings.Contains(xml, "Test problem") {
		t.Error("expected problem in XML")
	}
	if !strings.Contains(xml, "coding") {
		t.Error("expected domain in XML")
	}
}

func TestBuildXMLEpisodeBlockEmpty(t *testing.T) {
	xml := BuildXMLEpisodeBlock(nil)
	if xml != "" {
		t.Errorf("expected empty string, got %q", xml)
	}
}

func TestAgentProfilesAndSecretRedaction(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	for _, agent := range []string{"codex", "claude", "generic"} {
		result, err := PolishPromptWithOptions(Options{
			RawPrompt:    "fix auth bug using " + secret,
			TargetAgent:  agent,
			Context:      "previous attempt used " + secret,
			OutputFormat: "markdown",
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.TargetAgent != agent {
			t.Fatalf("target = %q, want %q", result.TargetAgent, agent)
		}
		if strings.Contains(result.PolishedPrompt, secret) {
			t.Fatalf("%s prompt leaked secret", agent)
		}
		for _, section := range []string{"Objective", "Acceptance Criteria"} {
			if !strings.Contains(result.PolishedPrompt, section) {
				t.Errorf("%s prompt missing %q", agent, section)
			}
		}
	}
}

func TestOutputFormatsAndBudget(t *testing.T) {
	for _, format := range []string{"json", "xml"} {
		result, err := PolishPromptWithOptions(Options{
			RawPrompt: "document the API", TargetAgent: "generic",
			OutputFormat: format, MaxChars: 20000,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.PolishedPrompt, "document the API") {
			t.Errorf("%s output lost user intent", format)
		}
	}

	result, err := PolishPromptWithOptions(Options{
		RawPrompt:   strings.Repeat("implement safely ", 100),
		TargetAgent: "codex", OutputFormat: "markdown", MaxChars: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len([]rune(result.PolishedPrompt)) > 500 {
		t.Fatalf("budget not enforced: truncated=%v chars=%d", result.Truncated, len([]rune(result.PolishedPrompt)))
	}
}

func TestPolishDeterministic(t *testing.T) {
	opts := Options{RawPrompt: "add database migration tests", TargetAgent: "claude", OutputFormat: "markdown"}
	a, err := PolishPromptWithOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PolishPromptWithOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	if a.PolishedPrompt != b.PolishedPrompt {
		t.Fatal("polishing is not deterministic")
	}
}

func TestContextCountPreserved(t *testing.T) {
	result, err := PolishPromptWithOptions(Options{
		RawPrompt: "fix auth", Context: "two concise memories",
		ContextCount: 2, OutputFormat: "markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextCount != 2 {
		t.Fatalf("context count = %d, want 2", result.ContextCount)
	}
}
