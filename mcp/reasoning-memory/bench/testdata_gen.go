package bench

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

var (
	domains  = []string{"coding", "agentic", "analysis", "general"}
	outcomes = []string{"success", "failure", "partial"}
	modelsID = []string{"gpt-4o", "claude-3-5-sonnet", "gemini-1.5-pro", "deepseek-r1"}
	tagsList = [][]string{
		{"go", "http", "json"},
		{"rust", "concurrency", "safety"},
		{"docker", "kubernetes", "deploy"},
		{"sqlite", "performance", "indexing"},
		{"python", "ai", "llm"},
		{"bash", "scripting", "automation"},
	}

	problemTemplates = []string{
		"Fix nil pointer dereference in %s parser under high load",
		"Orchestrate multi-stage %s workflow with fault tolerance",
		"Analyze memory leak using pprof heap profiling in %s service",
		"Optimize database query performance and indexing in %s application",
		"Implement secure JWT authentication middleware in %s API",
		"Migrate legacy codebase to modern %s design patterns",
		"Profile CPU hotspot in JSON marshalling of %s server",
		"Deploy highly available microservice cluster using %s tools",
		"Debug deadlock during concurrent map access in %s runtime",
		"Create automated end-to-end integration tests for %s module",
	}

	subjects = []string{"Go", "Python", "Rust", "Node.js", "Docker", "PostgreSQL", "SQLite", "Kubernetes", "AWS", "gRPC"}
)

// EnsureTestData generates testdata if they don't exist
func EnsureTestData(dir string) error {
	testdataDir := filepath.Join(dir, "testdata")
	if err := os.MkdirAll(testdataDir, 0755); err != nil {
		return fmt.Errorf("create testdata dir: %w", err)
	}

	episodes1kPath := filepath.Join(testdataDir, "episodes_1k.json")
	episodes10kPath := filepath.Join(testdataDir, "episodes_10k.json")
	queriesPath := filepath.Join(testdataDir, "queries_labeled.jsonl")
	promptsPath := filepath.Join(testdataDir, "polish_prompts.json")

	// Generate 1k episodes
	var eps1k []models.Episode
	if _, err := os.Stat(episodes1kPath); os.IsNotExist(err) {
		eps1k = generateEpisodes(1000)
		data, err := json.MarshalIndent(eps1k, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal 1k eps: %w", err)
		}
		if err := os.WriteFile(episodes1kPath, data, 0644); err != nil {
			return fmt.Errorf("write 1k eps: %w", err)
		}
	} else {
		// Read existing for query label generation
		data, err := os.ReadFile(episodes1kPath)
		if err == nil {
			_ = json.Unmarshal(data, &eps1k)
		}
	}

	// Generate 10k episodes
	if _, err := os.Stat(episodes10kPath); os.IsNotExist(err) {
		eps10k := generateEpisodes(10000)
		data, err := json.MarshalIndent(eps10k, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal 10k eps: %w", err)
		}
		if err := os.WriteFile(episodes10kPath, data, 0644); err != nil {
			return fmt.Errorf("write 10k eps: %w", err)
		}
	}

	// Generate queries_labeled.jsonl (using eps1k as corpus)
	if _, err := os.Stat(queriesPath); os.IsNotExist(err) {
		if len(eps1k) == 0 {
			eps1k = generateEpisodes(1000)
		}
		queries := generateLabeledQueries(eps1k)
		file, err := os.Create(queriesPath)
		if err != nil {
			return fmt.Errorf("create queries file: %w", err)
		}
		defer file.Close()

		for _, q := range queries {
			data, err := json.Marshal(q)
			if err != nil {
				return fmt.Errorf("marshal query: %w", err)
			}
			if _, err := file.Write(append(data, '\n')); err != nil {
				return fmt.Errorf("write query line: %w", err)
			}
		}
	}

	// Generate polish_prompts.json (always overwrite to prevent caching stale bias)
	prompts := generatePolishPrompts()
	pdata, err := json.MarshalIndent(prompts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal prompts: %w", err)
	}
	if err := os.WriteFile(promptsPath, pdata, 0644); err != nil {
		return fmt.Errorf("write prompts: %w", err)
	}

	return nil
}

func generateEpisodes(n int) []models.Episode {
	r := rand.New(rand.NewSource(42)) // seed for determinism
	eps := make([]models.Episode, n)
	startTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < n; i++ {
		id := fmt.Sprintf("re-20260713-%04d", i+1)
		domain := domains[r.Intn(len(domains))]
		outcome := outcomes[r.Intn(len(outcomes))]
		model := modelsID[r.Intn(len(modelsID))]
		tags := tagsList[r.Intn(len(tagsList))]

		subject := subjects[r.Intn(len(subjects))]
		problem := fmt.Sprintf(problemTemplates[r.Intn(len(problemTemplates))], subject)

		// Create trace
		traceLines := []string{
			"1. Initial analysis: problem involves " + subject + ".",
			"2. Examine implementation and stack traces.",
			"3. Formulate fix or workflow strategy.",
			"4. Verify correctness of solution under domain " + domain + ".",
		}
		thinkingTrace := strings.Join(traceLines, "\n")

		steps := []models.Step{
			{ID: "s1", Type: "analysis", Content: "Analyzed the " + subject + " issue."},
			{ID: "s2", Type: "fix", Content: "Applied standard resolution pattern for " + outcome + "."},
		}

		toolCalls := []models.ToolCall{
			{Tool: "ctx_read", Args: map[string]string{"path": "src/main.go"}, Outcome: "success", ResultExcerpt: "func main() {}"},
		}

		eps[i] = models.Episode{
			ID:              id,
			CreatedAt:       startTime.Add(time.Duration(i) * time.Hour),
			Domain:          domain,
			Outcome:         outcome,
			Tags:            tags,
			Problem:         problem,
			ThinkingTrace:   thinkingTrace,
			Steps:           steps,
			ToolCalls:       toolCalls,
			ModelID:         model,
			DurationSeconds: r.Intn(300) + 10,
		}
	}

	return eps
}

type LabeledQuery struct {
	Query       string         `json:"query"`
	RelevantIDs map[string]int `json:"relevant_ids"`
}

func generateLabeledQueries(eps []models.Episode) []LabeledQuery {
	r := rand.New(rand.NewSource(100))
	queries := make([]LabeledQuery, 200)

	for i := 0; i < 200; i++ {
		// Pick an episode to base the query on
		targetEp := eps[r.Intn(len(eps))]

		// Extract query terms (2-3 words from problem)
		words := strings.Fields(targetEp.Problem)
		var queryTerms []string
		for _, w := range words {
			clean := strings.Trim(strings.ToLower(w), ",.?!")
			if len(clean) > 3 && clean != "with" && clean != "under" && clean != "using" {
				queryTerms = append(queryTerms, clean)
			}
		}

		// limit to at most 3 words
		if len(queryTerms) > 3 {
			queryTerms = queryTerms[:3]
		}
		if len(queryTerms) == 0 {
			queryTerms = []string{"performance"}
		}
		query := strings.Join(queryTerms, " ")

		relevant := make(map[string]int)
		// Scan eps to calculate matching relevance
		for _, ep := range eps {
			score := 0
			// Word match
			probLower := strings.ToLower(ep.Problem)
			matchCount := 0
			for _, term := range queryTerms {
				if strings.Contains(probLower, term) {
					matchCount++
				}
			}
			if matchCount == len(queryTerms) {
				score += 3
			} else if matchCount > 0 {
				score += 1
			}

			// Tag match
			tagMatch := 0
			for _, t := range ep.Tags {
				for _, tt := range targetEp.Tags {
					if t == tt {
						tagMatch++
					}
				}
			}
			if tagMatch > 0 {
				score += 1
			}

			if score > 0 {
				relevant[ep.ID] = score
			}
		}

		queries[i] = LabeledQuery{
			Query:       query,
			RelevantIDs: relevant,
		}
	}

	return queries
}

type LabeledPrompt struct {
	Prompt string `json:"prompt"`
	Type   string `json:"task_type"`
}

func generatePolishPrompts() []LabeledPrompt {
	var prompts []LabeledPrompt

	// Coding (50 unique prompts via combinations)
	codingVerbs := []string{"implement", "refactor", "fix", "optimize", "write", "debug", "add", "migrate", "serialize", "parse"}
	codingSubjects := []string{"jwt authentication", "memory leak", "json parser", "sqlite indexes", "concurrency locks"}
	codingLangs := []string{"in go", "in python", "in rust", "in typescript"}
	count := 0
	for _, v := range codingVerbs {
		for _, s := range codingSubjects {
			for _, l := range codingLangs {
				if count >= 50 {
					break
				}
				prompts = append(prompts, LabeledPrompt{
					Prompt: fmt.Sprintf("%s %s %s", v, s, l),
					Type:   "coding",
				})
				count++
			}
		}
	}

	// Agentic (50 unique prompts via combinations)
	agenticVerbs := []string{"orchestrate", "automate", "schedule", "monitor", "trigger", "deploy", "setup", "configure", "run", "integrate"}
	agenticTasks := []string{"data pipeline", "deploy workflow", "cron job backups", "server scaling", "alert notifications"}
	agenticPlatforms := []string{"on aws", "via github actions", "on kubernetes", "using docker compose"}
	count = 0
	for _, v := range agenticVerbs {
		for _, t := range agenticTasks {
			for _, p := range agenticPlatforms {
				if count >= 50 {
					break
				}
				prompts = append(prompts, LabeledPrompt{
					Prompt: fmt.Sprintf("%s %s %s", v, t, p),
					Type:   "agentic",
				})
				count++
			}
		}
	}

	// Analysis (50 unique prompts via combinations)
	analysisVerbs := []string{"analyze", "investigate", "explain", "compare", "evaluate", "audit", "assess", "review", "how does", "why does"}
	analysisSubjects := []string{"memory footprint", "query performance", "latency increase", "security policy", "lock contention"}
	analysisScopes := []string{"in production", "after database migration", "under high concurrency", "for the new API"}
	count = 0
	for _, v := range analysisVerbs {
		for _, s := range analysisSubjects {
			for _, sc := range analysisScopes {
				if count >= 50 {
					break
				}
				var prompt string
				if strings.HasSuffix(v, "does") {
					prompt = fmt.Sprintf("%s %s behave %s", v, s, sc)
				} else {
					prompt = fmt.Sprintf("%s %s %s", v, s, sc)
				}
				prompts = append(prompts, LabeledPrompt{
					Prompt: prompt,
					Type:   "analysis",
				})
				count++
			}
		}
	}

	// General (50 unique prompts via combinations)
	generalVerbs := []string{"draft", "summarize", "suggest", "translate", "rephrase", "write", "help me", "what is", "create", "how to"}
	generalTopics := []string{"greeting message", "out-of-office email", "travel itinerary", "recipe", "project proposal"}
	generalStyles := []string{"politely", "simply", "for a beginner", "concisely"}
	count = 0
	for _, v := range generalVerbs {
		for _, t := range generalTopics {
			for _, s := range generalStyles {
				if count >= 50 {
					break
				}
				prompts = append(prompts, LabeledPrompt{
					Prompt: fmt.Sprintf("%s %s %s", v, t, s),
					Type:   "general",
				})
				count++
			}
		}
	}

	return prompts
}
