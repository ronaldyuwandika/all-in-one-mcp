package store

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	chromem "github.com/philippgille/chromem-go"
)

type VectorStore struct {
	db         *chromem.DB
	collection *chromem.Collection
	enabled    bool
	provider   string
}

func NewVectorStore(dataDir string, provider, model, baseURL, apiKey string, enabled bool) (*VectorStore, error) {
	vs := &VectorStore{enabled: enabled, provider: provider}
	if !enabled {
		return vs, nil
	}

	path := filepath.Join(dataDir, "vector")
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, fmt.Errorf("create vector dir: %w", err)
	}

	var db *chromem.DB
	var err error
	if provider == "mock" {
		db = chromem.NewDB()
	} else {
		db, err = chromem.NewPersistentDB(path, false)
		if err != nil {
			return nil, fmt.Errorf("open vector db: %w", err)
		}
	}
	vs.db = db

	var ef chromem.EmbeddingFunc
	switch provider {
	case "openai":
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for openai embedding provider")
		}
		m := chromem.EmbeddingModelOpenAI(model)
		ef = chromem.NewEmbeddingFuncOpenAI(apiKey, m)
	case "openai-compat":
		if baseURL == "" {
			baseURL = "http://localhost:8080/v1"
		}
		ef = chromem.NewEmbeddingFuncOpenAICompat(baseURL, apiKey, model, nil)
	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		if model == "" {
			model = "nomic-embed-text"
		}
		ef = chromem.NewEmbeddingFuncOllama(model, baseURL)
	case "mock":
		ef = func(ctx context.Context, text string) ([]float32, error) {
			vec := make([]float32, 1536)
			// Simple word hashing for mock embedding similarity
			words := strings.Fields(strings.ToLower(text))
			for _, w := range words {
				// Simple hash of the word
				hash := 0
				for i := 0; i < len(w); i++ {
					hash = (hash * 31) + int(w[i])
				}
				idx := (hash%1536 + 1536) % 1536
				vec[idx] += 1.0
			}
			// Normalize vector
			var sumSq float32
			for _, v := range vec {
				sumSq += v * v
			}
			if sumSq > 0 {
				norm := float32(math.Sqrt(float64(sumSq)))
				for i := range vec {
					vec[i] /= norm
				}
			} else {
				vec[0] = 1.0
			}
			return vec, nil
		}
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %s (supported: openai, openai-compat, ollama)", provider)
	}

	col, err := db.GetOrCreateCollection("episodes", map[string]string{
		"description": "Reasoning-memory episode embeddings for semantic search",
	}, ef)
	if err != nil {
		return nil, fmt.Errorf("create collection: %w", err)
	}
	vs.collection = col

	return vs, nil
}

func (vs *VectorStore) AddEpisode(ctx context.Context, id, problem, thinkingTrace string) error {
	if !vs.enabled {
		return nil
	}

	content := problem + "\n" + thinkingTrace
	doc := chromem.Document{
		ID:      id,
		Content: content,
		Metadata: map[string]string{
			"source": "reasoning-memory",
		},
	}
	return vs.collection.AddDocument(ctx, doc)
}

func (vs *VectorStore) AddEpisodes(ctx context.Context, episodes []EpisodeContent) error {
	if !vs.enabled || len(episodes) == 0 {
		return nil
	}

	docs := make([]chromem.Document, len(episodes))
	for i, ep := range episodes {
		docs[i] = chromem.Document{
			ID:      ep.ID,
			Content: ep.Content,
			Metadata: map[string]string{
				"source": "reasoning-memory",
			},
		}
	}
	return vs.collection.AddDocuments(ctx, docs, runtime.NumCPU())
}

type EpisodeContent struct {
	ID      string
	Content string
}

func (vs *VectorStore) Search(ctx context.Context, query string, topK int) ([]VectorResult, error) {
	if !vs.enabled {
		return nil, nil
	}

	results, err := vs.collection.Query(ctx, query, topK, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	var out []VectorResult
	for _, r := range results {
		out = append(out, VectorResult{
			ID:         r.ID,
			Similarity: float64(r.Similarity),
			Content:    r.Content,
		})
	}
	return out, nil
}

type VectorResult struct {
	ID         string
	Similarity float64
	Content    string
}

func (vs *VectorStore) DeleteEpisode(ctx context.Context, id string) error {
	if !vs.enabled {
		return nil
	}
	return vs.collection.Delete(ctx, nil, nil, id)
}

func (vs *VectorStore) Count() int {
	if !vs.enabled {
		return 0
	}
	return vs.collection.Count()
}

func (vs *VectorStore) Enabled() bool {
	return vs.enabled
}

func (vs *VectorStore) Provider() string {
	return vs.provider
}

func (vs *VectorStore) Close() error {
	if vs.db != nil {
		return vs.db.Reset()
	}
	return nil
}
