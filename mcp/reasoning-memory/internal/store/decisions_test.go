package store

import (
	"context"
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func TestDecisionRoundTripAndRepoScopedSearch(t *testing.T) {
	es := testStore(t)
	epID := createEpisode(es, "coding", "success", nil, "decision episode", "trace", 0)
	id, err := es.CreateDecision(context.Background(), &models.Decision{EpisodeID: epID, Repo: "repo-a", Title: "Choose SQLite", Selected: "SQLite", Rationale: "Simple and embedded", Tradeoffs: []string{"single writer"}, Assumptions: []string{"low write volume"}, Evidence: []string{"benchmark"}, Alternatives: []models.Alternative{{Name: "Postgres", RejectionReason: "Operational overhead"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := es.GetDecision(context.Background(), id)
	if err != nil || got == nil {
		t.Fatalf("get decision: %v", err)
	}
	if len(got.Alternatives) != 1 || got.Alternatives[0].RejectionReason == "" {
		t.Fatalf("alternatives not preserved: %+v", got.Alternatives)
	}
	results, err := es.SearchDecisions(context.Background(), "SQLite", "repo-a", 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("search: %v, %d results", err, len(results))
	}
	results, err = es.SearchDecisions(context.Background(), "SQLite", "repo-b", 10)
	if err != nil || len(results) != 0 {
		t.Fatalf("expected repository-scoped result, got %d", len(results))
	}
}
