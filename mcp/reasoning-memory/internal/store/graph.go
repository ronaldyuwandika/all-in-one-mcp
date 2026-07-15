package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

type GraphEdge struct {
	ID           string  `json:"id"`
	SourceID     string  `json:"source_id"`
	TargetID     string  `json:"target_id"`
	Relationship string  `json:"relationship"`
	Weight       float64 `json:"weight"`
	CreatedAt    string  `json:"created_at"`
}

func migrateGraph(db *sql.DB) error {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS graph_edges (
			id TEXT PRIMARY KEY,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			relationship TEXT NOT NULL,
			weight REAL NOT NULL DEFAULT 1.0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_source ON graph_edges(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_target ON graph_edges(target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_rel ON graph_edges(relationship)`,
	}
	for _, d := range ddl {
		if _, err := db.Exec(d); err != nil {
			return fmt.Errorf("migrate graph: %w\n%s", err, d)
		}
	}
	return nil
}

func (es *EpisodeStore) AddEdge(sourceID, targetID, relationship string, weight float64) (string, error) {
	edge := GraphEdge{
		ID:           fmt.Sprintf("ge-%d", time.Now().UnixNano()),
		SourceID:     sourceID,
		TargetID:     targetID,
		Relationship: relationship,
		Weight:       weight,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	_, err := es.db.Exec(
		`INSERT INTO graph_edges (id, source_id, target_id, relationship, weight, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		edge.ID, edge.SourceID, edge.TargetID, edge.Relationship, edge.Weight, edge.CreatedAt,
	)
	if err != nil {
		return "", fmt.Errorf("add edge: %w", err)
	}
	return edge.ID, nil
}

func (es *EpisodeStore) Traverse(startID, relationship string, maxHops int) ([]models.EpisodeSummary, error) {
	if maxHops <= 0 {
		maxHops = 3
	}
	if maxHops > 10 {
		maxHops = 10
	}

	visited := make(map[string]bool)
	visited[startID] = true

	type pathEntry struct {
		id   string
		hop  int
		path []string
	}
	queue := []pathEntry{{id: startID, hop: 0, path: []string{startID}}}
	var results []models.EpisodeSummary

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if curr.hop >= maxHops {
			continue
		}

		rows, err := es.db.Query(
			`SELECT target_id, relationship, weight FROM graph_edges WHERE source_id = ? AND (? = '' OR relationship = ?)`,
			curr.id, relationship, relationship,
		)
		if err != nil {
			continue
		}

		for rows.Next() {
			var targetID, rel string
			var weight float64
			if err := rows.Scan(&targetID, &rel, &weight); err != nil {
				continue
			}
			if visited[targetID] {
				continue
			}
			visited[targetID] = true

			summary, err := es.GetSummary(targetID)
			if err != nil || summary == nil {
				continue
			}
			summary.LocalScore = weight
			results = append(results, *summary)
			queue = append(queue, pathEntry{id: targetID, hop: curr.hop + 1, path: append(curr.path, targetID)})
		}
		_ = rows.Close()
	}
	return results, nil
}

func (es *EpisodeStore) GetRelatedEpisodes(episodeID string) ([]string, error) {
	rows, err := es.db.Query(
		`SELECT DISTINCT target_id FROM graph_edges WHERE source_id = ?
		UNION SELECT DISTINCT source_id FROM graph_edges WHERE target_id = ?`,
		episodeID, episodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("get related episodes: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (es *EpisodeStore) ListEdges(sourceID string) ([]GraphEdge, error) {
	var rows *sql.Rows
	var err error
	if sourceID != "" {
		rows, err = es.db.Query(
			`SELECT id, source_id, target_id, relationship, weight, created_at
			FROM graph_edges WHERE source_id = ?`, sourceID,
		)
	} else {
		rows, err = es.db.Query(
			`SELECT id, source_id, target_id, relationship, weight, created_at FROM graph_edges`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}
	defer rows.Close()

	var edges []GraphEdge
	for rows.Next() {
		var e GraphEdge
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.Relationship, &e.Weight, &e.CreatedAt); err != nil {
			continue
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func (es *EpisodeStore) DeleteEdge(id string) error {
	_, err := es.db.Exec("DELETE FROM graph_edges WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete edge: %w", err)
	}
	return nil
}

func (es *EpisodeStore) EdgeCount() (int, error) {
	var count int
	err := es.db.QueryRow("SELECT COUNT(*) FROM graph_edges").Scan(&count)
	return count, err
}
