package store

import (
	"testing"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func TestSearchLocalBasic(t *testing.T) {
	es := testStore(t)
	seedEpisode(es)

	results, err := es.SearchLocal("unit tests", "", "", "", nil, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 search result")
	}

	found := false
	for _, r := range results {
		if r.Domain == "coding" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find coding domain episode")
	}
}

func TestSearchLocalDomainFilter(t *testing.T) {
	es := testStore(t)
	seedEpisode(es)

	time.Sleep(10 * time.Millisecond)

	_, _ = es.CreateEpisode(&models.Episode{
		ID:            es.NextID(),
		Domain:        "agentic",
		Outcome:       "success",
		Tags:          []string{"agent"},
		Problem:       "Agentic task",
		ThinkingTrace: "Agentic trace",
	})

	results, err := es.SearchLocal("task", "agentic", "", "", nil, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range results {
		if r.Domain != "agentic" {
			t.Errorf("expected only agentic, got domain=%s", r.Domain)
		}
	}
}

func TestSearchLocalOutcomeFilter(t *testing.T) {
	es := testStore(t)
	seedEpisode(es)

	time.Sleep(10 * time.Millisecond)

	_, _ = es.CreateEpisode(&models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "failure",
		Tags:          []string{"bug"},
		Problem:       "Failed test",
		ThinkingTrace: "Debugging trace",
	})

	results, err := es.SearchLocal("test", "", "failure", "", nil, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range results {
		if r.Outcome != "failure" {
			t.Errorf("expected only failure, got outcome=%s", r.Outcome)
		}
	}
}

func TestSearchLocalNoMatch(t *testing.T) {
	es := testStore(t)
	seedEpisode(es)

	results, err := es.SearchLocal("xyznonexistentquery", "", "", "", nil, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range results {
		if r.LocalScore > 0.2 {
			t.Errorf("expected low scores for no-match query, got score=%f for %s", r.LocalScore, r.Problem)
		}
	}
}

func TestSearchLocalTopK(t *testing.T) {
	es := testStore(t)

	for i := 0; i < 5; i++ {
		_, _ = es.CreateEpisode(&models.Episode{
			ID:            es.NextID(),
			Domain:        "coding",
			Outcome:       "success",
			Tags:          []string{"test"},
			Problem:       "Test episode search functionality",
			ThinkingTrace: "Search trace content",
		})
	}

	results, err := es.SearchLocal("search functionality", "", "", "", nil, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(results))
	}
}
