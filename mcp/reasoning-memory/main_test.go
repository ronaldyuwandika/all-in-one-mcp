package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/linkcontent"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
)

func TestMain(m *testing.M) {
	// Set up temp db for testing
	dir, err := os.MkdirTemp("", "rmn-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "test.db")
	esStore, err := store.New(dbPath)
	if err != nil {
		panic(err)
	}
	es = esStore
	defer es.Close()
	cfg = &models.Config{}
	linkService = linkcontent.NewService(linkcontent.DefaultConfig(), nil, nil)

	// Seed some test data
	_, _ = es.CreateEpisode(&models.Episode{
		ID:              "re-20260726-001",
		Domain:          "coding",
		Outcome:         "success",
		Tags:            []string{"go", "http"},
		Problem:         "Fix http handler crash",
		ThinkingTrace:   "Checked logs, fixed nil pointer",
		DurationSeconds: 15,
		Repo:            "test-repo",
	})
	_, _ = es.CreateEpisode(&models.Episode{
		ID:              "re-20260726-002",
		Domain:          "agentic",
		Outcome:         "failure",
		Tags:            []string{"docker", "ci"},
		Problem:         "CI build fails",
		ThinkingTrace:   "Docker daemon not running",
		DurationSeconds: 120,
		Repo:            "test-repo",
	})

	os.Exit(m.Run())
}

func TestToolSchemasSerializeArrayItems(t *testing.T) {
	tools := map[string]mcp.Tool{
		"capture_reasoning_episode": mcp.NewTool("capture_reasoning_episode", mcp.WithArray("tool_calls", mcp.Items(toolCallSchema)), mcp.WithArray("tags", mcp.WithStringItems())),
		"record_decision":           mcp.NewTool("record_decision", mcp.WithArray("tradeoffs", mcp.WithStringItems()), mcp.WithArray("assumptions", mcp.WithStringItems()), mcp.WithArray("evidence", mcp.WithStringItems()), mcp.WithArray("alternatives", mcp.Items(alternativeSchema))),
		"retrieve_reasoning":        mcp.NewTool("retrieve_reasoning", mcp.WithArray("tags", mcp.WithStringItems())),
		"memorize_concept":          mcp.NewTool("memorize_concept", mcp.WithArray("tags", mcp.WithStringItems())),
	}

	assertArrayItems := func(toolName, propertyName string) map[string]any {
		t.Helper()
		property := tools[toolName].InputSchema.Properties[propertyName].(map[string]any)
		return property["items"].(map[string]any)
	}
	if got := assertArrayItems("capture_reasoning_episode", "tool_calls")["type"]; got != "object" {
		t.Fatalf("tool_calls items type = %v, want object", got)
	}
	alternativeItems := assertArrayItems("record_decision", "alternatives")
	if got := alternativeItems["type"]; got != "object" {
		t.Fatalf("alternatives items type = %v, want object", got)
	}
	alternativeProperties := alternativeItems["properties"].(map[string]any)
	tradeoffs := alternativeProperties["tradeoffs"].(map[string]any)
	if got := tradeoffs["type"]; got != "array" {
		t.Fatalf("alternatives.tradeoffs type = %v, want array", got)
	}
	tradeoffItems := tradeoffs["items"].(map[string]any)
	if got := tradeoffItems["type"]; got != "string" {
		t.Fatalf("alternatives.tradeoffs items type = %v, want string", got)
	}
	encoded, err := json.Marshal(tools["record_decision"].InputSchema)
	if err != nil {
		t.Fatalf("marshal decision schema: %v", err)
	}
	var serialized map[string]any
	if err := json.Unmarshal(encoded, &serialized); err != nil {
		t.Fatalf("unmarshal decision schema: %v", err)
	}
	serializedProperties := serialized["properties"].(map[string]any)
	serializedAlternatives := serializedProperties["alternatives"].(map[string]any)
	serializedAlternativeItems := serializedAlternatives["items"].(map[string]any)
	serializedAlternativeProperties := serializedAlternativeItems["properties"].(map[string]any)
	serializedTradeoffs := serializedAlternativeProperties["tradeoffs"].(map[string]any)
	if _, ok := serializedTradeoffs["items"]; !ok {
		t.Fatal("serialized alternatives.tradeoffs schema omitted items")
	}
	encoded, err = json.Marshal(tools["capture_reasoning_episode"].InputSchema)
	if err != nil || !strings.Contains(string(encoded), `"items"`) {
		t.Fatalf("serialized schema omitted array items: %v", err)
	}
}

func TestAPIEpisodes(t *testing.T) {
	// Test GET all
	req := httptest.NewRequest("GET", "/api/episodes", nil)
	rr := httptest.NewRecorder()
	handleAPIEpisodes(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if int(resp["total"].(float64)) != 2 {
		t.Errorf("expected total 2, got %v", resp["total"])
	}

	// Test tag filter
	req = httptest.NewRequest("GET", "/api/episodes?tag=go", nil)
	rr = httptest.NewRecorder()
	handleAPIEpisodes(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if int(resp["total"].(float64)) != 1 {
		t.Errorf("expected total 1 for tag 'go', got %v", resp["total"])
	}

	// Test POST method not allowed
	req = httptest.NewRequest("POST", "/api/episodes", nil)
	rr = httptest.NewRecorder()
	handleAPIEpisodes(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestAPIPatterns(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/patterns", nil)
	rr := httptest.NewRecorder()
	handleAPIPatterns(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestAPIStats(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/stats", nil)
	rr := httptest.NewRecorder()
	handleAPIStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var result models.StatsResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.EpisodesTotal != 2 {
		t.Errorf("expected 2 episodes, got %d", result.EpisodesTotal)
	}
}

func TestAPIPolishPreSummarizedOnly(t *testing.T) {
	original := cfg.LinkIngestion
	cfg.LinkIngestion.RestRequirePreSummarized = true
	t.Cleanup(func() { cfg.LinkIngestion = original })
	body := `{"raw_prompt": "See https://example.com/item"}`
	req := httptest.NewRequest("POST", "/api/polish", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAPIPolish(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var res prompter.PolishResult
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode polish response: %v", err)
	}
	found := false
	for _, warning := range res.Warnings {
		found = found || strings.Contains(warning, "link_summary_required")
	}
	if !found {
		t.Fatalf("expected link_summary_required warning, got %#v", res.Warnings)
	}
}

func TestAPIPolish(t *testing.T) {
	// Test valid POST
	body := `{"raw_prompt": "fix the http crash"}`
	req := httptest.NewRequest("POST", "/api/polish", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAPIPolish(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["polished_prompt"] == nil || result["polished_prompt"] == "" {
		t.Errorf("expected non-empty polished prompt, got %v", result["polished_prompt"])
	}

	// Test missing prompt
	body = `{"raw_prompt": ""}`
	req = httptest.NewRequest("POST", "/api/polish", bytes.NewBufferString(body))
	rr = httptest.NewRecorder()
	handleAPIPolish(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}

	// Test pre-summarized linked sources
	cfg.LinkIngestion.MaxLinks = 5
	cfg.LinkIngestion.RestRequirePreSummarized = true
	body = `{"raw_prompt": "review https://example.com/bug", "linked_sources": [{"source_url": "https://example.com/bug", "status": "summarized", "summary": "Fix bug", "instructions": ["fix"], "acceptance_criteria": ["tests pass"], "constraints": []}]}`
	req = httptest.NewRequest("POST", "/api/polish", bytes.NewBufferString(body))
	rr = httptest.NewRecorder()
	handleAPIPolish(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var polished map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &polished); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rendered, ok := polished["polished_prompt"].(string)
	if !ok || !strings.Contains(rendered, "Linked source summaries") || !strings.Contains(rendered, "Fix bug") {
		t.Fatalf("expected linked summary block, got %q", rendered)
	}

	// Test pre-summarized requirement when URLs present
	body = `{"raw_prompt": "check https://example.com/another"}`
	req = httptest.NewRequest("POST", "/api/polish", bytes.NewBufferString(body))
	rr = httptest.NewRecorder()
	handleAPIPolish(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &polished); err != nil {
		t.Fatalf("decode: %v", err)
	}
	warnings, _ := polished["warnings"].([]interface{})
	found := false
	for _, w := range warnings {
		if strings.Contains(w.(string), "link_summary_required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected link_summary_required warning, got %v", warnings)
	}
}

func TestMCPRichEpisodeToolsAndGetArchived(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"problem": "mcp rich", "thinking_trace": "trace", "outcome": "verified_success", "confidence": 0.8, "objectives": []any{"obj1"}, "project": "prj"}
	result, err := handleCapture(es, cfg)(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("handleCapture: %v result=%#v", err, result)
	}
	id := result.Content[0].(mcp.TextContent).Text
	reqGet := mcp.CallToolRequest{}
	reqGet.Params.Arguments = map[string]any{"episode_id": id}
	resultGet, err := handleGetEpisode(es)(context.Background(), reqGet)
	if err != nil || resultGet.IsError {
		t.Fatalf("handleGetEpisode: %v result=%#v", err, resultGet)
	}
	var ep models.Episode
	if err := json.Unmarshal([]byte(resultGet.Content[0].(mcp.TextContent).Text), &ep); err != nil {
		t.Fatal(err)
	}
	if ep.Project != "prj" || ep.Confidence == nil || *ep.Confidence != 0.8 || len(ep.Objectives) != 1 {
		t.Fatalf("MCP get_episode mismatch: %#v", ep)
	}
	reqUpdate := mcp.CallToolRequest{}
	ep.Lessons = []string{"learned"}
	reqUpdate.Params.Arguments = map[string]any{"episode": ep}
	resultUpdate, err := handleUpdateEpisode(es)(context.Background(), reqUpdate)
	if err != nil || resultUpdate.IsError {
		t.Fatalf("handleUpdateEpisode: %v result=%#v", err, resultUpdate)
	}
	reqList := mcp.CallToolRequest{}
	reqList.Params.Arguments = map[string]any{"limit": 10, "offset": 0}
	resultList, err := handleListEpisodes(es)(context.Background(), reqList)
	if err != nil || resultList.IsError {
		t.Fatalf("handleListEpisodes: %v result=%#v", err, resultList)
	}
	var summaries []models.EpisodeSummary
	if err := json.Unmarshal([]byte(resultList.Content[0].(mcp.TextContent).Text), &summaries); err != nil || len(summaries) == 0 {
		t.Fatalf("MCP list_episodes mismatch: %#v err=%v", summaries, err)
	}
}

func TestCreateEpisodeAlias(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"problem": "alias test", "thinking_trace": "trace", "outcome": "verified_success"}
	result, err := handleCapture(es, cfg)(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("handleCapture: %v result=%#v", err, result)
	}
	id := result.Content[0].(mcp.TextContent).Text
	ep, err := es.GetEpisode(id)
	if err != nil || ep == nil || ep.Outcome != models.OutcomeVerifiedSuccess {
		t.Fatalf("create_episode alias result mismatch: %#v err=%v", ep, err)
	}
}

func TestAPIGraph(t *testing.T) {
	// Add a semantic concept first
	cid, err := es.MemorizeConcept(context.Background(), "http-server", "concept", "Go http server component", []string{"go", "http"}, "re-20260726-001")
	if err != nil {
		t.Fatalf("failed to create concept: %v", err)
	}

	// Add an edge
	_, err = es.AddEdge("re-20260726-001", cid, "uses", 1.0)
	if err != nil {
		t.Fatalf("failed to create edge: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/graph", nil)
	rr := httptest.NewRecorder()
	handleAPIGraph(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp graphJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should contain the concept list or episodes
	foundNode := false
	for _, n := range resp.Nodes {
		if strings.Contains(n.ID, "re-") {
			foundNode = true
			break
		}
	}
	if !foundNode {
		t.Errorf("expected to find episode node in graph, got none")
	}

	foundConcept := false
	for _, n := range resp.Nodes {
		if n.ID == cid {
			foundConcept = true
			break
		}
	}
	if !foundConcept {
		t.Errorf("expected to find concept node %q in graph, got none", cid)
	}

	if len(resp.Edges) != 1 {
		t.Errorf("expected 1 edge in graph, got %d", len(resp.Edges))
	}
}
