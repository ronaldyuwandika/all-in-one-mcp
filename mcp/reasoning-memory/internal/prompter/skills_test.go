package prompter

import (
	"strings"
	"testing"
)

func TestParseSkill(t *testing.T) {
	skillMD := `# Docker Expert Skill

## Intent
Build optimized Docker images

## Core Principles
- Use multi-stage builds
- Minimize layers

## Validation Checklist
- Image builds successfully
- No root processes

## Workflow
- Read the Dockerfile
- Apply optimizations

## Constraints
- Must be production-ready
`

	skill := parseSkill("docker-expert", skillMD)
	if skill.Name != "docker-expert" {
		t.Errorf("expected name docker-expert, got %s", skill.Name)
	}
	if len(skill.Principles) != 2 {
		t.Errorf("expected 2 principles, got %d", len(skill.Principles))
	}
	if len(skill.Checklist) != 2 {
		t.Errorf("expected 2 checklist items, got %d", len(skill.Checklist))
	}
	if len(skill.Workflow) != 2 {
		t.Errorf("expected 2 workflow items, got %d", len(skill.Workflow))
	}
	if len(skill.Constraints) != 1 {
		t.Errorf("expected 1 constraint, got %d", len(skill.Constraints))
	}
}

func TestBuildSkillContext(t *testing.T) {
	skill := &SkillData{
		Name:        "test-skill",
		Intent:      "Test skill intent",
		Principles:  []string{"Follow best practices"},
		Checklist:   []string{"All tests pass"},
		Workflow:    []string{"Step 1: Do this"},
		Constraints: []string{"No external deps"},
	}

	ctx := BuildSkillContext(skill)
	if !strings.Contains(ctx, "Test skill intent") {
		t.Error("expected intent in context")
	}
	if !strings.Contains(ctx, "Follow best practices") {
		t.Error("expected principles in context")
	}
	if !strings.Contains(ctx, "All tests pass") {
		t.Error("expected checklist in context")
	}
}

func TestBuildCompactSkillContext(t *testing.T) {
	skill := &SkillData{
		Name:        "compact",
		Intent:      "Compact test",
		Principles:  []string{"P1", "P2"},
		Checklist:   []string{"C1"},
		Constraints: []string{"No CGO"},
	}

	ctx := BuildCompactSkillContext(skill)
	if !strings.Contains(ctx, "Compact test") {
		t.Error("expected intent in compact context")
	}
	if !strings.Contains(ctx, "P1; P2") {
		t.Error("expected principles in compact context")
	}
	if !strings.Contains(ctx, "No CGO") {
		t.Error("expected constraints in compact context")
	}
}

func TestLoadSkillNonexistent(t *testing.T) {
	skill, err := LoadSkill("nonexistent-skill-12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skill != nil {
		t.Error("expected nil for nonexistent skill")
	}
}
