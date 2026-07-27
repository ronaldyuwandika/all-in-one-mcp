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

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
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
