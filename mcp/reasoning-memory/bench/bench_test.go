package bench

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
	_ "modernc.org/sqlite"
)

var (
	eps1k       []models.Episode
	eps10k      []models.Episode
	store10kFTS *store.EpisodeStore
	store10kVec *store.EpisodeStore
	store1kFTS  *store.EpisodeStore
	initOnce    sync.Once
	tempDirs    []string
)

func initBenchStores(tb testing.TB) {
	initOnce.Do(func() {
		// Ensure testdata is generated
		err := EnsureTestData(".")
		if err != nil {
			tb.Fatalf("failed to generate test data: %v", err)
		}

		// Read testdata files
		eps1k = loadEpisodes(tb, "testdata/episodes_1k.json")
		eps10k = loadEpisodes(tb, "testdata/episodes_10k.json")

		// Create temp directories
		d1 := tb.TempDir()
		d2 := tb.TempDir()
		d3 := tb.TempDir()
		tempDirs = append(tempDirs, d1, d2, d3)

		// 1. 10k FTS-only store
		dbPath1 := filepath.Join(d1, "fts10k.db")
		store10kFTS = seedStore(tb, dbPath1, eps10k, nil)

		// 2. 10k Vector-enabled store
		dbPath2 := filepath.Join(d2, "vec10k.db")
		vStore, err := store.NewVectorStore(d2, "mock", "", "", "", true)
		if err != nil {
			tb.Fatalf("failed to create vector store: %v", err)
		}
		store10kVec = seedStore(tb, dbPath2, eps10k, vStore)

		// 3. 1k FTS-only store (for consolidation benchmark base)
		dbPath3 := filepath.Join(d3, "fts1k.db")
		store1kFTS = seedStore(tb, dbPath3, eps1k, nil)
	})
}

func loadEpisodes(tb testing.TB, path string) []models.Episode {
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("failed to read %s: %v", path, err)
	}
	var eps []models.Episode
	if err := json.Unmarshal(data, &eps); err != nil {
		tb.Fatalf("failed to unmarshal %s: %v", path, err)
	}
	return eps
}

func seedStore(tb testing.TB, dbPath string, eps []models.Episode, vs *store.VectorStore) *store.EpisodeStore {
	var es *store.EpisodeStore
	var err error
	if vs != nil {
		es, err = store.NewWithVector(dbPath, vs)
	} else {
		es, err = store.New(dbPath)
	}
	if err != nil {
		tb.Fatalf("failed to create store at %s: %v", dbPath, err)
	}

	// Open raw DB connection to perform quick bulk inserts in a transaction
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		tb.Fatalf("failed to open raw db: %v", err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		tb.Fatalf("tx begin failed: %v", err)
	}

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO episodes (id, created_at, domain, outcome, tags, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tb.Fatalf("prepare statement failed: %v", err)
	}
	defer stmt.Close()

	var vecContent []store.EpisodeContent

	for _, ep := range eps {
		tagsJSON, _ := json.Marshal(ep.Tags)
		stepsJSON, _ := json.Marshal(ep.Steps)
		toolCallsJSON, _ := json.Marshal(ep.ToolCalls)

		_, err = stmt.Exec(
			ep.ID,
			ep.CreatedAt.Format(time.RFC3339),
			ep.Domain,
			ep.Outcome,
			string(tagsJSON),
			ep.Problem,
			ep.ThinkingTrace,
			string(stepsJSON),
			string(toolCallsJSON),
			ep.ModelID,
			ep.DurationSeconds,
		)
		if err != nil {
			tb.Fatalf("tx execute failed: %v", err)
		}

		if vs != nil {
			vecContent = append(vecContent, store.EpisodeContent{
				ID:      ep.ID,
				Content: ep.Problem + "\n" + ep.ThinkingTrace,
			})
		}
	}

	if err := tx.Commit(); err != nil {
		tb.Fatalf("tx commit failed: %v", err)
	}

	if vs != nil {
		err = vs.AddEpisodes(context.Background(), vecContent)
		if err != nil {
			tb.Fatalf("failed to bulk add episodes to vector store: %v", err)
		}
	}

	return es
}

func BenchmarkFTS5Search(b *testing.B) {
	initBenchStores(b)

	queries := []string{
		"Fix nil pointer dereference",
		"Orchestrate microservice cluster",
		"pprof heap profiling",
		"Optimize database query performance",
		"nonexistentsearchterm",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := queries[i%len(queries)]
		_, err := store10kFTS.SearchLocal(q, "", "", nil, 10)
		if err != nil {
			b.Fatalf("FTS5 search failed: %v", err)
		}
	}
}

func BenchmarkVectorSearch(b *testing.B) {
	initBenchStores(b)
	vs := store10kVec.VectorStore()
	if vs == nil {
		b.Fatal("vector store is nil")
	}

	queries := []string{
		"Fix nil pointer dereference",
		"Orchestrate microservice cluster",
		"pprof heap profiling",
		"Optimize database query performance",
		"nonexistentsearchterm",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := queries[i%len(queries)]
		_, err := vs.Search(context.Background(), q, 10)
		if err != nil {
			b.Fatalf("vector search failed: %v", err)
		}
	}
}

func BenchmarkInsertEpisode(b *testing.B) {
	initBenchStores(b)

	// Create a single benchmark database for inserts to measure concurrent throughput
	dir := b.TempDir()
	dbPath := filepath.Join(dir, "insert.db")
	es, err := store.New(dbPath)
	if err != nil {
		b.Fatalf("failed to create insert store: %v", err)
	}
	defer es.Close()

	var idSeq int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			seq := atomic.AddInt64(&idSeq, 1)
			ep := &models.Episode{
				ID:            fmt.Sprintf("re-bench-insert-%08d", seq),
				Problem:       "Sample problem statement for concurrent throughput benchmarking",
				ThinkingTrace: "1. Step one of benchmark thinking trace\n2. Step two of thinking trace",
				Domain:        "coding",
				Outcome:       "success",
				Tags:          []string{"bench", "insert"},
			}
			_, err := es.CreateEpisode(ep)
			if err != nil {
				b.Fatalf("insert failed: %v", err)
			}
		}
	})
}

func BenchmarkInsertWithVector(b *testing.B) {
	initBenchStores(b)

	dir := b.TempDir()
	dbPath := filepath.Join(dir, "insert_vec.db")
	vStore, err := store.NewVectorStore(dir, "mock", "", "", "", true)
	if err != nil {
		b.Fatalf("failed to create vector store: %v", err)
	}
	defer vStore.Close()

	es, err := store.NewWithVector(dbPath, vStore)
	if err != nil {
		b.Fatalf("failed to create insert store: %v", err)
	}
	defer es.Close()

	var idSeq int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			seq := atomic.AddInt64(&idSeq, 1)
			ep := &models.Episode{
				ID:            fmt.Sprintf("re-bench-insert-vec-%08d", seq),
				Problem:       "Sample problem statement for concurrent throughput benchmarking with vector store enabled",
				ThinkingTrace: "1. Step one of benchmark thinking trace\n2. Step two of thinking trace",
				Domain:        "coding",
				Outcome:       "success",
				Tags:          []string{"bench", "insert", "vector"},
			}
			_, err := es.CreateEpisodeContext(context.Background(), ep)
			if err != nil {
				b.Fatalf("insert with vector failed: %v", err)
			}
		}
	})
}

func BenchmarkConsolidateAuto(b *testing.B) {
	initBenchStores(b)

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Copy store1kFTS database to a fresh temporary database to avoid state pollution
		dir := b.TempDir()
		dbPath := filepath.Join(dir, "consolidate_temp.db")
		es := seedStore(b, dbPath, eps1k, nil)

		b.StartTimer()
		// Run consolidation (strategy="auto" logic)
		candidates, err := es.FindMergeCandidates(3)
		if err != nil {
			b.Fatalf("failed to find merge candidates: %v", err)
		}
		for _, c := range candidates {
			_, _ = es.MergeToPattern(c)
		}
		_, _ = es.PruneFailures(30)
		_ = es.Close()
	}
}

func BenchmarkMemory(b *testing.B) {
	initBenchStores(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		rss := getRSS()
		_ = ms.HeapAlloc
		_ = rss
	}
}

func getRSS() uint64 {
	// Simple lookup for macOS / Linux RSS
	pid := os.Getpid()
	// Call ps -o rss= -p <pid>
	// Since we are on Mac, this is highly compatible
	out, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err == nil {
		var size, resident, share, text, lib, data, dt uint64
		_, err = fmt.Sscanf(string(out), "%d %d %d %d %d %d %d", &size, &resident, &share, &text, &lib, &data, &dt)
		if err == nil {
			return resident * uint64(os.Getpagesize())
		}
	}

	// Fallback/alternative for macOS: ps
	// Command execution (since we want accurate measurements)
	// Usually, MemStats.Sys is a great fallback too
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys
}

func TestMeasurePercentiles(t *testing.T) {
	// Call dummy testing.B to init stores
	var dummyB testing.B
	initBenchStores(&dummyB)

	// 1. FTS5 Search percentiles
	ftsDurations := make([]time.Duration, 1000)
	queries := []string{
		"Fix nil pointer dereference",
		"Orchestrate microservice cluster",
		"pprof heap profiling",
		"Optimize database query performance",
		"nonexistentsearchterm",
	}
	for i := 0; i < 1000; i++ {
		q := queries[i%len(queries)]
		start := time.Now()
		_, _ = store10kFTS.SearchLocal(q, "", "", nil, 10)
		ftsDurations[i] = time.Since(start)
	}
	sort.Slice(ftsDurations, func(i, j int) bool { return ftsDurations[i] < ftsDurations[j] })
	ftsP50 := ftsDurations[500]
	ftsP99 := ftsDurations[990]

	// 2. Vector Search percentiles
	vs := store10kVec.VectorStore()
	vecDurations := make([]time.Duration, 1000)
	for i := 0; i < 1000; i++ {
		q := queries[i%len(queries)]
		start := time.Now()
		_, _ = vs.Search(context.Background(), q, 10)
		vecDurations[i] = time.Since(start)
	}
	sort.Slice(vecDurations, func(i, j int) bool { return vecDurations[i] < vecDurations[j] })
	vecP50 := vecDurations[500]
	vecP99 := vecDurations[990]

	// 3. Insert Throughput (sequential/parallel sample)
	dir := t.TempDir()
	es, _ := store.New(filepath.Join(dir, "fts_insert.db"))
	defer es.Close()

	insertStart := time.Now()
	for i := 0; i < 5000; i++ {
		ep := &models.Episode{
			ID:            fmt.Sprintf("re-insert-%d", i),
			Problem:       "Sample problem statement for concurrent throughput benchmarking",
			ThinkingTrace: "1. Step one of benchmark thinking trace\n2. Step two of thinking trace",
			Domain:        "coding",
			Outcome:       "success",
			Tags:          []string{"bench", "insert"},
		}
		_, _ = es.CreateEpisode(ep)
	}
	insertDur := time.Since(insertStart)
	insertOpsSec := float64(5000) / insertDur.Seconds()

	// 4. Insert with Vector Throughput
	dir2 := t.TempDir()
	vStore, _ := store.NewVectorStore(dir2, "mock", "", "", "", true)
	defer vStore.Close()
	esVec, _ := store.NewWithVector(filepath.Join(dir2, "vec_insert.db"), vStore)
	defer esVec.Close()

	insertVecStart := time.Now()
	for i := 0; i < 3000; i++ {
		ep := &models.Episode{
			ID:            fmt.Sprintf("re-insert-vec-%d", i),
			Problem:       "Sample problem statement for concurrent throughput benchmarking with vector store enabled",
			ThinkingTrace: "1. Step one of benchmark thinking trace\n2. Step two of thinking trace",
			Domain:        "coding",
			Outcome:       "success",
			Tags:          []string{"bench", "insert", "vector"},
		}
		_, _ = esVec.CreateEpisodeContext(context.Background(), ep)
	}
	insertVecDur := time.Since(insertVecStart)
	insertVecOpsSec := float64(3000) / insertVecDur.Seconds()

	// 5. Consolidate Auto Duration
	dir3 := t.TempDir()
	esCons := seedStore(&dummyB, filepath.Join(dir3, "consolidate_temp.db"), eps1k, nil)
	consStart := time.Now()
	candidates, _ := esCons.FindMergeCandidates(3)
	for _, c := range candidates {
		_, _ = esCons.MergeToPattern(c)
	}
	_, _ = esCons.PruneFailures(30)
	_ = esCons.Close()
	consDur := time.Since(consStart)

	// 6. Memory Stats
	runtime.GC()
	debug.FreeOSMemory()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	rssMB := float64(getRSS()) / 1024 / 1024
	heapAllocMB := float64(ms.HeapAlloc) / 1024 / 1024

	// Print metrics cleanly
	fmt.Printf("[METRIC] FTS5_p50_ms: %.3f\n", float64(ftsP50.Microseconds())/1000.0)
	fmt.Printf("[METRIC] FTS5_p99_ms: %.3f\n", float64(ftsP99.Microseconds())/1000.0)
	fmt.Printf("[METRIC] Vector_p50_ms: %.3f\n", float64(vecP50.Microseconds())/1000.0)
	fmt.Printf("[METRIC] Vector_p99_ms: %.3f\n", float64(vecP99.Microseconds())/1000.0)
	fmt.Printf("[METRIC] Insert_ops_sec: %.0f\n", insertOpsSec)
	fmt.Printf("[METRIC] Insert_Vec_ops_sec: %.0f\n", insertVecOpsSec)
	fmt.Printf("[METRIC] Consolidate_duration_s: %.3f\n", consDur.Seconds())
	fmt.Printf("[METRIC] Memory_RSS_MB: %.2f\n", rssMB)
	fmt.Printf("[METRIC] Memory_Heap_MB: %.2f\n", heapAllocMB)
}

func TestMain(m *testing.M) {
	// Ensure testdata directory and files exist
	err := EnsureTestData(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to ensure test data: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
