package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/linkcontent"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func seedPattern(es *EpisodeStore) *models.Pattern {
	for i := 0; i < 3; i++ {
		_, _ = es.CreateEpisode(&models.Episode{
			ID:            es.NextID(),
			Domain:        "coding",
			Outcome:       "success",
			Tags:          []string{"go", "testing", "ci"},
			Problem:       "test",
			ThinkingTrace: "trace",
		})
	}
	candidates, _ := es.FindMergeCandidates(2)
	if len(candidates) > 0 {
		pid, _ := es.MergeToPattern(candidates[0])
		pat, _ := es.GetPattern(pid)
		return pat
	}
	return nil
}

func createEpisode(es *EpisodeStore, domain, outcome string, tags []string, prob, trace string, duration int) string {
	id := es.NextID()
	_, _ = es.CreateEpisode(&models.Episode{
		ID:              id,
		Domain:          domain,
		Outcome:         outcome,
		Tags:            tags,
		Problem:         prob,
		ThinkingTrace:   trace,
		DurationSeconds: duration,
	})
	return id
}

func TestRunMigrationPhaseRollsBackPhaseDataOnMarkerFailure(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_marker BEFORE INSERT ON store_metadata WHEN new.key = 'graph_migration_phase' BEGIN SELECT RAISE(FAIL, 'simulated marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	err = runMigrationPhase(db, "graph_migration_phase", "graph_migration_complete", migrateGraph)
	if err == nil {
		t.Fatal("expected phase execution to fail on marker trigger")
	}
	var hasGraph int
	_ = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='graph_edges'").Scan(&hasGraph)
	if hasGraph != 0 {
		t.Fatalf("expected graph_edges table creation to roll back on marker failure, got count=%d", hasGraph)
	}
}

func TestReconcileVectorStoreExhaustionReturnsPendingError(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	es.vec = vec
	if _, err := es.db.Exec(`INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at, claim_owner, claim_expires_at) VALUES ('unbounded-ep', 'p', 't', ?, 'other-worker', ?)`, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Add(time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	err = es.ReconcileVectorStore(context.Background())
	if !errors.Is(err, ErrVectorReconciliationPending) {
		t.Fatalf("expected ErrVectorReconciliationPending, got %v", err)
	}
}

func TestProducerUpdateAndDeletePreserveQueueGenerationAndPayload(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	vec.deleteEpisodeHook = func(ctx context.Context, id string) error { return errors.New("simulated deletion failure") }
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()

	ep := &models.Episode{ID: "preserve-ep-1", Problem: "orig prob", ThinkingTrace: "orig trace", Outcome: models.OutcomeVerifiedSuccess, Verification: []models.VerificationRecord{{Type: models.VerificationTests, Command: "cmd", Result: "res", Success: true}}}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	if err := enqueueVectorReconcileDB(context.Background(), es.db, ep.ID, "updated prob", "updated trace"); err != nil {
		t.Fatal(err)
	}

	var origGen int64
	var origProb string
	if err := es.db.QueryRow("SELECT queue_generation, problem FROM vector_reconcile WHERE episode_id = 'preserve-ep-1'").Scan(&origGen, &origProb); err != nil {
		t.Fatal(err)
	}

	tx, err := es.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := enqueueVectorMigrationTx(context.Background(), tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var newGen int64
	var newProb string
	var migVer int
	if err := es.db.QueryRow("SELECT queue_generation, problem, migration_version FROM vector_reconcile WHERE episode_id = 'preserve-ep-1'").Scan(&newGen, &newProb, &migVer); err != nil {
		t.Fatal(err)
	}
	if newGen != origGen {
		t.Fatalf("migration conflict mutated queue_generation: orig=%d new=%d", origGen, newGen)
	}
	if newProb != "updated prob" {
		t.Fatalf("migration conflict mutated update payload: got %q", newProb)
	}
	if migVer != currentVectorContentVersion {
		t.Fatalf("migration conflict failed to raise migration_version: got %d", migVer)
	}
}

func TestVectorVersionRaceConditionBlockedByConcurrentEnqueue(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()

	if _, err := es.CreateEpisode(&models.Episode{ID: "race-ep-1", Problem: "prob 1", ThinkingTrace: "trace 1", Outcome: models.OutcomeVerifiedSuccess, Verification: []models.VerificationRecord{{Type: models.VerificationTests, Command: "cmd", Result: "res", Success: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := es.db.Exec("DELETE FROM store_metadata WHERE key='vector_content_version'"); err != nil {
		t.Fatal(err)
	}
	if _, err := es.db.Exec(`INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at) VALUES ('race-ep-1', 'prob 1', 'trace 1', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	vec.deleteEpisodeHook = func(ctx context.Context, id string) error {
		if id == "race-ep-1" {
			tx, err := es.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at) VALUES ('race-ep-2', 'prob 2', 'trace 2', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
				_ = tx.Rollback()
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE store_metadata SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT) WHERE key='vector_queue_generation'`); err != nil {
				_ = tx.Rollback()
				return err
			}
			return tx.Commit()
		}
		return nil
	}

	err = es.ReconcileVectorStore(context.Background())
	if !errors.Is(err, ErrVectorReconciliationPending) {
		t.Fatalf("expected ErrVectorReconciliationPending when race refreshed migration target, got %v", err)
	}

	var version string
	_ = es.db.QueryRow("SELECT value FROM store_metadata WHERE key='vector_content_version'").Scan(&version)
	if version == "2" {
		t.Fatalf("expected vector_content_version unapplied due to pending item, got %q", version)
	}
	var pending int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM vector_reconcile").Scan(&pending); err != nil || pending == 0 {
		t.Fatalf("expected pending queue item after race enqueue, pending=%d err=%v", pending, err)
	}
}

func TestVectorContentVersionMixedGenerationDrainRequirement(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()

	if _, err := es.db.Exec(`INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at, claim_owner, claim_expires_at, migration_version) VALUES ('normal-update-ep', 'p', 't', ?, '', '', 0)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := es.db.Exec(`DELETE FROM store_metadata WHERE key='vector_content_version'`); err != nil {
		t.Fatal(err)
	}

	vec.deleteEpisodeHook = func(ctx context.Context, id string) error {
		if id == "normal-update-ep" {
			return errors.New("simulated normal update failure")
		}
		return nil
	}

	if err := es.ReconcileVectorStore(context.Background()); err == nil {
		t.Fatal("expected reconciliation failure")
	}

	var version string
	_ = es.db.QueryRow("SELECT value FROM store_metadata WHERE key='vector_content_version'").Scan(&version)
	if version == "2" {
		t.Fatal("vector_content_version must not advance while any generation row remains in vector_reconcile")
	}
}

func TestVectorQueueUpsertPreservesStateAndClaims(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE episodes (id TEXT PRIMARY KEY, created_at TEXT, updated_at TEXT, domain TEXT, outcome TEXT, tier TEXT, tags TEXT, repo TEXT, project TEXT, provenance TEXT, confidence REAL, labels TEXT, problem TEXT, objectives TEXT, decisions TEXT, alternatives TEXT, verification TEXT, lessons TEXT, thinking_trace TEXT, steps TEXT, tool_calls TEXT, model_id TEXT, duration_seconds INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO episodes (id, created_at, updated_at, domain, outcome, tier, tags, repo, project, provenance, labels, problem, objectives, decisions, alternatives, verification, lessons, thinking_trace, steps, tool_calls, model_id, duration_seconds) VALUES ('upsert-ep-1', '2026-03-31T10:00:00Z', '2026-03-31T10:00:00Z', 'coding', 'verified_success', 'episodic', '[]', '', '', '', '{}', 'p', '[]', '[]', '[]', '[]', '[]', 't', '[]', '[]', '', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE vector_reconcile (episode_id TEXT PRIMARY KEY, problem TEXT NOT NULL, thinking_trace TEXT NOT NULL, updated_at TEXT NOT NULL, claim_owner TEXT NOT NULL DEFAULT '', claim_expires_at TEXT NOT NULL DEFAULT '', migration_version INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at, claim_owner, claim_expires_at, migration_version) VALUES ('upsert-ep-1', 'custom_prob', 'custom_trace', '2026-03-31T10:00:00Z', 'owner1', '2026-03-31T11:00:00Z', 1)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	es, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()

	var prob, trace, owner, expires string
	var version int
	if err := es.db.QueryRow("SELECT problem, thinking_trace, claim_owner, claim_expires_at, migration_version FROM vector_reconcile WHERE episode_id = 'upsert-ep-1'").Scan(&prob, &trace, &owner, &expires, &version); err != nil {
		t.Fatal(err)
	}
	if prob != "custom_prob" || trace != "custom_trace" || owner != "owner1" || expires != "2026-03-31T11:00:00Z" || version != currentVectorContentVersion {
		t.Fatalf("upsert destroyed existing reconcile state: prob=%s trace=%s owner=%s expires=%s version=%d", prob, trace, owner, expires, version)
	}
}

func TestVectorContentVersionNoRequeueOnReopen(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	vec1, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	vec1.addEpisodeAfterHook = func(ctx context.Context, id, problem, trace string) error {
		counts[id]++
		return nil
	}
	es1, err := NewWithVector(dbPath, vec1)
	if err != nil {
		t.Fatal(err)
	}
	ep := &models.Episode{ID: "no-requeue-ep", Outcome: models.OutcomeVerifiedSuccess, Problem: "prob", ThinkingTrace: "trace", Verification: []models.VerificationRecord{{Type: models.VerificationTests, Command: "go test", Result: "res", Success: true}}}
	if _, err := es1.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	if err := es1.ReconcileVectorStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = es1.Close()

	vec2, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	vec2.addEpisodeAfterHook = func(ctx context.Context, id, problem, trace string) error {
		counts[id]++
		return nil
	}
	es2, err := NewWithVector(dbPath, vec2)
	if err != nil {
		t.Fatal(err)
	}
	defer es2.Close()
	if counts["no-requeue-ep"] != 1 {
		t.Fatalf("expected 1 embedding call, got %d", counts["no-requeue-ep"])
	}
}

func TestConcurrentVectorReconciliationLease(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	esSeed, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, _ = esSeed.CreateEpisode(&models.Episode{ID: fmt.Sprintf("concurrent-ep-%d", i), Problem: fmt.Sprintf("prob %d", i), ThinkingTrace: "trace"})
	}
	_ = esSeed.Close()

	db, _ := sql.Open("sqlite", dbPath)
	_, _ = db.Exec("DELETE FROM store_metadata WHERE key='vector_content_version'")
	_, _ = db.Exec("INSERT OR REPLACE INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at) SELECT id, problem, '', updated_at FROM episodes")
	_ = db.Close()

	vec1, _ := NewVectorStore(dataDir, "mock", "", "", "", true)
	vec2, _ := NewVectorStore(dataDir, "mock", "", "", "", true)
	es1, err := NewWithVector(dbPath, vec1)
	if err != nil {
		t.Fatal(err)
	}
	defer es1.Close()
	es2, err := NewWithVector(dbPath, vec2)
	if err != nil {
		t.Fatal(err)
	}
	defer es2.Close()

	c1 := make(chan error, 1)
	c2 := make(chan error, 1)
	go func() { c1 <- es1.ReconcileVectorStore(context.Background()) }()
	go func() { c2 <- es2.ReconcileVectorStore(context.Background()) }()
	if err := <-c1; err != nil {
		t.Fatal(err)
	}
	if err := <-c2; err != nil {
		t.Fatal(err)
	}
}

func TestLegacyVerificationMigrationDoubleReopenIdempotency(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE episodes (id TEXT PRIMARY KEY, created_at TEXT, updated_at TEXT, domain TEXT, outcome TEXT, tier TEXT, tags TEXT, repo TEXT, project TEXT, provenance TEXT, confidence REAL, labels TEXT, problem TEXT, objectives TEXT, decisions TEXT, alternatives TEXT, verification TEXT, lessons TEXT, thinking_trace TEXT, steps TEXT, tool_calls TEXT, model_id TEXT, duration_seconds INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO episodes (id, created_at, updated_at, domain, outcome, tier, tags, repo, project, provenance, labels, problem, objectives, decisions, alternatives, verification, lessons, thinking_trace, steps, tool_calls, model_id, duration_seconds) VALUES ('idemp-1', '2026-03-31T10:00:00Z', '2026-03-31T10:00:00Z', 'coding', 'verified_success', 'episodic', '[]', '', '', '', '{}', 'p', '[]', '[]', '[]', 'raw string payload', '[]', 't', '[]', '[]', '', 0)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	for i := 0; i < 2; i++ {
		es, err := New(dbPath)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		ep, err := es.GetEpisode("idemp-1")
		if err != nil || ep == nil {
			t.Fatalf("get %d: %v", i, err)
		}
		if ep.Outcome != models.OutcomeUnverifiedSuccess {
			t.Fatalf("expected outcome unverified_success, got %s", ep.Outcome)
		}
		if len(ep.Verification) != 2 {
			t.Fatalf("expected exactly 2 verification records on open %d, got %d", i, len(ep.Verification))
		}
		if !strings.HasPrefix(string(ep.Verification[0].Result), "Legacy verification payload converted:") {
			t.Fatalf("missing single marker on open %d: %s", i, ep.Verification[0].Result)
		}
		_ = es.Close()
	}
}

func TestLegacyVerificationMigrationAtomicRollbackOnFailure(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE episodes (id TEXT PRIMARY KEY, created_at TEXT, updated_at TEXT, domain TEXT, outcome TEXT, tier TEXT, tags TEXT, repo TEXT, labels TEXT, problem TEXT, thinking_trace TEXT, verification TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO episodes (id, created_at, updated_at, domain, outcome, tier, tags, repo, labels, problem, thinking_trace, verification) VALUES ('fail-atomic-1', '2026-03-31T10:00:00Z', '2026-03-31T10:00:00Z', 'coding', 'verified_success', 'episodic', '[]', '', '{}', 'p', 't', 'invalid json string')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE episode_verifications (id INTEGER PRIMARY KEY, episode_id TEXT, position INTEGER NOT NULL UNIQUE, type TEXT, command TEXT, result TEXT, success INTEGER, evidence TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_verif_insert BEFORE INSERT ON episode_verifications BEGIN SELECT RAISE(FAIL, 'simulated verification insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = migrateLegacyVerificationsTx(tx, "episodes", "episode_verifications")
	if err == nil {
		_ = tx.Rollback()
		t.Fatal("expected migration transaction failure")
	}
	_ = tx.Rollback()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM episode_verifications`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expected 0 verifications after rollback, got %d (err: %v)", count, err)
	}
	var outcome string
	if err := db.QueryRow(`SELECT outcome FROM episodes WHERE id = 'fail-atomic-1'`).Scan(&outcome); err != nil || outcome != "verified_success" {
		t.Fatalf("expected outcome verified_success preserved on rollback, got %s (err: %v)", outcome, err)
	}
}

func TestVectorContentVersionReconciliationFailuresAndRestart(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	db, err := openDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ep := &models.Episode{ID: "restart-vector-ep", Problem: "restart problem title", ThinkingTrace: "restart trace", Verification: []models.VerificationRecord{
		{Type: models.VerificationTests, Result: "restart_reconcile_term", Success: true},
	}}
	encoded, _ := json.Marshal(ep.Verification)
	if _, err := db.Exec(`INSERT INTO episodes (id, created_at, updated_at, domain, outcome, tier, tags, problem, thinking_trace, verification) VALUES (?, ?, ?, 'coding', 'verified_success', 'episodic', '[]', ?, ?, ?)`,
		ep.ID, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339), ep.Problem, ep.ThinkingTrace, string(encoded),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO episode_verifications (episode_id, position, type, command, result, success, evidence) VALUES (?, 0, ?, '', ?, 1, '')`, ep.ID, string(models.VerificationTests), "restart_reconcile_term"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM store_metadata WHERE key='vector_content_version'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	vecFail, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	vecFail.deleteEpisodeHook = func(ctx context.Context, id string) error {
		return errors.New("simulated vector failure")
	}
	if esFail, err := NewWithVector(dbPath, vecFail); err == nil {
		_ = esFail.Close()
		t.Fatal("expected reconciliation failure")
	}

	dbCheck, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var version string
	_ = dbCheck.QueryRow("SELECT value FROM store_metadata WHERE key='vector_content_version'").Scan(&version)
	_ = dbCheck.Close()
	if version == "2" {
		t.Fatal("vector_content_version must remain unapplied until reconciliation succeeds")
	}

	vecOk, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	esOk, err := NewWithVector(dbPath, vecOk)
	if err != nil {
		t.Fatal(err)
	}
	defer esOk.Close()

	results, err := esOk.SearchLocal("restart_reconcile_term", "", "", "", nil, 5)
	if err != nil || len(results) == 0 {
		t.Fatalf("failed to retrieve episode after restart reconciliation: results=%v err=%v", results, err)
	}
}

func TestVectorContentVersionReconciliationOnStartup(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "store.db")
	db, err := openDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ep := &models.Episode{ID: "legacy-vector-ep", Problem: "unrelated problem title", ThinkingTrace: "unrelated thinking trace", Verification: []models.VerificationRecord{
		{Type: models.VerificationTests, Result: "unique_verif_term_reconcile", Success: true},
	}}
	encoded, _ := json.Marshal(ep.Verification)
	if _, err := db.Exec(`INSERT INTO episodes (id, created_at, updated_at, domain, outcome, tier, tags, problem, thinking_trace, verification) VALUES (?, ?, ?, 'coding', 'verified_success', 'episodic', '[]', ?, ?, ?)`,
		ep.ID, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339), ep.Problem, ep.ThinkingTrace, string(encoded),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO episode_verifications (episode_id, position, type, command, result, success, evidence) VALUES (?, 0, ?, '', ?, 1, '')`, ep.ID, string(models.VerificationTests), "unique_verif_term_reconcile"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM store_metadata WHERE key='vector_content_version'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()

	results, err := es.SearchLocal("unique_verif_term_reconcile", "", "", "", nil, 5)
	if err != nil || len(results) == 0 {
		t.Fatalf("failed to retrieve episode by vector verification term after reconciliation: results=%v err=%v", results, err)
	}
}

func TestVectorVerificationTextInclusion(t *testing.T) {
	vs, err := NewVectorStore(t.TempDir(), "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(t.TempDir()+"/store.db", vs)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	ep := &models.Episode{ID: es.NextID(), Domain: "coding", Outcome: models.OutcomeVerifiedSuccess, Problem: "vector target", ThinkingTrace: "trace", Verification: []models.VerificationRecord{
		{Type: models.VerificationTests, Command: "secret_command", Result: "vector_verification_result", Success: true, Evidence: "vector_verification_evidence"},
	}}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	results, err := es.SearchLocal("vector_verification_result", "", "", "", nil, 5)
	if err != nil || len(results) == 0 {
		t.Fatalf("results=%v err=%v", results, err)
	}
}

func TestVerificationTextOmitsCommandsAndBoundsRecords(t *testing.T) {
	text := VerificationText([]models.VerificationRecord{
		{Type: models.VerificationTests, Command: "go test ./...", Result: "passed", Success: true},
		{Type: models.VerificationLint, Command: "go vet ./...", Evidence: "clean", Success: true},
		{Type: models.VerificationBuilds, Command: "go build ./...", Result: "built", Success: true},
		{Type: models.VerificationReview, Result: "must be omitted", Success: true},
	})
	if strings.Contains(text, "go test") || strings.Contains(text, "must be omitted") {
		t.Fatalf("verification vector text leaked command or exceeded record bound: %q", text)
	}
	for _, expected := range []string{"type=tests success=true", "result=passed", "evidence=clean"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in %q", expected, text)
		}
	}
}

func TestEpisodesByDomain(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 0)
	createEpisode(es, "agentic", "partial", nil, "p2", "t2", 0)
	createEpisode(es, "coding", "failure", nil, "p3", "t3", 0)

	byDomain, err := es.EpisodesByDomain()
	if err != nil {
		t.Fatalf("EpisodesByDomain: %v", err)
	}

	if byDomain["coding"] != 2 {
		t.Errorf("expected 2 coding, got %d", byDomain["coding"])
	}
	if byDomain["agentic"] != 1 {
		t.Errorf("expected 1 agentic, got %d", byDomain["agentic"])
	}
}

func TestEpisodesByOutcome(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 0)
	createEpisode(es, "coding", "success", nil, "p2", "t2", 0)
	createEpisode(es, "coding", "failure", nil, "p3", "t3", 0)

	byOutcome, err := es.EpisodesByOutcome()
	if err != nil {
		t.Fatalf("EpisodesByOutcome: %v", err)
	}

	if byOutcome["unverified_success"] != 2 {
		t.Errorf("expected 2 unverified_success, got %d", byOutcome["unverified_success"])
	}
	if byOutcome["failure"] != 1 {
		t.Errorf("expected 1 failure, got %d", byOutcome["failure"])
	}
}

func TestTopTags(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", []string{"go", "testing"}, "p1", "t1", 0)
	createEpisode(es, "coding", "success", []string{"go", "mcp"}, "p2", "t2", 0)
	createEpisode(es, "agentic", "partial", []string{"python", "testing"}, "p3", "t3", 0)

	tags, err := es.TopTags(10)
	if err != nil {
		t.Fatalf("TopTags: %v", err)
	}

	freq := make(map[string]int)
	for _, tc := range tags {
		freq[tc.Tag] = tc.Count
	}

	if freq["go"] != 2 {
		t.Errorf("expected go:2, got %d", freq["go"])
	}
	if freq["testing"] != 2 {
		t.Errorf("expected testing:2, got %d", freq["testing"])
	}
	if freq["mcp"] != 1 {
		t.Errorf("expected mcp:1, got %d", freq["mcp"])
	}
}

func TestTopTagsLimit(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", []string{"a", "b", "c"}, "p1", "t1", 0)
	createEpisode(es, "coding", "success", []string{"a", "b"}, "p2", "t2", 0)

	tags, err := es.TopTags(2)
	if err != nil {
		t.Fatalf("TopTags: %v", err)
	}

	if len(tags) > 2 {
		t.Errorf("expected at most 2 tags, got %d", len(tags))
	}
}

func TestAvgEpisodeLengths(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "short", "trace", 0)
	createEpisode(es, "coding", "success", nil, "longer problem", "longer thinking trace here", 0)

	avgProb, avgTrace, err := es.AvgEpisodeLengths()
	if err != nil {
		t.Fatalf("AvgEpisodeLengths: %v", err)
	}

	if avgProb < 5 || avgProb > 15 {
		t.Errorf("expected avg problem around 10, got %f", avgProb)
	}
	if avgTrace < 8 || avgTrace > 25 {
		t.Errorf("expected avg trace around 15, got %f", avgTrace)
	}
}

func TestEmptyThinkingTraceCount(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "full trace", 0)
	createEpisode(es, "coding", "success", nil, "p2", "", 0)

	count, err := es.EmptyThinkingTraceCount()
	if err != nil {
		t.Fatalf("EmptyThinkingTraceCount: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 empty trace, got %d", count)
	}
}

func TestDBSizeMB(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 0)

	size, err := es.DBSizeMB()
	if err != nil {
		t.Fatalf("DBSizeMB: %v", err)
	}

	if size <= 0 {
		t.Errorf("expected positive size, got %f", size)
	}
}

func TestDBPath(t *testing.T) {
	dir := t.TempDir()
	es, err := New(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer es.Close()

	if es.DBPath() == "" {
		t.Error("expected non-empty DBPath")
	}
}

func TestDB(t *testing.T) {
	es := testStore(t)
	if es.DB() == nil {
		t.Error("expected non-nil DB handle")
	}
}

func TestLastConsolidationTS(t *testing.T) {
	es := testStore(t)

	// No patterns yet
	ts, err := es.LastConsolidationTS()
	if err != nil {
		t.Fatalf("LastConsolidationTS (no patterns): %v", err)
	}
	if ts != nil {
		t.Errorf("expected nil, got %v", ts)
	}

	// Add a pattern
	pat := seedPattern(es)
	if pat == nil {
		t.Skip("no pattern merged (need 3+ episodes)")
	}

	ts, err = es.LastConsolidationTS()
	if err != nil {
		t.Fatalf("LastConsolidationTS: %v", err)
	}
	if ts == nil {
		t.Error("expected non-nil timestamp")
	}
}

func TestEpisodesByDay(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 10)
	createEpisode(es, "coding", "success", nil, "p2", "t2", 20)

	buckets, err := es.EpisodesByDay(1)
	if err != nil {
		t.Fatalf("EpisodesByDay: %v", err)
	}

	if len(buckets) == 0 {
		t.Error("expected at least 1 day bucket")
	}
}

func TestEmptyStoreStats(t *testing.T) {
	es := testStore(t)

	stats, err := es.SummaryStats()
	if err != nil {
		t.Fatalf("SummaryStats: %v", err)
	}

	if stats.TotalEpisodes != 0 {
		t.Errorf("expected 0 episodes, got %d", stats.TotalEpisodes)
	}
}

func TestSummaryStats(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 10)
	createEpisode(es, "coding", "success", nil, "p2", "t2", 20)
	createEpisode(es, "agentic", "failure", nil, "p3", "t3", 30)

	stats, err := es.SummaryStats()
	if err != nil {
		t.Fatalf("SummaryStats: %v", err)
	}

	if stats.TotalEpisodes != 3 {
		t.Errorf("expected 3 episodes, got %d", stats.TotalEpisodes)
	}

	if stats.SuccessRate <= 0 || stats.SuccessRate > 100 {
		t.Errorf("expected success rate between 0-100, got %f", stats.SuccessRate)
	}

	if stats.AvgDurationSec <= 0 {
		t.Errorf("expected positive avg duration, got %f", stats.AvgDurationSec)
	}

	if stats.TopDomain != "coding" {
		t.Errorf("expected top domain 'coding', got '%s'", stats.TopDomain)
	}
}

func TestDeletePattern(t *testing.T) {
	es := testStore(t)
	pat := seedPattern(es)
	if pat == nil {
		t.Skip("no pattern to delete")
	}

	if err := es.DeletePattern(pat.ID); err != nil {
		t.Fatalf("DeletePattern: %v", err)
	}

	got, err := es.GetPattern(pat.ID)
	if err != nil {
		t.Fatalf("GetPattern after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestReindexFTS5(t *testing.T) {
	es := testStore(t)
	createEpisode(es, "coding", "success", nil, "p1", "t1", 0)

	if err := es.ReindexFTS5(); err != nil {
		t.Fatalf("ReindexFTS5: %v", err)
	}

	results, err := es.SearchLocal("p1", "", "", "", nil, 10)
	if err != nil {
		t.Fatalf("SearchLocal after reindex: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result after reindex, got %d", len(results))
	}
}

func testStore(t *testing.T) *EpisodeStore {
	t.Helper()
	dir := t.TempDir()

	es, err := New(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = es.Close() })
	return es
}

func TestSchemaMigrationPre102(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// Create legacy v1 schema (without pre-102 fields/tables like episode_failed_approaches, project, provenance, model_id, etc.)
	legacySchema := `
	CREATE TABLE episodes (
		id TEXT PRIMARY KEY,
		problem TEXT NOT NULL,
		thinking_trace TEXT NOT NULL,
		domain TEXT NOT NULL,
		outcome TEXT NOT NULL,
		tier TEXT NOT NULL DEFAULT 'episodic',
		tags TEXT NOT NULL,
		repo TEXT NOT NULL DEFAULT '',
		labels TEXT NOT NULL DEFAULT '{}',
		steps TEXT NOT NULL DEFAULT '[]',
		tool_calls TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	_, err = db.Exec(`INSERT INTO episodes (id, problem, thinking_trace, domain, outcome, tier, tags, created_at, updated_at)
		VALUES ('legacy1', 'legacy problem', 'legacy trace', 'coding', 'success', 'episodic', '[]', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert legacy episode: %v", err)
	}
	_ = db.Close()

	// New store opening runs migrations
	vecDataDir := t.TempDir()
	vec, err := NewVectorStore(vecDataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()

	ep, err := es.GetEpisode("legacy1")
	if err != nil {
		t.Fatalf("GetEpisode after migration: %v", err)
	}
	if ep == nil || ep.ID != "legacy1" {
		t.Fatalf("expected legacy episode after migration, got %+v", ep)
	}
}

func TestTriggerSynchronization(t *testing.T) {
	es := testStore(t)
	ep := &models.Episode{
		ID:            "ep_trig",
		Domain:        "coding",
		Outcome:       "failure",
		Problem:       "trigger test problem",
		ThinkingTrace: "trigger test trace",
		FailedApproaches: []models.FailedApproach{
			{
				Approach:    "trigger approach test",
				FailureMode: "mode trig",
				RootCause:   "cause trig",
				Lesson:      "lesson trig",
			},
		},
	}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}

	// Verify trigger populated failed_approaches_fts
	var ftsCount int
	err := es.db.QueryRow("SELECT COUNT(*) FROM failed_approaches_fts WHERE failed_approaches_fts MATCH 'trigger'").Scan(&ftsCount)
	if err != nil {
		t.Fatalf("query failed_approaches_fts: %v", err)
	}
	if ftsCount != 1 {
		t.Fatalf("expected 1 match in failed_approaches_fts after insert, got %d", ftsCount)
	}

	// Update episode failed approaches
	ep.FailedApproaches[0].Approach = "updated approach test"
	if err := es.UpdateEpisode(context.Background(), ep); err != nil {
		t.Fatal(err)
	}

	err = es.db.QueryRow("SELECT COUNT(*) FROM failed_approaches_fts WHERE failed_approaches_fts MATCH 'updated'").Scan(&ftsCount)
	if err != nil {
		t.Fatalf("query failed_approaches_fts after update: %v", err)
	}
	if ftsCount != 1 {
		t.Fatalf("expected 1 match for updated text in failed_approaches_fts, got %d", ftsCount)
	}

	// Delete episode
	if err := es.DeleteEpisode(ep.ID); err != nil {
		t.Fatal(err)
	}

	err = es.db.QueryRow("SELECT COUNT(*) FROM failed_approaches_fts WHERE failed_approaches_fts MATCH 'updated'").Scan(&ftsCount)
	if err != nil {
		t.Fatalf("query failed_approaches_fts after delete: %v", err)
	}
	if ftsCount != 0 {
		t.Fatalf("expected 0 matches in failed_approaches_fts after delete, got %d", ftsCount)
	}
}

func TestRedactionBeforePersistenceAndEmbedding(t *testing.T) {
	es := testStore(t)
	vs, err := NewVectorStore(t.TempDir(), "mock", "", "", "", true)
	if err != nil {
		t.Fatalf("new vector store: %v", err)
	}
	defer vs.Close()
	es.vec = vs

	secret := "ghp_abcdefghijklmnopqrstuvwxyz1234567890"
	ep := &models.Episode{
		ID:            "ep_sec",
		Domain:        "coding",
		Outcome:       "success",
		Problem:       "fix bug using secret " + secret,
		ThinkingTrace: "trace containing token " + secret,
		FailedApproaches: []models.FailedApproach{
			{
				Approach:    "failed with secret " + secret,
				FailureMode: "mode secret " + secret,
				RootCause:   "cause secret " + secret,
				Lesson:      "lesson secret " + secret,
			},
		},
	}

	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}

	// Query raw DB rows to verify redaction before SQL persistence
	var prob, trace string
	err = es.db.QueryRow("SELECT problem, thinking_trace FROM episodes WHERE id = ?", ep.ID).Scan(&prob, &trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prob, secret) || strings.Contains(trace, secret) {
		t.Fatalf("unredacted secret in DB problem/trace: prob=%q trace=%q", prob, trace)
	}

	var app, mode, cause, lesson string
	err = es.db.QueryRow("SELECT approach, failure_mode, root_cause, lesson FROM episode_failed_approaches WHERE episode_id = ?", ep.ID).Scan(&app, &mode, &cause, &lesson)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{app, mode, cause, lesson} {
		if strings.Contains(field, secret) {
			t.Fatalf("unredacted secret in failed approach DB: %q", field)
		}
	}

	// Verify vector store document text is redacted
	results, err := vs.Search(context.Background(), secret, 1)
	if err != nil {
		t.Fatalf("Search vector: %v", err)
	}
	for _, res := range results {
		if res.ID == ep.ID && strings.Contains(res.Content, secret) {
			t.Fatalf("unredacted secret in vector store search: %v", res)
		}
	}

}

func seedEpisode(es *EpisodeStore) *models.Episode {
	ep := &models.Episode{
		ID:              es.NextID(),
		CreatedAt:       time.Now().UTC(),
		Domain:          "coding",
		Outcome:         "success",
		Tags:            []string{"golang", "testing"},
		Problem:         "Write unit tests for the reasoning-memory store layer",
		ThinkingTrace:   "1. Analyze the store interface\n2. Implement SQLite store with FTS5\n3. Write table-driven tests\n4. Verify all edge cases",
		Steps:           []models.Step{{ID: "s1", Type: "analysis", Content: "Analyze the store interface"}},
		ToolCalls:       []models.ToolCall{{Tool: "ctx_read", Outcome: "success"}},
		ModelID:         "test-model",
		DurationSeconds: 42,
	}
	_, _ = es.CreateEpisode(ep)
	return ep
}

func TestCreateEpisode(t *testing.T) {
	es := testStore(t)

	epID, err := es.CreateEpisode(&models.Episode{
		ID:            "re-20260713-001",
		Domain:        "coding",
		Outcome:       "success",
		Tags:          []string{"go", "test"},
		Problem:       "Test creating an episode",
		ThinkingTrace: "Test thinking trace content",
		Steps:         []models.Step{{ID: "s1", Type: "implementation", Content: "Test"}},
	})
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	if epID == "" {
		t.Fatal("expected non-empty episode ID")
	}

	ep, err := es.GetEpisode(epID)
	if err != nil {
		t.Fatalf("get episode: %v", err)
	}
	if ep == nil {
		t.Fatal("expected episode, got nil")
	}
	if ep.Domain != "coding" {
		t.Errorf("expected domain coding, got %s", ep.Domain)
	}
	if ep.Outcome != "unverified_success" {
		t.Errorf("expected outcome unverified_success, got %s", ep.Outcome)
	}
}

func TestGetEpisodeNotFound(t *testing.T) {
	es := testStore(t)
	ep, err := es.GetEpisode("nonexistent")
	if err != nil {
		t.Fatalf("get episode: %v", err)
	}
	if ep != nil {
		t.Error("expected nil for nonexistent episode")
	}
}

func TestGetSummary(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)

	summary, err := es.GetSummary(ep.ID)
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	if summary == nil {
		t.Fatal("expected summary, got nil")
	}
	if summary.Domain != "coding" {
		t.Errorf("expected domain coding, got %s", summary.Domain)
	}
	if summary.StepCount != 1 {
		t.Errorf("expected 1 step, got %d", summary.StepCount)
	}
	if summary.ToolCount != 1 {
		t.Errorf("expected 1 tool call, got %d", summary.ToolCount)
	}
}

func TestListEpisodes(t *testing.T) {
	es := testStore(t)
	ep1 := seedEpisode(es)

	ep2 := &models.Episode{
		ID:            es.NextID(),
		Domain:        "agentic",
		Outcome:       "partial",
		Tags:          []string{"mcp"},
		Problem:       "Second episode",
		ThinkingTrace: "Trace 2",
	}
	_, _ = es.CreateEpisode(ep2)

	episodes, err := es.ListEpisodes(10, 0)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Errorf("expected 2 episodes, got %d", len(episodes))
	}

	ids := map[string]bool{}
	for _, ep := range episodes {
		ids[ep.ID] = true
	}
	if !ids[ep1.ID] || !ids[ep2.ID] {
		t.Errorf("expected both episode IDs in results, got %v", ids)
	}
}

func TestDeleteEpisode(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)

	var count int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM metadata_idx WHERE episode_id = ?", ep.ID).Scan(&count); err != nil || count == 0 {
		t.Fatalf("expected metadata_idx rows before delete, count=%d err=%v", count, err)
	}

	if err := es.DeleteEpisode(ep.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}

	if err := es.db.QueryRow("SELECT COUNT(*) FROM metadata_idx WHERE episode_id = ?", ep.ID).Scan(&count); err != nil || count != 0 {
		t.Errorf("expected 0 metadata_idx rows after delete, got %d (err: %v)", count, err)
	}
}

func TestEpisodeCount(t *testing.T) {
	es := testStore(t)
	seedEpisode(es)

	count, err := es.EpisodeCount()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 episode, got %d", count)
	}
}

func TestNextID(t *testing.T) {
	es := testStore(t)
	id1 := es.NextID()
	if id1 == "" {
		t.Fatal("expected non-empty ID")
	}

	_, _ = es.CreateEpisode(&models.Episode{
		ID:            id1,
		Domain:        "test",
		Outcome:       "failure",
		Problem:       "test",
		ThinkingTrace: "test",
	})

	id2 := es.NextID()
	if id1 == id2 {
		t.Errorf("expected different IDs, got %s and %s", id1, id2)
	}
}

func TestPersistTagJSON(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)

	summary, _ := es.GetSummary(ep.ID)
	if len(summary.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(summary.Tags))
	}

	foundGo := false
	foundTesting := false
	for _, tag := range summary.Tags {
		if tag == "golang" {
			foundGo = true
		}
		if tag == "testing" {
			foundTesting = true
		}
	}
	if !foundGo || !foundTesting {
		t.Errorf("expected tags to contain golang and testing, got %v", summary.Tags)
	}
}

func TestToolCallsJSONRoundtrip(t *testing.T) {
	es := testStore(t)
	tc := models.ToolCall{
		Tool:          "ctx_read",
		Args:          map[string]any{"path": "/tmp/test.go", "mode": "auto"},
		ResultExcerpt: "func main() {",
		Outcome:       "success",
	}

	ep := &models.Episode{
		ID:            es.NextID(),
		Domain:        "coding",
		Outcome:       "success",
		Problem:       "test tool calls",
		ThinkingTrace: "trace",
		ToolCalls:     []models.ToolCall{tc},
	}
	_, _ = es.CreateEpisode(ep)

	got, _ := es.GetEpisode(ep.ID)
	if len(got.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(got.ToolCalls))
	}
	if got.ToolCalls[0].Tool != "ctx_read" {
		t.Errorf("expected ctx_read tool, got %s", got.ToolCalls[0].Tool)
	}
	if got.ToolCalls[0].Outcome != "success" {
		t.Errorf("expected success outcome, got %s", got.ToolCalls[0].Outcome)
	}

	argsJSON, _ := json.Marshal(got.ToolCalls[0].Args)
	var args map[string]any
	_ = json.Unmarshal(argsJSON, &args)
	if args["path"] != "/tmp/test.go" {
		t.Errorf("expected path /tmp/test.go, got %v", args["path"])
	}
}

func TestRichEpisodeRoundTripUpdateAndArchive(t *testing.T) {
	es := testStore(t)
	confidence := 0.75
	ep := &models.Episode{
		ID:           es.NextID(),
		Domain:       "coding",
		Outcome:      models.OutcomeVerifiedSuccess,
		Tags:         []string{"rich"},
		Repo:         "repo",
		Project:      "project",
		Provenance:   "agent",
		Confidence:   &confidence,
		Problem:      "rich problem",
		Objectives:   []string{"objective"},
		Decisions:    []string{"decision"},
		Alternatives: []string{"alternative"},
		Verification: []models.VerificationRecord{{Type: models.VerificationTests, Command: "go test ./...", Result: "pass", Success: true}},
		Lessons:      []string{"lesson"},
	}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	got, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != ep.Project || got.Provenance != ep.Provenance || got.Confidence == nil || *got.Confidence != confidence || len(got.Objectives) != 1 || len(got.Decisions) != 1 || len(got.Alternatives) != 1 || len(got.Verification) != 1 || len(got.Lessons) != 1 {
		t.Fatalf("rich fields did not round-trip: %#v", got)
	}
	got.Lessons = append(got.Lessons, "updated")
	if err := es.UpdateEpisode(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	updated, err := es.GetEpisode(ep.ID)
	if err != nil || len(updated.Lessons) != 2 || updated.UpdatedAt.Before(updated.CreatedAt) {
		t.Fatalf("updated episode mismatch: %#v err=%v", updated, err)
	}
	if _, err := es.db.Exec(`INSERT INTO episodes_archive SELECT * FROM episodes WHERE id = ?`, ep.ID); err != nil {
		t.Fatal(err)
	}
	archived, err := es.GetArchivedEpisode(ep.ID)
	if err != nil || archived.Project != ep.Project || len(archived.Lessons) != 2 {
		t.Fatalf("archived episode mismatch: %#v err=%v", archived, err)
	}
}

func TestLegacyMigrationAndValidation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE episodes (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, domain TEXT NOT NULL, outcome TEXT NOT NULL, tags TEXT NOT NULL, problem TEXT NOT NULL, thinking_trace TEXT NOT NULL, steps TEXT NOT NULL, tool_calls TEXT NOT NULL, model_id TEXT NOT NULL, duration_seconds INTEGER NOT NULL, repo TEXT NOT NULL DEFAULT '', labels TEXT NOT NULL DEFAULT '{}', tier TEXT NOT NULL DEFAULT 'episodic')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO episodes (id, created_at, domain, outcome, tags, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds, repo, labels, tier) VALUES ('legacy', '2026-01-01T00:00:00Z', 'coding', 'success', '[]', 'problem', '', '[]', '[]', '', 0, '', '{}', '')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	es, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	got, err := es.GetEpisode("legacy")
	if err != nil || got.Outcome != models.OutcomeUnverifiedSuccess || !got.IsEpisodic() {
		t.Fatalf("legacy migration mismatch: %#v err=%v", got, err)
	}
	for _, invalid := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.1, 1.1} {
		ep := &models.Episode{Problem: "problem", Outcome: models.OutcomeFailure, Confidence: &invalid}
		if err := ep.Validate(); err == nil {
			t.Fatalf("accepted invalid confidence %v", invalid)
		}
	}
	legacy := &models.Episode{ID: "compat", Problem: "problem", Outcome: "success"}
	if _, err := es.CreateEpisode(legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Outcome != "success" || legacy.Tier != "" {
		t.Fatalf("compatibility boundary mutated request: %#v", legacy)
	}
}

func TestMalformedPersistedJSONReturnsFieldError(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)
	if _, err := es.db.Exec("UPDATE episodes SET tags = ? WHERE id = ?", "{", ep.ID); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func() error{
		"get":     func() error { _, err := es.GetEpisode(ep.ID); return err },
		"summary": func() error { _, err := es.GetSummary(ep.ID); return err },
		"list":    func() error { _, err := es.ListEpisodes(10, 0); return err },
		"search":  func() error { _, err := es.SearchLocal("unit tests", "", "", "", nil, 5); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := read()
			if err == nil || !strings.Contains(err.Error(), "episode "+ep.ID+" field tags") {
				t.Fatalf("expected descriptive tags error, got %v", err)
			}
		})
	}
}

func TestSearchNormalizesLegacyOutcomeFilter(t *testing.T) {
	es := testStore(t)
	ep := &models.Episode{ID: es.NextID(), Outcome: "success", Problem: "legacy filter target", ThinkingTrace: "trace"}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	results, err := es.SearchLocal("legacy filter target", "", "success", "", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != models.OutcomeUnverifiedSuccess {
		t.Fatalf("legacy success filter mismatch: %#v", results)
	}
}

func TestUpdateEpisodeVectorReplaceFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configure  func(*VectorStore)
		wantErrors []string
	}{
		{
			name: "delete failure",
			configure: func(vec *VectorStore) {
				vec.deleteEpisodeHook = func(context.Context, string) error { return errors.New("forced delete failure") }
			},
			wantErrors: []string{"forced delete failure", "restore previous episode vector"},
		},
		{
			name: "partial add failure",
			configure: func(vec *VectorStore) {
				calls := 0
				vec.addEpisodeAfterHook = func(context.Context, string, string, string) error {
					calls++
					if calls == 1 {
						return errors.New("forced partial add failure")
					}
					return nil
				}
			},
			wantErrors: []string{"forced partial add failure"},
		},
		{
			name: "restore failure",
			configure: func(vec *VectorStore) {
				calls := 0
				vec.addEpisodeHook = func(context.Context, string, string, string) error {
					calls++
					if calls == 1 {
						return errors.New("forced replacement failure")
					}
					return errors.New("forced restore failure")
				}
			},
			wantErrors: []string{"forced replacement failure", "restore previous episode vector", "forced restore failure"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vec, err := NewVectorStore(t.TempDir(), "mock", "", "", "", true)
			if err != nil {
				t.Fatal(err)
			}
			es, err := NewWithVector(filepath.Join(t.TempDir(), "update-compensation.db"), vec)
			if err != nil {
				t.Fatal(err)
			}
			defer es.Close()
			ep := &models.Episode{ID: es.NextID(), Outcome: models.OutcomeFailure, Problem: "before", ThinkingTrace: "before trace"}
			if _, err := es.CreateEpisode(ep); err != nil {
				t.Fatal(err)
			}
			tc.configure(vec)
			updated, err := es.GetEpisode(ep.ID)
			if err != nil {
				t.Fatal(err)
			}
			updated.Problem = "after"
			err = es.UpdateEpisode(context.Background(), updated)
			for _, want := range tc.wantErrors {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error %v does not contain %q", err, want)
				}
			}
			got, getErr := es.GetEpisode(ep.ID)
			if getErr != nil || got.Problem != "before" {
				t.Fatalf("database update was not compensated: %#v err=%v", got, getErr)
			}
		})
	}
}

func TestUpdateEpisodePropagatesGetEpisodeErrorBeforeUpdate(t *testing.T) {
	es := testStore(t)
	ep := seedEpisode(es)
	if _, err := es.db.Exec("UPDATE episodes SET created_at = ? WHERE id = ?", "bad-created-at", ep.ID); err != nil {
		t.Fatal(err)
	}
	updateReq := &models.Episode{ID: ep.ID, Outcome: models.OutcomeFailure, Problem: "update problem", ThinkingTrace: "update trace"}
	err := es.UpdateEpisode(context.Background(), updateReq)
	if err == nil || !strings.Contains(err.Error(), "get existing episode before update") || !strings.Contains(err.Error(), "field created_at") {
		t.Fatalf("expected GetEpisode error propagation before update, got %v", err)
	}
}

func TestVectorReconciliationLifecycleAndRestart(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "reconcile.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	ep := &models.Episode{ID: es.NextID(), Outcome: models.OutcomeFailure, Problem: "initial problem", ThinkingTrace: "initial trace"}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	vec.deleteEpisodeHook = func(context.Context, string) error { return errors.New("forced replacement failure") }
	vec.addEpisodeHook = func(context.Context, string, string, string) error { return errors.New("forced restore failure") }
	updated, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.Problem = "attempted update"
	err = es.UpdateEpisode(context.Background(), updated)
	if err == nil || !strings.Contains(err.Error(), "forced replacement failure") {
		t.Fatalf("expected update failure, got %v", err)
	}
	var pendingCount int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM vector_reconcile WHERE episode_id = ?", ep.ID).Scan(&pendingCount); err != nil || pendingCount != 1 {
		t.Fatalf("expected 1 pending reconcile row after rollback failure, got %d err=%v", pendingCount, err)
	}
	var pendingProblem string
	if err := es.db.QueryRow("SELECT problem FROM vector_reconcile WHERE episode_id = ?", ep.ID).Scan(&pendingProblem); err != nil || pendingProblem != "initial problem" {
		t.Fatalf("expected durable pending problem 'initial problem', got %q err=%v", pendingProblem, err)
	}
	_ = es.Close()
	vec2, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es2, err := NewWithVector(dbPath, vec2)
	if err != nil {
		t.Fatalf("expected successful startup reconciliation, got %v", err)
	}
	defer es2.Close()
	if err := es2.db.QueryRow("SELECT COUNT(*) FROM vector_reconcile WHERE episode_id = ?", ep.ID).Scan(&pendingCount); err != nil || pendingCount != 0 {
		t.Fatalf("expected 0 pending reconcile rows after restart, got %d err=%v", pendingCount, err)
	}
}

func TestDeleteEpisodeRemovesPendingVectorReconciliation(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "delete-pending.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}
	ep := &models.Episode{ID: es.NextID(), Outcome: models.OutcomeFailure, Problem: "delete pending target", ThinkingTrace: "trace"}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	vec.deleteEpisodeHook = func(context.Context, string) error { return errors.New("forced update delete failure") }
	updated, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.Problem = "failed update"
	if err := es.UpdateEpisode(context.Background(), updated); err == nil {
		t.Fatal("expected vector update failure")
	}
	vec.deleteEpisodeHook = nil
	if err := es.DeleteEpisode(ep.ID); err != nil {
		t.Fatal(err)
	}
	var pendingCount int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM vector_reconcile WHERE episode_id = ?", ep.ID).Scan(&pendingCount); err != nil || pendingCount != 0 {
		t.Fatalf("pending reconcile row survived delete: count=%d err=%v", pendingCount, err)
	}
	_ = es.Close()
	vec2, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es2, err := NewWithVector(dbPath, vec2)
	if err != nil {
		t.Fatal(err)
	}
	defer es2.Close()
	if err := es2.ReconcileVectorStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := vec2.Count(); got != 0 {
		t.Fatalf("ghost vector recreated for deleted episode %s; vector count=%d", ep.ID, got)
	}
}

func TestMigrationBackfillRebuildsFTSAfterOutcomeNormalization(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fts-order.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE episodes (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, domain TEXT NOT NULL, outcome TEXT NOT NULL, tags TEXT NOT NULL, problem TEXT NOT NULL, thinking_trace TEXT NOT NULL, steps TEXT NOT NULL, tool_calls TEXT NOT NULL, model_id TEXT NOT NULL, duration_seconds INTEGER NOT NULL); INSERT INTO episodes VALUES ('legacy-fts', '2026-01-01T00:00:00Z', 'coding', 'success', '[]', 'fts migration target', '', '[]', '[]', '', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	es, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	var outcome string
	if err := es.db.QueryRow(`SELECT outcome FROM episodes_fts WHERE episodes_fts MATCH 'unverified_success'`).Scan(&outcome); err != nil || outcome != models.OutcomeUnverifiedSuccess {
		t.Fatalf("FTS outcome not rebuilt after backfill: outcome=%q err=%v", outcome, err)
	}
}

func TestFreshDatabaseFTSTriggersTrackInsertUpdateDelete(t *testing.T) {
	es := testStore(t)
	ep := &models.Episode{ID: es.NextID(), Outcome: models.OutcomeFailure, Problem: "fresh insert target", ThinkingTrace: "trace"}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}
	assertSearchCount := func(query string, want int) {
		t.Helper()
		results, err := es.SearchLocal(query, "", "", "", nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != want {
			t.Fatalf("search %q returned %d, want %d", query, len(results), want)
		}
	}
	assertSearchCount("fresh insert target", 1)
	stored, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Problem = "fresh update target"
	if err := es.UpdateEpisode(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := es.db.QueryRow(`SELECT COUNT(*) FROM episodes_fts WHERE episodes_fts MATCH '"fresh insert target"'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old FTS content still indexed: count=%d err=%v", count, err)
	}
	assertSearchCount("fresh update target", 1)
	if err := es.DeleteEpisode(ep.ID); err != nil {
		t.Fatal(err)
	}
	assertSearchCount("fresh update target", 0)
}

func TestLegacyVerificationMigrationBackfillAndVerifiedSuccessCorrection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-verif.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE episodes (
		id TEXT PRIMARY KEY, created_at TEXT NOT NULL, domain TEXT NOT NULL, outcome TEXT NOT NULL, tags TEXT NOT NULL,
		problem TEXT NOT NULL, thinking_trace TEXT NOT NULL, steps TEXT NOT NULL, tool_calls TEXT NOT NULL,
		model_id TEXT NOT NULL, duration_seconds INTEGER NOT NULL, verification TEXT NOT NULL DEFAULT '[]'
	);
	CREATE TABLE episodes_archive (
		id TEXT PRIMARY KEY, created_at TEXT NOT NULL, domain TEXT NOT NULL, outcome TEXT NOT NULL, tags TEXT NOT NULL,
		problem TEXT NOT NULL, thinking_trace TEXT NOT NULL, steps TEXT NOT NULL, tool_calls TEXT NOT NULL,
		model_id TEXT NOT NULL, duration_seconds INTEGER NOT NULL, verification TEXT NOT NULL DEFAULT '[]'
	);
	INSERT INTO episodes VALUES (
		'ep-legacy-1', '2026-01-01T00:00:00Z', 'coding', 'verified_success', '[]',
		'legacy problem 1', 'trace', '[]', '[]', '', 0, '"legacy observation string"'
	);
	INSERT INTO episodes_archive VALUES (
		'ep-legacy-2', '2026-01-01T00:00:00Z', 'coding', 'verified_success', '[]',
		'legacy problem 2', 'trace', '[]', '[]', '', 0, '[{"type":"tests","command":"go test ./...","result":"pass","success":true}]'
	);`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	es, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()

	ep1, err := es.GetEpisode("ep-legacy-1")
	if err != nil || ep1 == nil {
		t.Fatalf("ep-legacy-1 missing: %v", err)
	}
	if ep1.Outcome != models.OutcomeUnverifiedSuccess {
		t.Fatalf("expected outcome unverified_success for converted legacy episode, got %s", ep1.Outcome)
	}
	if len(ep1.Verification) != 2 {
		t.Fatalf("expected 2 verification records (legacy observation + correction note), got %d", len(ep1.Verification))
	}
	if ep1.Verification[0].Type != models.VerificationObservation || ep1.Verification[0].Result != "Legacy verification payload converted: legacy observation string" {
		t.Fatalf("unexpected legacy observation record: %+v", ep1.Verification[0])
	}
	if ep1.Verification[1].Type != models.VerificationObservation || !strings.Contains(ep1.Verification[1].Result, "converted verified_success to unverified_success") {
		t.Fatalf("unexpected correction note record: %+v", ep1.Verification[1])
	}

	var archOutcome string
	if err := es.db.QueryRow("SELECT outcome FROM episodes_archive WHERE id = 'ep-legacy-2'").Scan(&archOutcome); err != nil || archOutcome != models.OutcomeVerifiedSuccess {
		t.Fatalf("expected verified_success preserved for ep-legacy-2, got %s (err: %v)", archOutcome, err)
	}
	var archCount int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM episode_verifications_archive WHERE episode_id = 'ep-legacy-2'").Scan(&archCount); err != nil || archCount != 1 {
		t.Fatalf("expected 1 archive verification record for ep-legacy-2, got %d (err: %v)", archCount, err)
	}

	var ftsCount int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM verification_fts WHERE verification_fts MATCH 'legacy'").Scan(&ftsCount); err != nil || ftsCount != 2 {
		t.Fatalf("expected both active migration observations indexed after rebuild, got %d hits (err: %v)", ftsCount, err)
	}
}

func TestRestoreEpisodeDBRestoresVerificationRows(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "rollback-verif.db")
	vec, err := NewVectorStore(dataDir, "mock", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	es, err := NewWithVector(dbPath, vec)
	if err != nil {
		t.Fatal(err)
	}

	ep := &models.Episode{
		ID:      es.NextID(),
		Outcome: models.OutcomeVerifiedSuccess,
		Problem: "rollback verification problem",
		Verification: []models.VerificationRecord{
			{Type: models.VerificationTests, Command: "go test ./...", Result: "pass", Success: true},
		},
	}
	if _, err := es.CreateEpisode(ep); err != nil {
		t.Fatal(err)
	}

	vec.deleteEpisodeHook = func(context.Context, string) error { return errors.New("forced vector replace failure") }
	updated, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.Problem = "updated problem"
	updated.Verification = []models.VerificationRecord{
		{Type: models.VerificationLint, Command: "go vet ./...", Result: "pass", Success: true},
		{Type: models.VerificationTests, Command: "go test ./...", Result: "pass", Success: true},
	}

	if err := es.UpdateEpisode(context.Background(), updated); err == nil {
		t.Fatal("expected vector update failure")
	}

	rolledBack, err := es.GetEpisode(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Problem != "rollback verification problem" {
		t.Fatalf("expected rolled back problem, got %s", rolledBack.Problem)
	}
	if len(rolledBack.Verification) != 1 || rolledBack.Verification[0].Command != "go test ./..." {
		t.Fatalf("expected restored verification records, got %+v", rolledBack.Verification)
	}

	var count int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM episode_verifications WHERE episode_id = ?", ep.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected 1 active verification row after rollback, got %d (err: %v)", count, err)
	}
}

func TestCreateEpisodeWithSourcesSanitizesFields(t *testing.T) {
	es := testStore(t)
	ep := &models.Episode{
		ID:            es.NextID(),
		Outcome:       models.OutcomeUnverifiedSuccess,
		Problem:       "source sanitization problem",
		ThinkingTrace: "trace",
	}
	secret := "gh" + "p_abcdefghijklmnopqrstuvwxyz1234567890"
	sources := []linkcontent.Source{
		{
			SourceURL:          "https://example.com/spec",
			SourceType:         "web_page",
			Title:              "Spec",
			Status:             "summarized",
			Summary:            "API token is " + secret,
			Warning:            "Exposed key " + secret,
			Instructions:       []string{"Use key " + secret},
			AcceptanceCriteria: []string{"Validated " + secret},
			Constraints:        []string{"Do not share " + secret},
		},
	}

	if _, err := es.CreateEpisodeWithSourcesContext(context.Background(), ep, sources); err != nil {
		t.Fatal(err)
	}

	var summary, warning, instructions, acceptance, constraints string
	if err := es.db.QueryRow("SELECT summary, warning, instructions, acceptance_criteria, constraints FROM episode_sources WHERE episode_id = ?", ep.ID).Scan(&summary, &warning, &instructions, &acceptance, &constraints); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(summary, secret) || strings.Contains(warning, secret) || strings.Contains(instructions, secret) || strings.Contains(acceptance, secret) || strings.Contains(constraints, secret) {
		t.Fatalf("expected secret token redacted from source fields, got:\nsummary=%s\nwarning=%s\ninstructions=%s\nacceptance=%s\nconstraints=%s", summary, warning, instructions, acceptance, constraints)
	}
}

func TestMalformedPersistedTimestampReturnsFieldError(t *testing.T) {
	for _, field := range []string{"created_at", "updated_at"} {
		t.Run(field, func(t *testing.T) {
			es := testStore(t)
			ep := seedEpisode(es)
			if _, err := es.db.Exec("UPDATE episodes SET "+field+" = ? WHERE id = ?", "not-a-time", ep.ID); err != nil {
				t.Fatal(err)
			}
			for name, read := range map[string]func() error{
				"get":     func() error { _, err := es.GetEpisode(ep.ID); return err },
				"summary": func() error { _, err := es.GetSummary(ep.ID); return err },
				"list":    func() error { _, err := es.ListEpisodes(10, 0); return err },
				"search":  func() error { _, err := es.SearchLocal("unit tests", "", "", "", nil, 5); return err },
			} {
				t.Run(name, func(t *testing.T) {
					err := read()
					if err == nil || !strings.Contains(err.Error(), "episode "+ep.ID+" field "+field) {
						t.Fatalf("expected descriptive %s error, got %v", field, err)
					}
				})
			}
		})
	}
}
