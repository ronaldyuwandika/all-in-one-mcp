package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/linkcontent"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/security"
)

type EpisodeStore struct {
	db               *sql.DB
	vec              *VectorStore
	dbPath           string
	CompactionCancel context.CancelFunc
}

func New(dbPath string) (*EpisodeStore, error) {
	db, err := openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	return &EpisodeStore{db: db, dbPath: dbPath}, nil
}

func NewWithVector(dbPath string, vec *VectorStore) (*EpisodeStore, error) {
	db, err := openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	es := &EpisodeStore{db: db, dbPath: dbPath, vec: vec}
	if vec != nil && vec.Enabled() {
		if err := es.ReconcileVectorStore(context.Background()); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("reconcile vector store: %w", err)
		}
	}
	return es, nil
}

func openDatabase(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable wal: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

const currentVectorContentVersion = 2

func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ddl := []string{
		`CREATE TABLE IF NOT EXISTS episodes (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			domain TEXT NOT NULL DEFAULT 'coding',
			outcome TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '[]',
			problem TEXT NOT NULL,
			thinking_trace TEXT NOT NULL,
			steps TEXT NOT NULL DEFAULT '[]',
			tool_calls TEXT NOT NULL DEFAULT '[]',
			model_id TEXT NOT NULL DEFAULT '',
			duration_seconds INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS episodes_fts USING fts5(
			id UNINDEXED,
			problem,
			thinking_trace,
			domain,
			outcome,
			tags,
			content='episodes',
			content_rowid='rowid'
		)`,
		`CREATE TABLE IF NOT EXISTS patterns (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			domain TEXT NOT NULL,
			merge_score REAL NOT NULL DEFAULT 0,
			sources TEXT NOT NULL DEFAULT '[]',
			consolidated_prompt TEXT NOT NULL,
			master_thinking_path TEXT NOT NULL,
			master_tool_calls TEXT NOT NULL DEFAULT '[]',
			tags TEXT NOT NULL DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS episodes_archive (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			domain TEXT NOT NULL DEFAULT 'coding',
			outcome TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '[]',
			problem TEXT NOT NULL,
			thinking_trace TEXT NOT NULL,
			steps TEXT NOT NULL DEFAULT '[]',
			tool_calls TEXT NOT NULL DEFAULT '[]',
			model_id TEXT NOT NULL DEFAULT '',
			duration_seconds INTEGER NOT NULL DEFAULT 0,
			repo TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '{}',
			tier TEXT NOT NULL DEFAULT 'episodic'
		)`,
		`CREATE TABLE IF NOT EXISTS episode_failed_approaches (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			episode_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			approach TEXT NOT NULL,
			failure_mode TEXT NOT NULL,
			root_cause TEXT NOT NULL,
			lesson TEXT NOT NULL,
			UNIQUE(episode_id, position),
			FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS episode_failed_approaches_archive (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			episode_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			approach TEXT NOT NULL,
			failure_mode TEXT NOT NULL,
			root_cause TEXT NOT NULL,
			lesson TEXT NOT NULL,
			UNIQUE(episode_id, position),
			FOREIGN KEY (episode_id) REFERENCES episodes_archive(id) ON DELETE CASCADE
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS failed_approaches_fts USING fts5(
			episode_id UNINDEXED, approach, failure_mode, root_cause, lesson,
			content='episode_failed_approaches', content_rowid='id'
		)`,
		`CREATE TABLE IF NOT EXISTS episode_verifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT, episode_id TEXT NOT NULL, position INTEGER NOT NULL,
			type TEXT NOT NULL, command TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '', success INTEGER NOT NULL DEFAULT 0, evidence TEXT NOT NULL DEFAULT '',
			UNIQUE(episode_id, position), FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS episode_verifications_archive (
			id INTEGER PRIMARY KEY AUTOINCREMENT, episode_id TEXT NOT NULL, position INTEGER NOT NULL,
			type TEXT NOT NULL, command TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '', success INTEGER NOT NULL DEFAULT 0, evidence TEXT NOT NULL DEFAULT '',
			UNIQUE(episode_id, position), FOREIGN KEY (episode_id) REFERENCES episodes_archive(id) ON DELETE CASCADE
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS verification_fts USING fts5(
			episode_id UNINDEXED, type, command, result, evidence,
			content='episode_verifications', content_rowid='id'
		)`,
		`CREATE TABLE IF NOT EXISTS compaction_stats (
			key TEXT PRIMARY KEY,
			value INTEGER NOT NULL DEFAULT 0
		)`,
	}

	for _, d := range ddl {
		if _, err := tx.Exec(d); err != nil {
			return fmt.Errorf("exec ddl: %w\n%s", err, d)
		}
	}

	hasCol := func(name string) bool {
		if rows, err := tx.Query("PRAGMA table_info(episodes)"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var cid int
				var colName, ctype string
				var notnull int
				var dflt sql.NullString
				var pk int
				if err := rows.Scan(&cid, &colName, &ctype, &notnull, &dflt, &pk); err == nil && colName == name {
					return true
				}
			}
		}
		return false
	}

	if !hasCol("repo") {
		if _, err := tx.Exec("ALTER TABLE episodes ADD COLUMN repo TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add repo column: %w", err)
		}
	}
	if !hasCol("labels") {
		if _, err := tx.Exec("ALTER TABLE episodes ADD COLUMN labels TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return fmt.Errorf("add labels column: %w", err)
		}
	}
	if !hasCol("tier") {
		if _, err := tx.Exec("ALTER TABLE episodes ADD COLUMN tier TEXT NOT NULL DEFAULT 'episodic'"); err != nil {
			return fmt.Errorf("add tier column: %w", err)
		}
	}

	richColumns := []struct {
		name string
		ddl  string
	}{
		{"model_id", "TEXT NOT NULL DEFAULT ''"},
		{"duration_seconds", "INTEGER NOT NULL DEFAULT 0"},
		{"updated_at", "TEXT NOT NULL DEFAULT ''"},
		{"project", "TEXT NOT NULL DEFAULT ''"},
		{"provenance", "TEXT NOT NULL DEFAULT ''"},
		{"confidence", "REAL"},
		{"objectives", "TEXT NOT NULL DEFAULT '[]'"},
		{"decisions", "TEXT NOT NULL DEFAULT '[]'"},
		{"alternatives", "TEXT NOT NULL DEFAULT '[]'"},
		{"verification", "TEXT NOT NULL DEFAULT '[]'"},
		{"lessons", "TEXT NOT NULL DEFAULT '[]'"},
	}

	for _, table := range []string{"episodes", "episodes_archive"} {
		for _, column := range richColumns {
			var count int
			if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column.name).Scan(&count); err != nil {
				return fmt.Errorf("inspect %s.%s: %w", table, column.name, err)
			}
			if count == 0 {
				if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column.name, column.ddl)); err != nil {
					return fmt.Errorf("add %s.%s: %w", table, column.name, err)
				}
			}
		}
	}
	for _, table := range []string{"episodes", "episodes_archive"} {
		var hasTable int
		if err := tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&hasTable); err == nil && hasTable > 0 {
			if _, err := tx.Exec("UPDATE " + table + " SET outcome = 'unverified_success' WHERE outcome = 'success'"); err != nil {
				return fmt.Errorf("backfill %s success outcomes: %w", table, err)
			}
			if _, err := tx.Exec("UPDATE " + table + " SET outcome = 'partial_success' WHERE outcome = 'partial'"); err != nil {
				return fmt.Errorf("backfill %s partial outcomes: %w", table, err)
			}
			if _, err := tx.Exec("UPDATE " + table + " SET updated_at = created_at WHERE updated_at = '' OR updated_at IS NULL"); err != nil {
				return fmt.Errorf("backfill %s updated_at: %w", table, err)
			}
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create store_metadata: %w", err)
	}
	if err := migrateLegacyVerificationsTx(tx, "episodes", "episode_verifications"); err != nil {
		return err
	}
	if err := migrateLegacyVerificationsTx(tx, "episodes_archive", "episode_verifications_archive"); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO store_metadata(key,value) VALUES('verification_migration_phase', 'verification_backfill_complete') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		return fmt.Errorf("record verification migration phase: %w", err)
	}
	var hasFTS int
	if err := tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'episodes_fts'").Scan(&hasFTS); err == nil && hasFTS > 0 {
		if _, err := tx.Exec("INSERT INTO episodes_fts(episodes_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("rebuild episodes fts after backfills: %w", err)
		}
	}
	var hasFaFTS int
	if err := tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'failed_approaches_fts'").Scan(&hasFaFTS); err == nil && hasFaFTS > 0 {
		if _, err := tx.Exec("INSERT INTO failed_approaches_fts(failed_approaches_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("rebuild failed_approaches fts after backfills: %w", err)
		}
	}
	var hasVerifFTS int
	if err := tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'verification_fts'").Scan(&hasVerifFTS); err == nil && hasVerifFTS > 0 {
		if _, err := tx.Exec("INSERT INTO verification_fts(verification_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("rebuild verification fts after backfills: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO store_metadata(key,value) VALUES('schema_migrations_phase', 'schema_migrations_complete') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		return fmt.Errorf("record schema migration phase: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO store_metadata(key,value) VALUES('verification_migration_phase', 'verification_backfill_complete') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		return fmt.Errorf("record verification migration phase: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS vector_reconcile (
		episode_id TEXT PRIMARY KEY,
		problem TEXT NOT NULL,
		thinking_trace TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		claim_owner TEXT NOT NULL DEFAULT '',
		claim_expires_at TEXT NOT NULL DEFAULT '',
		migration_version INTEGER NOT NULL DEFAULT 0,
		queue_generation INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		return fmt.Errorf("create vector_reconcile: %w", err)
	}
	hasOwnerCol := func() bool {
		rows, err := tx.Query("PRAGMA table_info(vector_reconcile)")
		if err != nil {
			return false
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notnull, pk int
			var colName, ctype string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &colName, &ctype, &notnull, &dflt, &pk); err == nil && colName == "claim_owner" {
				return true
			}
		}
		return false
	}
	hasMigCol := func() bool {
		rows, err := tx.Query("PRAGMA table_info(vector_reconcile)")
		if err != nil {
			return false
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notnull, pk int
			var colName, ctype string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &colName, &ctype, &notnull, &dflt, &pk); err == nil && colName == "migration_version" {
				return true
			}
		}
		return false
	}
	if !hasOwnerCol() {
		if _, err := tx.Exec("ALTER TABLE vector_reconcile ADD COLUMN claim_owner TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add claim_owner column: %w", err)
		}
		if _, err := tx.Exec("ALTER TABLE vector_reconcile ADD COLUMN claim_expires_at TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add claim_expires_at column: %w", err)
		}
	}
	if !hasMigCol() {
		if _, err := tx.Exec("ALTER TABLE vector_reconcile ADD COLUMN migration_version INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add migration_version column: %w", err)
		}
	}
	var hasGen int
	if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('vector_reconcile') WHERE name='queue_generation'").Scan(&hasGen); err != nil {
		return err
	}
	if hasGen == 0 {
		if _, err := tx.Exec("ALTER TABLE vector_reconcile ADD COLUMN queue_generation INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add queue_generation column: %w", err)
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create store_metadata: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO store_metadata(key,value) VALUES('vector_queue_generation', '0') ON CONFLICT(key) DO NOTHING`); err != nil {
		return fmt.Errorf("initialize vector queue generation: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO store_metadata(key,value) VALUES('vector_migration_generation', '0') ON CONFLICT(key) DO NOTHING`); err != nil {
		return fmt.Errorf("initialize vector migration generation: %w", err)
	}
	var vectorVersion int
	_ = tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM store_metadata WHERE key='vector_content_version'`).Scan(&vectorVersion)
	if vectorVersion < currentVectorContentVersion {
		if err := enqueueVectorMigrationTx(context.Background(), tx); err != nil {
			return fmt.Errorf("queue vector content migration: %w", err)
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS metadata_idx (
		episode_id TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create metadata_idx: %w", err)
	}
	if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_meta_kv ON metadata_idx(key, value)"); err != nil {
		return fmt.Errorf("create idx_meta_kv: %w", err)
	}
	if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_meta_eid ON metadata_idx(episode_id)"); err != nil {
		return fmt.Errorf("create idx_meta_eid: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS episode_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		episode_id TEXT NOT NULL,
		source_url TEXT NOT NULL,
		source_type TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		warning TEXT NOT NULL DEFAULT '',
		content_hash TEXT NOT NULL DEFAULT '',
		fetched_at TEXT NOT NULL DEFAULT '',
		truncated INTEGER NOT NULL DEFAULT 0,
		summary TEXT NOT NULL DEFAULT '',
		instructions TEXT NOT NULL DEFAULT '[]',
		acceptance_criteria TEXT NOT NULL DEFAULT '[]',
		constraints TEXT NOT NULL DEFAULT '[]',
		FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("create episode_sources: %w", err)
	}
	if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_episode_sources_eid ON episode_sources(episode_id)"); err != nil {
		return fmt.Errorf("create idx_episode_sources_eid: %w", err)
	}
	if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_failed_approaches_eid ON episode_failed_approaches(episode_id)"); err != nil {
		return fmt.Errorf("create idx_failed_approaches_eid: %w", err)
	}
	if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_failed_approaches_arch_eid ON episode_failed_approaches_archive(episode_id)"); err != nil {
		return fmt.Errorf("create idx_failed_approaches_arch_eid: %w", err)
	}
	if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_verifications_eid ON episode_verifications(episode_id)"); err != nil {
		return fmt.Errorf("create idx_verifications_eid: %w", err)
	}
	if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_verifications_arch_eid ON episode_verifications_archive(episode_id)"); err != nil {
		return fmt.Errorf("create idx_verifications_arch_eid: %w", err)
	}

	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS episodes_ai AFTER INSERT ON episodes BEGIN
			INSERT INTO episodes_fts(rowid, problem, thinking_trace, domain, outcome, tags)
			VALUES (new.rowid, new.problem, new.thinking_trace, new.domain, new.outcome, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS episodes_ad AFTER DELETE ON episodes BEGIN
			INSERT INTO episodes_fts(episodes_fts, rowid, problem, thinking_trace, domain, outcome, tags)
			VALUES ('delete', old.rowid, old.problem, old.thinking_trace, old.domain, old.outcome, old.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS episodes_au AFTER UPDATE ON episodes BEGIN
			INSERT INTO episodes_fts(episodes_fts, rowid, problem, thinking_trace, domain, outcome, tags)
			VALUES ('delete', old.rowid, old.problem, old.thinking_trace, old.domain, old.outcome, old.tags);
			INSERT INTO episodes_fts(rowid, problem, thinking_trace, domain, outcome, tags)
			VALUES (new.rowid, new.problem, new.thinking_trace, new.domain, new.outcome, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS failed_approaches_ai AFTER INSERT ON episode_failed_approaches BEGIN
			INSERT INTO failed_approaches_fts(rowid, episode_id, approach, failure_mode, root_cause, lesson)
			VALUES (new.id, new.episode_id, new.approach, new.failure_mode, new.root_cause, new.lesson);
		END`,
		`CREATE TRIGGER IF NOT EXISTS failed_approaches_ad AFTER DELETE ON episode_failed_approaches BEGIN
			INSERT INTO failed_approaches_fts(failed_approaches_fts, rowid, episode_id, approach, failure_mode, root_cause, lesson)
			VALUES ('delete', old.id, old.episode_id, old.approach, old.failure_mode, old.root_cause, old.lesson);
		END`,
		`CREATE TRIGGER IF NOT EXISTS failed_approaches_au AFTER UPDATE ON episode_failed_approaches BEGIN
			INSERT INTO failed_approaches_fts(failed_approaches_fts, rowid, episode_id, approach, failure_mode, root_cause, lesson)
			VALUES ('delete', old.id, old.episode_id, old.approach, old.failure_mode, old.root_cause, old.lesson);
			INSERT INTO failed_approaches_fts(rowid, episode_id, approach, failure_mode, root_cause, lesson)
			VALUES (new.id, new.episode_id, new.approach, new.failure_mode, new.root_cause, new.lesson);
		END`,
		`CREATE TRIGGER IF NOT EXISTS verification_ai AFTER INSERT ON episode_verifications BEGIN
			INSERT INTO verification_fts(rowid, episode_id, type, command, result, evidence)
			VALUES (new.id, new.episode_id, new.type, new.command, new.result, new.evidence);
		END`,
		`CREATE TRIGGER IF NOT EXISTS verification_ad AFTER DELETE ON episode_verifications BEGIN
			INSERT INTO verification_fts(verification_fts, rowid, episode_id, type, command, result, evidence)
			VALUES ('delete', old.id, old.episode_id, old.type, old.command, old.result, old.evidence);
		END`,
		`CREATE TRIGGER IF NOT EXISTS verification_au AFTER UPDATE ON episode_verifications BEGIN
			INSERT INTO verification_fts(verification_fts, rowid, episode_id, type, command, result, evidence)
			VALUES ('delete', old.id, old.episode_id, old.type, old.command, old.result, old.evidence);
			INSERT INTO verification_fts(rowid, episode_id, type, command, result, evidence)
			VALUES (new.id, new.episode_id, new.type, new.command, new.result, new.evidence);
		END`,
	}
	for _, trigger := range triggers {
		if _, err := tx.Exec(trigger); err != nil {
			return fmt.Errorf("create episodes FTS trigger: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if err := runMigrationPhase(db, "graph_migration_phase", "graph_migration_complete", migrateGraph); err != nil {
		return fmt.Errorf("migrate graph: %w", err)
	}
	if err := runMigrationPhase(db, "concept_migration_phase", "concept_migration_complete", migrateConcepts); err != nil {
		return fmt.Errorf("migrate concepts: %w", err)
	}
	return runMigrationPhase(db, "decision_migration_phase", "decision_migration_complete", migrateDecisions)
}

func runMigrationPhase(db *sql.DB, phaseKey, completeValue string, fn func(sqlExecutor) error) error {
	var current string
	_ = db.QueryRow("SELECT value FROM store_metadata WHERE key = ?", phaseKey).Scan(&current)
	if current == completeValue {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO store_metadata(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", phaseKey, completeValue); err != nil {
		return err
	}
	return tx.Commit()
}

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func migrateLegacyVerificationsTx(exec sqlExecutor, episodeTable, verificationTable string) error {
	if (episodeTable != "episodes" && episodeTable != "episodes_archive") || (verificationTable != "episode_verifications" && verificationTable != "episode_verifications_archive") {
		return fmt.Errorf("invalid verification migration tables")
	}
	rows, err := exec.Query(`SELECT id, verification, outcome FROM ` + episodeTable + ` WHERE NOT EXISTS (SELECT 1 FROM ` + verificationTable + ` WHERE episode_id = ` + episodeTable + `.id)`)
	if err != nil {
		return fmt.Errorf("query legacy %s verification: %w", episodeTable, err)
	}
	type legacyEpisode struct{ id, verification, outcome string }
	var episodes []legacyEpisode
	for rows.Next() {
		var episode legacyEpisode
		if err := rows.Scan(&episode.id, &episode.verification, &episode.outcome); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy %s verification: %w", episodeTable, err)
		}
		episodes = append(episodes, episode)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy %s verification rows: %w", episodeTable, err)
	}

	for _, episode := range episodes {
		records := legacyVerificationRecords(episode.verification)
		for i, record := range records {
			if _, err := exec.Exec(`INSERT INTO `+verificationTable+` (episode_id, position, type, command, result, success, evidence) VALUES (?, ?, ?, ?, ?, ?, ?)`, episode.id, i, record.Type, record.Command, record.Result, boolToInt(record.Success), record.Evidence); err != nil {
				return fmt.Errorf("backfill %s verification: %w", episodeTable, err)
			}
		}
		if episode.outcome == models.OutcomeVerifiedSuccess && !models.HasSuccessfulVerification(records) {
			note := models.VerificationRecord{Type: models.VerificationObservation, Result: security.Text("Legacy verification payload converted: Migration converted verified_success to unverified_success because no valid successful verification evidence was found. Raw: " + truncateRunes(episode.verification, 240)), Success: false}
			if _, err := exec.Exec(`INSERT INTO `+verificationTable+` (episode_id, position, type, command, result, success, evidence) VALUES (?, ?, ?, '', ?, 0, '')`, episode.id, len(records), note.Type, note.Result); err != nil {
				return fmt.Errorf("record %s verification correction: %w", episodeTable, err)
			}
			if _, err := exec.Exec(`UPDATE `+episodeTable+` SET outcome = ? WHERE id = ?`, models.OutcomeUnverifiedSuccess, episode.id); err != nil {
				return fmt.Errorf("correct %s verified outcome: %w", episodeTable, err)
			}
		}
	}
	return nil
}

func legacyVerificationRecords(raw string) []models.VerificationRecord {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "[]" || strings.TrimSpace(raw) == "null" {
		return nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		values = []json.RawMessage{json.RawMessage(raw)}
	}
	records := make([]models.VerificationRecord, 0, len(values))
	for _, value := range values {
		var record models.VerificationRecord
		if err := json.Unmarshal(value, &record); err == nil && record.Type != "" {
			if normalized, err := models.NormalizeVerificationRecords([]models.VerificationRecord{record}); err == nil {
				records = append(records, normalized[0])
				continue
			}
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			text = string(value)
		}
		text = security.Text(strings.TrimSpace(text))
		if text != "" && text != "null" {
			records = append(records, models.VerificationRecord{Type: models.VerificationObservation, Result: "Legacy verification payload converted: " + truncateRunes(text, 240)})
		}
	}
	return records
}

func detectGitRepo() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = wd
	out, err := cmd.Output()
	if err != nil {
		return filepath.Base(wd)
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return filepath.Base(wd)
	}
	if strings.Contains(url, "/") {
		parts := strings.Split(strings.TrimSuffix(url, ".git"), "/")
		return parts[len(parts)-1]
	}
	return url
}

func (es *EpisodeStore) Close() error {
	if es.CompactionCancel != nil {
		es.CompactionCancel()
	}
	if es.vec != nil {
		_ = es.vec.Close()
	}
	return es.db.Close()
}

func (es *EpisodeStore) Readiness() error {
	if err := es.db.Ping(); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if es.vec != nil && es.vec.Enabled() {
		if err := es.vec.Ready(); err != nil {
			return err
		}
		if err := es.ReconcileVectorStore(context.Background()); err != nil {
			return fmt.Errorf("reconcile vector store: %w", err)
		}
	}
	return nil
}

func (es *EpisodeStore) Shutdown() error {
	return es.Close()
}

func (es *EpisodeStore) CreateEpisode(ep *models.Episode) (string, error) {
	return es.createEpisode(context.Background(), ep)
}

func (es *EpisodeStore) createEpisode(ctx context.Context, ep *models.Episode) (string, error) {
	return es.createEpisodeWithSources(ctx, ep, nil)
}

func (es *EpisodeStore) createEpisodeWithSources(ctx context.Context, request *models.Episode, sources []linkcontent.Source) (string, error) {
	if request == nil {
		return "", fmt.Errorf("episode is required")
	}
	// This compatibility boundary accepts legacy outcomes without mutating the caller's canonical request.
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("copy episode: %w", err)
	}
	var value models.Episode
	if err := json.Unmarshal(encoded, &value); err != nil {
		return "", fmt.Errorf("copy episode: %w", err)
	}
	ep := &value
	normalizedOutcome, ok := models.NormalizeOutcome(ep.Outcome)
	if !ok {
		return "", fmt.Errorf("invalid outcome %q", ep.Outcome)
	}
	ep.Outcome = normalizedOutcome
	security.Episode(ep)
	if ep.CreatedAt.IsZero() {
		ep.CreatedAt = time.Now().UTC()
	}
	if ep.UpdatedAt.IsZero() {
		ep.UpdatedAt = ep.CreatedAt
	}
	if ep.Domain == "" {
		ep.Domain = "coding"
	}
	if ep.Tier == "" {
		ep.Tier = models.TierEpisodic
	}
	if err := ep.Validate(); err != nil {
		return "", err
	}

	stepsJSON, _ := json.Marshal(ep.Steps)
	toolCallsJSON, _ := json.Marshal(ep.ToolCalls)
	tagsJSON, _ := json.Marshal(ep.Tags)
	objectivesJSON, _ := json.Marshal(ep.Objectives)
	decisionsJSON, _ := json.Marshal(ep.Decisions)
	alternativesJSON, _ := json.Marshal(ep.Alternatives)
	verificationJSON, _ := json.Marshal(ep.Verification)
	lessonsJSON, _ := json.Marshal(ep.Lessons)

	if ep.Repo == "" {
		ep.Repo = detectGitRepo()
	}

	labels := ep.Labels
	if labels == nil {
		ec := EnrichCtx{
			Problem:       ep.Problem,
			ThinkingTrace: ep.ThinkingTrace,
			ToolCalls:     string(toolCallsJSON),
			Outcome:       string(ep.Outcome),
			Domain:        ep.Domain,
			ExistingTags:  ep.Tags,
			ExistingRepo:  ep.Repo,
		}
		labels = EnrichLabels(ec)
		ep.Labels = labels
	}
	labelsJSON, _ := json.Marshal(labels)

	tx, err := es.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin create episode tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var confidenceVal sql.NullFloat64
	if ep.Confidence != nil {
		confidenceVal = sql.NullFloat64{Float64: *ep.Confidence, Valid: true}
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO episodes (
			id, created_at, updated_at, domain, outcome, tier, tags, repo, project, provenance, confidence, labels, problem,
			objectives, decisions, alternatives, verification, lessons, thinking_trace, steps, tool_calls, model_id, duration_seconds
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ep.ID,
		ep.CreatedAt.Format(time.RFC3339),
		ep.UpdatedAt.Format(time.RFC3339),
		ep.Domain,
		string(ep.Outcome),
		string(ep.Tier),
		string(tagsJSON),
		ep.Repo,
		ep.Project,
		ep.Provenance,
		confidenceVal,
		string(labelsJSON),
		ep.Problem,
		string(objectivesJSON),
		string(decisionsJSON),
		string(alternativesJSON),
		string(verificationJSON),
		string(lessonsJSON),
		ep.ThinkingTrace,
		string(stepsJSON),
		string(toolCallsJSON),
		ep.ModelID,
		ep.DurationSeconds,
	)
	if err != nil {
		return "", fmt.Errorf("create episode: %w", err)
	}

	for i, verification := range ep.Verification {
		if _, err := tx.ExecContext(ctx, `INSERT INTO episode_verifications (episode_id, position, type, command, result, success, evidence) VALUES (?, ?, ?, ?, ?, ?, ?)`, ep.ID, i, string(verification.Type), verification.Command, verification.Result, boolToInt(verification.Success), verification.Evidence); err != nil {
			return "", fmt.Errorf("insert verification: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM metadata_idx WHERE episode_id = ?", ep.ID); err != nil {
		return "", fmt.Errorf("delete metadata_idx: %w", err)
	}
	for k, vs := range labels {
		for _, v := range vs {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO metadata_idx (episode_id, key, value) VALUES (?, ?, ?)",
				ep.ID, k, v,
			); err != nil {
				return "", fmt.Errorf("insert metadata_idx: %w", err)
			}
		}
	}

	for _, source := range sources {
		source.Summary = security.Text(source.Summary)
		source.Warning = security.Text(source.Warning)
		source.Instructions = security.Strings(source.Instructions)
		source.AcceptanceCriteria = security.Strings(source.AcceptanceCriteria)
		source.Constraints = security.Strings(source.Constraints)
		instructionsJSON, _ := json.Marshal(source.Instructions)
		acceptanceJSON, _ := json.Marshal(source.AcceptanceCriteria)
		constraintsJSON, _ := json.Marshal(source.Constraints)
		fetchedAt := ""
		if !source.FetchedAt.IsZero() {
			fetchedAt = source.FetchedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO episode_sources (episode_id, source_url, source_type, title, status, warning, content_hash, fetched_at, truncated, summary, instructions, acceptance_criteria, constraints)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ep.ID, source.SourceURL, source.SourceType, source.Title, source.Status, source.Warning, source.ContentHash, fetchedAt, boolToInt(source.Truncated), source.Summary, string(instructionsJSON), string(acceptanceJSON), string(constraintsJSON),
		); err != nil {
			return "", fmt.Errorf("insert episode source: %w", err)
		}
	}

	if err := insertFailedApproaches(ctx, tx, "episode_failed_approaches", ep.ID, ep.FailedApproaches); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit episode tx: %w", err)
	}

	if es.vec != nil && es.vec.Enabled() {
		if verr := es.vec.AddEpisode(ctx, ep.ID, ep.Problem, VectorContentText(ep)); verr != nil {
			if derr := es.DeleteEpisode(ep.ID); derr != nil {
				return "", errors.Join(
					fmt.Errorf("add episode vector: %w", verr),
					fmt.Errorf("compensate episode creation: %w", derr),
				)
			}
			return "", fmt.Errorf("add episode vector: %w", verr)
		}
	}

	return ep.ID, nil
}

func (es *EpisodeStore) CreateEpisodeContext(ctx context.Context, ep *models.Episode) (string, error) {
	return es.createEpisodeWithSources(ctx, ep, nil)
}

func (es *EpisodeStore) CreateEpisodeWithSourcesContext(ctx context.Context, ep *models.Episode, sources []linkcontent.Source) (string, error) {
	return es.createEpisodeWithSources(ctx, ep, sources)
}

func insertFailedApproaches(ctx context.Context, tx *sql.Tx, table, episodeID string, values []models.FailedApproach) error {
	if table != "episode_failed_approaches" && table != "episode_failed_approaches_archive" {
		return fmt.Errorf("invalid failed approaches table %q", table)
	}
	for i, value := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+` (episode_id, position, approach, failure_mode, root_cause, lesson) VALUES (?, ?, ?, ?, ?, ?)`, episodeID, i, value.Approach, value.FailureMode, value.RootCause, value.Lesson); err != nil {
			return fmt.Errorf("insert failed approach: %w", err)
		}
	}
	return nil
}

func (es *EpisodeStore) getVerifications(table, episodeID string) ([]models.VerificationRecord, error) {
	if table != "episode_verifications" && table != "episode_verifications_archive" {
		return nil, fmt.Errorf("invalid verifications table %q", table)
	}
	rows, err := es.db.Query(`SELECT type, command, result, success, evidence FROM `+table+` WHERE episode_id = ? ORDER BY position`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("get verifications: %w", err)
	}
	defer rows.Close()
	var values []models.VerificationRecord
	for rows.Next() {
		var value models.VerificationRecord
		var success int
		if err := rows.Scan(&value.Type, &value.Command, &value.Result, &success, &value.Evidence); err != nil {
			return nil, fmt.Errorf("scan verification: %w", err)
		}
		value.Success = success != 0
		values = append(values, value)
	}
	return values, rows.Err()
}

func (es *EpisodeStore) getFailedApproaches(table, episodeID string) ([]models.FailedApproach, error) {
	if table != "episode_failed_approaches" && table != "episode_failed_approaches_archive" {
		return nil, fmt.Errorf("invalid failed approaches table %q", table)
	}
	rows, err := es.db.Query(`SELECT approach, failure_mode, root_cause, lesson FROM `+table+` WHERE episode_id = ? ORDER BY position`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("get failed approaches: %w", err)
	}
	defer rows.Close()
	var values []models.FailedApproach
	for rows.Next() {
		var value models.FailedApproach
		if err := rows.Scan(&value.Approach, &value.FailureMode, &value.RootCause, &value.Lesson); err != nil {
			return nil, fmt.Errorf("scan failed approach: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func FailedApproachesText(values []models.FailedApproach) string {
	var b strings.Builder
	for _, value := range values {
		fmt.Fprintf(&b, "\nFailed approach: %s\nFailure mode: %s\nRoot cause: %s\nLesson: %s", value.Approach, value.FailureMode, value.RootCause, value.Lesson)
	}
	return b.String()
}

func VectorContentText(ep *models.Episode) string {
	if ep == nil {
		return ""
	}
	return ep.ThinkingTrace + FailedApproachesText(ep.FailedApproaches) + VerificationText(ep.Verification)
}

func VerificationText(values []models.VerificationRecord) string {
	var b strings.Builder
	for i, value := range values {
		if i == 3 {
			break
		}
		typ := security.Text(string(value.Type))
		result := security.Text(value.Result)
		evidence := security.Text(value.Evidence)
		fmt.Fprintf(&b, "\nVerification: type=%s success=%t", typ, value.Success)
		if result != "" {
			fmt.Fprintf(&b, " result=%s", truncateRunes(result, 240))
		}
		if evidence != "" {
			fmt.Fprintf(&b, " evidence=%s", truncateRunes(evidence, 240))
		}
	}
	return b.String()
}

func decodePersistedJSON(episodeID, field, raw string, destination any) error {
	if err := json.Unmarshal([]byte(raw), destination); err != nil {
		return fmt.Errorf("decode episode %s field %s: %w", episodeID, field, err)
	}
	return nil
}

func nextVectorQueueGeneration(ctx context.Context, tx *sql.Tx) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE store_metadata SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT) WHERE key='vector_queue_generation'`); err != nil {
		return 0, err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM store_metadata WHERE key='vector_queue_generation'`).Scan(&generation); err != nil {
		return 0, err
	}
	return generation, nil
}

func enqueueVectorReconcileTx(ctx context.Context, tx *sql.Tx, episodeID, problem, thinkingTrace string) error {
	generation, err := nextVectorQueueGeneration(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at, queue_generation) VALUES (?, ?, ?, ?, ?) ON CONFLICT(episode_id) DO UPDATE SET problem=excluded.problem, thinking_trace=excluded.thinking_trace, updated_at=excluded.updated_at, queue_generation=excluded.queue_generation`, episodeID, problem, thinkingTrace, time.Now().UTC().Format(time.RFC3339), generation)
	return err
}

func enqueueVectorMigrationTx(ctx context.Context, tx *sql.Tx) error {
	generation, err := nextVectorQueueGeneration(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO store_metadata(key,value) VALUES('vector_migration_generation', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf("%d", generation)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at, migration_version, queue_generation)
		SELECT id, problem, '', updated_at, ?, ? FROM episodes WHERE true
		ON CONFLICT(episode_id) DO UPDATE SET migration_version=max(vector_reconcile.migration_version, excluded.migration_version)`, currentVectorContentVersion, generation)
	return err
}

func enqueueVectorReconcileDB(ctx context.Context, db *sql.DB, episodeID, problem, thinkingTrace string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := enqueueVectorReconcileTx(ctx, tx, episodeID, problem, thinkingTrace); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteVectorReconcileDB(ctx context.Context, db *sql.DB, episodeID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteVectorReconcileTx(ctx, tx, episodeID); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteVectorReconcileTx(ctx context.Context, tx *sql.Tx, episodeID string) error {
	if _, err := nextVectorQueueGeneration(ctx, tx); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM vector_reconcile WHERE episode_id = ?", episodeID)
	return err
}

var ErrVectorReconciliationPending = errors.New("vector reconciliation pending")

func (es *EpisodeStore) ReconcileVectorStore(ctx context.Context) error {
	var vectorVersion int
	_ = es.db.QueryRowContext(ctx, "SELECT CAST(value AS INTEGER) FROM store_metadata WHERE key='vector_content_version'").Scan(&vectorVersion)
	if vectorVersion >= currentVectorContentVersion {
		var pending int
		if err := es.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM vector_reconcile").Scan(&pending); err == nil && pending == 0 {
			return nil
		}
	}
	for attempt := 0; attempt < 10; attempt++ {
		if err := es.reconcileVectorBatch(ctx); err != nil {
			return err
		}
		var applied int
		_ = es.db.QueryRowContext(ctx, "SELECT CAST(value AS INTEGER) FROM store_metadata WHERE key='vector_content_version'").Scan(&applied)
		if applied >= currentVectorContentVersion {
			var remaining int
			if err := es.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM vector_reconcile").Scan(&remaining); err == nil && remaining == 0 {
				return nil
			}
		}
	}
	return ErrVectorReconciliationPending
}

func (es *EpisodeStore) reconcileVectorBatch(ctx context.Context) error {
	if es.vec == nil || !es.vec.Enabled() {
		return nil
	}
	ownerID := fmt.Sprintf("%d_%d", os.Getpid(), time.Now().UnixNano())
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	expiresStr := now.Add(30 * time.Second).Format(time.RFC3339)

	rows, err := es.db.QueryContext(ctx, "SELECT episode_id, problem, thinking_trace FROM vector_reconcile WHERE claim_owner = '' OR claim_expires_at < ? ORDER BY updated_at ASC LIMIT 50", nowStr)
	if err != nil {
		return fmt.Errorf("query vector_reconcile: %w", err)
	}
	type pendingItem struct {
		id            string
		problem       string
		thinkingTrace string
	}
	var candidates []pendingItem
	for rows.Next() {
		var item pendingItem
		if err := rows.Scan(&item.id, &item.problem, &item.thinkingTrace); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan vector_reconcile: %w", err)
		}
		candidates = append(candidates, item)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var claimed []pendingItem
	for _, item := range candidates {
		res, err := es.db.ExecContext(ctx, "UPDATE vector_reconcile SET claim_owner = ?, claim_expires_at = ? WHERE episode_id = ? AND (claim_owner = '' OR claim_expires_at < ?)", ownerID, expiresStr, item.id, nowStr)
		if err != nil {
			continue
		}
		if rowsAffected, err := res.RowsAffected(); err == nil && rowsAffected == 1 {
			claimed = append(claimed, item)
		}
	}

	var reconciliationErrs []error
	for _, item := range claimed {
		var verr error
		if item.problem == "" && item.thinkingTrace == "" {
			verr = es.vec.DeleteEpisode(ctx, item.id)
		} else {
			ep, getErr := es.GetEpisode(item.id)
			if getErr != nil || ep == nil {
				verr = es.vec.DeleteEpisode(ctx, item.id)
			} else {
				verr = es.vec.ReplaceEpisode(ctx, ep.ID, ep.Problem, VectorContentText(ep))
			}
		}
		if verr != nil {
			reconciliationErrs = append(reconciliationErrs, fmt.Errorf("reconcile vector episode %s: %w", item.id, verr))
			_, _ = es.db.ExecContext(ctx, "UPDATE vector_reconcile SET claim_owner = '', claim_expires_at = '' WHERE episode_id = ? AND claim_owner = ?", item.id, ownerID)
		} else {
			if _, err := es.db.ExecContext(ctx, "DELETE FROM vector_reconcile WHERE episode_id = ? AND claim_owner = ?", item.id, ownerID); err != nil {
				reconciliationErrs = append(reconciliationErrs, fmt.Errorf("clear vector_reconcile %s: %w", item.id, err))
			}
		}
	}

	if len(reconciliationErrs) == 0 {
		for attempt := 0; attempt < 5; attempt++ {
			tx, err := es.db.BeginTx(ctx, nil)
			if err != nil {
				if strings.Contains(err.Error(), "database is locked") {
					time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
					continue
				}
				return err
			}
			var targetGen int64
			_ = tx.QueryRowContext(ctx, "SELECT CAST(value AS INTEGER) FROM store_metadata WHERE key='vector_migration_generation'").Scan(&targetGen)
			var currentGen int64
			_ = tx.QueryRowContext(ctx, "SELECT CAST(value AS INTEGER) FROM store_metadata WHERE key='vector_queue_generation'").Scan(&currentGen)

			var relCount int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM vector_reconcile WHERE migration_version = ? OR queue_generation > ?", currentVectorContentVersion, targetGen).Scan(&relCount); err != nil {
				_ = tx.Rollback()
				return err
			}

			if targetGen > 0 && relCount == 0 && currentGen == targetGen {
				if _, err := tx.ExecContext(ctx, `INSERT INTO store_metadata(key,value) VALUES('vector_content_version', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, currentVectorContentVersion); err != nil {
					_ = tx.Rollback()
					if strings.Contains(err.Error(), "database is locked") {
						time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
						continue
					}
					return fmt.Errorf("record vector content version: %w", err)
				}
			} else if targetGen > 0 && currentGen != targetGen {
				if err := enqueueVectorMigrationTx(ctx, tx); err != nil {
					_ = tx.Rollback()
					return fmt.Errorf("refresh migration target: %w", err)
				}
			}

			if err := tx.Commit(); err != nil {
				if strings.Contains(err.Error(), "database is locked") {
					time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
					continue
				}
				return err
			}
			break
		}
	}
	return errors.Join(reconciliationErrs...)
}

func (es *EpisodeStore) GetEpisode(id string) (*models.Episode, error) {
	return es.getEpisodeFrom("episodes", id)
}

func (es *EpisodeStore) getEpisodeFrom(table, id string) (*models.Episode, error) {
	if table != "episodes" && table != "episodes_archive" {
		return nil, fmt.Errorf("invalid episode table %q", table)
	}
	row := es.db.QueryRow(`SELECT id, created_at, updated_at, domain, outcome, tier, tags, repo, project, provenance, confidence,
		labels, problem, objectives, decisions, alternatives, verification, lessons, thinking_trace, steps, tool_calls, model_id, duration_seconds
		FROM `+table+` WHERE id = ?`, id)
	var ep models.Episode
	var createdAt, updatedAt, tier, tagsJSON, labelsJSON, objectivesJSON, decisionsJSON, alternativesJSON, verificationJSON, lessonsJSON, stepsJSON, toolCallsJSON string
	var confidence sql.NullFloat64
	if err := row.Scan(&ep.ID, &createdAt, &updatedAt, &ep.Domain, &ep.Outcome, &tier, &tagsJSON, &ep.Repo, &ep.Project, &ep.Provenance, &confidence,
		&labelsJSON, &ep.Problem, &objectivesJSON, &decisionsJSON, &alternativesJSON, &verificationJSON, &lessonsJSON, &ep.ThinkingTrace,
		&stepsJSON, &toolCallsJSON, &ep.ModelID, &ep.DurationSeconds); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get episode: %w", err)
	}
	var err error
	ep.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("decode episode %s field created_at: %w", ep.ID, err)
	}
	if updatedAt == "" {
		ep.UpdatedAt = ep.CreatedAt
	} else {
		ep.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("decode episode %s field updated_at: %w", ep.ID, err)
		}
	}
	ep.Tier = models.MemoryTier(tier)
	if confidence.Valid {
		ep.Confidence = &confidence.Float64
	}
	labels, err := es.parseLabelsJSONErr(ep.ID, labelsJSON)
	if err != nil {
		return nil, err
	}
	ep.Labels = labels
	for field, item := range map[string]struct {
		raw         string
		destination any
	}{
		"tags":         {tagsJSON, &ep.Tags},
		"objectives":   {objectivesJSON, &ep.Objectives},
		"decisions":    {decisionsJSON, &ep.Decisions},
		"alternatives": {alternativesJSON, &ep.Alternatives},

		"lessons":    {lessonsJSON, &ep.Lessons},
		"steps":      {stepsJSON, &ep.Steps},
		"tool_calls": {toolCallsJSON, &ep.ToolCalls},
	} {
		if err := decodePersistedJSON(ep.ID, field, item.raw, item.destination); err != nil {
			return nil, err
		}
	}
	failedTable := "episode_failed_approaches"
	verifTable := "episode_verifications"
	if table == "episodes_archive" {
		failedTable = "episode_failed_approaches_archive"
		verifTable = "episode_verifications_archive"
	}
	failed, err := es.getFailedApproaches(failedTable, ep.ID)
	if err != nil {
		return nil, err
	}
	ep.FailedApproaches = failed
	verifRecords, err := es.getVerifications(verifTable, ep.ID)
	if err != nil {
		return nil, err
	}
	ep.Verification = verifRecords
	security.Episode(&ep)
	return &ep, nil
}

func (es *EpisodeStore) GetArchivedEpisode(id string) (*models.Episode, error) {
	return es.getEpisodeFrom("episodes_archive", id)
}

func (es *EpisodeStore) GetSummary(id string) (*models.EpisodeSummary, error) {
	row := es.db.QueryRow(
		`SELECT id, created_at, updated_at, problem, domain, outcome, tier, tags, repo, project, provenance, confidence, labels, steps, tool_calls, model_id, duration_seconds
		FROM episodes WHERE id = ?`, id,
	)

	var (
		tagsJSON      string
		labelsJSON    string
		stepsJSON     string
		toolCallsJSON string
		createdAt     string
		updatedAt     string
		confidence    sql.NullFloat64
		steps         []models.Step
		summary       models.EpisodeSummary
		tier          string
	)

	err := row.Scan(
		&summary.ID, &createdAt, &updatedAt, &summary.Problem, &summary.Domain,
		&summary.Outcome, &tier, &tagsJSON, &summary.Repo, &summary.Project, &summary.Provenance, &confidence, &labelsJSON, &stepsJSON, &toolCallsJSON,
		&summary.ModelID, &summary.DurationSeconds,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get summary: %w", err)
	}

	if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, fmt.Errorf("decode episode %s field created_at: %w", summary.ID, err)
	}
	if updatedAt != "" {
		if _, err := time.Parse(time.RFC3339, updatedAt); err != nil {
			return nil, fmt.Errorf("decode episode %s field updated_at: %w", summary.ID, err)
		}
	}
	summary.CreatedAt = createdAt
	summary.UpdatedAt = updatedAt
	if confidence.Valid {
		summary.Confidence = &confidence.Float64
	}
	summary.Tier = models.MemoryTier(tier)
	labels, err := es.parseLabelsJSONErr(summary.ID, labelsJSON)
	if err != nil {
		return nil, err
	}
	summary.Labels = labels
	if err := decodePersistedJSON(summary.ID, "tags", tagsJSON, &summary.Tags); err != nil {
		return nil, err
	}
	if err := decodePersistedJSON(summary.ID, "steps", stepsJSON, &steps); err != nil {
		return nil, err
	}
	summary.StepCount = len(steps)
	for _, s := range steps {
		summary.StepTypes = append(summary.StepTypes, s.Type)
	}
	var toolCalls []models.ToolCall
	if err := decodePersistedJSON(summary.ID, "tool_calls", toolCallsJSON, &toolCalls); err != nil {
		return nil, err
	}
	summary.ToolCount = len(toolCalls)
	verifSummary, err := es.verificationSummary(summary.ID)
	if err != nil {
		return nil, err
	}
	summary.VerificationCount = verifSummary.VerificationCount
	summary.SuccessfulVerificationCount = verifSummary.SuccessfulVerificationCount
	summary.VerificationTypes = verifSummary.VerificationTypes
	summary.VerificationSummaries = verifSummary.VerificationSummaries
	security.Summary(&summary)

	return &summary, nil
}

func (es *EpisodeStore) ListEpisodes(limit, offset int) ([]models.EpisodeSummary, error) {
	rows, err := es.db.Query(
		`SELECT id, created_at, updated_at, problem, domain, outcome, tier, tags, repo, project, provenance, confidence, labels, steps, tool_calls, model_id, duration_seconds
		FROM episodes ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list episodes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []models.EpisodeSummary
	for rows.Next() {
		var tagsJSON, labelsJSON, stepsJSON, toolCallsJSON, tier string
		var confidence sql.NullFloat64
		var steps []models.Step
		var s models.EpisodeSummary
		if err := rows.Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt, &s.Problem, &s.Domain, &s.Outcome, &tier, &tagsJSON, &s.Repo, &s.Project, &s.Provenance, &confidence, &labelsJSON, &stepsJSON, &toolCallsJSON, &s.ModelID, &s.DurationSeconds); err != nil {
			return nil, fmt.Errorf("scan episode: %w", err)
		}
		if _, err := time.Parse(time.RFC3339, s.CreatedAt); err != nil {
			return nil, fmt.Errorf("decode episode %s field created_at: %w", s.ID, err)
		}
		if s.UpdatedAt != "" {
			if _, err := time.Parse(time.RFC3339, s.UpdatedAt); err != nil {
				return nil, fmt.Errorf("decode episode %s field updated_at: %w", s.ID, err)
			}
		}
		if confidence.Valid {
			s.Confidence = &confidence.Float64
		}
		s.Tier = models.MemoryTier(tier)
		labels, err := es.parseLabelsJSONErr(s.ID, labelsJSON)
		if err != nil {
			return nil, err
		}
		s.Labels = labels
		if err := decodePersistedJSON(s.ID, "tags", tagsJSON, &s.Tags); err != nil {
			return nil, err
		}
		if err := decodePersistedJSON(s.ID, "steps", stepsJSON, &steps); err != nil {
			return nil, err
		}
		s.StepCount = len(steps)
		for _, st := range steps {
			s.StepTypes = append(s.StepTypes, st.Type)
		}
		var toolCalls []models.ToolCall
		if err := decodePersistedJSON(s.ID, "tool_calls", toolCallsJSON, &toolCalls); err != nil {
			return nil, err
		}
		s.ToolCount = len(toolCalls)
		security.Summary(&s)
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

func (es *EpisodeStore) UpdateEpisode(ctx context.Context, request *models.Episode) (returnErr error) {
	if request == nil || strings.TrimSpace(request.ID) == "" {
		return fmt.Errorf("valid episode with ID is required")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("copy episode: %w", err)
	}
	var value models.Episode
	if err := json.Unmarshal(encoded, &value); err != nil {
		return fmt.Errorf("copy episode: %w", err)
	}
	ep := &value
	normalizedOutcome, ok := models.NormalizeOutcome(ep.Outcome)
	if !ok {
		return fmt.Errorf("invalid outcome %q", ep.Outcome)
	}
	ep.Outcome = normalizedOutcome
	security.Episode(ep)
	ep.UpdatedAt = time.Now().UTC()
	if err := ep.Validate(); err != nil {
		return err
	}
	encode := func(value any) string { data, _ := json.Marshal(value); return string(data) }
	labels := ep.Labels
	if labels == nil {
		labels = EnrichLabels(EnrichCtx{Problem: ep.Problem, ThinkingTrace: ep.ThinkingTrace, ToolCalls: encode(ep.ToolCalls), Outcome: string(ep.Outcome), Domain: ep.Domain, ExistingTags: ep.Tags, ExistingRepo: ep.Repo})
		ep.Labels = labels
	}
	var confidence sql.NullFloat64
	if ep.Confidence != nil {
		confidence = sql.NullFloat64{Float64: *ep.Confidence, Valid: true}
	}
	oldEp, err := es.GetEpisode(ep.ID)
	if err != nil {
		return fmt.Errorf("get existing episode before update: %w", err)
	}
	if err := models.ValidateOutcomeTransition(oldEp, ep); err != nil {
		return err
	}
	tx, err := es.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update episode tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE episodes SET updated_at=?, domain=?, outcome=?, tier=?, tags=?, repo=?, project=?, provenance=?, confidence=?, labels=?, problem=?, objectives=?, decisions=?, alternatives=?, verification=?, lessons=?, thinking_trace=?, steps=?, tool_calls=?, model_id=?, duration_seconds=? WHERE id=?`, ep.UpdatedAt.Format(time.RFC3339), ep.Domain, string(ep.Outcome), string(ep.Tier), encode(ep.Tags), ep.Repo, ep.Project, ep.Provenance, confidence, encode(labels), ep.Problem, encode(ep.Objectives), encode(ep.Decisions), encode(ep.Alternatives), encode(ep.Verification), encode(ep.Lessons), ep.ThinkingTrace, encode(ep.Steps), encode(ep.ToolCalls), ep.ModelID, ep.DurationSeconds, ep.ID)
	if err != nil {
		return fmt.Errorf("update episode: %w", err)
	}
	if count, err := res.RowsAffected(); err != nil || count == 0 {
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		return fmt.Errorf("episode not found: %s", ep.ID)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM metadata_idx WHERE episode_id = ?", ep.ID); err != nil {
		return fmt.Errorf("delete metadata_idx: %w", err)
	}
	for key, values := range labels {
		for _, value := range values {
			if _, err := tx.ExecContext(ctx, "INSERT INTO metadata_idx (episode_id, key, value) VALUES (?, ?, ?)", ep.ID, key, value); err != nil {
				return fmt.Errorf("insert metadata_idx: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM episode_failed_approaches WHERE episode_id = ?", ep.ID); err != nil {
		return fmt.Errorf("replace failed approaches: %w", err)
	}
	if err := insertFailedApproaches(ctx, tx, "episode_failed_approaches", ep.ID, ep.FailedApproaches); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM episode_verifications WHERE episode_id = ?", ep.ID); err != nil {
		return fmt.Errorf("replace verifications: %w", err)
	}
	for i, verification := range ep.Verification {
		if _, err := tx.ExecContext(ctx, `INSERT INTO episode_verifications (episode_id, position, type, command, result, success, evidence) VALUES (?, ?, ?, ?, ?, ?, ?)`, ep.ID, i, string(verification.Type), verification.Command, verification.Result, boolToInt(verification.Success), verification.Evidence); err != nil {
			return fmt.Errorf("insert verification: %w", err)
		}
	}
	vectorTrace := VectorContentText(ep)
	if es.vec != nil && es.vec.Enabled() {
		if err := enqueueVectorReconcileTx(ctx, tx, ep.ID, ep.Problem, vectorTrace); err != nil {
			return fmt.Errorf("insert vector_reconcile: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update episode tx: %w", err)
	}
	if es.vec != nil && es.vec.Enabled() {
		if verr := es.vec.ReplaceEpisode(ctx, ep.ID, ep.Problem, vectorTrace); verr != nil {
			var compensation []error
			if oldEp != nil {
				oldVectorTrace := VectorContentText(oldEp)
				if err := es.restoreEpisodeDB(ctx, oldEp); err != nil {
					compensation = append(compensation, fmt.Errorf("restore previous episode database: %w", err))
				} else if _, err := es.db.ExecContext(ctx, `UPDATE vector_reconcile SET problem=?, thinking_trace=?, updated_at=? WHERE episode_id=?`, oldEp.Problem, oldVectorTrace, time.Now().UTC().Format(time.RFC3339), oldEp.ID); err != nil {
					compensation = append(compensation, fmt.Errorf("persist previous vector reconciliation: %w", err))
				}
				if err := es.vec.ReplaceEpisode(ctx, oldEp.ID, oldEp.Problem, oldVectorTrace); err != nil {
					compensation = append(compensation, fmt.Errorf("restore previous episode vector: %w", err))
				}
			}
			return errors.Join(append([]error{fmt.Errorf("update episode vector: %w", verr)}, compensation...)...)
		}
		if _, err := es.db.ExecContext(ctx, "DELETE FROM vector_reconcile WHERE episode_id = ?", ep.ID); err != nil {
			return fmt.Errorf("vector updated but clear vector_reconcile failed for %s: %w", ep.ID, err)
		}
	}
	return nil
}

func (es *EpisodeStore) restoreEpisodeDB(ctx context.Context, ep *models.Episode) error {
	encode := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	var confidence sql.NullFloat64
	if ep.Confidence != nil {
		confidence = sql.NullFloat64{Float64: *ep.Confidence, Valid: true}
	}
	tx, err := es.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE episodes SET created_at=?, updated_at=?, domain=?, outcome=?, tier=?, tags=?, repo=?, project=?, provenance=?, confidence=?, labels=?, problem=?, objectives=?, decisions=?, alternatives=?, verification=?, lessons=?, thinking_trace=?, steps=?, tool_calls=?, model_id=?, duration_seconds=? WHERE id=?`,
		ep.CreatedAt.Format(time.RFC3339), ep.UpdatedAt.Format(time.RFC3339), ep.Domain, string(ep.Outcome), string(ep.Tier), encode(ep.Tags), ep.Repo, ep.Project, ep.Provenance, confidence, encode(ep.Labels), ep.Problem, encode(ep.Objectives), encode(ep.Decisions), encode(ep.Alternatives), encode(ep.Verification), encode(ep.Lessons), ep.ThinkingTrace, encode(ep.Steps), encode(ep.ToolCalls), ep.ModelID, ep.DurationSeconds, ep.ID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM metadata_idx WHERE episode_id = ?", ep.ID); err != nil {
		return err
	}
	for key, values := range ep.Labels {
		for _, value := range values {
			if _, err := tx.ExecContext(ctx, "INSERT INTO metadata_idx (episode_id, key, value) VALUES (?, ?, ?)", ep.ID, key, value); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM episode_failed_approaches WHERE episode_id = ?", ep.ID); err != nil {
		return err
	}
	if err := insertFailedApproaches(ctx, tx, "episode_failed_approaches", ep.ID, ep.FailedApproaches); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM episode_verifications WHERE episode_id = ?", ep.ID); err != nil {
		return err
	}
	for i, verification := range ep.Verification {
		if _, err := tx.ExecContext(ctx, `INSERT INTO episode_verifications (episode_id, position, type, command, result, success, evidence) VALUES (?, ?, ?, ?, ?, ?, ?)`, ep.ID, i, string(verification.Type), verification.Command, verification.Result, boolToInt(verification.Success), verification.Evidence); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (es *EpisodeStore) DeleteEpisode(id string) error {
	tx, err := es.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete episode: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("DELETE FROM episode_sources WHERE episode_id = ?", id); err != nil {
		return fmt.Errorf("delete episode sources: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM metadata_idx WHERE episode_id = ?", id); err != nil {
		return fmt.Errorf("delete episode metadata_idx: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM episode_failed_approaches WHERE episode_id = ?", id); err != nil {
		return fmt.Errorf("delete episode failed approaches: %w", err)
	}
	if err := deleteVectorReconcileTx(context.Background(), tx, id); err != nil {
		return fmt.Errorf("delete vector_reconcile: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM episodes WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete episode: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete episode: %w", err)
	}
	if es.vec != nil && es.vec.Enabled() {
		if verr := es.vec.DeleteEpisode(context.Background(), id); verr != nil {
			if err := enqueueVectorReconcileDB(context.Background(), es.db, id, "", ""); err != nil {
				return errors.Join(
					fmt.Errorf("delete episode vector: %w", verr),
					fmt.Errorf("enqueue vector deletion reconciliation: %w", err),
				)
			}
		}
	}
	return nil
}

func (es *EpisodeStore) EpisodeCount() (int, error) {
	var count int
	err := es.db.QueryRow("SELECT COUNT(*) FROM episodes").Scan(&count)
	return count, err
}

func (es *EpisodeStore) VectorStore() *VectorStore {
	return es.vec
}

func (es *EpisodeStore) DB() *sql.DB {
	return es.db
}

func (es *EpisodeStore) DeletePattern(id string) error {
	_, err := es.db.Exec("DELETE FROM patterns WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete pattern: %w", err)
	}
	return nil
}

func (es *EpisodeStore) PersistEpisodeSources(ctx context.Context, episodeID string, sources []linkcontent.Source) error {
	if episodeID == "" || len(sources) == 0 {
		return nil
	}
	tx, err := es.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, source := range sources {
		instructionsJSON, _ := json.Marshal(source.Instructions)
		acceptanceJSON, _ := json.Marshal(source.AcceptanceCriteria)
		constraintsJSON, _ := json.Marshal(source.Constraints)
		fetchedAt := ""
		if !source.FetchedAt.IsZero() {
			fetchedAt = source.FetchedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO episode_sources (episode_id, source_url, source_type, title, status, warning, content_hash, fetched_at, truncated, summary, instructions, acceptance_criteria, constraints) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			episodeID, source.SourceURL, source.SourceType, source.Title, source.Status, source.Warning, source.ContentHash, fetchedAt, boolToInt(source.Truncated), source.Summary, string(instructionsJSON), string(acceptanceJSON), string(constraintsJSON),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (es *EpisodeStore) ListEpisodeSources(episodeID string) ([]linkcontent.Source, error) {
	rows, err := es.db.Query(`SELECT source_url, source_type, title, status, warning, content_hash, fetched_at, truncated, summary, instructions, acceptance_criteria, constraints FROM episode_sources WHERE episode_id = ? ORDER BY id`, episodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []linkcontent.Source
	for rows.Next() {
		var (
			source      linkcontent.Source
			fetchedAt   string
			truncated   int
			instJSON    string
			acceptJSON  string
			constrainJS string
		)
		if err := rows.Scan(&source.SourceURL, &source.SourceType, &source.Title, &source.Status, &source.Warning, &source.ContentHash, &fetchedAt, &truncated, &source.Summary, &instJSON, &acceptJSON, &constrainJS); err != nil {
			return nil, err
		}
		if fetchedAt != "" {
			if t, err := time.Parse(time.RFC3339, fetchedAt); err == nil {
				source.FetchedAt = t
			}
		}
		source.Truncated = truncated != 0
		_ = json.Unmarshal([]byte(instJSON), &source.Instructions)
		_ = json.Unmarshal([]byte(acceptJSON), &source.AcceptanceCriteria)
		_ = json.Unmarshal([]byte(constrainJS), &source.Constraints)
		if source.Instructions == nil {
			source.Instructions = []string{}
		}
		if source.AcceptanceCriteria == nil {
			source.AcceptanceCriteria = []string{}
		}
		if source.Constraints == nil {
			source.Constraints = []string{}
		}
		out = append(out, source)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (es *EpisodeStore) ReindexFTS5() error {
	_, err := es.db.Exec("INSERT INTO episodes_fts(episodes_fts) VALUES('rebuild')")
	if err != nil {
		return fmt.Errorf("reindex fts5: %w", err)
	}
	return nil
}

func (es *EpisodeStore) DBPath() string {
	return es.dbPath
}

func (es *EpisodeStore) EpisodesByDomain() (map[string]int, error) {
	rows, err := es.db.Query("SELECT domain, COUNT(*) FROM episodes GROUP BY domain")
	if err != nil {
		return nil, fmt.Errorf("episodes by domain: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var domain string
		var count int
		if err := rows.Scan(&domain, &count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result[domain] = count
	}
	return result, rows.Err()
}

func (es *EpisodeStore) EpisodesByOutcome() (map[string]int, error) {
	rows, err := es.db.Query("SELECT outcome, COUNT(*) FROM episodes GROUP BY outcome")
	if err != nil {
		return nil, fmt.Errorf("episodes by outcome: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var outcome string
		var count int
		if err := rows.Scan(&outcome, &count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result[outcome] = count
	}
	return result, rows.Err()
}

func (es *EpisodeStore) EpisodesByRepo() (map[string]int, error) {
	rows, err := es.db.Query("SELECT repo, COUNT(*) FROM episodes WHERE repo != '' GROUP BY repo ORDER BY COUNT(*) DESC")
	if err != nil {
		return nil, fmt.Errorf("episodes by repo: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var repo string
		var count int
		if err := rows.Scan(&repo, &count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result[repo] = count
	}
	return result, rows.Err()
}

func (es *EpisodeStore) TopTags(limit int) ([]models.TagCount, error) {
	rows, err := es.db.Query("SELECT tags FROM episodes")
	if err != nil {
		return nil, fmt.Errorf("top tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	freq := make(map[string]int)
	for rows.Next() {
		var tagsJSON string
		if err := rows.Scan(&tagsJSON); err != nil {
			continue
		}
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			continue
		}
		for _, t := range tags {
			freq[t]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var tc []models.TagCount
	for tag, count := range freq {
		tc = append(tc, models.TagCount{Tag: tag, Count: count})
	}

	sort.Slice(tc, func(i, j int) bool {
		return tc[i].Count > tc[j].Count
	})
	if limit > 0 && len(tc) > limit {
		tc = tc[:limit]
	}
	return tc, nil
}

func (es *EpisodeStore) AvgEpisodeLengths() (avgProblem, avgTrace float64, err error) {
	err = es.db.QueryRow(
		"SELECT COALESCE(AVG(LENGTH(problem)),0), COALESCE(AVG(LENGTH(thinking_trace)),0) FROM episodes",
	).Scan(&avgProblem, &avgTrace)
	if err != nil {
		return 0, 0, fmt.Errorf("avg lengths: %w", err)
	}
	return
}

func (es *EpisodeStore) EmptyThinkingTraceCount() (int, error) {
	var count int
	err := es.db.QueryRow("SELECT COUNT(*) FROM episodes WHERE thinking_trace = ''").Scan(&count)
	return count, err
}

func (es *EpisodeStore) DBSizeMB() (float64, error) {
	info, err := os.Stat(es.dbPath)
	if err != nil {
		return 0, fmt.Errorf("db stat: %w", err)
	}
	return float64(info.Size()) / 1024 / 1024, nil
}

func (es *EpisodeStore) FTSSizeMB() (float64, error) {
	var size float64
	err := es.db.QueryRow(
		"SELECT COALESCE(SUM(pgsize), 0) FROM dbstat WHERE name LIKE 'episodes_fts%'",
	).Scan(&size)
	if err != nil {
		return 0, nil
	}
	return size / 1024 / 1024, nil
}

func (es *EpisodeStore) LastConsolidationTS() (*time.Time, error) {
	var ts string
	err := es.db.QueryRow("SELECT MAX(created_at) FROM patterns").Scan(&ts)
	if err != nil || ts == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return nil, nil
	}
	return &t, nil
}

func (es *EpisodeStore) EpisodesByDay(days int) ([]models.DayBucket, error) {
	rows, err := es.db.Query(
		`SELECT date(created_at) as d, COUNT(*) as cnt,
		        COUNT(CASE WHEN outcome IN ('success', 'verified_success', 'unverified_success') THEN 1 END) as ok,
		        COALESCE(AVG(duration_seconds),0),
		        COALESCE(AVG(LENGTH(thinking_trace)),0)
		 FROM episodes
		 WHERE created_at >= date('now', ?)
		 GROUP BY d ORDER BY d`,
		fmt.Sprintf("-%d days", days),
	)
	if err != nil {
		return nil, fmt.Errorf("episodes by day: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var buckets []models.DayBucket
	for rows.Next() {
		var b models.DayBucket
		if err := rows.Scan(&b.Date, &b.Count, &b.Successes, &b.AvgDuration, &b.AvgTraceLen); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

func (es *EpisodeStore) SummaryStats() (*models.SummaryStats, error) {
	stats := &models.SummaryStats{}

	total, err := es.EpisodeCount()
	if err != nil {
		return nil, err
	}
	stats.TotalEpisodes = total

	patCount, err := es.PatternCount()
	if err != nil {
		return nil, err
	}
	stats.TotalPatterns = patCount

	if total > 0 {
		var successCount int
		_ = es.db.QueryRow("SELECT COUNT(*) FROM episodes WHERE outcome IN ('success', 'verified_success', 'unverified_success')").Scan(&successCount)
		stats.SuccessRate = float64(successCount) / float64(total) * 100
	}

	var avgDur, avgTrace float64
	_ = es.db.QueryRow(
		"SELECT COALESCE(AVG(duration_seconds),0), COALESCE(AVG(LENGTH(thinking_trace)),0) FROM episodes",
	).Scan(&avgDur, &avgTrace)
	stats.AvgDurationSec = avgDur
	stats.AvgTraceLenChars = avgTrace

	if stats.TotalPatterns > 0 && stats.TotalEpisodes > 0 {
		var patternSourced int
		_ = es.db.QueryRow(`
			SELECT COALESCE(SUM(json_array_length(sources)), 0)
			FROM patterns`).Scan(&patternSourced)
		if stats.TotalEpisodes > 0 {
			stats.ConsolidationRatio = math.Min(
				float64(patternSourced)/float64(stats.TotalEpisodes)*100, 100)
		}
	}

	var topDomain string
	_ = es.db.QueryRow(`
		SELECT domain FROM episodes
		GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 1`,
	).Scan(&topDomain)
	stats.TopDomain = topDomain

	var topRepo string
	_ = es.db.QueryRow(`
		SELECT repo FROM episodes WHERE repo != ''
		GROUP BY repo ORDER BY COUNT(*) DESC LIMIT 1`,
	).Scan(&topRepo)
	stats.TopRepo = topRepo

	var topLabelKey string
	_ = es.db.QueryRow(`
		SELECT key FROM metadata_idx
		GROUP BY key ORDER BY COUNT(DISTINCT episode_id) DESC LIMIT 1`,
	).Scan(&topLabelKey)
	stats.TopLabelKey = topLabelKey

	var labelCard int
	_ = es.db.QueryRow(`SELECT COUNT(DISTINCT key) FROM metadata_idx`).Scan(&labelCard)
	stats.LabelCardinality = labelCard

	unlabeled, _ := es.UnlabeledCount()
	stats.UnlabeledCount = unlabeled

	var archivedCount int
	_ = es.db.QueryRow("SELECT COUNT(*) FROM episodes_archive").Scan(&archivedCount)
	stats.TotalArchived = archivedCount

	var prunedCount int
	_ = es.db.QueryRow("SELECT COALESCE(value, 0) FROM compaction_stats WHERE key = 'pruned_count'").Scan(&prunedCount)
	stats.TotalPruned = prunedCount

	return stats, nil
}

func (es *EpisodeStore) NextID() string {
	now := time.Now().UTC().Format("20060102")
	prefix := fmt.Sprintf("re-%s-", now)

	var maxSeq int
	err := es.db.QueryRow(
		`SELECT COALESCE(CAST(SUBSTR(id, -3) AS INTEGER), 0)
		 FROM episodes WHERE id LIKE ? ORDER BY id DESC LIMIT 1`,
		prefix+"%",
	).Scan(&maxSeq)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Sprintf("%s001", prefix)
	}

	return fmt.Sprintf("%s%03d", prefix, maxSeq+1)
}
