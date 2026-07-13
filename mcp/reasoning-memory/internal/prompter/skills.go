package prompter

import (
	"os"
	"path/filepath"
	"strings"
)

var SkillSearchPaths []string

func SetHomeDir(dir string) {
	home = dir
	SkillSearchPaths = []string{
		filepath.Join(dir, ".claude", "skills"),
		filepath.Join(dir, ".agents", "skills"),
		filepath.Join(dir, ".config", "opencode", "skill"),
	}
}

var home string

func init() {
	home, _ = os.UserHomeDir()
	SkillSearchPaths = []string{
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".config", "opencode", "skill"),
	}
}

type SkillData struct {
	Name        string
	Intent      string
	Principles  []string
	Checklist   []string
	Workflow    []string
	Constraints []string
}

func LoadSkill(skillName string) (*SkillData, error) {
	for _, base := range SkillSearchPaths {
		path := filepath.Join(base, skillName, "SKILL.md")
		data, err := os.ReadFile(path) // #nosec G304 -- path constructed from predefined search paths
		if err != nil {
			continue
		}
		return parseSkill(skillName, string(data)), nil
	}
	return nil, nil
}

func parseSkill(name, text string) *SkillData {
	s := &SkillData{Name: name}
	lines := strings.Split(text, "\n")
	s.Intent = extractSection(lines, "Intent", "Intent")

	var inSection string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			sectionName := strings.TrimLeft(trimmed, "# ")
			switch {
			case strings.Contains(strings.ToLower(sectionName), "principle"):
				inSection = "principles"
			case strings.Contains(strings.ToLower(sectionName), "checklist"),
				strings.Contains(strings.ToLower(sectionName), "validation"):
				inSection = "checklist"
			case strings.Contains(strings.ToLower(sectionName), "workflow"),
				strings.Contains(strings.ToLower(sectionName), "process"):
				inSection = "workflow"
			case strings.Contains(strings.ToLower(sectionName), "constraint"),
				strings.Contains(strings.ToLower(sectionName), "rule"):
				inSection = "constraints"
			default:
				inSection = ""
			}
			continue
		}
		if trimmed == "" || !strings.HasPrefix(trimmed, "-") {
			continue
		}
		item := strings.TrimPrefix(trimmed, "- ")
		item = strings.TrimPrefix(item, "-")
		switch inSection {
		case "principles":
			s.Principles = append(s.Principles, item)
		case "checklist":
			s.Checklist = append(s.Checklist, item)
		case "workflow":
			s.Workflow = append(s.Workflow, item)
		case "constraints":
			s.Constraints = append(s.Constraints, item)
		}
	}

	return s
}

func extractSection(lines []string, heading, _ string) string {
	var buf strings.Builder
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") && strings.Contains(strings.ToLower(trimmed), strings.ToLower(heading)) {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "#") {
			break
		}
		if inSection && trimmed != "" {
			cleaned := strings.TrimPrefix(trimmed, "- ")
			buf.WriteString(cleaned)
			buf.WriteString(" ")
		}
	}
	return strings.TrimSpace(buf.String())
}

func BuildSkillContext(data *SkillData) string {
	var buf strings.Builder
	if data.Intent != "" {
		buf.WriteString("Intent: ")
		buf.WriteString(data.Intent)
		buf.WriteString("\n")
	}
	if len(data.Principles) > 0 {
		buf.WriteString("Core Principles:\n")
		for _, p := range data.Principles {
			buf.WriteString("- ")
			buf.WriteString(p)
			buf.WriteString("\n")
		}
	}
	if len(data.Checklist) > 0 {
		buf.WriteString("Validation Checklist:\n")
		for _, c := range data.Checklist {
			buf.WriteString("- ")
			buf.WriteString(c)
			buf.WriteString("\n")
		}
	}
	if len(data.Workflow) > 0 {
		buf.WriteString("Workflow:\n")
		for _, w := range data.Workflow {
			buf.WriteString("- ")
			buf.WriteString(w)
			buf.WriteString("\n")
		}
	}
	if len(data.Constraints) > 0 {
		buf.WriteString("Constraints:\n")
		for _, c := range data.Constraints {
			buf.WriteString("- ")
			buf.WriteString(c)
			buf.WriteString("\n")
		}
	}
	return buf.String()
}

func BuildCompactSkillContext(data *SkillData) string {
	var parts []string
	if data.Intent != "" {
		parts = append(parts, "Intent: "+data.Intent)
	}
	if len(data.Principles) > 0 {
		parts = append(parts, "Principles: "+strings.Join(data.Principles, "; "))
	}
	if len(data.Checklist) > 0 {
		parts = append(parts, "Checklist: "+strings.Join(data.Checklist, "; "))
	}
	if len(data.Constraints) > 0 {
		parts = append(parts, "Constraints: "+strings.Join(data.Constraints, "; "))
	}
	return strings.Join(parts, " | ")
}
