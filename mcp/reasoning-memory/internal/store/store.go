package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

type EpisodeStore struct {
	db  *sql.DB
	vec *VectorStore
}

func New(dbPath string) (*EpisodeStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(1)

	// WAL mode reduces lock contention significantly for concurrent readers
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("enable wal: %w", err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &EpisodeStore{db: db}, nil
}

func NewWithVector(dbPath string, vec *VectorStore) (*EpisodeStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("enable wal: %w", err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &EpisodeStore{db: db, vec: vec}, nil
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
	}

	for _, d := range ddl {
		if _, err := db.Exec(d); err != nil {
			return fmt.Errorf("exec ddl: %w\n%s", err, d)
		}
	}

	return nil
}

func (es *EpisodeStore) Close() error {
	return es.db.Close()
}

func (es *EpisodeStore) CreateEpisode(ep *models.Episode) (string, error) {
	if ep.CreatedAt.IsZero() {
		ep.CreatedAt = time.Now().UTC()
	}
	if ep.Domain == "" {
		ep.Domain = "coding"
	}

	stepsJSON, _ := json.Marshal(ep.Steps)
	toolCallsJSON, _ := json.Marshal(ep.ToolCalls)
	tagsJSON, _ := json.Marshal(ep.Tags)

	_, err := es.db.Exec(
		`INSERT INTO episodes (id, created_at, domain, outcome, tags, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		return "", fmt.Errorf("create episode: %w", err)
	}

	if es.vec != nil && es.vec.Enabled() {
		_ = es.vec.AddEpisode(context.Background(), ep.ID, ep.Problem, ep.ThinkingTrace)
	}

	return ep.ID, nil
}

func (es *EpisodeStore) CreateEpisodeContext(ctx context.Context, ep *models.Episode) (string, error) {
	id, err := es.CreateEpisode(ep)
	if err != nil {
		return "", err
	}
	if es.vec != nil && es.vec.Enabled() {
		return id, es.vec.AddEpisode(ctx, ep.ID, ep.Problem, ep.ThinkingTrace)
	}
	return id, nil
}

func (es *EpisodeStore) GetEpisode(id string) (*models.Episode, error) {
	row := es.db.QueryRow(
		`SELECT id, created_at, domain, outcome, tags, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds
		FROM episodes WHERE id = ?`, id,
	)

	var (
		tagsJSON      string
		stepsJSON     string
		toolCallsJSON string
		createdAt     string
		ep            models.Episode
	)

	err := row.Scan(
		&ep.ID, &createdAt, &ep.Domain, &ep.Outcome, &tagsJSON,
		&ep.Problem, &ep.ThinkingTrace, &stepsJSON, &toolCallsJSON,
		&ep.ModelID, &ep.DurationSeconds,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get episode: %w", err)
	}

	ep.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	_ = json.Unmarshal([]byte(tagsJSON), &ep.Tags)
	_ = json.Unmarshal([]byte(stepsJSON), &ep.Steps)
	_ = json.Unmarshal([]byte(toolCallsJSON), &ep.ToolCalls)

	return &ep, nil
}

func (es *EpisodeStore) GetSummary(id string) (*models.EpisodeSummary, error) {
	row := es.db.QueryRow(
		`SELECT id, created_at, problem, domain, outcome, tags, steps, tool_calls, model_id, duration_seconds
		FROM episodes WHERE id = ?`, id,
	)

	var (
		tagsJSON      string
		stepsJSON     string
		toolCallsJSON string
		createdAt     string
		steps         []models.Step
		summary       models.EpisodeSummary
	)

	err := row.Scan(
		&summary.ID, &createdAt, &summary.Problem, &summary.Domain,
		&summary.Outcome, &tagsJSON, &stepsJSON, &toolCallsJSON,
		&summary.ModelID, &summary.DurationSeconds,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get summary: %w", err)
	}

	summary.CreatedAt = createdAt
	_ = json.Unmarshal([]byte(tagsJSON), &summary.Tags)
	_ = json.Unmarshal([]byte(stepsJSON), &steps)
	summary.StepCount = len(steps)
	for _, s := range steps {
		summary.StepTypes = append(summary.StepTypes, s.Type)
	}
	var toolCalls []models.ToolCall
	_ = json.Unmarshal([]byte(toolCallsJSON), &toolCalls)
	summary.ToolCount = len(toolCalls)

	return &summary, nil
}

func (es *EpisodeStore) ListEpisodes(limit, offset int) ([]models.EpisodeSummary, error) {
	rows, err := es.db.Query(
		`SELECT id, created_at, problem, domain, outcome, tags, steps, tool_calls, model_id, duration_seconds
		FROM episodes ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list episodes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []models.EpisodeSummary
	for rows.Next() {
		var (
			tagsJSON      string
			stepsJSON     string
			toolCallsJSON string
			steps         []models.Step
			s             models.EpisodeSummary
		)
		if err := rows.Scan(
			&s.ID, &s.CreatedAt, &s.Problem, &s.Domain,
			&s.Outcome, &tagsJSON, &stepsJSON, &toolCallsJSON,
			&s.ModelID, &s.DurationSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan episode: %w", err)
		}
		_ = json.Unmarshal([]byte(tagsJSON), &s.Tags)
		_ = json.Unmarshal([]byte(stepsJSON), &steps)
		s.StepCount = len(steps)
		for _, st := range steps {
			s.StepTypes = append(s.StepTypes, st.Type)
		}
		var toolCalls []models.ToolCall
		_ = json.Unmarshal([]byte(toolCallsJSON), &toolCalls)
		s.ToolCount = len(toolCalls)
		summaries = append(summaries, s)
	}

	return summaries, rows.Err()
}

func (es *EpisodeStore) DeleteEpisode(id string) error {
	_, err := es.db.Exec("DELETE FROM episodes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete episode: %w", err)
	}
	if es.vec != nil && es.vec.Enabled() {
		_ = es.vec.DeleteEpisode(context.Background(), id)
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
