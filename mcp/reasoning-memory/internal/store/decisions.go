package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func migrateDecisions(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS decisions (
		id TEXT PRIMARY KEY, episode_id TEXT NOT NULL, created_at TEXT NOT NULL,
		repo TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, selected TEXT NOT NULL,
		rationale TEXT NOT NULL, tradeoffs TEXT NOT NULL DEFAULT '[]',
		assumptions TEXT NOT NULL DEFAULT '[]', evidence TEXT NOT NULL DEFAULT '[]',
		alternatives TEXT NOT NULL DEFAULT '[]',
		FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE
	)`)
	if err != nil {
		return fmt.Errorf("create decisions: %w", err)
	}
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_decisions_repo ON decisions(repo)")
	return err
}

func (es *EpisodeStore) CreateDecision(ctx context.Context, d *models.Decision) (string, error) {
	if d.ID == "" {
		d.ID = fmt.Sprintf("decision-%d", time.Now().UnixNano())
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if d.EpisodeID == "" || d.Title == "" || d.Selected == "" || d.Rationale == "" {
		return "", fmt.Errorf("episode_id, title, selected, and rationale are required")
	}
	var episodeRepo string
	if err := es.db.QueryRowContext(ctx, "SELECT repo FROM episodes WHERE id = ?", d.EpisodeID).Scan(&episodeRepo); err != nil {
		return "", fmt.Errorf("resolve episode: %w", err)
	}
	if d.Repo != "" && d.Repo != episodeRepo {
		return "", fmt.Errorf("repository mismatch: decision repo %q does not match episode repo %q", d.Repo, episodeRepo)
	}
	d.Repo = episodeRepo
	encode := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	_, err := es.db.ExecContext(ctx, `INSERT INTO decisions
		(id, episode_id, created_at, repo, title, selected, rationale, tradeoffs, assumptions, evidence, alternatives)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.ID, d.EpisodeID, d.CreatedAt.Format(time.RFC3339), d.Repo,
		d.Title, d.Selected, d.Rationale, encode(d.Tradeoffs), encode(d.Assumptions), encode(d.Evidence), encode(d.Alternatives))
	if err != nil {
		return "", fmt.Errorf("create decision: %w", err)
	}
	return d.ID, nil
}

func (es *EpisodeStore) GetDecision(ctx context.Context, id string) (*models.Decision, error) {
	return es.scanDecision(es.db.QueryRowContext(ctx, `SELECT id, episode_id, created_at, repo, title, selected, rationale, tradeoffs, assumptions, evidence, alternatives FROM decisions WHERE id = ?`, id))
}

func (es *EpisodeStore) SearchDecisions(ctx context.Context, query, repo string, limit int) ([]models.Decision, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	q := `SELECT id, episode_id, created_at, repo, title, selected, rationale, tradeoffs, assumptions, evidence, alternatives FROM decisions WHERE repo = ? AND (title LIKE ? ESCAPE '\' OR selected LIKE ? ESCAPE '\' OR rationale LIKE ? ESCAPE '\' OR evidence LIKE ? ESCAPE '\' OR alternatives LIKE ? ESCAPE '\') ORDER BY created_at DESC LIMIT ?`
	escaped := strings.ReplaceAll(query, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "%", "\\%")
	escaped = strings.ReplaceAll(escaped, "_", "\\_")
	like := "%" + escaped + "%"
	rows, err := es.db.QueryContext(ctx, q, repo, like, like, like, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("search decisions: %w", err)
	}
	defer rows.Close()
	var out []models.Decision
	for rows.Next() {
		d, err := es.scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

type decisionScanner interface{ Scan(...any) error }

func (es *EpisodeStore) scanDecision(s decisionScanner) (*models.Decision, error) {
	var d models.Decision
	var created string
	var tr, as, ev, al string
	if err := s.Scan(&d.ID, &d.EpisodeID, &created, &d.Repo, &d.Title, &d.Selected, &d.Rationale, &tr, &as, &ev, &al); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("scan decision: %w", err)
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, created)
	for raw, dst := range map[string]any{tr: &d.Tradeoffs, as: &d.Assumptions, ev: &d.Evidence, al: &d.Alternatives} {
		_ = json.Unmarshal([]byte(raw), dst)
	}
	return &d, nil
}
