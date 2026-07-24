package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

type EpisodeStore struct {
	db               *sql.DB
	vec              *VectorStore
	dbPath           string
	CompactionCancel context.CancelFunc
}

func New(dbPath string) (*EpisodeStore, error) {
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

	return &EpisodeStore{db: db, dbPath: dbPath}, nil
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

	return &EpisodeStore{db: db, dbPath: dbPath, vec: vec}, nil
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
		`CREATE TABLE IF NOT EXISTS episodes_archive (
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
			duration_seconds INTEGER NOT NULL DEFAULT 0,
			repo TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '{}',
			tier TEXT NOT NULL DEFAULT 'episodic'
		)`,
		`CREATE TABLE IF NOT EXISTS compaction_stats (
			key TEXT PRIMARY KEY,
			value INTEGER NOT NULL DEFAULT 0
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

	hasCol := func(name string) bool {
		if rows, err := db.Query("PRAGMA table_info(episodes)"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var cid int
				var colName, ctype string
				var notnull int
				var dflt sql.NullString
				var pk int
				if err := rows.Scan(&cid, &colName, &ctype, &notnull, &dflt, &pk); err == nil && colName == name {
					return true
				}
			}
		}
		return false
	}

	if !hasCol("repo") {
		if _, err := db.Exec("ALTER TABLE episodes ADD COLUMN repo TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add repo column: %w", err)
		}
	}
	if !hasCol("labels") {
		if _, err := db.Exec("ALTER TABLE episodes ADD COLUMN labels TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return fmt.Errorf("add labels column: %w", err)
		}
	}
	if !hasCol("tier") {
		if _, err := db.Exec("ALTER TABLE episodes ADD COLUMN tier TEXT NOT NULL DEFAULT 'episodic'"); err != nil {
			return fmt.Errorf("add tier column: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS metadata_idx (
		episode_id TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create metadata_idx: %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_meta_kv ON metadata_idx(key, value)"); err != nil {
		return fmt.Errorf("create idx_meta_kv: %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_meta_eid ON metadata_idx(episode_id)"); err != nil {
		return fmt.Errorf("create idx_meta_eid: %w", err)
	}

	if err := migrateGraph(db); err != nil {
		return fmt.Errorf("migrate graph: %w", err)
	}
	if err := migrateConcepts(db); err != nil {
		return fmt.Errorf("migrate concepts: %w", err)
	}

	return nil
}

func detectGitRepo() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = wd
	out, err := cmd.Output()
	if err != nil {
		return filepath.Base(wd)
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return filepath.Base(wd)
	}
	if strings.Contains(url, "/") {
		parts := strings.Split(strings.TrimSuffix(url, ".git"), "/")
		return parts[len(parts)-1]
	}
	return url
}

func (es *EpisodeStore) Close() error {
	if es.CompactionCancel != nil {
		es.CompactionCancel()
	}
	if es.vec != nil {
		_ = es.vec.Close()
	}
	return es.db.Close()
}

func (es *EpisodeStore) Readiness() error {
	if err := es.db.Ping(); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if es.vec != nil && es.vec.Enabled() {
		return es.vec.Ready()
	}
	return nil
}

func (es *EpisodeStore) Shutdown() error {
	return es.Close()
}

func (es *EpisodeStore) CreateEpisode(ep *models.Episode) (string, error) {
	if ep.CreatedAt.IsZero() {
		ep.CreatedAt = time.Now().UTC()
	}
	if ep.Domain == "" {
		ep.Domain = "coding"
	}
	if ep.Tier == "" {
		ep.Tier = models.TierEpisodic
	}

	stepsJSON, _ := json.Marshal(ep.Steps)
	toolCallsJSON, _ := json.Marshal(ep.ToolCalls)
	tagsJSON, _ := json.Marshal(ep.Tags)

	if ep.Repo == "" {
		ep.Repo = detectGitRepo()
	}

	labels := ep.Labels
	if labels == nil {
		ec := EnrichCtx{
			Problem:       ep.Problem,
			ThinkingTrace: ep.ThinkingTrace,
			ToolCalls:     string(toolCallsJSON),
			Outcome:       ep.Outcome,
			Domain:        ep.Domain,
			ExistingTags:  ep.Tags,
			ExistingRepo:  ep.Repo,
		}
		labels = EnrichLabels(ec)
		ep.Labels = labels
	}
	labelsJSON, _ := json.Marshal(labels)

	_, err := es.db.Exec(
		`INSERT INTO episodes (id, created_at, domain, outcome, tier, tags, repo, labels, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ep.ID,
		ep.CreatedAt.Format(time.RFC3339),
		ep.Domain,
		ep.Outcome,
		string(ep.Tier),
		string(tagsJSON),
		ep.Repo,
		string(labelsJSON),
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

	if err := es.syncMetadataIndex(ep.ID, labels); err != nil {
		return "", fmt.Errorf("sync labels: %w", err)
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
		`SELECT id, created_at, domain, outcome, tier, tags, repo, labels, problem, thinking_trace, steps, tool_calls, model_id, duration_seconds
		FROM episodes WHERE id = ?`, id,
	)

	var (
		tagsJSON      string
		labelsJSON    string
		stepsJSON     string
		toolCallsJSON string
		createdAt     string
		ep            models.Episode
		tier          string
	)

	err := row.Scan(
		&ep.ID, &createdAt, &ep.Domain, &ep.Outcome, &tier, &tagsJSON,
		&ep.Repo, &labelsJSON, &ep.Problem, &ep.ThinkingTrace, &stepsJSON, &toolCallsJSON,
		&ep.ModelID, &ep.DurationSeconds,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get episode: %w", err)
	}

	ep.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	ep.Tier = models.MemoryTier(tier)
	ep.Labels = es.parseLabelsJSON(labelsJSON)
	_ = json.Unmarshal([]byte(tagsJSON), &ep.Tags)
	_ = json.Unmarshal([]byte(stepsJSON), &ep.Steps)
	_ = json.Unmarshal([]byte(toolCallsJSON), &ep.ToolCalls)

	return &ep, nil
}

func (es *EpisodeStore) GetSummary(id string) (*models.EpisodeSummary, error) {
	row := es.db.QueryRow(
		`SELECT id, created_at, problem, domain, outcome, tier, tags, repo, labels, steps, tool_calls, model_id, duration_seconds
		FROM episodes WHERE id = ?`, id,
	)

	var (
		tagsJSON      string
		labelsJSON    string
		stepsJSON     string
		toolCallsJSON string
		createdAt     string
		steps         []models.Step
		summary       models.EpisodeSummary
		tier          string
	)

	err := row.Scan(
		&summary.ID, &createdAt, &summary.Problem, &summary.Domain,
		&summary.Outcome, &tier, &tagsJSON, &summary.Repo, &labelsJSON, &stepsJSON, &toolCallsJSON,
		&summary.ModelID, &summary.DurationSeconds,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get summary: %w", err)
	}

	summary.CreatedAt = createdAt
	summary.Tier = models.MemoryTier(tier)
	summary.Labels = es.parseLabelsJSON(labelsJSON)
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
		`SELECT id, created_at, problem, domain, outcome, tier, tags, repo, labels, steps, tool_calls, model_id, duration_seconds
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
			labelsJSON    string
			stepsJSON     string
			toolCallsJSON string
			steps         []models.Step
			s             models.EpisodeSummary
			tier          string
		)
		if err := rows.Scan(
			&s.ID, &s.CreatedAt, &s.Problem, &s.Domain,
			&s.Outcome, &tier, &tagsJSON, &s.Repo, &labelsJSON, &stepsJSON, &toolCallsJSON,
			&s.ModelID, &s.DurationSeconds,
		); err != nil {
			return nil, fmt.Errorf("scan episode: %w", err)
		}
		s.Tier = models.MemoryTier(tier)
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

func (es *EpisodeStore) DB() *sql.DB {
	return es.db
}

func (es *EpisodeStore) DeletePattern(id string) error {
	_, err := es.db.Exec("DELETE FROM patterns WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete pattern: %w", err)
	}
	return nil
}

func (es *EpisodeStore) ReindexFTS5() error {
	_, err := es.db.Exec("INSERT INTO episodes_fts(episodes_fts) VALUES('rebuild')")
	if err != nil {
		return fmt.Errorf("reindex fts5: %w", err)
	}
	return nil
}

func (es *EpisodeStore) DBPath() string {
	return es.dbPath
}

func (es *EpisodeStore) EpisodesByDomain() (map[string]int, error) {
	rows, err := es.db.Query("SELECT domain, COUNT(*) FROM episodes GROUP BY domain")
	if err != nil {
		return nil, fmt.Errorf("episodes by domain: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var domain string
		var count int
		if err := rows.Scan(&domain, &count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result[domain] = count
	}
	return result, rows.Err()
}

func (es *EpisodeStore) EpisodesByOutcome() (map[string]int, error) {
	rows, err := es.db.Query("SELECT outcome, COUNT(*) FROM episodes GROUP BY outcome")
	if err != nil {
		return nil, fmt.Errorf("episodes by outcome: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var outcome string
		var count int
		if err := rows.Scan(&outcome, &count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result[outcome] = count
	}
	return result, rows.Err()
}

func (es *EpisodeStore) EpisodesByRepo() (map[string]int, error) {
	rows, err := es.db.Query("SELECT repo, COUNT(*) FROM episodes WHERE repo != '' GROUP BY repo ORDER BY COUNT(*) DESC")
	if err != nil {
		return nil, fmt.Errorf("episodes by repo: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var repo string
		var count int
		if err := rows.Scan(&repo, &count); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result[repo] = count
	}
	return result, rows.Err()
}

func (es *EpisodeStore) TopTags(limit int) ([]models.TagCount, error) {
	rows, err := es.db.Query("SELECT tags FROM episodes")
	if err != nil {
		return nil, fmt.Errorf("top tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	freq := make(map[string]int)
	for rows.Next() {
		var tagsJSON string
		if err := rows.Scan(&tagsJSON); err != nil {
			continue
		}
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			continue
		}
		for _, t := range tags {
			freq[t]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var tc []models.TagCount
	for tag, count := range freq {
		tc = append(tc, models.TagCount{Tag: tag, Count: count})
	}

	sort.Slice(tc, func(i, j int) bool {
		return tc[i].Count > tc[j].Count
	})
	if limit > 0 && len(tc) > limit {
		tc = tc[:limit]
	}
	return tc, nil
}

func (es *EpisodeStore) AvgEpisodeLengths() (avgProblem, avgTrace float64, err error) {
	err = es.db.QueryRow(
		"SELECT COALESCE(AVG(LENGTH(problem)),0), COALESCE(AVG(LENGTH(thinking_trace)),0) FROM episodes",
	).Scan(&avgProblem, &avgTrace)
	if err != nil {
		return 0, 0, fmt.Errorf("avg lengths: %w", err)
	}
	return
}

func (es *EpisodeStore) EmptyThinkingTraceCount() (int, error) {
	var count int
	err := es.db.QueryRow("SELECT COUNT(*) FROM episodes WHERE thinking_trace = ''").Scan(&count)
	return count, err
}

func (es *EpisodeStore) DBSizeMB() (float64, error) {
	info, err := os.Stat(es.dbPath)
	if err != nil {
		return 0, fmt.Errorf("db stat: %w", err)
	}
	return float64(info.Size()) / 1024 / 1024, nil
}

func (es *EpisodeStore) FTSSizeMB() (float64, error) {
	var size float64
	err := es.db.QueryRow(
		"SELECT COALESCE(SUM(pgsize), 0) FROM dbstat WHERE name LIKE 'episodes_fts%'",
	).Scan(&size)
	if err != nil {
		return 0, nil
	}
	return size / 1024 / 1024, nil
}

func (es *EpisodeStore) LastConsolidationTS() (*time.Time, error) {
	var ts string
	err := es.db.QueryRow("SELECT MAX(created_at) FROM patterns").Scan(&ts)
	if err != nil || ts == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return nil, nil
	}
	return &t, nil
}

func (es *EpisodeStore) EpisodesByDay(days int) ([]models.DayBucket, error) {
	rows, err := es.db.Query(
		`SELECT date(created_at) as d, COUNT(*) as cnt,
		        COUNT(CASE WHEN outcome='success' THEN 1 END) as ok,
		        COALESCE(AVG(duration_seconds),0),
		        COALESCE(AVG(LENGTH(thinking_trace)),0)
		 FROM episodes
		 WHERE created_at >= date('now', ?)
		 GROUP BY d ORDER BY d`,
		fmt.Sprintf("-%d days", days),
	)
	if err != nil {
		return nil, fmt.Errorf("episodes by day: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var buckets []models.DayBucket
	for rows.Next() {
		var b models.DayBucket
		if err := rows.Scan(&b.Date, &b.Count, &b.Successes, &b.AvgDuration, &b.AvgTraceLen); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

func (es *EpisodeStore) SummaryStats() (*models.SummaryStats, error) {
	stats := &models.SummaryStats{}

	total, err := es.EpisodeCount()
	if err != nil {
		return nil, err
	}
	stats.TotalEpisodes = total

	patCount, err := es.PatternCount()
	if err != nil {
		return nil, err
	}
	stats.TotalPatterns = patCount

	if total > 0 {
		var successCount int
		_ = es.db.QueryRow("SELECT COUNT(*) FROM episodes WHERE outcome='success'").Scan(&successCount)
		stats.SuccessRate = float64(successCount) / float64(total) * 100
	}

	var avgDur, avgTrace float64
	_ = es.db.QueryRow(
		"SELECT COALESCE(AVG(duration_seconds),0), COALESCE(AVG(LENGTH(thinking_trace)),0) FROM episodes",
	).Scan(&avgDur, &avgTrace)
	stats.AvgDurationSec = avgDur
	stats.AvgTraceLenChars = avgTrace

	if stats.TotalPatterns > 0 && stats.TotalEpisodes > 0 {
		var patternSourced int
		_ = es.db.QueryRow(`
			SELECT COALESCE(SUM(json_array_length(sources)), 0)
			FROM patterns`).Scan(&patternSourced)
		if stats.TotalEpisodes > 0 {
			stats.ConsolidationRatio = math.Min(
				float64(patternSourced)/float64(stats.TotalEpisodes)*100, 100)
		}
	}

	var topDomain string
	_ = es.db.QueryRow(`
		SELECT domain FROM episodes
		GROUP BY domain ORDER BY COUNT(*) DESC LIMIT 1`,
	).Scan(&topDomain)
	stats.TopDomain = topDomain

	var topRepo string
	_ = es.db.QueryRow(`
		SELECT repo FROM episodes WHERE repo != ''
		GROUP BY repo ORDER BY COUNT(*) DESC LIMIT 1`,
	).Scan(&topRepo)
	stats.TopRepo = topRepo

	var topLabelKey string
	_ = es.db.QueryRow(`
		SELECT key FROM metadata_idx
		GROUP BY key ORDER BY COUNT(DISTINCT episode_id) DESC LIMIT 1`,
	).Scan(&topLabelKey)
	stats.TopLabelKey = topLabelKey

	var labelCard int
	_ = es.db.QueryRow(`SELECT COUNT(DISTINCT key) FROM metadata_idx`).Scan(&labelCard)
	stats.LabelCardinality = labelCard

	unlabeled, _ := es.UnlabeledCount()
	stats.UnlabeledCount = unlabeled

	var archivedCount int
	_ = es.db.QueryRow("SELECT COUNT(*) FROM episodes_archive").Scan(&archivedCount)
	stats.TotalArchived = archivedCount

	var prunedCount int
	_ = es.db.QueryRow("SELECT COALESCE(value, 0) FROM compaction_stats WHERE key = 'pruned_count'").Scan(&prunedCount)
	stats.TotalPruned = prunedCount

	return stats, nil
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
