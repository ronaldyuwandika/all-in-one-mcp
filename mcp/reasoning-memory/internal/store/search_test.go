package store

import (
	"context"
	"strings"
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

func TestSearchLocalVectorFilterAndTieBreak(t *testing.T) {
	es := testStore(t)
	vs, err := NewVectorStore(t.TempDir(), "mock", "", "", "", true)
	if err != nil {
		t.Fatalf("vector store: %v", err)
	}
	defer vs.Close()
	es.vec = vs

	ep1 := &models.Episode{
		ID:            "ep1",
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"test"},
		Repo:          "repoA",
		Problem:       "unique query term alpha",
		ThinkingTrace: "trace alpha",
		CreatedAt:     time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
	}
	ep2 := &models.Episode{
		ID:            "ep2",
		Domain:        "agentic",
		Outcome:       "success",
		Tags:          []string{"test"},
		Repo:          "repoA",
		Problem:       "unique query term alpha",
		ThinkingTrace: "trace alpha",
		CreatedAt:     time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	if _, err := es.CreateEpisode(ep1); err != nil {
		t.Fatal(err)
	}
	if _, err := es.CreateEpisode(ep2); err != nil {
		t.Fatal(err)
	}
	if err := vs.AddEpisode(context.Background(), ep1.ID, ep1.Problem, ep1.ThinkingTrace); err != nil {
		t.Fatal(err)
	}
	if err := vs.AddEpisode(context.Background(), ep2.ID, ep2.Problem, ep2.ThinkingTrace); err != nil {
		t.Fatal(err)
	}

	results, err := es.SearchLocal("alpha", "coding", "", "", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "ep1" {
		t.Fatalf("expected only ep1 to pass domainFilter, got %+v", results)
	}

	// Tie-breaking: two episodes with equal score sort by CreatedAt DESC then ID ASC
	ep3 := &models.Episode{
		ID:            "ep3",
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"test"},
		Repo:          "repoA",
		Problem:       "unique query term beta",
		ThinkingTrace: "trace beta",
		CreatedAt:     time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
	}
	ep4 := &models.Episode{
		ID:            "ep4",
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"test"},
		Repo:          "repoA",
		Problem:       "unique query term beta",
		ThinkingTrace: "trace beta",
		CreatedAt:     time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	if _, err := es.CreateEpisode(ep3); err != nil {
		t.Fatal(err)
	}
	if _, err := es.CreateEpisode(ep4); err != nil {
		t.Fatal(err)
	}

	resTie, err := es.SearchLocal("beta", "coding", "", "", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resTie) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resTie))
	}
	if resTie[0].ID != "ep4" || resTie[1].ID != "ep3" {
		t.Fatalf("expected tie-breaker order [ep4, ep3], got [%s, %s]", resTie[0].ID, resTie[1].ID)
	}
}

func TestSearchLocalRepoFilterCaseInsensitive(t *testing.T) {
	es := testStore(t)
	ep := &models.Episode{
		ID:            "ep_repo_case",
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"test"},
		Repo:          "MyOrg/MyRepo",
		Problem:       "unique repo case matching test problem",
		ThinkingTrace: "trace repo case",
	}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}

	for _, filter := range []string{"myorg/myrepo", "MYORG/MYREPO", "MyOrg/MyRepo"} {
		results, err := es.SearchLocal("unique repo case matching", "", "", filter, nil, 5)
		if err != nil {
			t.Fatalf("search repo filter %q: %v", filter, err)
		}
		if len(results) != 1 || results[0].ID != ep.ID {
			t.Fatalf("expected exact match for filter %q, got %+v", filter, results)
		}
	}

	results, err := es.SearchLocal("unique repo case matching", "", "", "myrepo", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected partial repo filter 'myrepo' not to match exact repo 'MyOrg/MyRepo', got %d results", len(results))
	}
}

func TestSearchVectorOnlyFailureMatchesPopulated(t *testing.T) {
	es := testStore(t)
	vs, err := NewVectorStore(t.TempDir(), "mock", "", "", "", true)
	if err != nil {
		t.Fatalf("vector store: %v", err)
	}
	defer vs.Close()
	es.vec = vs

	ep := &models.Episode{
		ID:            "ep_vec_fail",
		Domain:        "coding",
		Outcome:       "failure",
		Problem:       "unrelated problem description that will not match FTS text search query",
		ThinkingTrace: "some thinking trace that also has no matching words",
		FailedApproaches: []models.FailedApproach{
			{
				Approach:    "vector approach match",
				FailureMode: "mode A",
				RootCause:   "cause A",
				Lesson:      "lesson A",
			},
		},
	}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	if err := vs.AddEpisode(context.Background(), ep.ID, ep.Problem, ep.ThinkingTrace); err != nil {
		t.Fatal(err)
	}

	results, err := es.SearchLocal("vector", "", "", "", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search result for vector candidate")
	}
	var target *models.EpisodeSummary
	for i := range results {
		if results[i].ID == ep.ID {
			target = &results[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("expected episode %s in search results, got %+v", ep.ID, results)
	}
	if len(target.FailureMatches) == 0 {
		t.Fatalf("expected FailureMatches populated on vector candidate %s", ep.ID)
	}
	if target.FailureMatches[0].Approach != "vector approach match" {
		t.Errorf("unexpected FailureMatch approach: got %q", target.FailureMatches[0].Approach)
	}
}

func TestFTSFallbackFailureApproachMatching(t *testing.T) {
	es := testStore(t)
	ep := &models.Episode{
		ID:            "ep_fallback_fail",
		Domain:        "coding",
		Outcome:       "failure",
		Problem:       "unrelated problem statement",
		ThinkingTrace: "unrelated thinking trace",
		FailedApproaches: []models.FailedApproach{
			{
				Approach:    "fallback unique failure approach term",
				FailureMode: "mode B",
				RootCause:   "cause B",
				Lesson:      "lesson B",
			},
		},
	}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}

	// Drop FTS tables to force fallbackSearch
	if _, err := es.db.Exec("DROP TABLE IF EXISTS episodes_fts"); err != nil {
		t.Fatal(err)
	}
	if _, err := es.db.Exec("DROP TABLE IF EXISTS failed_approaches_fts"); err != nil {
		t.Fatal(err)
	}

	results, err := es.SearchLocal("fallback", "", "", "", nil, 5)
	if err != nil {
		t.Fatalf("SearchLocal with FTS disabled: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search result from fallback failure-approach matching")
	}
	found := false
	for _, r := range results {
		if r.ID == ep.ID {
			found = true
			if len(r.FailureMatches) == 0 {
				t.Fatalf("expected FailureMatches populated in fallback result for %s", ep.ID)
			}
			if !strings.Contains(r.FailureMatches[0].Approach, "fallback") {
				t.Errorf("unexpected FailureMatch approach in fallback: got %q", r.FailureMatches[0].Approach)
			}
		}
	}
	if !found {
		t.Fatalf("expected episode %s in fallback results", ep.ID)
	}
}
