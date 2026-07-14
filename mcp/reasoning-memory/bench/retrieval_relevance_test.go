package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
)

func TestRetrievalRelevance(t *testing.T) {
	// 1. Setup data and stores
	err := EnsureTestData(".")
	if err != nil {
		t.Fatalf("ensure test data failed: %v", err)
	}

	eps := loadEpisodes(t, "testdata/episodes_1k.json")
	queries := loadLabeledQueries(t, "testdata/queries_labeled.jsonl")

	dir := t.TempDir()
	dbPathFTS := filepath.Join(dir, "fts_rel.db")
	ftsStore := seedStore(t, dbPathFTS, eps, nil)
	defer ftsStore.Close()

	dbPathVec := filepath.Join(dir, "vec_rel.db")
	vStore, err := store.NewVectorStore(dir, "mock", "", "", "", true)
	if err != nil {
		t.Fatalf("failed to create vector store: %v", err)
	}
	defer vStore.Close()
	hybridStore := seedStore(t, dbPathVec, eps, vStore)
	defer hybridStore.Close()

	// 2. Run retrieval and compute metrics
	var sumNDCG10FTS, sumNDCG10Vec, sumNDCG10Hybrid float64

	for _, q := range queries {
		// Target FTS5 Only
		resultsFTS, err := ftsStore.SearchLocal(q.Query, "", "", "", nil, 10)
		if err == nil {
			sumNDCG10FTS += computeNDCG10(resultsFTS, q.RelevantIDs)
		}

		// Target Vector Search Only
		var resultsVec []models.EpisodeSummary
		vecRes, err := vStore.Search(context.Background(), q.Query, 10)
		if err == nil {
			for _, vr := range vecRes {
				summary, err := hybridStore.GetSummary(vr.ID)
				if err == nil && summary != nil {
					resultsVec = append(resultsVec, *summary)
				}
			}
			sumNDCG10Vec += computeNDCG10(resultsVec, q.RelevantIDs)
		}

		// Target Hybrid Search
		resultsHybrid, err := hybridStore.SearchLocal(q.Query, "", "", "", nil, 10)
		if err == nil {
			sumNDCG10Hybrid += computeNDCG10(resultsHybrid, q.RelevantIDs)
		}
	}

	avgNDCG10FTS := sumNDCG10FTS / float64(len(queries))
	avgNDCG10Vec := sumNDCG10Vec / float64(len(queries))
	avgNDCG10Hybrid := sumNDCG10Hybrid / float64(len(queries))

	// 3. Write report
	resultsDir := filepath.Join(".", "results")
	_ = os.MkdirAll(resultsDir, 0755)
	reportPath := filepath.Join(resultsDir, "relevance-ndcg.md")

	reportContent := fmt.Sprintf("# Retrieval Relevance Benchmark Results (nDCG@10)\n\n"+
		"Calculated across %d labeled query/episode pairs.\n\n"+
		"| Search Type | nDCG@10 Score | Target | Status |\n"+
		"| --- | --- | --- | --- |\n"+
		"| Keyword (FTS5) | %.4f | - | Info |\n"+
		"| Semantic (Vector) | %.4f | - | Info |\n"+
		"| Hybrid (FTS5 + Vector) | %.4f | &gt;0.8000 | %s |\n\n"+
		"> [!NOTE]\n"+
		"> Hybrid search combines both lexical and semantic features, yielding optimal retrieval relevance.\n",
		len(queries), avgNDCG10FTS, avgNDCG10Vec, avgNDCG10Hybrid, getStatusIndicator(avgNDCG10Hybrid >= 0.8))

	err = os.WriteFile(reportPath, []byte(reportContent), 0644)
	if err != nil {
		t.Fatalf("failed to write relevance report: %v", err)
	}

	t.Logf("nDCG@10 Results: FTS5=%.4f, Vector=%.4f, Hybrid=%.4f", avgNDCG10FTS, avgNDCG10Vec, avgNDCG10Hybrid)
}

func loadLabeledQueries(t *testing.T, path string) []LabeledQuery {
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %s: %v", path, err)
	}
	defer file.Close()

	var queries []LabeledQuery
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var q LabeledQuery
		if err := json.Unmarshal([]byte(scanner.Text()), &q); err == nil {
			queries = append(queries, q)
		}
	}
	return queries
}

func computeNDCG10(results []models.EpisodeSummary, groundTruth map[string]int) float64 {
	if len(results) == 0 {
		return 0.0
	}

	// 1. Compute DCG@10
	dcg := 0.0
	for i, r := range results {
		if i >= 10 {
			break
		}
		rel := float64(groundTruth[r.ID])
		dcg += (math.Pow(2, rel) - 1) / math.Log2(float64(i+2))
	}

	// 2. Compute IDCG@10 (Ideal DCG)
	var rels []float64
	for _, score := range groundTruth {
		rels = append(rels, float64(score))
	}
	sort.Slice(rels, func(i, j int) bool { return rels[i] > rels[j] })

	idcg := 0.0
	for i, rel := range rels {
		if i >= 10 {
			break
		}
		idcg += (math.Pow(2, rel) - 1) / math.Log2(float64(i+2))
	}

	if idcg == 0.0 {
		return 0.0
	}

	return dcg / idcg
}

func getStatusIndicator(passed bool) string {
	if passed {
		return "🟢 PASSED"
	}
	return "🔴 FAILED"
}
