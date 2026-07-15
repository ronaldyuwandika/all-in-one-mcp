package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

type SemanticConcept struct {
	ID             string   `json:"id"`
	EntityName     string   `json:"entity_name"`
	Type           string   `json:"type"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	SourceEpisode  string   `json:"source_episode_id,omitempty"`
	AccessCount    int      `json:"access_count"`
	LastAccessedAt string   `json:"last_accessed_at,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

func migrateConcepts(db *sql.DB) error {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS semantic_concepts (
			id TEXT PRIMARY KEY,
			entity_name TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '[]',
			embedding BLOB,
			source_episode_id TEXT,
			access_count INTEGER NOT NULL DEFAULT 0,
			last_accessed_at TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_concepts_type ON semantic_concepts(type)`,
		`CREATE INDEX IF NOT EXISTS idx_concepts_name ON semantic_concepts(entity_name)`,
	}
	for _, d := range ddl {
		if _, err := db.Exec(d); err != nil {
			return fmt.Errorf("migrate concepts: %w\n%s", err, d)
		}
	}
	return nil
}

func (es *EpisodeStore) MemorizeConcept(ctx context.Context, entityName, conceptType, description string, tags []string, sourceEpisodeID string) (string, error) {
	id := fmt.Sprintf("sc-%s-%03d", time.Now().UTC().Format("20060102"), time.Now().UnixNano()%1000)

	tagsJSON, _ := json.Marshal(tags)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := es.db.ExecContext(ctx,
		`INSERT INTO semantic_concepts (id, entity_name, type, description, tags, source_episode_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, entityName, conceptType, description, string(tagsJSON), sourceEpisodeID, now,
	)
	if err != nil {
		return "", fmt.Errorf("memorize concept: %w", err)
	}

	if es.vec != nil && es.vec.Enabled() {
		embedText := entityName + " " + conceptType + " " + description
		embedding, err := es.vec.embed(ctx, embedText)
		if err == nil {
			blob := floatsToBytes(embedding)
			_, _ = es.db.ExecContext(ctx,
				"UPDATE semantic_concepts SET embedding = ? WHERE id = ?", blob, id,
			)
		}
	}

	return id, nil
}

func (es *EpisodeStore) RecallSemantic(ctx context.Context, query string, limit int, typeFilter string) ([]SemanticConcept, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	if es.vec != nil && es.vec.Enabled() {
		return es.recallWithEmbedding(ctx, query, limit, typeFilter)
	}

	return es.recallFallback(query, limit, typeFilter)
}

func (es *EpisodeStore) recallWithEmbedding(ctx context.Context, query string, limit int, typeFilter string) ([]SemanticConcept, error) {
	embedding, err := es.vec.embed(ctx, query)
	if err != nil {
		return es.recallFallback(query, limit, typeFilter)
	}

	q := "SELECT id, entity_name, type, description, tags, source_episode_id, access_count, last_accessed_at, created_at, embedding FROM semantic_concepts"
	if typeFilter != "" {
		q += " WHERE type = ?"
	}

	var rows *sql.Rows
	if typeFilter != "" {
		rows, err = es.db.QueryContext(ctx, q, typeFilter)
	} else {
		rows, err = es.db.QueryContext(ctx, q)
	}
	if err != nil {
		return nil, fmt.Errorf("recall semantic: %w", err)
	}
	defer rows.Close()

	type entry struct {
		concept SemanticConcept
		score   float64
	}
	var entries []entry

	for rows.Next() {
		var tagsJSON string
		var sourceEp, lastAcc sql.NullString
		var embBlob []byte
		c := SemanticConcept{}
		if err := rows.Scan(&c.ID, &c.EntityName, &c.Type, &c.Description, &tagsJSON,
			&sourceEp, &c.AccessCount, &lastAcc, &c.CreatedAt, &embBlob); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(tagsJSON), &c.Tags)
		if sourceEp.Valid {
			c.SourceEpisode = sourceEp.String
		}
		if lastAcc.Valid {
			c.LastAccessedAt = lastAcc.String
		}

		var score float64
		if embBlob != nil {
			conceptVec := bytesToFloats(embBlob)
			score = cosineSimilarity(embedding, conceptVec)
		}
		entries = append(entries, entry{concept: c, score: score})
	}

	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].score > entries[i].score {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	if limit > len(entries) {
		limit = len(entries)
	}

	var results []SemanticConcept
	for i := 0; i < limit; i++ {
		results = append(results, entries[i].concept)
	}
	return results, nil
}

func (es *EpisodeStore) recallFallback(query string, limit int, typeFilter string) ([]SemanticConcept, error) {
	q := "SELECT id, entity_name, type, description, tags, source_episode_id, access_count, last_accessed_at, created_at FROM semantic_concepts"
	args := []interface{}{}
	if typeFilter != "" {
		q += " WHERE type = ?"
		args = append(args, typeFilter)
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)

	rows, err := es.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("recall fallback: %w", err)
	}
	defer rows.Close()

	var results []SemanticConcept
	for rows.Next() {
		var tagsJSON string
		var sourceEp, lastAcc sql.NullString
		c := SemanticConcept{}
		if err := rows.Scan(&c.ID, &c.EntityName, &c.Type, &c.Description, &tagsJSON,
			&sourceEp, &c.AccessCount, &lastAcc, &c.CreatedAt); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(tagsJSON), &c.Tags)
		if sourceEp.Valid {
			c.SourceEpisode = sourceEp.String
		}
		if lastAcc.Valid {
			c.LastAccessedAt = lastAcc.String
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

func (es *EpisodeStore) GetConcept(id string) (*SemanticConcept, error) {
	row := es.db.QueryRow(
		"SELECT id, entity_name, type, description, tags, source_episode_id, access_count, last_accessed_at, created_at FROM semantic_concepts WHERE id = ?", id,
	)
	var tagsJSON string
	var sourceEp, lastAcc sql.NullString
	c := &SemanticConcept{}
	err := row.Scan(&c.ID, &c.EntityName, &c.Type, &c.Description, &tagsJSON,
		&sourceEp, &c.AccessCount, &lastAcc, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get concept: %w", err)
	}
	_ = json.Unmarshal([]byte(tagsJSON), &c.Tags)
	if sourceEp.Valid {
		c.SourceEpisode = sourceEp.String
	}
	if lastAcc.Valid {
		c.LastAccessedAt = lastAcc.String
	}
	return c, nil
}

func (es *EpisodeStore) DeleteConcept(id string) error {
	_, err := es.db.Exec("DELETE FROM semantic_concepts WHERE id = ?", id)
	return err
}

func (es *EpisodeStore) ConceptCount() (int, error) {
	var count int
	err := es.db.QueryRow("SELECT COUNT(*) FROM semantic_concepts").Scan(&count)
	return count, err
}

func (es *EpisodeStore) ListConcepts(limit, offset int, typeFilter string) ([]SemanticConcept, error) {
	q := "SELECT id, entity_name, type, description, tags, source_episode_id, access_count, last_accessed_at, created_at FROM semantic_concepts"
	args := []interface{}{}
	if typeFilter != "" {
		q += " WHERE type = ?"
		args = append(args, typeFilter)
	}
	q += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := es.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list concepts: %w", err)
	}
	defer rows.Close()

	var results []SemanticConcept
	for rows.Next() {
		var tagsJSON string
		var sourceEp, lastAcc sql.NullString
		c := SemanticConcept{}
		if err := rows.Scan(&c.ID, &c.EntityName, &c.Type, &c.Description, &tagsJSON,
			&sourceEp, &c.AccessCount, &lastAcc, &c.CreatedAt); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(tagsJSON), &c.Tags)
		if sourceEp.Valid {
			c.SourceEpisode = sourceEp.String
		}
		if lastAcc.Valid {
			c.LastAccessedAt = lastAcc.String
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

func (es *EpisodeStore) PromoteEpisodeToSemantic(episodeID string) error {
	_, err := es.db.Exec("UPDATE episodes SET tier = ? WHERE id = ?", string(models.TierSemantic), episodeID)
	return err
}

func (es *EpisodeStore) PromoteConceptFromEpisode(episodeID string) (string, error) {
	ep, err := es.GetEpisode(episodeID)
	if err != nil || ep == nil {
		return "", fmt.Errorf("get episode: %w", err)
	}
	return es.MemorizeConcept(context.Background(),
		ep.Problem,
		ep.Domain,
		ep.ThinkingTrace,
		ep.Tags,
		episodeID,
	)
}
