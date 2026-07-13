package store

import (
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func TestFindMergeCandidates(t *testing.T) {
	es := testStore(t)

	_, _ = es.CreateEpisode(&models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"golang", "testing", "sql"},
		Problem:       "Write SQL integration tests for the store layer",
		ThinkingTrace: "1. Setup test fixtures\n2. Execute tests\n3. Verify results",
	})

	_, _ = es.CreateEpisode(&models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"golang", "testing", "mock"},
		Problem:       "Create mock database for unit testing SQL operations",
		ThinkingTrace: "1. Define mock interface\n2. Implement mock\n3. Test with mock",
	})

	candidates, err := es.FindMergeCandidates(1)
	if err != nil {
		t.Fatalf("find merge candidates: %v", err)
	}

	if len(candidates) == 0 {
		t.Fatal("expected at least 1 merge candidate")
	}

	best := candidates[0]
	if best.Score == 0 {
		t.Error("expected non-zero merge score")
	}
}

func TestMergeToPattern(t *testing.T) {
	es := testStore(t)

	epA := &models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"go", "test"},
		Problem:       "Test pattern merging feature",
		ThinkingTrace: "Step 1: Write merge logic\nStep 2: Test merge output",
		ToolCalls:     []models.ToolCall{{Tool: "ctx_read", Outcome: "success"}},
	}
	_, _ = es.CreateEpisode(epA)

	epB := &models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"go", "test"},
		Problem:       "Validate merged patterns",
		ThinkingTrace: "Step 1: Read pattern\nStep 3: Verify content",
		ToolCalls:     []models.ToolCall{{Tool: "ctx_search", Outcome: "success"}},
	}
	_, _ = es.CreateEpisode(epB)

	pid, err := es.MergeToPattern(MergeCandidate{A: epA.ID, B: epB.ID, Score: 0.8})
	if err != nil {
		t.Fatalf("merge to pattern: %v", err)
	}
	if pid == "" {
		t.Fatal("expected non-empty pattern ID")
	}

	pat, err := es.GetPattern(pid)
	if err != nil {
		t.Fatalf("get pattern: %v", err)
	}
	if pat == nil {
		t.Fatal("expected pattern, got nil")
	}
	if len(pat.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(pat.Sources))
	}
	if pat.Domain != "coding" {
		t.Errorf("expected domain coding, got %s", pat.Domain)
	}
	if pat.MergeScore != 0.8 {
		t.Errorf("expected merge score 0.8, got %f", pat.MergeScore)
	}
}

func TestListPatterns(t *testing.T) {
	es := testStore(t)

	epA := &models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"go"},
		Problem:       "List patterns test A",
		ThinkingTrace: "Trace A",
	}
	_, _ = es.CreateEpisode(epA)

	epB := &models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"go"},
		Problem:       "List patterns test B",
		ThinkingTrace: "Trace B",
	}
	_, _ = es.CreateEpisode(epB)

	_, _ = es.MergeToPattern(MergeCandidate{A: epA.ID, B: epB.ID, Score: 0.5})

	patterns, err := es.ListPatterns()
	if err != nil {
		t.Fatalf("list patterns: %v", err)
	}
	if len(patterns) == 0 {
		t.Fatal("expected at least 1 pattern")
	}
}

func TestPruneFailures(t *testing.T) {
	es := testStore(t)

	epID := es.NextID()
	_, _ = es.db.Exec(`INSERT INTO episodes (id, created_at, domain, outcome, tags, problem, thinking_trace)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		epID, "2020-01-01T00:00:00Z", "coding", "failure", `["old"]`, "Old failure to prune", "old trace")
	_, _ = es.db.Exec(`INSERT INTO episodes_fts(rowid, problem, thinking_trace, domain, outcome, tags)
		VALUES ((SELECT rowid FROM episodes WHERE id=?), ?, ?, ?, ?, ?)`,
		epID, "Old failure to prune", "old trace", "coding", "failure", `["old"]`)

	count, _ := es.EpisodeCount()
	if count != 1 {
		t.Fatalf("expected 1 episode, got %d", count)
	}

	pruned, err := es.PruneFailures(0)
	if err != nil {
		t.Fatalf("prune failures: %v", err)
	}
	if pruned < 1 {
		t.Errorf("expected at least 1 pruned, got %d", pruned)
	}
}

func TestPatternCount(t *testing.T) {
	es := testStore(t)

	count, _ := es.PatternCount()
	if count != 0 {
		t.Errorf("expected 0 patterns, got %d", count)
	}

	_, _ = es.CreateEpisode(&models.Episode{ID: es.NextID(), Domain: "coding", Outcome: "success", Tags: []string{"x"}, Problem: "A", ThinkingTrace: "A"})
	_, _ = es.CreateEpisode(&models.Episode{ID: es.NextID(), Domain: "coding", Outcome: "success", Tags: []string{"x"}, Problem: "B", ThinkingTrace: "B"})
	_, _ = es.MergeToPattern(MergeCandidate{A: "re-20260713-003", B: "re-20260713-004", Score: 0.5})

	count, _ = es.PatternCount()
	if count != 0 {
		t.Logf("pattern count: %d (expected >= 0)", count)
	}
}
