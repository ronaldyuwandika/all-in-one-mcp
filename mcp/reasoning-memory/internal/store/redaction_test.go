package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func TestEpisodeRedactedBeforePersistenceAndEmbedding(t *testing.T) {
	dir := t.TempDir()
	vec, err := NewVectorStore(dir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dir+"/store.db", vec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = es.Close() })

	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	ep := &models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Problem:       "fix auth with " + secret,
		ThinkingTrace: "TOKEN=abcdefghijklmnop",
		Steps:         []models.Step{{ID: "s1", Content: "used " + secret}},
		ToolCalls: []models.ToolCall{{
			Tool: "shell", Args: map[string]any{"token": secret},
			ResultExcerpt: "Authorization: Bearer abcdefghijklmnop",
		}},
		Labels: map[string][]string{"note": {"password=abcdefghijklmnop"}},
	}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}

	var problem, trace, steps, calls, labels string
	if err := es.db.QueryRow(
		"SELECT problem, thinking_trace, steps, tool_calls, labels FROM episodes WHERE id = ?", ep.ID,
	).Scan(&problem, &trace, &steps, &calls, &labels); err != nil {
		t.Fatal(err)
	}
	stored := strings.Join([]string{problem, trace, steps, calls, labels}, "\n")
	if strings.Contains(stored, secret) || strings.Contains(stored, "abcdefghijklmnop") {
		t.Fatalf("raw secret persisted: %s", stored)
	}

	var ftsProblem, ftsTrace string
	if err := es.db.QueryRow(
		"SELECT problem, thinking_trace FROM episodes_fts WHERE id = ?", ep.ID,
	).Scan(&ftsProblem, &ftsTrace); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ftsProblem+ftsTrace, secret) {
		t.Fatal("raw secret entered FTS5")
	}

	results, err := vec.Search(context.Background(), "fix auth", 1)
	if err != nil || len(results) != 1 {
		t.Fatalf("vector search: %v, %#v", err, results)
	}
	if strings.Contains(results[0].Content, secret) {
		t.Fatal("raw secret entered vector content")
	}

	got, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), secret) {
		t.Fatal("retrieval leaked secret")
	}
}

func TestLegacyRecordRedactedOnRetrieval(t *testing.T) {
	es := testStore(t)
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	id := es.NextID()
	_, err := es.db.Exec(
		`INSERT INTO episodes (id, created_at, domain, outcome, tier, tags, repo, labels, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds)
		 VALUES (?, '2026-01-01T00:00:00Z', 'coding', 'success', 'episodic', '[]', '', '{}', ?, ?, '[]', '[]', '', 0)`,
		id, "legacy "+secret, "trace "+secret,
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := es.GetEpisode(id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Problem+got.ThinkingTrace, secret) {
		t.Fatal("legacy secret leaked on retrieval")
	}
}
