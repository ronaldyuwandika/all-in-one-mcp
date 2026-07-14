package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsolidationQuality(t *testing.T) {
	// 1. Setup data and store
	err := EnsureTestData(".")
	if err != nil {
		t.Fatalf("ensure test data failed: %v", err)
	}

	eps := loadEpisodes(t, "testdata/episodes_1k.json")

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "consolidate_quality.db")
	es := seedStore(t, dbPath, eps, nil)
	defer es.Close()

	// 2. Find merge candidates and consolidate them
	candidates, err := es.FindMergeCandidates(3)
	if err != nil {
		t.Fatalf("failed to find merge candidates: %v", err)
	}

	// Limit to top 50 candidates
	limit := 50
	if len(candidates) < limit {
		limit = len(candidates)
	}
	topCandidates := candidates[:limit]

	type patternDetail struct {
		ID                 string
		AProblem           string
		BProblem           string
		Score              float64
		ConsolidatedPrompt string
		MasterThinkingPath string
	}

	var patterns []patternDetail
	for _, c := range topCandidates {
		pid, err := es.MergeToPattern(c)
		if err != nil {
			continue
		}
		p, err := es.GetPattern(pid)
		if err == nil && p != nil {
			epA, _ := es.GetEpisode(c.A)
			epB, _ := es.GetEpisode(c.B)
			if epA != nil && epB != nil {
				patterns = append(patterns, patternDetail{
					ID:                 pid,
					AProblem:           epA.Problem,
					BProblem:           epB.Problem,
					Score:              c.Score,
					ConsolidatedPrompt: p.ConsolidatedPrompt,
					MasterThinkingPath: p.MasterThinkingPath,
				})
			}
		}
	}

	// 3. Write report
	resultsDir := filepath.Join(".", "results")
	_ = os.MkdirAll(resultsDir, 0755)
	reportPath := filepath.Join(resultsDir, "consolidation-quality.md")

	var report strings.Builder
	report.WriteString("# Consolidation Quality Evaluation Report\n\n")
	report.WriteString("This report lists the top 50 consolidated patterns for human evaluation based on the [evaluation protocol](../eval-protocol.md).\n\n")
	report.WriteString("## Summary Scorecard\n\n")
	report.WriteString("| Target | Rated Patterns | Average Score | Status |\n")
	report.WriteString("| --- | --- | --- | --- |\n")
	report.WriteString("| &gt;3.5 / 5.0 | 50 | [Pending] | 🟡 PENDING EVALUATION |\n\n")
	report.WriteString("## Patterns for Review\n\n")

	for i, pat := range patterns {
		fmt.Fprintf(&report, "### Pattern %d: %s\n\n", i+1, pat.ID)
		fmt.Fprintf(&report, "- **Merge Score**: %.3f\n", pat.Score)
		fmt.Fprintf(&report, "- **Source Episode A**: \"%s\"\n", pat.AProblem)
		fmt.Fprintf(&report, "- **Source Episode B**: \"%s\"\n", pat.BProblem)
		fmt.Fprintf(&report, "- **Consolidated Prompt**:\n  ```\n  %s\n  ```\n", pat.ConsolidatedPrompt)
		fmt.Fprintf(&report, "- **Master Thinking Path Excerpt**:\n  ```\n  %s\n  ```\n", pat.MasterThinkingPath)
		report.WriteString("- **Human Rating (1-5)**: `[ ]` (refer to eval-protocol.md for grading criteria)\n")
		report.WriteString("- **Notes**: `________________________________________________`\n\n")
		report.WriteString("---\n\n")
	}

	err = os.WriteFile(reportPath, []byte(report.String()), 0644)
	if err != nil {
		t.Fatalf("failed to write consolidation quality report: %v", err)
	}

	t.Logf("Consolidation Quality report written to: %s", reportPath)
}
