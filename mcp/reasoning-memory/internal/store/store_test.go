package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func seedPattern(es *EpisodeStore) *models.Pattern {
	for i := 0; i < 3; i++ {
		_, _ = es.CreateEpisode(&models.Episode{
			ID:            es.NextID(),
			Domain:        "coding",
			Outcome:       "success",
			Tags:          []string{"go", "testing", "ci"},
			Problem:       "test",
			ThinkingTrace: "trace",
		})
	}
	candidates, _ := es.FindMergeCandidates(2)
	if len(candidates) > 0 {
		pid, _ := es.MergeToPattern(candidates[0])
		pat, _ := es.GetPattern(pid)
		return pat
	}
	return nil
}

func createEpisode(es *EpisodeStore, domain, outcome string, tags []string, prob, trace string, duration int) string {
	id := es.NextID()
	_, _ = es.CreateEpisode(&models.Episode{
		ID:              id,
		Domain:          domain,
		Outcome:         outcome,
		Tags:            tags,
		Problem:         prob,
		ThinkingTrace:   trace,
		DurationSeconds: duration,
	})
	return id
}

func TestEpisodesByDomain(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 0)
	createEpisode(es, "agentic", "partial", nil, "p2", "t2", 0)
	createEpisode(es, "coding", "failure", nil, "p3", "t3", 0)

	byDomain, err := es.EpisodesByDomain()
	if err != nil {
		t.Fatalf("EpisodesByDomain: %v", err)
	}

	if byDomain["coding"] != 2 {
		t.Errorf("expected 2 coding, got %d", byDomain["coding"])
	}
	if byDomain["agentic"] != 1 {
		t.Errorf("expected 1 agentic, got %d", byDomain["agentic"])
	}
}

func TestEpisodesByOutcome(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 0)
	createEpisode(es, "coding", "success", nil, "p2", "t2", 0)
	createEpisode(es, "coding", "failure", nil, "p3", "t3", 0)

	byOutcome, err := es.EpisodesByOutcome()
	if err != nil {
		t.Fatalf("EpisodesByOutcome: %v", err)
	}

	if byOutcome["unverified_success"] != 2 {
		t.Errorf("expected 2 unverified_success, got %d", byOutcome["unverified_success"])
	}
	if byOutcome["failure"] != 1 {
		t.Errorf("expected 1 failure, got %d", byOutcome["failure"])
	}
}

func TestTopTags(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", []string{"go", "testing"}, "p1", "t1", 0)
	createEpisode(es, "coding", "success", []string{"go", "mcp"}, "p2", "t2", 0)
	createEpisode(es, "agentic", "partial", []string{"python", "testing"}, "p3", "t3", 0)

	tags, err := es.TopTags(10)
	if err != nil {
		t.Fatalf("TopTags: %v", err)
	}

	freq := make(map[string]int)
	for _, tc := range tags {
		freq[tc.Tag] = tc.Count
	}

	if freq["go"] != 2 {
		t.Errorf("expected go:2, got %d", freq["go"])
	}
	if freq["testing"] != 2 {
		t.Errorf("expected testing:2, got %d", freq["testing"])
	}
	if freq["mcp"] != 1 {
		t.Errorf("expected mcp:1, got %d", freq["mcp"])
	}
}

func TestTopTagsLimit(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", []string{"a", "b", "c"}, "p1", "t1", 0)
	createEpisode(es, "coding", "success", []string{"a", "b"}, "p2", "t2", 0)

	tags, err := es.TopTags(2)
	if err != nil {
		t.Fatalf("TopTags: %v", err)
	}

	if len(tags) > 2 {
		t.Errorf("expected at most 2 tags, got %d", len(tags))
	}
}

func TestAvgEpisodeLengths(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "short", "trace", 0)
	createEpisode(es, "coding", "success", nil, "longer problem", "longer thinking trace here", 0)

	avgProb, avgTrace, err := es.AvgEpisodeLengths()
	if err != nil {
		t.Fatalf("AvgEpisodeLengths: %v", err)
	}

	if avgProb < 5 || avgProb > 15 {
		t.Errorf("expected avg problem around 10, got %f", avgProb)
	}
	if avgTrace < 8 || avgTrace > 25 {
		t.Errorf("expected avg trace around 15, got %f", avgTrace)
	}
}

func TestEmptyThinkingTraceCount(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "full trace", 0)
	createEpisode(es, "coding", "success", nil, "p2", "", 0)

	count, err := es.EmptyThinkingTraceCount()
	if err != nil {
		t.Fatalf("EmptyThinkingTraceCount: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 empty trace, got %d", count)
	}
}

func TestDBSizeMB(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 0)

	size, err := es.DBSizeMB()
	if err != nil {
		t.Fatalf("DBSizeMB: %v", err)
	}

	if size <= 0 {
		t.Errorf("expected positive size, got %f", size)
	}
}

func TestDBPath(t *testing.T) {
	dir := t.TempDir()
	es, err := New(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer es.Close()

	if es.DBPath() == "" {
		t.Error("expected non-empty DBPath")
	}
}

func TestDB(t *testing.T) {
	es := testStore(t)
	if es.DB() == nil {
		t.Error("expected non-nil DB handle")
	}
}

func TestLastConsolidationTS(t *testing.T) {
	es := testStore(t)

	// No patterns yet
	ts, err := es.LastConsolidationTS()
	if err != nil {
		t.Fatalf("LastConsolidationTS (no patterns): %v", err)
	}
	if ts != nil {
		t.Errorf("expected nil, got %v", ts)
	}

	// Add a pattern
	pat := seedPattern(es)
	if pat == nil {
		t.Skip("no pattern merged (need 3+ episodes)")
	}

	ts, err = es.LastConsolidationTS()
	if err != nil {
		t.Fatalf("LastConsolidationTS: %v", err)
	}
	if ts == nil {
		t.Error("expected non-nil timestamp")
	}
}

func TestEpisodesByDay(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 10)
	createEpisode(es, "coding", "success", nil, "p2", "t2", 20)

	buckets, err := es.EpisodesByDay(1)
	if err != nil {
		t.Fatalf("EpisodesByDay: %v", err)
	}

	if len(buckets) == 0 {
		t.Error("expected at least 1 day bucket")
	}
}

func TestEmptyStoreStats(t *testing.T) {
	es := testStore(t)

	stats, err := es.SummaryStats()
	if err != nil {
		t.Fatalf("SummaryStats: %v", err)
	}

	if stats.TotalEpisodes != 0 {
		t.Errorf("expected 0 episodes, got %d", stats.TotalEpisodes)
	}
}

func TestSummaryStats(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 10)
	createEpisode(es, "coding", "success", nil, "p2", "t2", 20)
	createEpisode(es, "agentic", "failure", nil, "p3", "t3", 30)

	stats, err := es.SummaryStats()
	if err != nil {
		t.Fatalf("SummaryStats: %v", err)
	}

	if stats.TotalEpisodes != 3 {
		t.Errorf("expected 3 episodes, got %d", stats.TotalEpisodes)
	}

	if stats.SuccessRate <= 0 || stats.SuccessRate > 100 {
		t.Errorf("expected success rate between 0-100, got %f", stats.SuccessRate)
	}

	if stats.AvgDurationSec <= 0 {
		t.Errorf("expected positive avg duration, got %f", stats.AvgDurationSec)
	}

	if stats.TopDomain != "coding" {
		t.Errorf("expected top domain 'coding', got '%s'", stats.TopDomain)
	}
}

func TestDeletePattern(t *testing.T) {
	es := testStore(t)
	pat := seedPattern(es)
	if pat == nil {
		t.Skip("no pattern to delete")
	}

	if err := es.DeletePattern(pat.ID); err != nil {
		t.Fatalf("DeletePattern: %v", err)
	}

	got, err := es.GetPattern(pat.ID)
	if err != nil {
		t.Fatalf("GetPattern after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestReindexFTS5(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 0)

	if err := es.ReindexFTS5(); err != nil {
		t.Fatalf("ReindexFTS5: %v", err)
	}

	results, err := es.SearchLocal("p1", "", "", "", nil, 10)
	if err != nil {
		t.Fatalf("SearchLocal after reindex: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result after reindex, got %d", len(results))
	}
}

func testStore(t *testing.T) *EpisodeStore {
	t.Helper()
	dir := t.TempDir()

	es, err := New(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = es.Close() })
	return es
}

func seedEpisode(es *EpisodeStore) *models.Episode {
	ep := &models.Episode{
		ID:              es.NextID(),
		CreatedAt:       time.Now().UTC(),
		Domain:          "coding",
		Outcome:         "success",
		Tags:            []string{"golang", "testing"},
		Problem:         "Write unit tests for the reasoning-memory store layer",
		ThinkingTrace:   "1. Analyze the store interface\n2. Implement SQLite store with FTS5\n3. Write table-driven tests\n4. Verify all edge cases",
		Steps:           []models.Step{{ID: "s1", Type: "analysis", Content: "Analyze the store interface"}},
		ToolCalls:       []models.ToolCall{{Tool: "ctx_read", Outcome: "success"}},
		ModelID:         "test-model",
		DurationSeconds: 42,
	}
	_, _ = es.CreateEpisode(ep)
	return ep
}

func TestCreateEpisode(t *testing.T) {
	es := testStore(t)

	epID, err := es.CreateEpisode(&models.Episode{
		ID:            "re-20260713-001",
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"go", "test"},
		Problem:       "Test creating an episode",
		ThinkingTrace: "Test thinking trace content",
		Steps:         []models.Step{{ID: "s1", Type: "implementation", Content: "Test"}},
	})
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	if epID == "" {
		t.Fatal("expected non-empty episode ID")
	}

	ep, err := es.GetEpisode(epID)
	if err != nil {
		t.Fatalf("get episode: %v", err)
	}
	if ep == nil {
		t.Fatal("expected episode, got nil")
	}
	if ep.Domain != "coding" {
		t.Errorf("expected domain coding, got %s", ep.Domain)
	}
	if ep.Outcome != "unverified_success" {
		t.Errorf("expected outcome unverified_success, got %s", ep.Outcome)
	}
}

func TestGetEpisodeNotFound(t *testing.T) {
	es := testStore(t)
	ep, err := es.GetEpisode("nonexistent")
	if err != nil {
		t.Fatalf("get episode: %v", err)
	}
	if ep != nil {
		t.Error("expected nil for nonexistent episode")
	}
}

func TestGetSummary(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)

	summary, err := es.GetSummary(ep.ID)
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	if summary == nil {
		t.Fatal("expected summary, got nil")
	}
	if summary.Domain != "coding" {
		t.Errorf("expected domain coding, got %s", summary.Domain)
	}
	if summary.StepCount != 1 {
		t.Errorf("expected 1 step, got %d", summary.StepCount)
	}
	if summary.ToolCount != 1 {
		t.Errorf("expected 1 tool call, got %d", summary.ToolCount)
	}
}

func TestListEpisodes(t *testing.T) {
	es := testStore(t)
	ep1 := seedEpisode(es)

	ep2 := &models.Episode{
		ID:            es.NextID(),
		Domain:        "agentic",
		Outcome:       "partial",
		Tags:          []string{"mcp"},
		Problem:       "Second episode",
		ThinkingTrace: "Trace 2",
	}
	_, _ = es.CreateEpisode(ep2)

	episodes, err := es.ListEpisodes(10, 0)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Errorf("expected 2 episodes, got %d", len(episodes))
	}

	ids := map[string]bool{}
	for _, ep := range episodes {
		ids[ep.ID] = true
	}
	if !ids[ep1.ID] || !ids[ep2.ID] {
		t.Errorf("expected both episode IDs in results, got %v", ids)
	}
}

func TestDeleteEpisode(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)

	var count int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM metadata_idx WHERE episode_id = ?", ep.ID).Scan(&count); err != nil || count == 0 {
		t.Fatalf("expected metadata_idx rows before delete, count=%d err=%v", count, err)
	}

	if err := es.DeleteEpisode(ep.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}

	if err := es.db.QueryRow("SELECT COUNT(*) FROM metadata_idx WHERE episode_id = ?", ep.ID).Scan(&count); err != nil || count != 0 {
		t.Errorf("expected 0 metadata_idx rows after delete, got %d (err: %v)", count, err)
	}
}

func TestEpisodeCount(t *testing.T) {
	es := testStore(t)
	seedEpisode(es)

	count, err := es.EpisodeCount()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 episode, got %d", count)
	}
}

func TestNextID(t *testing.T) {
	es := testStore(t)
	id1 := es.NextID()
	if id1 == "" {
		t.Fatal("expected non-empty ID")
	}

	_, _ = es.CreateEpisode(&models.Episode{
		ID:            id1,
		Domain:        "test",
		Outcome:       "failure",
		Problem:       "test",
		ThinkingTrace: "test",
	})

	id2 := es.NextID()
	if id1 == id2 {
		t.Errorf("expected different IDs, got %s and %s", id1, id2)
	}
}

func TestPersistTagJSON(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)

	summary, _ := es.GetSummary(ep.ID)
	if len(summary.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(summary.Tags))
	}

	foundGo := false
	foundTesting := false
	for _, tag := range summary.Tags {
		if tag == "golang" {
			foundGo = true
		}
		if tag == "testing" {
			foundTesting = true
		}
	}
	if !foundGo || !foundTesting {
		t.Errorf("expected tags to contain golang and testing, got %v", summary.Tags)
	}
}

func TestToolCallsJSONRoundtrip(t *testing.T) {
	es := testStore(t)
	tc := models.ToolCall{
		Tool:          "ctx_read",
		Args:          map[string]any{"path": "/tmp/test.go", "mode": "auto"},
		ResultExcerpt: "func main() {",
		Outcome:       "success",
	}

	ep := &models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Problem:       "test tool calls",
		ThinkingTrace: "trace",
		ToolCalls:     []models.ToolCall{tc},
	}
	_, _ = es.CreateEpisode(ep)

	got, _ := es.GetEpisode(ep.ID)
	if len(got.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(got.ToolCalls))
	}
	if got.ToolCalls[0].Tool != "ctx_read" {
		t.Errorf("expected ctx_read tool, got %s", got.ToolCalls[0].Tool)
	}
	if got.ToolCalls[0].Outcome != "success" {
		t.Errorf("expected success outcome, got %s", got.ToolCalls[0].Outcome)
	}

	argsJSON, _ := json.Marshal(got.ToolCalls[0].Args)
	var args map[string]any
	_ = json.Unmarshal(argsJSON, &args)
	if args["path"] != "/tmp/test.go" {
		t.Errorf("expected path /tmp/test.go, got %v", args["path"])
	}
}

func TestRichEpisodeRoundTripUpdateAndArchive(t *testing.T) {
	es := testStore(t)
	confidence := 0.75
	ep := &models.Episode{
		ID:           es.NextID(),
		Domain:       "coding",
		Outcome:      models.OutcomeVerifiedSuccess,
		Tags:         []string{"rich"},
		Repo:         "repo",
		Project:      "project",
		Provenance:   "agent",
		Confidence:   &confidence,
		Problem:      "rich problem",
		Objectives:   []string{"objective"},
		Decisions:    []string{"decision"},
		Alternatives: []string{"alternative"},
		Verification: []string{"go test ./..."},
		Lessons:      []string{"lesson"},
	}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	got, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != ep.Project || got.Provenance != ep.Provenance || got.Confidence == nil || *got.Confidence != confidence || len(got.Objectives) != 1 || len(got.Decisions) != 1 || len(got.Alternatives) != 1 || len(got.Verification) != 1 || len(got.Lessons) != 1 {
		t.Fatalf("rich fields did not round-trip: %#v", got)
	}
	got.Lessons = append(got.Lessons, "updated")
	if err := es.UpdateEpisode(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	updated, err := es.GetEpisode(ep.ID)
	if err != nil || len(updated.Lessons) != 2 || updated.UpdatedAt.Before(updated.CreatedAt) {
		t.Fatalf("updated episode mismatch: %#v err=%v", updated, err)
	}
	if _, err := es.db.Exec(`INSERT INTO episodes_archive SELECT * FROM episodes WHERE id = ?`, ep.ID); err != nil {
		t.Fatal(err)
	}
	archived, err := es.GetArchivedEpisode(ep.ID)
	if err != nil || archived.Project != ep.Project || len(archived.Lessons) != 2 {
		t.Fatalf("archived episode mismatch: %#v err=%v", archived, err)
	}
}

func TestLegacyMigrationAndValidation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE episodes (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, domain TEXT NOT NULL, outcome TEXT NOT NULL, tags TEXT NOT NULL, problem TEXT NOT NULL, thinking_trace TEXT NOT NULL, steps TEXT NOT NULL, tool_calls TEXT NOT NULL, model_id TEXT NOT NULL, duration_seconds INTEGER NOT NULL, repo TEXT NOT NULL DEFAULT '', labels TEXT NOT NULL DEFAULT '{}', tier TEXT NOT NULL DEFAULT 'episodic')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO episodes (id, created_at, domain, outcome, tags, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds, repo, labels, tier) VALUES ('legacy', '2026-01-01T00:00:00Z', 'coding', 'success', '[]', 'problem', '', '[]', '[]', '', 0, '', '{}', '')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	es, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	got, err := es.GetEpisode("legacy")
	if err != nil || got.Outcome != models.OutcomeUnverifiedSuccess || !got.IsEpisodic() {
		t.Fatalf("legacy migration mismatch: %#v err=%v", got, err)
	}
	for _, invalid := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.1, 1.1} {
		ep := &models.Episode{Problem: "problem", Outcome: models.OutcomeFailure, Confidence: &invalid}
		if err := ep.Validate(); err == nil {
			t.Fatalf("accepted invalid confidence %v", invalid)
		}
	}
	legacy := &models.Episode{ID: "compat", Problem: "problem", Outcome: "success"}
	if _, err := es.CreateEpisode(legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Outcome != "success" || legacy.Tier != "" {
		t.Fatalf("compatibility boundary mutated request: %#v", legacy)
	}
}

func TestMalformedPersistedJSONReturnsFieldError(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)
	if _, err := es.db.Exec("UPDATE episodes SET tags = ? WHERE id = ?", "{", ep.ID); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func() error{
		"get":     func() error { _, err := es.GetEpisode(ep.ID); return err },
		"summary": func() error { _, err := es.GetSummary(ep.ID); return err },
		"list":    func() error { _, err := es.ListEpisodes(10, 0); return err },
		"search":  func() error { _, err := es.SearchLocal("unit tests", "", "", "", nil, 5); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := read()
			if err == nil || !strings.Contains(err.Error(), "episode "+ep.ID+" field tags") {
				t.Fatalf("expected descriptive tags error, got %v", err)
			}
		})
	}
}

func TestSearchNormalizesLegacyOutcomeFilter(t *testing.T) {
	es := testStore(t)
	ep := &models.Episode{ID: es.NextID(), Outcome: "success", Problem: "legacy filter target", ThinkingTrace: "trace"}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	results, err := es.SearchLocal("legacy filter target", "", "success", "", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != models.OutcomeUnverifiedSuccess {
		t.Fatalf("legacy success filter mismatch: %#v", results)
	}
}

func TestUpdateEpisodeVectorReplaceFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configure  func(*VectorStore)
		wantErrors []string
	}{
		{
			name: "delete failure",
			configure: func(vec *VectorStore) {
				vec.deleteEpisodeHook = func(context.Context, string) error { return errors.New("forced delete failure") }
			},
			wantErrors: []string{"forced delete failure", "restore previous episode vector"},
		},
		{
			name: "partial add failure",
			configure: func(vec *VectorStore) {
				calls := 0
				vec.addEpisodeAfterHook = func(context.Context, string, string, string) error {
					calls++
					if calls == 1 {
						return errors.New("forced partial add failure")
					}
					return nil
				}
			},
			wantErrors: []string{"forced partial add failure"},
		},
		{
			name: "restore failure",
			configure: func(vec *VectorStore) {
				calls := 0
				vec.addEpisodeHook = func(context.Context, string, string, string) error {
					calls++
					if calls == 1 {
						return errors.New("forced replacement failure")
					}
					return errors.New("forced restore failure")
				}
			},
			wantErrors: []string{"forced replacement failure", "restore previous episode vector", "forced restore failure"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vec, err := NewVectorStore(t.TempDir(), "mock", "", "", "", true)
			if err != nil {
				t.Fatal(err)
			}
			es, err := NewWithVector(filepath.Join(t.TempDir(), "update-compensation.db"), vec)
			if err != nil {
				t.Fatal(err)
			}
			defer es.Close()
			ep := &models.Episode{ID: es.NextID(), Outcome: models.OutcomeFailure, Problem: "before", ThinkingTrace: "before trace"}
			if _, err := es.CreateEpisode(ep); err != nil {
				t.Fatal(err)
			}
			tc.configure(vec)
			updated, err := es.GetEpisode(ep.ID)
			if err != nil {
				t.Fatal(err)
			}
			updated.Problem = "after"
			err = es.UpdateEpisode(context.Background(), updated)
			for _, want := range tc.wantErrors {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error %v does not contain %q", err, want)
				}
			}
			got, getErr := es.GetEpisode(ep.ID)
			if getErr != nil || got.Problem != "before" {
				t.Fatalf("database update was not compensated: %#v err=%v", got, getErr)
			}
		})
	}
}

func TestUpdateEpisodePropagatesGetEpisodeErrorBeforeUpdate(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)
	if _, err := es.db.Exec("UPDATE episodes SET created_at = ? WHERE id = ?", "bad-created-at", ep.ID); err != nil {
		t.Fatal(err)
	}
	updateReq := &models.Episode{ID: ep.ID, Outcome: models.OutcomeFailure, Problem: "update problem", ThinkingTrace: "update trace"}
	err := es.UpdateEpisode(context.Background(), updateReq)
	if err == nil || !strings.Contains(err.Error(), "get existing episode before update") || !strings.Contains(err.Error(), "field created_at") {
		t.Fatalf("expected GetEpisode error propagation before update, got %v", err)
	}
}

func TestVectorReconciliationLifecycleAndRestart(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "reconcile.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	ep := &models.Episode{ID: es.NextID(), Outcome: models.OutcomeFailure, Problem: "initial problem", ThinkingTrace: "initial trace"}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	vec.deleteEpisodeHook = func(context.Context, string) error { return errors.New("forced replacement failure") }
	vec.addEpisodeHook = func(context.Context, string, string, string) error { return errors.New("forced restore failure") }
	updated, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.Problem = "attempted update"
	err = es.UpdateEpisode(context.Background(), updated)
	if err == nil || !strings.Contains(err.Error(), "forced replacement failure") {
		t.Fatalf("expected update failure, got %v", err)
	}
	var pendingCount int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM vector_reconcile WHERE episode_id = ?", ep.ID).Scan(&pendingCount); err != nil || pendingCount != 1 {
		t.Fatalf("expected 1 pending reconcile row after rollback failure, got %d err=%v", pendingCount, err)
	}
	var pendingProblem string
	if err := es.db.QueryRow("SELECT problem FROM vector_reconcile WHERE episode_id = ?", ep.ID).Scan(&pendingProblem); err != nil || pendingProblem != "initial problem" {
		t.Fatalf("expected durable pending problem 'initial problem', got %q err=%v", pendingProblem, err)
	}
	_ = es.Close()
	vec2, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es2, err := NewWithVector(dbPath, vec2)
	if err != nil {
		t.Fatalf("expected successful startup reconciliation, got %v", err)
	}
	defer es2.Close()
	if err := es2.db.QueryRow("SELECT COUNT(*) FROM vector_reconcile WHERE episode_id = ?", ep.ID).Scan(&pendingCount); err != nil || pendingCount != 0 {
		t.Fatalf("expected 0 pending reconcile rows after restart, got %d err=%v", pendingCount, err)
	}
}

func TestDeleteEpisodeRemovesPendingVectorReconciliation(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "delete-pending.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	ep := &models.Episode{ID: es.NextID(), Outcome: models.OutcomeFailure, Problem: "delete pending target", ThinkingTrace: "trace"}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	vec.deleteEpisodeHook = func(context.Context, string) error { return errors.New("forced update delete failure") }
	updated, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.Problem = "failed update"
	if err := es.UpdateEpisode(context.Background(), updated); err == nil {
		t.Fatal("expected vector update failure")
	}
	vec.deleteEpisodeHook = nil
	if err := es.DeleteEpisode(ep.ID); err != nil {
		t.Fatal(err)
	}
	var pendingCount int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM vector_reconcile WHERE episode_id = ?", ep.ID).Scan(&pendingCount); err != nil || pendingCount != 0 {
		t.Fatalf("pending reconcile row survived delete: count=%d err=%v", pendingCount, err)
	}
	_ = es.Close()
	vec2, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es2, err := NewWithVector(dbPath, vec2)
	if err != nil {
		t.Fatal(err)
	}
	defer es2.Close()
	if err := es2.ReconcileVectorStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := vec2.Count(); got != 0 {
		t.Fatalf("ghost vector recreated for deleted episode %s; vector count=%d", ep.ID, got)
	}
}

func TestMigrationBackfillRebuildsFTSAfterOutcomeNormalization(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fts-order.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE episodes (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, domain TEXT NOT NULL, outcome TEXT NOT NULL, tags TEXT NOT NULL, problem TEXT NOT NULL, thinking_trace TEXT NOT NULL, steps TEXT NOT NULL, tool_calls TEXT NOT NULL, model_id TEXT NOT NULL, duration_seconds INTEGER NOT NULL); INSERT INTO episodes VALUES ('legacy-fts', '2026-01-01T00:00:00Z', 'coding', 'success', '[]', 'fts migration target', '', '[]', '[]', '', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	es, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	var outcome string
	if err := es.db.QueryRow(`SELECT outcome FROM episodes_fts WHERE episodes_fts MATCH 'unverified_success'`).Scan(&outcome); err != nil || outcome != models.OutcomeUnverifiedSuccess {
		t.Fatalf("FTS outcome not rebuilt after backfill: outcome=%q err=%v", outcome, err)
	}
}

func TestFreshDatabaseFTSTriggersTrackInsertUpdateDelete(t *testing.T) {
	es := testStore(t)
	ep := &models.Episode{ID: es.NextID(), Outcome: models.OutcomeFailure, Problem: "fresh insert target", ThinkingTrace: "trace"}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	assertSearchCount := func(query string, want int) {
		t.Helper()
		results, err := es.SearchLocal(query, "", "", "", nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != want {
			t.Fatalf("search %q returned %d, want %d", query, len(results), want)
		}
	}
	assertSearchCount("fresh insert target", 1)
	stored, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Problem = "fresh update target"
	if err := es.UpdateEpisode(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := es.db.QueryRow(`SELECT COUNT(*) FROM episodes_fts WHERE episodes_fts MATCH '"fresh insert target"'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old FTS content still indexed: count=%d err=%v", count, err)
	}
	assertSearchCount("fresh update target", 1)
	if err := es.DeleteEpisode(ep.ID); err != nil {
		t.Fatal(err)
	}
	assertSearchCount("fresh update target", 0)
}

func TestMalformedPersistedTimestampReturnsFieldError(t *testing.T) {
	for _, field := range []string{"created_at", "updated_at"} {
		t.Run(field, func(t *testing.T) {
			es := testStore(t)
			ep := seedEpisode(es)
			if _, err := es.db.Exec("UPDATE episodes SET "+field+" = ? WHERE id = ?", "not-a-time", ep.ID); err != nil {
				t.Fatal(err)
			}
			for name, read := range map[string]func() error{
				"get":     func() error { _, err := es.GetEpisode(ep.ID); return err },
				"summary": func() error { _, err := es.GetSummary(ep.ID); return err },
				"list":    func() error { _, err := es.ListEpisodes(10, 0); return err },
				"search":  func() error { _, err := es.SearchLocal("unit tests", "", "", "", nil, 5); return err },
			} {
				t.Run(name, func(t *testing.T) {
					err := read()
					if err == nil || !strings.Contains(err.Error(), "episode "+ep.ID+" field "+field) {
						t.Fatalf("expected descriptive %s error, got %v", field, err)
					}
				})
			}
		})
	}
}
