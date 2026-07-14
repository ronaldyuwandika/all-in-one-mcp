package store

import (
	"encoding/json"
	"path/filepath"
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

	if byOutcome["success"] != 2 {
		t.Errorf("expected 2 success, got %d", byOutcome["success"])
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
	if ep.Outcome != "success" {
		t.Errorf("expected outcome success, got %s", ep.Outcome)
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
		Outcome:       "test",
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
