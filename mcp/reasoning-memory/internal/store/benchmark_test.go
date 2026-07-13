package store

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

var benchCreateSeq atomic.Int64

func benchmarkStore(b *testing.B) *EpisodeStore {
	b.Helper()
	dir := b.TempDir()
	es, err := New(dir + "/bench.db")
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	b.Cleanup(func() { _ = es.Close() })
	return es
}

func seedBenchmarkEpisodes(b *testing.B, es *EpisodeStore, n int) {
	b.Helper()
	for i := 0; i < n; i++ {
		_, _ = es.CreateEpisode(&models.Episode{
			ID:              es.NextID(),
			Domain:          "coding",
			Outcome:         "success",
			Tags:            []string{"benchmark", "test"},
			Problem:         "Benchmark episode number " + itoa(i) + " for performance testing",
			ThinkingTrace:   thinkingTrace(i),
			Steps:           []models.Step{{ID: "s1", Type: "analysis", Content: "Step content " + itoa(i)}},
			ToolCalls:       []models.ToolCall{{Tool: "ctx_read", Outcome: "success", ResultExcerpt: "func main() {"}},
			ModelID:         "bench-model",
			DurationSeconds: 100,
		})
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func thinkingTrace(i int) string {
	n := i % 10
	switch n {
	case 0:
		return "1. Analyze requirements\n2. Design solution architecture\n3. Implement core logic\n4. Write unit tests\n5. Verify edge cases"
	case 1:
		return "1. Reproduce the bug\n2. Identify root cause in parser\n3. Write regression test\n4. Apply fix\n5. Run full test suite"
	case 2:
		return "1. Research available Go libraries for vector search\n2. Compare chromem-go vs weaviate vs qdrant\n3. Select chromem-go for simplicity\n4. Implement wrapper with Ollama provider\n5. Integrate with existing FTS5 store\n6. Write integration tests"
	case 3:
		return "1. Profile the slow endpoint with pprof\n2. Identify allocation hotspot in JSON marshal\n3. Pre-allocate buffer with strings.Builder\n4. Reduce allocs by reusing encoder\n5. Verify latency drops below threshold"
	case 4:
		return "1. Review current CI pipeline\n2. Identify missing linting stage\n3. Add golangci-lint step\n4. Configure Dependabot for Go modules\n5. Add security scan with govulncheck\n6. Verify pipeline passes cleanly"
	case 5:
		return "1. Audit current API design\n2. Identify single-user assumption\n3. Design multi-tenant isolation model\n4. Add tenant context to request chain\n5. Migrate storage to tenant-scoped keys\n6. Update all handlers\n7. Run regression tests"
	case 6:
		return "1. Gather requirements from stakeholders\n2. Write PRD with success metrics\n3. Design high-level architecture\n4. Create task breakdown\n5. Assign sprint items\n6. Schedule review meetings"
	case 7:
		return "1. Expose Prometheus metrics endpoint\n2. Add RED metrics for all handlers\n3. Configure Grafana dashboard\n4. Set up alert rules for p99 latency\n5. Create runbook for common alerts\n6. Verify metrics in staging"
	case 8:
		return "1. Understand the reactive streams pattern\n2. Design event pipeline with typed channels\n3. Implement cold observable wrapper\n4. Add error propagation strategy\n5. Write marble-diagram tests\n6. Benchmark concurrent throughput"
	case 9:
		return "1. Scan dependencies with govulncheck\n2. Pin vulnerable transitive deps\n3. Update go.mod with safe versions\n4. Run full test suite\n5. Verify no regressions\n6. Review sbom for remaining risks"
	default:
		return "1. Step one\n2. Step two\n3. Step three"
	}
}

func BenchmarkCreateEpisode(b *testing.B) {
	es := benchmarkStore(b)

	for _, traceSize := range []struct {
		name string
		ep   models.Episode
	}{
		{
			name: "small_trace_100b",
			ep: models.Episode{
				Domain:          "coding",
				Outcome:         "success",
				Tags:            []string{"go", "test"},
				Problem:         "Fix nil pointer dereference in parser",
				ThinkingTrace:   "1. Reproduce\n2. Fix\n3. Test",
				Steps:           []models.Step{{ID: "s1", Type: "implementation", Content: "Fix the bug"}},
				ToolCalls:       []models.ToolCall{{Tool: "ctx_edit", Outcome: "success"}},
				ModelID:         "test-model",
				DurationSeconds: 30,
			},
		},
		{
			name: "medium_trace_1kb",
			ep: models.Episode{
				Domain:          "coding",
				Outcome:         "success",
				Tags:            []string{"go", "vector", "fts5", "hybrid-search"},
				Problem:         "Implement hybrid search combining FTS5 and vector similarity for reasoning episodes",
				ThinkingTrace:   strings.Repeat("1. Analyze current FTS5-only search\n2. Research chromem-go API for vector embedding\n3. Implement VectorStore with Ollama provider\n4. Merge FTS5 results with cosine similarity scores\n5. Add configurable hybrid weighting\n6. Write tests for all search paths\n7. Verify end-to-end\n", 5),
				Steps:           []models.Step{{ID: "s1", Type: "implementation", Content: "Implement"}},
				ToolCalls:       []models.ToolCall{{Tool: "ctx_read", Outcome: "success"}, {Tool: "ctx_edit", Outcome: "success"}, {Tool: "ctx_shell", Outcome: "success"}},
				ModelID:         "deepseek-v4-pro",
				DurationSeconds: 180,
			},
		},
		{
			name: "large_trace_10kb",
			ep: models.Episode{
				Domain:          "agentic",
				Outcome:         "partial",
				Tags:            []string{"agent", "orchestration", "multi-step", "resilience", "retry", "timeout", "circuit-breaker"},
				Problem:         "Orchestrate multi-step data pipeline with failure recovery and circuit breaker patterns across distributed services",
				ThinkingTrace:   strings.Repeat("1. Design the orchestration topology\n2. Implement circuit breaker with configurable thresholds\n3. Add retry logic with exponential backoff\n4. Wire up health check probes\n5. Add graceful degradation for partial failures\n6. Test failure scenarios end-to-end\n7. Document recovery procedures\n", 50),
				Steps:           []models.Step{{ID: "s1", Type: "analysis", Content: "Design"}},
				ToolCalls:       []models.ToolCall{{Tool: "ctx_read", Outcome: "success"}, {Tool: "ctx_edit", Outcome: "success"}, {Tool: "ctx_shell", Outcome: "success"}, {Tool: "ctx_tree", Outcome: "success"}},
				ModelID:         "claude-sonnet-4",
				DurationSeconds: 600,
			},
		},
	} {
		b.Run(traceSize.name, func(b *testing.B) {
			for b.Loop() {
				ep := traceSize.ep
				id := benchCreateSeq.Add(1)
				ep.ID = "bench-" + itoa(int(id))
				_, err := es.CreateEpisode(&ep)
				if err != nil {
					b.Fatalf("create: %v", err)
				}
			}
		})
	}
}

func BenchmarkGetEpisode(b *testing.B) {
	es := benchmarkStore(b)
	ids := make([]string, 100)
	for i := 0; i < 100; i++ {
		id, err := es.CreateEpisode(&models.Episode{
			ID:            es.NextID(),
			Domain:        "coding",
			Outcome:       "success",
			Problem:       "Benchmark get " + itoa(i),
			ThinkingTrace: "1. Analyze\n2. Implement\n3. Verify",
		})
		if err != nil {
			b.Fatalf("seed: %v", err)
		}
		ids[i] = id
	}

	b.Run("hit_first", func(b *testing.B) {
		for b.Loop() {
			_, err := es.GetEpisode(ids[0])
			if err != nil {
				b.Fatalf("get: %v", err)
			}
		}
	})

	b.Run("miss", func(b *testing.B) {
		for b.Loop() {
			_, err := es.GetEpisode("re-99999999-999")
			if err != nil {
				b.Fatalf("get: %v", err)
			}
		}
	})

	b.Run("rotate_100", func(b *testing.B) {
		var idx int
		for i := 0; i < b.N; i++ {
			_, err := es.GetEpisode(ids[idx%100])
			if err != nil {
				b.Fatalf("get: %v", err)
			}
			idx++
		}
	})
}

func BenchmarkGetSummary(b *testing.B) {
	es := benchmarkStore(b)
	id, err := es.CreateEpisode(&models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"go", "benchmark", "summary", "test", "fts5"},
		Problem:       "Benchmark getting episode summary from the SQLite store layer",
		ThinkingTrace: strings.Repeat("1. Understand store interface\n2. Implement summary query\n3. Test with edge cases\n", 3),
		Steps:         []models.Step{{ID: "s1", Type: "analysis", Content: "Analyze"}, {ID: "s2", Type: "implementation", Content: "Implement"}, {ID: "s3", Type: "verification", Content: "Test"}},
		ToolCalls:     []models.ToolCall{{Tool: "ctx_read", Outcome: "success"}, {Tool: "ctx_edit", Outcome: "success"}, {Tool: "ctx_shell", Outcome: "success"}},
		ModelID:       "test-model",
	})
	if err != nil {
		b.Fatalf("seed: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		_, err := es.GetSummary(id)
		if err != nil {
			b.Fatalf("get summary: %v", err)
		}
	}
}

func BenchmarkListEpisodes(b *testing.B) {
	es := benchmarkStore(b)
	seedBenchmarkEpisodes(b, es, 50)

	for _, limit := range []int{10, 50} {
		b.Run("limit_"+itoa(limit), func(b *testing.B) {
			for b.Loop() {
				_, err := es.ListEpisodes(limit, 0)
				if err != nil {
					b.Fatalf("list: %v", err)
				}
			}
		})
	}
}

func BenchmarkSearchLocal(b *testing.B) {
	es := benchmarkStore(b)
	seedBenchmarkEpisodes(b, es, 100)

	for _, query := range []struct {
		name string
		q    string
	}{
		{"exact_match", "Benchmark episode number 42"},
		{"partial_match", "hybrid search vector"},
		{"broad", "implement test design analyze"},
		{"no_match", "xyznonexistentqueryhyperion"},
	} {
		b.Run(query.name, func(b *testing.B) {
			for b.Loop() {
				results, err := es.SearchLocal(query.q, "", "", nil, 5)
				if err != nil {
					b.Fatalf("search: %v", err)
				}
				_ = results
			}
		})
	}
}

func BenchmarkSearchLocalWithFilters(b *testing.B) {
	es := benchmarkStore(b)
	seedBenchmarkEpisodes(b, es, 100)

	b.Run("domain_filter", func(b *testing.B) {
		for b.Loop() {
			results, err := es.SearchLocal("benchmark", "coding", "", nil, 5)
			if err != nil {
				b.Fatalf("search: %v", err)
			}
			_ = results
		}
	})

	b.Run("outcome_filter", func(b *testing.B) {
		for b.Loop() {
			results, err := es.SearchLocal("benchmark", "", "success", nil, 5)
			if err != nil {
				b.Fatalf("search: %v", err)
			}
			_ = results
		}
	})

	b.Run("all_filters", func(b *testing.B) {
		for b.Loop() {
			results, err := es.SearchLocal("benchmark", "coding", "success", []string{"benchmark"}, 5)
			if err != nil {
				b.Fatalf("search: %v", err)
			}
			_ = results
		}
	})
}

func BenchmarkSearchLocalTopK(b *testing.B) {
	es := benchmarkStore(b)
	seedBenchmarkEpisodes(b, es, 100)

	for _, topK := range []int{1, 5, 10, 20} {
		b.Run("topk_"+itoa(topK), func(b *testing.B) {
			for b.Loop() {
				results, err := es.SearchLocal("benchmark", "", "", nil, topK)
				if err != nil {
					b.Fatalf("search: %v", err)
				}
				_ = results
			}
		})
	}
}

func BenchmarkDeleteEpisode(b *testing.B) {
	es := benchmarkStore(b)

	for b.Loop() {
		id, err := es.CreateEpisode(&models.Episode{
			ID:            es.NextID(),
			Domain:        "coding",
			Outcome:       "success",
			Problem:       "Benchmark delete",
			ThinkingTrace: "1. Delete me",
		})
		if err != nil {
			b.Fatalf("create: %v", err)
		}
		if err := es.DeleteEpisode(id); err != nil {
			b.Fatalf("delete: %v", err)
		}
	}
}

func BenchmarkNextID(b *testing.B) {
	es := benchmarkStore(b)

	// Seed some episodes so NextID has to scan
	seedBenchmarkEpisodes(b, es, 50)

	b.ResetTimer()
	for b.Loop() {
		_ = es.NextID()
	}
}

func BenchmarkFindMergeCandidates(b *testing.B) {
	es := benchmarkStore(b)
	seedBenchmarkEpisodes(b, es, 20)

	for b.Loop() {
		candidates, err := es.FindMergeCandidates(3)
		if err != nil {
			b.Fatalf("find: %v", err)
		}
		_ = candidates
	}
}
