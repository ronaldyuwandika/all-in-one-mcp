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

func migrate(db *sql.DB) error {
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
		`CREATE VIRTUAL TABLE IF NOT EXISTS patterns_fts USING fts5(
			id UNINDEXED, consolidated_prompt, master_thinking_path, domain, tags,
			content='patterns', content_rowid='rowid'
		)`,
		`CREATE TABLE IF NOT EXISTS compaction_stats (
			key TEXT PRIMARY KEY,
			value INTEGER NOT NULL DEFAULT 0
		)`,
	}

	for _, d := range ddl {
		if _, err := db.Exec(d); err != nil {
			return fmt.Errorf("exec ddl: %w\n%s", err, d)
		}
	}

	hasCol := func(name string) bool {
		if rows, err := db.Query("PRAGMA table_info(episodes)"); err == nil {
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
		if _, err := db.Exec("ALTER TABLE episodes ADD COLUMN repo TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add repo column: %w", err)
		}
	}
	if !hasCol("labels") {
		if _, err := db.Exec("ALTER TABLE episodes ADD COLUMN labels TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return fmt.Errorf("add labels column: %w", err)
		}
	}
	if !hasCol("tier") {
		if _, err := db.Exec("ALTER TABLE episodes ADD COLUMN tier TEXT NOT NULL DEFAULT 'episodic'"); err != nil {
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
			if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column.name).Scan(&count); err != nil {
				return fmt.Errorf("inspect %s.%s: %w", table, column.name, err)
			}
			if count == 0 {
				if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column.name, column.ddl)); err != nil {
					return fmt.Errorf("add %s.%s: %w", table, column.name, err)
				}
			}
		}
	}
	for _, table := range []string{"episodes", "episodes_archive"} {
		var hasTable int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&hasTable); err == nil && hasTable > 0 {
			if _, err := db.Exec("UPDATE " + table + " SET outcome = 'unverified_success' WHERE outcome = 'success'"); err != nil {
				return fmt.Errorf("backfill %s success outcomes: %w", table, err)
			}
			if _, err := db.Exec("UPDATE " + table + " SET outcome = 'partial_success' WHERE outcome = 'partial'"); err != nil {
				return fmt.Errorf("backfill %s partial outcomes: %w", table, err)
			}
			if _, err := db.Exec("UPDATE " + table + " SET updated_at = created_at WHERE updated_at = '' OR updated_at IS NULL"); err != nil {
				return fmt.Errorf("backfill %s updated_at: %w", table, err)
			}
		}
	}
	var hasFTS int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'episodes_fts'").Scan(&hasFTS); err == nil && hasFTS > 0 {
		if _, err := db.Exec("INSERT INTO episodes_fts(episodes_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("rebuild episodes fts after backfills: %w", err)
		}
	}
	var hasFaFTS int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'failed_approaches_fts'").Scan(&hasFaFTS); err == nil && hasFaFTS > 0 {
		if _, err := db.Exec("INSERT INTO failed_approaches_fts(failed_approaches_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("rebuild failed_approaches fts after backfills: %w", err)
		}
	}
	var hasPatFTS int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'patterns_fts'").Scan(&hasPatFTS); err == nil && hasPatFTS > 0 {
		if _, err := db.Exec("INSERT INTO patterns_fts(patterns_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("rebuild patterns fts after backfills: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vector_reconcile (
		episode_id TEXT PRIMARY KEY,
		problem TEXT NOT NULL,
		thinking_trace TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create vector_reconcile: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS metadata_idx (
		episode_id TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create metadata_idx: %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_meta_kv ON metadata_idx(key, value)"); err != nil {
		return fmt.Errorf("create idx_meta_kv: %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_meta_eid ON metadata_idx(episode_id)"); err != nil {
		return fmt.Errorf("create idx_meta_eid: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS episode_sources (
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
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_episode_sources_eid ON episode_sources(episode_id)"); err != nil {
		return fmt.Errorf("create idx_episode_sources_eid: %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_failed_approaches_eid ON episode_failed_approaches(episode_id)"); err != nil {
		return fmt.Errorf("create idx_failed_approaches_eid: %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_failed_approaches_arch_eid ON episode_failed_approaches_archive(episode_id)"); err != nil {
		return fmt.Errorf("create idx_failed_approaches_arch_eid: %w", err)
	}

	if err := migrateGraph(db); err != nil {
		return fmt.Errorf("migrate graph: %w", err)
	}
	if err := migrateConcepts(db); err != nil {
		return fmt.Errorf("migrate concepts: %w", err)
	}
	if err := migrateDecisions(db); err != nil {
		return fmt.Errorf("migrate decisions: %w", err)
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
		`CREATE TRIGGER IF NOT EXISTS patterns_ai AFTER INSERT ON patterns BEGIN
			INSERT INTO patterns_fts(rowid, id, consolidated_prompt, master_thinking_path, domain, tags)
			VALUES (new.rowid, new.id, new.consolidated_prompt, new.master_thinking_path, new.domain, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS patterns_ad AFTER DELETE ON patterns BEGIN
			INSERT INTO patterns_fts(patterns_fts, rowid, id, consolidated_prompt, master_thinking_path, domain, tags)
			VALUES ('delete', old.rowid, old.id, old.consolidated_prompt, old.master_thinking_path, old.domain, old.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS patterns_au AFTER UPDATE ON patterns BEGIN
			INSERT INTO patterns_fts(patterns_fts, rowid, id, consolidated_prompt, master_thinking_path, domain, tags)
			VALUES ('delete', old.rowid, old.id, old.consolidated_prompt, old.master_thinking_path, old.domain, old.tags);
			INSERT INTO patterns_fts(rowid, id, consolidated_prompt, master_thinking_path, domain, tags)
			VALUES (new.rowid, new.id, new.consolidated_prompt, new.master_thinking_path, new.domain, new.tags);
		END`,
	}
	for _, trigger := range triggers {
		if _, err := db.Exec(trigger); err != nil {
			return fmt.Errorf("create episodes FTS trigger: %w", err)
		}
	}

	return nil
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
		if verr := es.vec.AddEpisode(ctx, ep.ID, ep.Problem, ep.ThinkingTrace+FailedApproachesText(ep.FailedApproaches)); verr != nil {
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

func decodePersistedJSON(episodeID, field, raw string, destination any) error {
	if err := json.Unmarshal([]byte(raw), destination); err != nil {
		return fmt.Errorf("decode episode %s field %s: %w", episodeID, field, err)
	}
	return nil
}

func (es *EpisodeStore) ReconcileVectorStore(ctx context.Context) error {
	if es.vec == nil || !es.vec.Enabled() {
		return nil
	}
	rows, err := es.db.QueryContext(ctx, "SELECT episode_id, problem, thinking_trace FROM vector_reconcile ORDER BY updated_at ASC")
	if err != nil {
		return fmt.Errorf("query vector_reconcile: %w", err)
	}
	defer rows.Close()
	type pendingItem struct {
		id            string
		problem       string
		thinkingTrace string
	}
	var pending []pendingItem
	for rows.Next() {
		var item pendingItem
		if err := rows.Scan(&item.id, &item.problem, &item.thinkingTrace); err != nil {
			return fmt.Errorf("scan vector_reconcile: %w", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close vector_reconcile rows: %w", err)
	}
	for _, item := range pending {
		var verr error
		if item.problem == "" && item.thinkingTrace == "" {
			verr = es.vec.DeleteEpisode(ctx, item.id)
		} else {
			verr = es.vec.ReplaceEpisode(ctx, item.id, item.problem, item.thinkingTrace)
		}
		if verr != nil {
			return fmt.Errorf("reconcile vector episode %s: %w", item.id, verr)
		}
		if _, err := es.db.ExecContext(ctx, "DELETE FROM vector_reconcile WHERE episode_id = ?", item.id); err != nil {
			return fmt.Errorf("clear vector_reconcile %s: %w", item.id, err)
		}
	}
	return nil
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
		"verification": {verificationJSON, &ep.Verification},
		"lessons":      {lessonsJSON, &ep.Lessons},
		"steps":        {stepsJSON, &ep.Steps},
		"tool_calls":   {toolCallsJSON, &ep.ToolCalls},
	} {
		if err := decodePersistedJSON(ep.ID, field, item.raw, item.destination); err != nil {
			return nil, err
		}
	}
	failedTable := "episode_failed_approaches"
	if table == "episodes_archive" {
		failedTable = "episode_failed_approaches_archive"
	}
	failed, err := es.getFailedApproaches(failedTable, ep.ID)
	if err != nil {
		return nil, err
	}
	ep.FailedApproaches = failed
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
	vectorTrace := ep.ThinkingTrace + FailedApproachesText(ep.FailedApproaches)
	if es.vec != nil && es.vec.Enabled() {
		if _, err := tx.ExecContext(ctx, `INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(episode_id) DO UPDATE SET problem=excluded.problem, thinking_trace=excluded.thinking_trace, updated_at=excluded.updated_at`,
			ep.ID, ep.Problem, vectorTrace, ep.UpdatedAt.Format(time.RFC3339),
		); err != nil {
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
				oldVectorTrace := oldEp.ThinkingTrace + FailedApproachesText(oldEp.FailedApproaches)
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
	if _, err := tx.Exec("DELETE FROM vector_reconcile WHERE episode_id = ?", id); err != nil {
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
			if _, err := es.db.Exec(`INSERT INTO vector_reconcile (episode_id, problem, thinking_trace, updated_at) VALUES (?, '', '', ?) ON CONFLICT(episode_id) DO UPDATE SET problem='', thinking_trace='', updated_at=excluded.updated_at`,
				id, time.Now().UTC().Format(time.RFC3339),
			); err != nil {
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
	var hasEpFTS int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'episodes_fts'").Scan(&hasEpFTS); err == nil && hasEpFTS > 0 {
		if _, err := es.db.Exec("INSERT INTO episodes_fts(episodes_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("reindex episodes fts5: %w", err)
		}
	}
	var hasFaFTS int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'failed_approaches_fts'").Scan(&hasFaFTS); err == nil && hasFaFTS > 0 {
		if _, err := es.db.Exec("INSERT INTO failed_approaches_fts(failed_approaches_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("reindex failed_approaches fts5: %w", err)
		}
	}
	var hasPatFTS int
	if err := es.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'patterns_fts'").Scan(&hasPatFTS); err == nil && hasPatFTS > 0 {
		if _, err := es.db.Exec("INSERT INTO patterns_fts(patterns_fts) VALUES('rebuild')"); err != nil {
			return fmt.Errorf("reindex patterns fts5: %w", err)
		}
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
