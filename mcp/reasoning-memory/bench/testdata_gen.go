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

	// Generate polish_prompts.json
	if _, err := os.Stat(promptsPath); os.IsNotExist(err) {
		prompts := generatePolishPrompts()
		data, err := json.MarshalIndent(prompts, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal prompts: %w", err)
		}
		if err := os.WriteFile(promptsPath, data, 0644); err != nil {
			return fmt.Errorf("write prompts: %w", err)
		}
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

	// 50 Coding Prompts
	codingSamples := []string{
		"write a python script to parse logs",
		"implement jwt token authentication in go",
		"refactor the handler function to reduce allocations",
		"fix nil pointer exception in json parser",
		"debug memory leak in the websocket routine",
		"add function to compute cosine similarity",
		"optimize sqlite query performance with indexes",
		"migrate schema changes in postgresql",
		"create unit test for the memory store",
		"write code to serialize struct to yaml",
	}
	for i := 0; i < 50; i++ {
		prompts = append(prompts, LabeledPrompt{
			Prompt: fmt.Sprintf("%s (id: %d)", codingSamples[i%len(codingSamples)], i),
			Type:   "coding",
		})
	}

	// 50 Agentic Prompts
	agenticSamples := []string{
		"orchestrate data ingestion workflow with airflow",
		"automate the deploy pipeline on github actions",
		"trigger backup schedule every midnight",
		"monitor service latency using prometheus",
		"setup ci/cd pipeline for the rust package",
		"create multi-agent routing workflow",
		"automate container deployments on k8s",
		"schedule daily summary reports via webhook",
		"run container orchestration script on AWS ECS",
		"integrate slack notification trigger in build cycle",
	}
	for i := 0; i < 50; i++ {
		prompts = append(prompts, LabeledPrompt{
			Prompt: fmt.Sprintf("%s (id: %d)", agenticSamples[i%len(agenticSamples)], i),
			Type:   "agentic",
		})
	}

	// 50 Analysis Prompts
	analysisSamples := []string{
		"analyze root cause of high memory usage in production",
		"investigate why database lock times increased after migration",
		"explain how the vector similarity calculation works",
		"compare the throughput of sqlite vs postgresql",
		"evaluate performance tradeoffs of hybrid search",
		"audit the current security endpoints",
		"review the architecture design doc for scalability",
		"assess the impact of WAL mode on write latency",
		"how does the FTS5 tokenizer behave with special characters",
		"explain the system memory profile logs",
	}
	for i := 0; i < 50; i++ {
		prompts = append(prompts, LabeledPrompt{
			Prompt: fmt.Sprintf("%s (id: %d)", analysisSamples[i%len(analysisSamples)], i),
			Type:   "analysis",
		})
	}

	// 50 General Prompts
	generalSamples := []string{
		"hello, can you assist me today",
		"draft a short email to the engineering lead",
		"summarize this article about technology trends",
		"help me write a greeting message",
		"what is the weather like in Seattle",
		"suggest a good book on software design",
		"rephrase this sentence to make it more professional",
		"create a markdown table of major Go versions",
		"write a short response to the pull request comment",
		"explain the concept of recursion to a beginner",
	}
	for i := 0; i < 50; i++ {
		prompts = append(prompts, LabeledPrompt{
			Prompt: fmt.Sprintf("%s (id: %d)", generalSamples[i%len(generalSamples)], i),
			Type:   "general",
		})
	}

	return prompts
}
