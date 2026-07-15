package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
)

type VectorBackend interface {
	Query(ctx context.Context, embedding []float32, topK int) ([]VectorResult, error)
	Insert(ctx context.Context, id string, embedding []float32) error
	Delete(ctx context.Context, id string) error
	Count() int
	Ready() error
	Close() error
}

type VectorSQLite struct {
	db    *sql.DB
	table string
}

func NewVectorSQLite(db *sql.DB, table string) (*VectorSQLite, error) {
	_, err := db.Exec(fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			episode_id TEXT PRIMARY KEY REFERENCES episodes(id),
			embedding BLOB NOT NULL
		)`, table,
	))
	if err != nil {
		return nil, fmt.Errorf("create vector table: %w", err)
	}
	return &VectorSQLite{db: db, table: table}, nil
}

func (v *VectorSQLite) Insert(ctx context.Context, id string, embedding []float32) error {
	blob := floatsToBytes(embedding)
	_, err := v.db.ExecContext(ctx,
		fmt.Sprintf("INSERT OR REPLACE INTO %s (episode_id, embedding) VALUES (?, ?)", v.table),
		id, blob,
	)
	return err
}

func (v *VectorSQLite) Delete(ctx context.Context, id string) error {
	_, err := v.db.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE episode_id = ?", v.table), id,
	)
	return err
}

func (v *VectorSQLite) Query(ctx context.Context, embedding []float32, topK int) ([]VectorResult, error) {
	rows, err := v.db.QueryContext(ctx,
		fmt.Sprintf("SELECT episode_id, embedding FROM %s", v.table),
	)
	if err != nil {
		return nil, fmt.Errorf("vector query: %w", err)
	}
	defer rows.Close()

	type entry struct {
		id  string
		vec []float32
	}
	var entries []entry
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		entries = append(entries, entry{id: id, vec: bytesToFloats(blob)})
	}

	type result struct {
		id    string
		score float64
	}
	var ranked []result
	for _, e := range entries {
		s := cosineSimilarity(embedding, e.vec)
		ranked = append(ranked, result{id: e.id, score: s})
	}

	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}

	if topK > len(ranked) {
		topK = len(ranked)
	}

	var results []VectorResult
	for i := 0; i < topK; i++ {
		results = append(results, VectorResult{
			ID:         ranked[i].id,
			Similarity: math.Round(ranked[i].score*1000) / 1000,
		})
	}
	return results, nil
}

func (v *VectorSQLite) Count() int {
	var n int
	_ = v.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", v.table)).Scan(&n)
	return n
}

func (v *VectorSQLite) Ready() error {
	return v.db.Ping()
}

func (v *VectorSQLite) Close() error {
	return nil
}

func floatsToBytes(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func bytesToFloats(data []byte) []float32 {
	vec := make([]float32, len(data)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vec
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
