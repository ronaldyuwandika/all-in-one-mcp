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

type SearchOptions struct {
	MetadataFilter      map[string][]string
	VerificationTypes   []string
	VerificationSuccess *bool
}

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

func (es *EpisodeStore) SearchLocal(query string, domainFilter, outcomeFilter, repoFilter string, tagsFilter []string, topK int, options ...any) ([]models.EpisodeSummary, error) {
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

	var opts SearchOptions
	if len(options) > 0 {
		switch v := options[0].(type) {
		case SearchOptions:
			opts = v
		case map[string][]string:
			opts.MetadataFilter = v
		}
		opts.MetadataFilter = security.Labels(opts.MetadataFilter)
		opts.VerificationTypes = security.Strings(opts.VerificationTypes)
	}

	ftsResults, err := es.ftsSearch(query, domainFilter, outcomeFilter, repoFilter, tagsFilter, topK, opts)
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

	const hybridWeight = 0.5
	candidates := make(map[string]models.EpisodeSummary, len(ftsResults)+len(vecResults))
	for _, summary := range ftsResults {
		summary.LocalScore = math.Round(summary.LocalScore*(1-hybridWeight)*1000) / 1000
		candidates[summary.ID] = summary
	}
	failureMatches, err := es.searchFailedApproaches(query)
	if err != nil {
		return nil, err
	}
	for _, vr := range vecResults {
		if vr.Similarity < 0.3 {
			continue
		}
		summary, ok := candidates[vr.ID]
		if !ok {
			loaded, err := es.GetSummary(vr.ID)
			if err != nil || loaded == nil || !matchesSearchFilters(loaded, domainFilter, outcomeFilter, repoFilter, tagsFilter, opts) {
				continue
			}
			summary = *loaded
			summary.FailureMatches = failureMatches[summary.ID]
		}
		summary.VectorScore = math.Round(vr.Similarity*1000) / 1000
		summary.LocalScore = math.Round((summary.LocalScore+vr.Similarity*hybridWeight)*1000) / 1000
		candidates[vr.ID] = summary
	}
	ftsResults = ftsResults[:0]
	for _, summary := range candidates {
		ftsResults = append(ftsResults, summary)
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

func (es *EpisodeStore) ftsSearch(query string, domainFilter, outcomeFilter, repoFilter string, tagsFilter []string, topK int, opts SearchOptions) ([]models.EpisodeSummary, error) {
	metadataFilter := opts.MetadataFilter
	queryWords := strings.Fields(strings.ToLower(query))

	ftsRows, err := es.searchFTS(query, repoFilter)
	if err != nil {
		ftsRows, err = es.fallbackSearch(query, repoFilter)
		if err != nil {
			return nil, err
		}
	}
	failureMatches, err := es.searchFailedApproaches(query)
	if err != nil {
		return nil, err
	}
	verifHits, err := es.searchVerifications(query, repoFilter)
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
		for _, r := range rows {
			if !seenRows[r.ID] {
				ftsRows = append(ftsRows, r)
				seenRows[r.ID] = true
			}
		}
	}
	for id := range verifHits {
		if seenRows[id] {
			continue
		}
		rows, err := es.loadSearchRows([]string{id})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !seenRows[r.ID] {
				ftsRows = append(ftsRows, r)
				seenRows[r.ID] = true
			}
		}
	}

	ids := make([]string, 0, len(ftsRows))
	for _, row := range ftsRows {
		ids = append(ids, row.ID)
	}
	verificationByID, err := es.verificationSummaries(ids)
	if err != nil {
		return nil, err
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
		verification, ok := verificationByID[r.ID]
		if !ok {
			verification, err = es.verificationSummary(r.ID)
			if err != nil {
				return nil, err
			}
		}
		if !matchesVerificationFilters(verification, opts.VerificationTypes, opts.VerificationSuccess) {
			continue
		}
		if verifHits[r.ID] {
			score += 0.10
		}
		if r.Outcome == models.OutcomeVerifiedSuccess && verification.SuccessfulVerificationCount > 0 {
			score += 0.05
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
				verifSummary := verificationByID[r.ID]
				if verifSummary == nil {
					verifSummary = &models.EpisodeSummary{}
				}
				s := models.EpisodeSummary{
					ID:                          r.ID,
					CreatedAt:                   r.CreatedAt,
					UpdatedAt:                   r.UpdatedAt,
					Problem:                     truncate(r.Problem, 200),
					Domain:                      r.Domain,
					Outcome:                     models.EpisodeOutcome(r.Outcome),
					Tier:                        models.MemoryTier(r.Tier),
					Tags:                        tags,
					Repo:                        r.Repo,
					Project:                     r.Project,
					Provenance:                  r.Provenance,
					Confidence:                  confidencePtr,
					Labels:                      labels,
					StepCount:                   len(steps),
					ToolCount:                   len(toolCalls),
					StepTypes:                   stepTypes(steps),
					ModelID:                     r.ModelID,
					DurationSeconds:             r.DurationSeconds,
					FailureMatches:              failureMatches[r.ID],
					VerificationCount:           verifSummary.VerificationCount,
					SuccessfulVerificationCount: verifSummary.SuccessfulVerificationCount,
					VerificationTypes:           verifSummary.VerificationTypes,
					VerificationSummaries:       verifSummary.VerificationSummaries,
					LocalScore:                  math.Round(entry.score*1000) / 1000,
				}
				results = append(results, s)
				break
			}
		}
	}

	return results, nil
}

func (es *EpisodeStore) verificationSummaries(episodeIDs []string) (map[string]*models.EpisodeSummary, error) {
	out := make(map[string]*models.EpisodeSummary, len(episodeIDs))
	if len(episodeIDs) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(episodeIDs)), ",")
	args := make([]any, len(episodeIDs))
	for i, id := range episodeIDs {
		args[i] = id
	}
	rows, err := es.db.Query(`SELECT episode_id, type, command, result, success, evidence FROM episode_verifications WHERE episode_id IN (`+placeholders+`) ORDER BY episode_id, position`, args...)
	if err != nil {
		return nil, fmt.Errorf("batch get verifications: %w", err)
	}
	defer rows.Close()

	recordsByEpisode := make(map[string][]models.VerificationRecord)
	for rows.Next() {
		var epID string
		var record models.VerificationRecord
		var success int
		if err := rows.Scan(&epID, &record.Type, &record.Command, &record.Result, &success, &record.Evidence); err != nil {
			return nil, fmt.Errorf("scan batch verification: %w", err)
		}
		record.Success = success != 0
		recordsByEpisode[epID] = append(recordsByEpisode[epID], record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range episodeIDs {
		records := recordsByEpisode[id]
		summary := &models.EpisodeSummary{VerificationCount: len(records)}
		types := make(map[string]bool)
		for _, record := range records {
			if record.Success {
				summary.SuccessfulVerificationCount++
			}
			types[string(record.Type)] = true
		}
		for typ := range types {
			summary.VerificationTypes = append(summary.VerificationTypes, typ)
		}
		sort.Strings(summary.VerificationTypes)
		sort.SliceStable(records, func(i, j int) bool { return records[i].Success && !records[j].Success })
		for _, record := range records {
			if len(summary.VerificationSummaries) == 3 {
				break
			}
			summary.VerificationSummaries = append(summary.VerificationSummaries, models.VerificationSummary{
				Type: string(record.Type), Success: record.Success, CommandPresent: record.Command != "",
				ResultExcerpt: truncateRunes(record.Result, 240), EvidenceExcerpt: truncateRunes(record.Evidence, 240),
			})
		}
		out[id] = summary
	}
	return out, nil
}

func (es *EpisodeStore) searchVerifications(query, repoFilter string) (map[string]bool, error) {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return map[string]bool{}, nil
	}
	parts := make([]string, len(terms))
	for i, term := range terms {
		parts[i] = fmt.Sprintf(`"%s"`, strings.ReplaceAll(term, `"`, `""`))
	}
	var rows *sql.Rows
	var err error
	if repoFilter != "" {
		rows, err = es.db.Query(`SELECT DISTINCT v.episode_id FROM verification_fts x JOIN episode_verifications v ON v.id = x.rowid JOIN episodes e ON e.id = v.episode_id WHERE verification_fts MATCH ? AND LOWER(e.repo) = LOWER(?) ORDER BY e.created_at DESC, e.id ASC LIMIT 50`, strings.Join(parts, " OR "), repoFilter)
	} else {
		rows, err = es.db.Query(`SELECT DISTINCT v.episode_id FROM verification_fts x JOIN episode_verifications v ON v.id = x.rowid JOIN episodes e ON e.id = v.episode_id WHERE verification_fts MATCH ? ORDER BY e.created_at DESC, e.id ASC LIMIT 50`, strings.Join(parts, " OR "))
	}
	if err != nil {
		conditions := make([]string, 0, len(terms))
		args := make([]any, 0, len(terms)*4+1)
		for _, term := range terms {
			pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(term) + "%"
			conditions = append(conditions, `(v.type LIKE ? ESCAPE '\' OR v.command LIKE ? ESCAPE '\' OR v.result LIKE ? ESCAPE '\' OR v.evidence LIKE ? ESCAPE '\')`)
			args = append(args, pattern, pattern, pattern, pattern)
		}
		whereClause := strings.Join(conditions, " OR ")
		if repoFilter != "" {
			whereClause = fmt.Sprintf("(%s) AND LOWER(e.repo) = LOWER(?)", whereClause)
			args = append(args, repoFilter)
		}
		rows, err = es.db.Query(`SELECT DISTINCT v.episode_id FROM episode_verifications v JOIN episodes e ON e.id = v.episode_id WHERE `+whereClause+` ORDER BY e.created_at DESC, e.id ASC LIMIT 50`, args...)
		if err != nil {
			return nil, fmt.Errorf("fallback search verifications: %w", err)
		}
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (es *EpisodeStore) verificationSummary(episodeID string) (*models.EpisodeSummary, error) {
	summaries, err := es.verificationSummaries([]string{episodeID})
	if err != nil {
		return nil, err
	}
	return summaries[episodeID], nil
}

func matchesVerificationFilters(summary *models.EpisodeSummary, types []string, success *bool) bool {
	for _, wanted := range types {
		found := false
		for _, typ := range summary.VerificationTypes {
			if strings.EqualFold(typ, wanted) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if success != nil {
		if *success && summary.SuccessfulVerificationCount == 0 {
			return false
		}
		if !*success && summary.VerificationCount == summary.SuccessfulVerificationCount {
			return false
		}
	}
	return true
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
		     ORDER BY e.created_at DESC, e.id ASC
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
	verificationMatch := `EXISTS (SELECT 1 FROM episode_verifications v WHERE v.episode_id=e.id AND (v.type LIKE ? ESCAPE '\' OR v.command LIKE ? ESCAPE '\' OR v.result LIKE ? ESCAPE '\' OR v.evidence LIKE ? ESCAPE '\'))`
	var q string
	var args []interface{}
	if repoFilter != "" {
		q = `SELECT e.id, e.problem, e.thinking_trace, e.domain, e.outcome, e.tier, e.tags,
		            e.repo, e.labels, e.steps, e.tool_calls, e.created_at, e.updated_at, e.project, e.provenance, e.confidence, e.model_id, e.duration_seconds
		     FROM episodes e
		     WHERE (e.problem LIKE ? ESCAPE '\' OR e.thinking_trace LIKE ? ESCAPE '\' OR ` + verificationMatch + `)
		       AND LOWER(e.repo) = LOWER(?)
		     ORDER BY e.created_at DESC, e.id ASC
		     LIMIT 50`
		args = append(args, likePattern, likePattern, likePattern, likePattern, likePattern, likePattern, repoFilter)
	} else {
		q = `SELECT e.id, e.problem, e.thinking_trace, e.domain, e.outcome, e.tier, e.tags,
		            e.repo, e.labels, e.steps, e.tool_calls, e.created_at, e.updated_at, e.project, e.provenance, e.confidence, e.model_id, e.duration_seconds
		     FROM episodes e WHERE e.problem LIKE ? ESCAPE '\' OR e.thinking_trace LIKE ? ESCAPE '\' OR ` + verificationMatch + `
		     ORDER BY e.created_at DESC, e.id ASC
		     LIMIT 50`
		args = append(args, likePattern, likePattern, likePattern, likePattern, likePattern, likePattern)
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

func matchesSearchFilters(summary *models.EpisodeSummary, domainFilter, outcomeFilter, repoFilter string, tagsFilter []string, opts SearchOptions) bool {
	metadataFilter := opts.MetadataFilter
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
	return matchesVerificationFilters(summary, opts.VerificationTypes, opts.VerificationSuccess)
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

func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
