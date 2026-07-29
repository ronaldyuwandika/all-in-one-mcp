package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/security"
)

type searchRow struct {
	ID              string
	Problem         string
	ThinkingTrace   string
	Domain          string
	Outcome         string
	Tier            string
	TagsJSON        string
	Repo            string
	LabelsJSON      string
	StepsJSON       string
	ToolCallsJSON   string
	CreatedAt       string
	UpdatedAt       string
	Project         string
	Provenance      string
	Confidence      sql.NullFloat64
	ModelID         string
	DurationSeconds int
}

func (es *EpisodeStore) SearchLocal(query string, domainFilter, outcomeFilter, repoFilter string, tagsFilter []string, topK int, metadataFilter ...map[string][]string) ([]models.EpisodeSummary, error) {
	query = security.Text(query)
	domainFilter = security.Text(domainFilter)
	outcomeFilter = security.Text(outcomeFilter)
	if normalized, ok := models.NormalizeOutcome(outcomeFilter); ok {
		outcomeFilter = string(normalized)
	}
	repoFilter = security.Text(repoFilter)
	tagsFilter = security.Strings(tagsFilter)
	if topK <= 0 {
		topK = 5
	}

	var mf map[string][]string
	if len(metadataFilter) > 0 {
		mf = security.Labels(metadataFilter[0])
	}

	ftsResults, err := es.ftsSearch(query, domainFilter, outcomeFilter, repoFilter, tagsFilter, topK, mf)
	if err != nil {
		return nil, err
	}

	if es.vec == nil || !es.vec.Enabled() {
		for i := range ftsResults {
			security.Summary(&ftsResults[i])
		}
		return ftsResults, nil
	}

	vecResults, err := es.vec.Search(context.Background(), query, topK*2)
	if err != nil || len(vecResults) == 0 {
		for i := range ftsResults {
			security.Summary(&ftsResults[i])
		}
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
	failureMatches, err := es.searchFailedApproaches(query)
	if err != nil {
		return nil, err
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
		summary, err := es.GetSummary(vr.ID)
		if err != nil || summary == nil {
			continue
		}
		if !matchesSearchFilters(summary, domainFilter, outcomeFilter, repoFilter, tagsFilter, mf) {
			continue
		}
		score := vr.Similarity * hybridWeight
		if existing, ok := ftsByID[vr.ID]; ok {
			score += existing.LocalScore * (1 - hybridWeight)
			existing.VectorScore = math.Round(vr.Similarity*1000) / 1000
			existing.LocalScore = math.Round(score*1000) / 1000
		} else {
			summary.LocalScore = math.Round(score*1000) / 1000
			summary.VectorScore = math.Round(vr.Similarity*1000) / 1000
			summary.FailureMatches = failureMatches[summary.ID]
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

	sort.Slice(ftsResults, func(i, j int) bool {
		if ftsResults[i].LocalScore != ftsResults[j].LocalScore {
			return ftsResults[i].LocalScore > ftsResults[j].LocalScore
		}
		if ftsResults[i].CreatedAt != ftsResults[j].CreatedAt {
			return ftsResults[i].CreatedAt > ftsResults[j].CreatedAt
		}
		return ftsResults[i].ID < ftsResults[j].ID
	})

	if topK < len(ftsResults) {
		ftsResults = ftsResults[:topK]
	}
	for i := range ftsResults {
		security.Summary(&ftsResults[i])
	}

	return ftsResults, nil
}

func (es *EpisodeStore) ftsSearch(query string, domainFilter, outcomeFilter, repoFilter string, tagsFilter []string, topK int, metadataFilter map[string][]string) ([]models.EpisodeSummary, error) {
	queryWords := strings.Fields(strings.ToLower(query))

	ftsRows, err := es.searchFTS(query, repoFilter)
	if err != nil {
		return nil, err
	}
	failureMatches, err := es.searchFailedApproaches(query)
	if err != nil {
		return nil, err
	}
	seenRows := make(map[string]bool, len(ftsRows))
	for _, row := range ftsRows {
		seenRows[row.ID] = true
	}
	for id := range failureMatches {
		if seenRows[id] {
			continue
		}
		rows, err := es.loadSearchRows([]string{id})
		if err != nil {
			return nil, err
		}
		ftsRows = append(ftsRows, rows...)
	}

	scored := make(map[string]float64)
	for _, r := range ftsRows {
		score := 0.0

		problemLower := strings.ToLower(r.Problem)
		tagsList, err := parseTagsErr(r.ID, r.TagsJSON)
		if err != nil {
			return nil, err
		}
		labels, err := es.parseLabelsJSONErr(r.ID, r.LabelsJSON)
		if err != nil {
			return nil, err
		}

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
		if len(tagsFilter) > 0 && tagMatches != len(tagsFilter) {
			continue
		}
		if tagMatches > 0 {
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
		if outcomeFilter != "" {
			normalizedFilter, ok := models.NormalizeOutcome(outcomeFilter)
			if ok && r.Outcome != string(normalizedFilter) {
				continue
			}
			if !ok && r.Outcome != outcomeFilter {
				continue
			}
		}
		if repoFilter != "" && !strings.EqualFold(r.Repo, repoFilter) {
			continue
		}

		if repoFilter != "" {
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
		if matches := failureMatches[r.ID]; len(matches) > 0 {
			score += 0.15
		}

		if score > 0 {
			if existing, ok := scored[r.ID]; !ok || score > existing {
				scored[r.ID] = score
			}
		}
	}

	createdAt := make(map[string]string, len(ftsRows))
	for _, r := range ftsRows {
		createdAt[r.ID] = r.CreatedAt
	}
	sorted := rankByScore(scored, createdAt, topK)

	var results []models.EpisodeSummary
	for _, entry := range sorted {
		for _, r := range ftsRows {
			if r.ID == entry.id {
				var steps []models.Step
				if err := decodePersistedJSON(r.ID, "steps", r.StepsJSON, &steps); err != nil {
					return nil, err
				}
				var toolCalls []models.ToolCall
				if err := decodePersistedJSON(r.ID, "tool_calls", r.ToolCallsJSON, &toolCalls); err != nil {
					return nil, err
				}
				tags, err := parseTagsErr(r.ID, r.TagsJSON)
				if err != nil {
					return nil, err
				}
				labels, err := es.parseLabelsJSONErr(r.ID, r.LabelsJSON)
				if err != nil {
					return nil, err
				}
				if _, err := time.Parse(time.RFC3339, r.CreatedAt); err != nil {
					return nil, fmt.Errorf("decode episode %s field created_at: %w", r.ID, err)
				}
				if r.UpdatedAt != "" {
					if _, err := time.Parse(time.RFC3339, r.UpdatedAt); err != nil {
						return nil, fmt.Errorf("decode episode %s field updated_at: %w", r.ID, err)
					}
				}
				var confidencePtr *float64
				if r.Confidence.Valid {
					confidencePtr = &r.Confidence.Float64
				}
				s := models.EpisodeSummary{
					ID:              r.ID,
					CreatedAt:       r.CreatedAt,
					UpdatedAt:       r.UpdatedAt,
					Problem:         truncate(r.Problem, 200),
					Domain:          r.Domain,
					Outcome:         models.EpisodeOutcome(r.Outcome),
					Tier:            models.MemoryTier(r.Tier),
					Tags:            tags,
					Repo:            r.Repo,
					Project:         r.Project,
					Provenance:      r.Provenance,
					Confidence:      confidencePtr,
					Labels:          labels,
					StepCount:       len(steps),
					ToolCount:       len(toolCalls),
					StepTypes:       stepTypes(steps),
					ModelID:         r.ModelID,
					DurationSeconds: r.DurationSeconds,
					FailureMatches:  failureMatches[r.ID],
					LocalScore:      math.Round(entry.score*1000) / 1000,
				}
				results = append(results, s)
				break
			}
		}
	}

	return results, nil
}

func (es *EpisodeStore) searchFailedApproaches(query string) (map[string][]models.FailureMatch, error) {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return map[string][]models.FailureMatch{}, nil
	}
	parts := make([]string, len(terms))
	for i, term := range terms {
		parts[i] = fmt.Sprintf(`"%s"`, strings.ReplaceAll(term, `"`, `""`))
	}
	rows, err := es.db.Query(`SELECT f.episode_id, f.approach, f.failure_mode, f.root_cause, f.lesson, bm25(failed_approaches_fts)
		FROM failed_approaches_fts x JOIN episode_failed_approaches f ON f.id = x.rowid
		WHERE failed_approaches_fts MATCH ? LIMIT 100`, strings.Join(parts, " OR "))
	if err != nil {
		return es.fallbackSearchFailedApproaches(terms)
	}
	defer rows.Close()
	out := make(map[string][]models.FailureMatch)
	for rows.Next() {
		var id string
		var match models.FailureMatch
		var rank float64
		if err := rows.Scan(&id, &match.Approach, &match.FailureMode, &match.RootCause, &match.Lesson, &rank); err != nil {
			return nil, err
		}
		match.Score = 1 / (1 + math.Abs(rank))
		out[id] = append(out[id], match)
	}
	return out, rows.Err()
}

func (es *EpisodeStore) fallbackSearchFailedApproaches(terms []string) (map[string][]models.FailureMatch, error) {
	if len(terms) == 0 {
		return map[string][]models.FailureMatch{}, nil
	}
	var conditions []string
	var args []any
	for _, term := range terms {
		escaped := strings.ReplaceAll(term, "'", "''")
		escaped = strings.ReplaceAll(escaped, "\\", "\\\\")
		pattern := "%" + escaped + "%"
		conditions = append(conditions, "(approach LIKE ? ESCAPE '\\' OR failure_mode LIKE ? ESCAPE '\\' OR root_cause LIKE ? ESCAPE '\\' OR lesson LIKE ? ESCAPE '\\')")
		args = append(args, pattern, pattern, pattern, pattern)
	}
	query := `SELECT episode_id, approach, failure_mode, root_cause, lesson FROM episode_failed_approaches WHERE ` + strings.Join(conditions, " OR ") + ` LIMIT 100`
	rows, err := es.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("fallback search failed approaches: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]models.FailureMatch)
	for rows.Next() {
		var id string
		var match models.FailureMatch
		if err := rows.Scan(&id, &match.Approach, &match.FailureMode, &match.RootCause, &match.Lesson); err != nil {
			return nil, err
		}
		match.Score = 0.5
		out[id] = append(out[id], match)
	}
	return out, rows.Err()
}

func (es *EpisodeStore) loadSearchRows(ids []string) ([]searchRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i := range ids {
		args[i] = ids[i]
	}
	rows, err := es.db.Query(`SELECT id, problem, thinking_trace, domain, outcome, tier, tags, repo, labels, steps, tool_calls, created_at, updated_at, project, provenance, confidence, model_id, duration_seconds FROM episodes WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []searchRow
	for rows.Next() {
		var r searchRow
		if err := rows.Scan(&r.ID, &r.Problem, &r.ThinkingTrace, &r.Domain, &r.Outcome, &r.Tier, &r.TagsJSON, &r.Repo, &r.LabelsJSON, &r.StepsJSON, &r.ToolCallsJSON, &r.CreatedAt, &r.UpdatedAt, &r.Project, &r.Provenance, &r.Confidence, &r.ModelID, &r.DurationSeconds); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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
		q = `SELECT e.id, e.problem, e.thinking_trace, e.domain, e.outcome, e.tier, e.tags,
		            e.repo, e.labels, e.steps, e.tool_calls, e.created_at, e.updated_at, e.project, e.provenance, e.confidence, e.model_id, e.duration_seconds
		     FROM episodes_fts f
		     JOIN episodes e ON e.rowid = f.rowid
		     WHERE episodes_fts MATCH ? AND LOWER(e.repo) = LOWER(?)
		     LIMIT 50`
		args = append(args, ftsQuery, repoFilter)
	} else {
		q = `SELECT e.id, e.problem, e.thinking_trace, e.domain, e.outcome, e.tier, e.tags,
		            e.repo, e.labels, e.steps, e.tool_calls, e.created_at, e.updated_at, e.project, e.provenance, e.confidence, e.model_id, e.duration_seconds
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
			&r.ID, &r.Problem, &r.ThinkingTrace, &r.Domain, &r.Outcome, &r.Tier,
			&r.TagsJSON, &r.Repo, &r.LabelsJSON, &r.StepsJSON, &r.ToolCallsJSON, &r.CreatedAt,
			&r.UpdatedAt, &r.Project, &r.Provenance, &r.Confidence, &r.ModelID, &r.DurationSeconds,
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
		q = `SELECT id, problem, thinking_trace, domain, outcome, tier, tags,
		            repo, labels, steps, tool_calls, created_at, updated_at, project, provenance, confidence, model_id, duration_seconds
		     FROM episodes
		     WHERE (problem LIKE ? ESCAPE '\' OR thinking_trace LIKE ? ESCAPE '\')
		       AND LOWER(repo) = LOWER(?)
		     LIMIT 50`
		args = append(args, likePattern, likePattern, repoFilter)
	} else {
		q = `SELECT id, problem, thinking_trace, domain, outcome, tier, tags,
		            repo, labels, steps, tool_calls, created_at, updated_at, project, provenance, confidence, model_id, duration_seconds
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
			&r.ID, &r.Problem, &r.ThinkingTrace, &r.Domain, &r.Outcome, &r.Tier,
			&r.TagsJSON, &r.Repo, &r.LabelsJSON, &r.StepsJSON, &r.ToolCallsJSON, &r.CreatedAt,
			&r.UpdatedAt, &r.Project, &r.Provenance, &r.Confidence, &r.ModelID, &r.DurationSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan fallback result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type scoredID struct {
	id        string
	score     float64
	createdAt string
}

func rankByScore(scored map[string]float64, createdAt map[string]string, topK int) []scoredID {
	var entries []scoredID
	for id, score := range scored {
		entries = append(entries, scoredID{id: id, score: score, createdAt: createdAt[id]})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		if entries[i].createdAt != entries[j].createdAt {
			return entries[i].createdAt > entries[j].createdAt
		}
		return entries[i].id < entries[j].id
	})

	if topK < len(entries) {
		entries = entries[:topK]
	}
	return entries
}

func matchesSearchFilters(summary *models.EpisodeSummary, domainFilter, outcomeFilter, repoFilter string, tagsFilter []string, metadataFilter map[string][]string) bool {
	if domainFilter != "" && summary.Domain != domainFilter {
		return false
	}
	if outcomeFilter != "" && string(summary.Outcome) != outcomeFilter {
		return false
	}
	if repoFilter != "" && !strings.EqualFold(summary.Repo, repoFilter) {
		return false
	}
	for _, filterTag := range tagsFilter {
		found := false
		for _, tag := range summary.Tags {
			if tag == filterTag {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for key, filterValues := range metadataFilter {
		found := false
		for _, filterValue := range filterValues {
			for _, value := range summary.Labels[key] {
				if strings.EqualFold(value, filterValue) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func parseTags(jsonStr string) []string {
	if jsonStr == "" {
		return nil
	}
	var tags []string
	_ = json.Unmarshal([]byte(jsonStr), &tags)
	return tags
}

func parseTagsErr(episodeID, jsonStr string) ([]string, error) {
	var tags []string
	if err := decodePersistedJSON(episodeID, "tags", jsonStr, &tags); err != nil {
		return nil, err
	}
	return tags, nil
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
