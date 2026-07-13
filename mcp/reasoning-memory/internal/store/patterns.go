package store

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func (es *EpisodeStore) FindMergeCandidates(minTagOverlap int) ([]MergeCandidate, error) {
	rows, err := es.db.Query(
		`SELECT id, domain, outcome, tags, problem
		 FROM episodes ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("find merge candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type rowData struct {
		ID      string
		Domain  string
		Outcome string
		Tags    string
		Problem string
	}

	var episodes []rowData
	for rows.Next() {
		var r rowData
		if err := rows.Scan(&r.ID, &r.Domain, &r.Outcome, &r.Tags, &r.Problem); err != nil {
			return nil, fmt.Errorf("scan merge candidate: %w", err)
		}
		episodes = append(episodes, r)
	}

	var candidates []MergeCandidate
	for i := 0; i < len(episodes); i++ {
		for j := i + 1; j < len(episodes); j++ {
			a, b := episodes[i], episodes[j]
			if a.Domain != b.Domain {
				continue
			}

			tagsA := parseTags(a.Tags)
			tagsB := parseTags(b.Tags)
			overlap := tagOverlap(tagsA, tagsB)

			if overlap >= minTagOverlap {
				aTerms := strings.Fields(strings.ToLower(a.Problem))
				bTerms := strings.Fields(strings.ToLower(b.Problem))
				union := unionTerms(aTerms, bTerms)
				intersection := intersectTerms(aTerms, bTerms)

				textOverlap := 0.0
				if len(union) > 0 {
					textOverlap = float64(len(intersection)) / float64(len(union))
				}
				score := float64(overlap)*0.3 + textOverlap*0.4
				candidates = append(candidates, MergeCandidate{
					A:     a.ID,
					B:     b.ID,
					Score: math.Round(score*1000) / 1000,
				})
			}
		}
	}

	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].Score > candidates[i].Score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	return candidates, nil
}

type MergeCandidate struct {
	A     string  `json:"a"`
	B     string  `json:"b"`
	Score float64 `json:"merge_score"`
}

func (es *EpisodeStore) MergeToPattern(c MergeCandidate) (string, error) {
	epA, err := es.GetEpisode(c.A)
	if err != nil || epA == nil {
		return "", fmt.Errorf("get episode A %s: %w", c.A, err)
	}
	epB, err := es.GetEpisode(c.B)
	if err != nil || epB == nil {
		return "", fmt.Errorf("get episode B %s: %w", c.B, err)
	}

	patternID := fmt.Sprintf("pat-%s-%s", epA.ID, epB.ID)

	combinedPrompt := epA.Problem + "\n\n(Combined with: " + epB.Problem + ")"

	seenPhrases := make(map[string]bool)
	var mergedTraceLines []string
	for _, line := range strings.Split(epA.ThinkingTrace, "\n") {
		line = strings.TrimSpace(line)
		key := strings.ToLower(line)
		if len(key) > 60 {
			key = key[:60]
		}
		if !seenPhrases[key] {
			seenPhrases[key] = true
			mergedTraceLines = append(mergedTraceLines, line)
		}
	}
	for _, line := range strings.Split(epB.ThinkingTrace, "\n") {
		line = strings.TrimSpace(line)
		key := strings.ToLower(line)
		if len(key) > 60 {
			key = key[:60]
		}
		if !seenPhrases[key] {
			seenPhrases[key] = true
			mergedTraceLines = append(mergedTraceLines, line)
		}
	}

	seenTools := make(map[string]bool)
	var mergedTools []models.ToolCall
	for _, tc := range epA.ToolCalls {
		key := tc.Tool + toJSON(tc.Args)
		if len(key) > 100 {
			key = key[:100]
		}
		if !seenTools[key] {
			seenTools[key] = true
			mergedTools = append(mergedTools, tc)
		}
	}
	for _, tc := range epB.ToolCalls {
		key := tc.Tool + toJSON(tc.Args)
		if len(key) > 100 {
			key = key[:100]
		}
		if !seenTools[key] {
			seenTools[key] = true
			mergedTools = append(mergedTools, tc)
		}
	}

	tagSet := make(map[string]bool)
	for _, t := range epA.Tags {
		tagSet[t] = true
	}
	for _, t := range epB.Tags {
		tagSet[t] = true
	}
	var allTags []string
	for t := range tagSet {
		allTags = append(allTags, t)
	}

	sourcesJSON, _ := json.Marshal([]string{epA.ID, epB.ID})
	tagsJSON, _ := json.Marshal(allTags)
	toolsJSON, _ := json.Marshal(mergedTools)

	_, err = es.db.Exec(
		`INSERT OR REPLACE INTO patterns (id, created_at, domain, merge_score, sources, consolidated_prompt, master_thinking_path, master_tool_calls, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		patternID, time.Now().UTC().Format(time.RFC3339), epA.Domain, c.Score,
		string(sourcesJSON), combinedPrompt, strings.Join(mergedTraceLines, "\n"),
		string(toolsJSON), string(tagsJSON),
	)
	if err != nil {
		return "", fmt.Errorf("save pattern: %w", err)
	}

	return patternID, nil
}

func (es *EpisodeStore) PatternCount() (int, error) {
	var count int
	err := es.db.QueryRow("SELECT COUNT(*) FROM patterns").Scan(&count)
	return count, err
}

func (es *EpisodeStore) GetPattern(id string) (*models.Pattern, error) {
	row := es.db.QueryRow(
		`SELECT id, created_at, domain, merge_score, sources, consolidated_prompt, master_thinking_path, master_tool_calls, tags
		FROM patterns WHERE id = ?`, id,
	)

	var (
		sourcesJSON string
		toolsJSON   string
		tagsJSON    string
		p           models.Pattern
	)

	if err := row.Scan(
		&p.ID, &p.CreatedAt, &p.Domain, &p.MergeScore, &sourcesJSON,
		&p.ConsolidatedPrompt, &p.MasterThinkingPath, &toolsJSON, &tagsJSON,
	); err != nil {
		return nil, nil
	}

	_ = json.Unmarshal([]byte(sourcesJSON), &p.Sources)
	_ = json.Unmarshal([]byte(toolsJSON), &p.MasterToolCalls)
	_ = json.Unmarshal([]byte(tagsJSON), &p.Tags)

	return &p, nil
}

func (es *EpisodeStore) ListPatterns() ([]models.Pattern, error) {
	rows, err := es.db.Query(
		`SELECT id, created_at, domain, merge_score, sources, consolidated_prompt, master_thinking_path, master_tool_calls, tags
		FROM patterns ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list patterns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var patterns []models.Pattern
	for rows.Next() {
		var (
			sourcesJSON string
			toolsJSON   string
			tagsJSON    string
			p           models.Pattern
		)
		if err := rows.Scan(
			&p.ID, &p.CreatedAt, &p.Domain, &p.MergeScore, &sourcesJSON,
			&p.ConsolidatedPrompt, &p.MasterThinkingPath, &toolsJSON, &tagsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan pattern: %w", err)
		}
		_ = json.Unmarshal([]byte(sourcesJSON), &p.Sources)
		_ = json.Unmarshal([]byte(toolsJSON), &p.MasterToolCalls)
		_ = json.Unmarshal([]byte(tagsJSON), &p.Tags)
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

func (es *EpisodeStore) PruneFailures(olderThanDays int) (int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -olderThanDays).Format(time.RFC3339)
	result, err := es.db.Exec(
		"DELETE FROM episodes WHERE outcome = 'failure' AND created_at < ?", cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("prune failures: %w", err)
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

func tagOverlap(a, b []string) int {
	set := make(map[string]bool)
	for _, t := range a {
		set[t] = true
	}
	count := 0
	for _, t := range b {
		if set[t] {
			count++
		}
	}
	return count
}

func unionTerms(a, b []string) []string {
	set := make(map[string]bool)
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		set[t] = true
	}
	var result []string
	for t := range set {
		result = append(result, t)
	}
	return result
}

func intersectTerms(a, b []string) []string {
	set := make(map[string]bool)
	for _, t := range a {
		set[t] = true
	}
	var result []string
	seen := make(map[string]bool)
	for _, t := range b {
		if set[t] && !seen[t] {
			result = append(result, t)
			seen[t] = true
		}
	}
	return result
}

func toJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
