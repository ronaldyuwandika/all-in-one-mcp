package store

import (
	"context"
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func TestVectorStoreDisabled(t *testing.T) {
	vs, err := NewVectorStore(t.TempDir(), "ollama", "nomic-embed-text", "http://localhost:11434", "", false)
	if err != nil {
		t.Fatalf("new vector store: %v", err)
	}
	if vs.Enabled() {
		t.Error("expected disabled vector store")
	}
	if vs.Count() != 0 {
		t.Errorf("expected 0 count, got %d", vs.Count())
	}
	if err := vs.AddEpisode(context.TODO(), "test", "problem", "trace"); err != nil {
		t.Errorf("expected no error on disabled add: %v", err)
	}
	results, err := vs.Search(context.TODO(), "query", 5)
	if err != nil {
		t.Errorf("expected no error on disabled search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestVectorStoreUnsupportedProvider(t *testing.T) {
	_, err := NewVectorStore(t.TempDir(), "unknown", "", "", "", true)
	if err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestEpisodeStoreWithoutVector(t *testing.T) {
	es := testStore(t)
	if es.vec != nil && es.vec.Enabled() {
		t.Skip("vector store is enabled")
	}

	ep := &models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"test"},
		Problem:       "Test vector-less creation",
		ThinkingTrace: "Trace content",
	}
	id, err := es.CreateEpisode(ep)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	results, err := es.SearchLocal("vector-less", "", "", "", nil, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected FTS results even without vector store")
	}
}
