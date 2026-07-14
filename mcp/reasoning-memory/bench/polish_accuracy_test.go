package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
)

type LabeledPromptTest struct {
	Prompt string `json:"prompt"`
	Type   string `json:"task_type"`
}

func TestPolishAccuracy(t *testing.T) {
	// 1. Setup data
	err := EnsureTestData(".")
	if err != nil {
		t.Fatalf("ensure test data failed: %v", err)
	}

	data, err := os.ReadFile("testdata/polish_prompts.json")
	if err != nil {
		t.Fatalf("failed to read prompts: %v", err)
	}

	var prompts []LabeledPromptTest
	if err := json.Unmarshal(data, &prompts); err != nil {
		t.Fatalf("failed to unmarshal prompts: %v", err)
	}

	// 2. Classify and count
	correct := 0
	total := len(prompts)
	categoryTotal := make(map[string]int)
	categoryCorrect := make(map[string]int)

	type mismatchDetail struct {
		Prompt   string
		Expected string
		Got      string
	}
	var mismatches []mismatchDetail

	for _, p := range prompts {
		categoryTotal[p.Type]++
		got := prompter.DetectTaskType(p.Prompt)
		if got == p.Type {
			correct++
			categoryCorrect[p.Type]++
		} else {
			mismatches = append(mismatches, mismatchDetail{
				Prompt:   p.Prompt,
				Expected: p.Type,
				Got:      got,
			})
		}
	}

	overallAccuracy := (float64(correct) / float64(total)) * 100.0

	// 3. Generate report
	resultsDir := filepath.Join(".", "results")
	_ = os.MkdirAll(resultsDir, 0755)
	reportPath := filepath.Join(resultsDir, "polish-accuracy.md")

	reportContent := fmt.Sprintf("# Prompt Polish Task Detection Accuracy Report\n\n"+
		"Calculated across %d test prompts.\n\n"+
		"## Overall Accuracy: %.2f%%\n\n"+
		"## Breakdown by Task Type\n\n"+
		"| Task Type | Total Prompts | Correct | Accuracy |\n"+
		"| --- | --- | --- | --- |\n"+
		"| Coding | %d | %d | %.2f%% |\n"+
		"| Agentic | %d | %d | %.2f%% |\n"+
		"| Analysis | %d | %d | %.2f%% |\n"+
		"| General | %d | %d | %.2f%% |\n\n"+
		"## Sample Mismatches\n\n",
		total, overallAccuracy,
		categoryTotal["coding"], categoryCorrect["coding"], (float64(categoryCorrect["coding"])/float64(categoryTotal["coding"]))*100.0,
		categoryTotal["agentic"], categoryCorrect["agentic"], (float64(categoryCorrect["agentic"])/float64(categoryTotal["agentic"]))*100.0,
		categoryTotal["analysis"], categoryCorrect["analysis"], (float64(categoryCorrect["analysis"])/float64(categoryTotal["analysis"]))*100.0,
		categoryTotal["general"], categoryCorrect["general"], (float64(categoryCorrect["general"])/float64(categoryTotal["general"]))*100.0)

	if len(mismatches) > 0 {
		reportContent += "| Prompt | Expected | Got |\n| --- | --- | --- |\n"
		// Print top 10 mismatches
		limit := 10
		if len(mismatches) < limit {
			limit = len(mismatches)
		}
		for i := 0; i < limit; i++ {
			m := mismatches[i]
			reportContent += fmt.Sprintf("| \"%s\" | %s | %s |\n", m.Prompt, m.Expected, m.Got)
		}
	} else {
		reportContent += "*No classification mismatches found! Perfect accuracy.*\n"
	}

	err = os.WriteFile(reportPath, []byte(reportContent), 0644)
	if err != nil {
		t.Fatalf("failed to write polish accuracy report: %v", err)
	}

	t.Logf("Polish accuracy test completed: %.2f%% accuracy", overallAccuracy)
}
