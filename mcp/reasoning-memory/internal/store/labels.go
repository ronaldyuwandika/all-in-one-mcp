package store

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

type EnrichCtx struct {
	Problem       string
	ThinkingTrace string
	ToolCalls     string
	Outcome       string
	Domain        string
	ExistingTags  []string
	ExistingRepo  string
}

func EnrichLabels(ec EnrichCtx) map[string][]string {
	labels := make(map[string][]string)

	if ec.ExistingRepo != "" {
		labels["repo"] = []string{ec.ExistingRepo}
	}
	if len(ec.ExistingTags) > 0 {
		labels["tag"] = ec.ExistingTags
	}

	if ec.Domain != "" {
		labels["domain"] = []string{ec.Domain}
	}
	if ec.Outcome != "" {
		labels["outcome"] = []string{ec.Outcome}
	}

	text := strings.ToLower(ec.Problem + " " + ec.ThinkingTrace + " " + ec.ToolCalls)

	langs := detectLanguages(text)
	if len(langs) > 0 {
		labels["language"] = langs
	}

	fw := detectFrameworks(text)
	if len(fw) > 0 {
		labels["framework"] = fw
	}

	if sv := detectSeverity(ec.Outcome, ec.Problem, ec.ThinkingTrace); sv != "" {
		labels["severity"] = []string{sv}
	}

	ent := detectEntities(text)
	if len(ent) > 0 {
		labels["entity"] = ent
	}

	return labels
}

var (
	langPatterns = []struct {
		name string
		re   *regexp.Regexp
	}{
		{"go", regexp.MustCompile(`\b(?:func |package |import \(|go func|goroutine|\.go\b|golang|go test|go build)`)},
		{"python", regexp.MustCompile(`\b(?:def |import |from |class |\.py\b|python|pip |conda |pytest)`)},
		{"javascript", regexp.MustCompile(`\b(?:function |const |let |var |=>|\.js\b|npm |yarn |node |react|typescript)`)},
		{"typescript", regexp.MustCompile(`\b(?:interface |type |enum |\.ts\b|tsconfig|angular)`)},
		{"rust", regexp.MustCompile(`\b(?:fn |let mut |impl |cargo |\.rs\b|rustc|unsafe )`)},
		{"shell", regexp.MustCompile(`\b(?:bash|zsh|#!/|chmod |chown |grep |awk |sed |curl |wget )`)},
		{"sql", regexp.MustCompile(`\b(?:SELECT |FROM |WHERE |JOIN |INSERT |CREATE TABLE|ALTER TABLE|GROUP BY)`)},
		{"yaml", regexp.MustCompile(`\b(?:apiVersion|kind:|metadata:|spec:|yaml|\.yml\b)`)},
		{"docker", regexp.MustCompile(`\b(?:FROM |RUN |CMD |ENTRYPOINT|docker |Dockerfile|docker-compose)`)},
		{"terraform", regexp.MustCompile(`\b(?:resource "|data "|provider |terraform |\.tf\b|terragrunt)`)},
	}

	fwPatterns = []struct {
		name string
		re   *regexp.Regexp
	}{
		{"bubbletea", regexp.MustCompile(`\b(?:bubbletea|bubble.tea|tea\.New|tea\.Update|tea\.View|charmbracelet/bubbletea)`)},
		{"cobra", regexp.MustCompile(`\b(?:cobra|spf13/cobra|cobra\.Command|rootCmd)`)},
		{"gin", regexp.MustCompile(`\b(?:gin\.|gin-gonic|gin\.Default|gin\.Router)`)},
		{"echo", regexp.MustCompile(`\b(?:echo\.|labstack/echo|echo\.New|echo\.Context)`)},
		{"fiber", regexp.MustCompile(`\b(?:fiber\.|gofiber|fiber\.New|fiber\.Ctx)`)},
		{"react", regexp.MustCompile(`\b(?:react|useState|useEffect|useRef|useCallback|jsx|tsx|component)`)},
		{"nextjs", regexp.MustCompile(`\b(?:next\.|nextjs|getServerSideProps|getStaticProps|next/link)`)},
		{"fastapi", regexp.MustCompile(`\b(?:fastapi|FastAPI|@app\.get|@app\.post|uvicorn)`)},
		{"flask", regexp.MustCompile(`\b(?:flask|Flask|@app\.route|flask_|werkzeug)`)},
		{"django", regexp.MustCompile(`\b(?:django|Django|manage\.py|urlpatterns|models\.)`)},
	}
)

func detectLanguages(text string) []string {
	var langs []string
	seen := make(map[string]bool)
	for _, lp := range langPatterns {
		if lp.re.MatchString(text) && !seen[lp.name] {
			langs = append(langs, lp.name)
			seen[lp.name] = true
		}
	}
	return langs
}

func detectFrameworks(text string) []string {
	var fws []string
	seen := make(map[string]bool)
	for _, fp := range fwPatterns {
		if fp.re.MatchString(text) && !seen[fp.name] {
			fws = append(fws, fp.name)
			seen[fp.name] = true
		}
	}
	return fws
}

func detectSeverity(outcome, problem, thinkingTrace string) string {
	if outcome == "failure" || outcome == "error" {
		return "bug"
	}
	if strings.Contains(strings.ToLower(problem), "bug") ||
		strings.Contains(strings.ToLower(problem), "error") ||
		strings.Contains(strings.ToLower(problem), "fix") {
		return "bugfix"
	}
	if strings.Contains(strings.ToLower(problem), "refactor") ||
		strings.Contains(strings.ToLower(problem), "clean") {
		return "refactor"
	}
	if strings.Contains(strings.ToLower(problem), "feature") ||
		strings.Contains(strings.ToLower(problem), "add ") {
		return "feature"
	}
	if strings.Contains(strings.ToLower(problem), "investigat") ||
		strings.Contains(strings.ToLower(problem), "debug") {
		return "investigation"
	}
	if len(thinkingTrace) > 5000 {
		return "deep"
	}
	return ""
}

func detectEntities(text string) []string {
	var entities []string
	seen := make(map[string]bool)

	toolRefs := []*regexp.Regexp{
		regexp.MustCompile(`\b(ctx_read|ctx_search|ctx_shell|ctx_edit|ctx_overview|ctx_knowledge|ctx_session|ctx_tree)\b`),
		regexp.MustCompile(`\b(vault_get|vault_set|vault_scan|run_safe|vault_mask)\b`),
		regexp.MustCompile(`\b(grafana_api|query_prometheus|query_loki|get_dashboard|search_dashboards)\b`),
		regexp.MustCompile(`\b(kubectl|helm|docker|terraform|ansible|packer)\b`),
		regexp.MustCompile(`\b(git |gh |git push|git commit|git merge|git rebase|git checkout)\b`),
		regexp.MustCompile(`\b(pytest|jest|go test|rspec|mocha|vitest)\b`),
		regexp.MustCompile(`\b(openai|claude|gemini|llama|gpt-4|gpt-3)\b`),
		regexp.MustCompile(`\b(prometheus|loki|tempo|grafana|datadog|sentry|newrelic)\b`),
		regexp.MustCompile(`\b(postgres|mysql|redis|mongodb|sqlite|elasticsearch)\b`),
		regexp.MustCompile(`\b(aws|gcp|azure|cloudflare|vercel|netlify)\b`),
		regexp.MustCompile(`\b(kubernetes|k8s|docker|podman|nomad|swarm)\b`),
	}

	for _, re := range toolRefs {
		matches := re.FindAllString(text, -1)
		for _, m := range matches {
			m = strings.TrimSpace(m)
			if m != "" && !seen[m] {
				entities = append(entities, m)
				seen[m] = true
			}
		}
	}

	return entities
}

func mergeLabels(existing map[string][]string, incoming map[string][]string) map[string][]string {
	if existing == nil {
		existing = make(map[string][]string)
	}
	for k, vs := range incoming {
		seen := make(map[string]bool)
		for _, v := range existing[k] {
			seen[v] = true
		}
		for _, v := range vs {
			if !seen[v] {
				existing[k] = append(existing[k], v)
				seen[v] = true
			}
		}
	}
	return existing
}

func labelsToTags(labels map[string][]string) []string {
	if labels == nil {
		return nil
	}
	return labels["tag"]
}

func labelsToRepo(labels map[string][]string) string {
	if labels == nil {
		return ""
	}
	vals := labels["repo"]
	if len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func (es *EpisodeStore) SetLabels(episodeID string, labels map[string][]string) error {
	lj, _ := json.Marshal(labels)
	_, err := es.db.Exec("UPDATE episodes SET labels = ? WHERE id = ?", string(lj), episodeID)
	if err != nil {
		return fmt.Errorf("set labels: %w", err)
	}
	return es.syncMetadataIndex(episodeID, labels)
}

func (es *EpisodeStore) syncMetadataIndex(episodeID string, labels map[string][]string) error {
	tx, err := es.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("DELETE FROM metadata_idx WHERE episode_id = ?", episodeID); err != nil {
		return fmt.Errorf("delete idx: %w", err)
	}

	for k, vs := range labels {
		for _, v := range vs {
			if _, err := tx.Exec(
				"INSERT INTO metadata_idx (episode_id, key, value) VALUES (?, ?, ?)",
				episodeID, k, v,
			); err != nil {
				return fmt.Errorf("insert idx: %w", err)
			}
		}
	}

	return tx.Commit()
}

func (es *EpisodeStore) EpisodesByLabel(key, value string) ([]string, error) {
	rows, err := es.db.Query(
		"SELECT episode_id FROM metadata_idx WHERE key = ? AND value = ? ORDER BY rowid DESC",
		key, value,
	)
	if err != nil {
		return nil, fmt.Errorf("episodes by label: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (es *EpisodeStore) DistinctLabelKeys() ([]string, error) {
	rows, err := es.db.Query(
		"SELECT DISTINCT key FROM metadata_idx ORDER BY key",
	)
	if err != nil {
		return nil, fmt.Errorf("distinct label keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (es *EpisodeStore) DistinctLabelValues(key string) ([]string, error) {
	rows, err := es.db.Query(
		"SELECT DISTINCT value FROM metadata_idx WHERE key = ? ORDER BY value",
		key,
	)
	if err != nil {
		return nil, fmt.Errorf("distinct label values: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func (es *EpisodeStore) LabelDistribution(key string) (map[string]int, error) {
	rows, err := es.db.Query(
		"SELECT value, COUNT(*) as cnt FROM metadata_idx WHERE key = ? GROUP BY value ORDER BY cnt DESC",
		key,
	)
	if err != nil {
		return nil, fmt.Errorf("label distribution: %w", err)
	}
	defer func() { _ = rows.Close() }()

	dist := make(map[string]int)
	for rows.Next() {
		var value string
		var count int
		if err := rows.Scan(&value, &count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		dist[value] = count
	}
	return dist, rows.Err()
}

func (es *EpisodeStore) TopLabelKeys(limit int) ([]models.TagCount, error) {
	rows, err := es.db.Query(
		"SELECT key, COUNT(DISTINCT episode_id) as cnt FROM metadata_idx GROUP BY key ORDER BY cnt DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("top label keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []models.TagCount
	for rows.Next() {
		var tc models.TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		results = append(results, tc)
	}
	return results, rows.Err()
}

func (es *EpisodeStore) UnlabeledCount() (int, error) {
	var count int
	err := es.db.QueryRow(
		"SELECT COUNT(*) FROM episodes WHERE labels = '{}' OR labels = '' OR labels IS NULL",
	).Scan(&count)
	return count, err
}

func (es *EpisodeStore) parseLabelsJSON(labelsStr string) map[string][]string {
	if labelsStr == "" || labelsStr == "{}" {
		return nil
	}
	var labels map[string][]string
	if err := json.Unmarshal([]byte(labelsStr), &labels); err != nil {
		return nil
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func (es *EpisodeStore) BackfillLabels() (int, error) {
	rows, err := es.db.Query(
		`SELECT id, tags, repo, domain, outcome FROM episodes WHERE labels IS NULL OR labels = '' OR labels = '{}'`,
	)
	if err != nil {
		return 0, fmt.Errorf("backfill select: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var count int
	for rows.Next() {
		var id, tagsJSON, repo, domain, outcome string
		if err := rows.Scan(&id, &tagsJSON, &repo, &domain, &outcome); err != nil {
			continue
		}

		var tags []string
		_ = json.Unmarshal([]byte(tagsJSON), &tags)

		ec := EnrichCtx{
			ExistingTags: tags,
			ExistingRepo: repo,
			Domain:       domain,
			Outcome:      outcome,
		}
		labels := EnrichLabels(ec)

		if err := es.SetLabels(id, labels); err != nil {
			continue
		}
		count++
	}

	if err := rows.Err(); err != nil {
		return count, err
	}

	return count, es.db.QueryRow("SELECT COUNT(*) FROM episodes WHERE labels IS NULL OR labels = '' OR labels = '{}'").Scan(new(int))
}
