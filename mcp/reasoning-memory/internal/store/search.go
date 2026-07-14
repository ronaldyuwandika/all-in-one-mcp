package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

type searchRow struct {
	ID              string
	Problem         string
	ThinkingTrace   string
	Domain          string
	Outcome         string
	TagsJSON        string
	Repo            string
	LabelsJSON      string
	StepsJSON       string
	ToolCallsJSON   string
	CreatedAt       string
	ModelID         string
	DurationSeconds int
}

func (es *EpisodeStore) SearchLocal(query string, domainFilter, outcomeFilter, repoFilter string, tagsFilter []string, topK int, metadataFilter ...map[string][]string) ([]models.EpisodeSummary, error) {
	if topK <= 0 {
		topK = 5
	}

	var mf map[string][]string
	if len(metadataFilter) > 0 {
		mf = metadataFilter[0]
	}

	ftsResults, err := es.ftsSearch(query, domainFilter, outcomeFilter, repoFilter, tagsFilter, topK, mf)
	if err != nil {
		return nil, err
	}

	if es.vec == nil || !es.vec.Enabled() {
		return ftsResults, nil
	}

	vecResults, err := es.vec.Search(context.Background(), query, topK*2)
	if err != nil || len(vecResults) == 0 {
		return ftsResults, nil
	}

	ftsByID := make(map[string]*models.EpisodeSummary)
	for i := range ftsResults {
		ftsByID[ftsResults[i].ID] = &ftsResults[i]
	}

	vecScores := make(map[string]float64)
	for _, vr := range vecResults {
		vecScores[vr.ID] = vr.Similarity
	}

	hybridWeight := 0.5

	type scoredResult struct {
		summary models.EpisodeSummary
		score   float64
	}
	var hybrid []scoredResult

	for _, vr := range vecResults {
		if vr.Similarity < 0.3 {
			continue
		}
		score := vr.Similarity * hybridWeight
		if existing, ok := ftsByID[vr.ID]; ok {
			score += existing.LocalScore * (1 - hybridWeight)
			existing.VectorScore = math.Round(vr.Similarity*1000) / 1000
			existing.LocalScore = math.Round(score*1000) / 1000
		} else {
			summary, err := es.GetSummary(vr.ID)
			if err != nil || summary == nil {
				continue
			}
			summary.LocalScore = math.Round(score*1000) / 1000
			summary.VectorScore = math.Round(vr.Similarity*1000) / 1000
			hybrid = append(hybrid, scoredResult{summary: *summary, score: score})
		}
	}

	for i := range ftsResults {
		if _, ok := vecScores[ftsResults[i].ID]; !ok {
			score := ftsResults[i].LocalScore * (1 - hybridWeight)
			ftsResults[i].LocalScore = math.Round(score*1000) / 1000
			ftsResults[i].VectorScore = 0
			hybrid = append(hybrid, scoredResult{summary: ftsResults[i], score: score})
		}
	}

	for _, sr := range hybrid {
		ftsResults = append(ftsResults, sr.summary)
	}

	for i := 0; i < len(ftsResults); i++ {
		for j := i + 1; j < len(ftsResults); j++ {
			if ftsResults[j].LocalScore > ftsResults[i].LocalScore {
				ftsResults[i], ftsResults[j] = ftsResults[j], ftsResults[i]
			}
		}
	}

	if topK < len(ftsResults) {
		ftsResults = ftsResults[:topK]
	}

	return ftsResults, nil
}

func (es *EpisodeStore) ftsSearch(query string, domainFilter, outcomeFilter, repoFilter string, tagsFilter []string, topK int, metadataFilter map[string][]string) ([]models.EpisodeSummary, error) {
	queryWords := strings.Fields(strings.ToLower(query))

	ftsRows, err := es.searchFTS(query, repoFilter)
	if err != nil {
		return nil, err
	}

	scored := make(map[string]float64)
	for _, r := range ftsRows {
		score := 0.0

		problemLower := strings.ToLower(r.Problem)
		tagsList := parseTags(r.TagsJSON)
		labels := es.parseLabelsJSON(r.LabelsJSON)

		match := true
		for mk, mv := range metadataFilter {
			vals := labels[mk]
			found := false
			for _, filterVal := range mv {
				for _, v := range vals {
					if strings.EqualFold(v, filterVal) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if len(metadataFilter) > 0 {
			score += 0.25
		}

		termMatches := 0
		for _, w := range queryWords {
			if strings.Contains(problemLower, w) {
				termMatches++
			}
		}
		if termMatches > 0 && len(queryWords) > 0 {
			score += float64(termMatches) / float64(len(queryWords)) * 0.6
		}

		if strings.Contains(problemLower, strings.ToLower(query)) {
			score += 0.3
		}

		tagMatches := 0
		for _, filterTag := range tagsFilter {
			for _, t := range tagsList {
				if t == filterTag {
					tagMatches++
					break
				}
			}
		}
		if tagMatches > 0 && len(tagsFilter) > 0 {
			score += float64(tagMatches) / float64(len(tagsFilter)) * 0.3
		}

		if domainFilter != "" && r.Domain == domainFilter {
			score += 0.2
		}
		if outcomeFilter != "" && r.Outcome == outcomeFilter {
			score += 0.15
		}

		if domainFilter != "" && r.Domain != domainFilter {
			continue
		}
		if outcomeFilter != "" && r.Outcome != outcomeFilter {
			continue
		}
		if repoFilter != "" && !strings.Contains(strings.ToLower(r.Repo), strings.ToLower(repoFilter)) {
			continue
		}

		if repoFilter != "" && r.Repo == repoFilter {
			score += 0.2
		}

		traceLower := strings.ToLower(r.ThinkingTrace)
		content := traceLower + " " + problemLower
		ctMatches := 0
		for _, w := range queryWords {
			if strings.Contains(content, w) {
				ctMatches++
			}
		}
		ctScore := 0.0
		if ctMatches > 0 && len(queryWords) > 0 {
			ctScore = float64(ctMatches) / float64(len(queryWords)) * 0.5
		}
		if strings.Contains(content, strings.ToLower(query)) {
			ctScore += 0.3
		}
		score += ctScore

		if score > 0 {
			if existing, ok := scored[r.ID]; !ok || score > existing {
				scored[r.ID] = score
			}
		}
	}

	for _, r := range ftsRows {
		if _, ok := scored[r.ID]; !ok {
			scored[r.ID] = 0.1
		}
	}

	sorted := rankByScore(scored, topK)

	var results []models.EpisodeSummary
	for _, entry := range sorted {
		for _, r := range ftsRows {
			if r.ID == entry.id {
				steps := parseSteps(r.StepsJSON)
				toolCalls := parseToolCalls(r.ToolCallsJSON)
				labels := es.parseLabelsJSON(r.LabelsJSON)
				s := models.EpisodeSummary{
					ID:              r.ID,
					CreatedAt:       r.CreatedAt,
					Problem:         truncate(r.Problem, 200),
					Domain:          r.Domain,
					Outcome:         r.Outcome,
					Tags:            parseTags(r.TagsJSON),
					Repo:            r.Repo,
					Labels:          labels,
					StepCount:       len(steps),
					ToolCount:       len(toolCalls),
					StepTypes:       stepTypes(steps),
					ModelID:         r.ModelID,
					DurationSeconds: r.DurationSeconds,
					LocalScore:      math.Round(entry.score*1000) / 1000,
				}
				results = append(results, s)
				break
			}
		}
	}

	return results, nil
}

func (es *EpisodeStore) searchFTS(query, repoFilter string) ([]searchRow, error) {
	terms := strings.Fields(query)
	var ftsQuery string
	for i, t := range terms {
		if i > 0 {
			ftsQuery += " OR "
		}
		ftsQuery += fmt.Sprintf(`"%s"`, strings.ReplaceAll(t, `"`, `""`))
	}

	var q string
	var args []interface{}
	if repoFilter != "" {
		q = `SELECT e.id, e.problem, e.thinking_trace, e.domain, e.outcome, e.tags,
		            e.repo, e.labels, e.steps, e.tool_calls, e.created_at, e.model_id, e.duration_seconds
		     FROM episodes_fts f
		     JOIN episodes e ON e.rowid = f.rowid
		     WHERE episodes_fts MATCH ? AND e.repo = ?
		     LIMIT 50`
		args = append(args, ftsQuery, repoFilter)
	} else {
		q = `SELECT e.id, e.problem, e.thinking_trace, e.domain, e.outcome, e.tags,
		            e.repo, e.labels, e.steps, e.tool_calls, e.created_at, e.model_id, e.duration_seconds
		     FROM episodes_fts f
		     JOIN episodes e ON e.rowid = f.rowid
		     WHERE episodes_fts MATCH ?
		     LIMIT 50`
		args = append(args, ftsQuery)
	}

	rows, err := es.db.Query(q, args...)
	if err != nil {
		return es.fallbackSearch(query, repoFilter)
	}
	defer func() { _ = rows.Close() }()

	var results []searchRow
	for rows.Next() {
		var r searchRow
		if err := rows.Scan(
			&r.ID, &r.Problem, &r.ThinkingTrace, &r.Domain, &r.Outcome,
			&r.TagsJSON, &r.Repo, &r.LabelsJSON, &r.StepsJSON, &r.ToolCallsJSON, &r.CreatedAt,
			&r.ModelID, &r.DurationSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan fts result: %w", err)
		}
		results = append(results, r)
	}

	return results, rows.Err()
}

func (es *EpisodeStore) fallbackSearch(query, repoFilter string) ([]searchRow, error) {
	escaped := strings.ReplaceAll(query, "'", "''")
	escaped = strings.ReplaceAll(escaped, "\\", "\\\\")
	likePattern := "%" + escaped + "%"
	var q string
	var args []interface{}
	if repoFilter != "" {
		q = `SELECT id, problem, thinking_trace, domain, outcome, tags,
		            repo, labels, steps, tool_calls, created_at, model_id, duration_seconds
		     FROM episodes
		     WHERE (problem LIKE ? ESCAPE '\' OR thinking_trace LIKE ? ESCAPE '\')
		       AND repo = ?
		     LIMIT 50`
		args = append(args, likePattern, likePattern, repoFilter)
	} else {
		q = `SELECT id, problem, thinking_trace, domain, outcome, tags,
		            repo, labels, steps, tool_calls, created_at, model_id, duration_seconds
		     FROM episodes WHERE problem LIKE ? ESCAPE '\' OR thinking_trace LIKE ? ESCAPE '\'
		     LIMIT 50`
		args = append(args, likePattern, likePattern)
	}

	rows, err := es.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("fallback search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []searchRow
	for rows.Next() {
		var r searchRow
		if err := rows.Scan(
			&r.ID, &r.Problem, &r.ThinkingTrace, &r.Domain, &r.Outcome,
			&r.TagsJSON, &r.Repo, &r.LabelsJSON, &r.StepsJSON, &r.ToolCallsJSON, &r.CreatedAt,
			&r.ModelID, &r.DurationSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan fallback result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type scoredID struct {
	id    string
	score float64
}

func rankByScore(scored map[string]float64, topK int) []scoredID {
	var entries []scoredID
	for id, score := range scored {
		entries = append(entries, scoredID{id: id, score: score})
	}

	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].score > entries[i].score {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	if topK < len(entries) {
		entries = entries[:topK]
	}
	return entries
}

func parseTags(jsonStr string) []string {
	if jsonStr == "" {
		return nil
	}
	var tags []string
	_ = json.Unmarshal([]byte(jsonStr), &tags)
	return tags
}

func parseSteps(jsonStr string) []models.Step {
	if jsonStr == "" {
		return nil
	}
	var steps []models.Step
	_ = json.Unmarshal([]byte(jsonStr), &steps)
	return steps
}

func parseToolCalls(jsonStr string) []models.ToolCall {
	if jsonStr == "" {
		return nil
	}
	var tc []models.ToolCall
	_ = json.Unmarshal([]byte(jsonStr), &tc)
	return tc
}

func stepTypes(steps []models.Step) []string {
	var types []string
	for _, s := range steps {
		types = append(types, s.Type)
	}
	return types
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
