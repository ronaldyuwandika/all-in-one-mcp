package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/config"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
)

func main() {
	cfg, err := config.Load(configPath())
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	dbPath := filepath.Join(dataDir(), "store.db")

	vecDataDir := dataDir()
	vec, vecErr := store.NewVectorStore(
		vecDataDir,
		cfg.Embedding.Provider,
		cfg.Embedding.Model,
		cfg.Embedding.BaseURL,
		cfg.Embedding.APIKey,
		cfg.Embedding.Enabled,
	)
	if vecErr != nil {
		log.Printf("⚠ vector store disabled: %v", vecErr)
		vec = nil
	}

	var es *store.EpisodeStore
	if vec != nil && vec.Enabled() {
		es, err = store.NewWithVector(dbPath, vec)
	} else {
		es, err = store.New(dbPath)
	}
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer func() { _ = es.Close() }()
	if vec != nil {
		log.Printf("Vector search enabled (provider=%s, model=%s)", cfg.Embedding.Provider, cfg.Embedding.Model)

		epCount, _ := es.EpisodeCount()
		if epCount > 0 && vec.Count() == 0 {
			log.Printf("Reindexing %d episodes into vector DB...", epCount)
			ctx := context.Background()
			reindexEpisodes(ctx, es, vec)
		}
	}

	s := server.NewMCPServer(
		"reasoning-memory",
		"1.0.0",
		server.WithInstructions("Reasoning Memory Network for LLM reasoning trace capture and retrieval."),
	)

	s.AddTool(
		mcp.NewTool("capture_reasoning_episode",
			mcp.WithDescription("Capture a completed reasoning episode at the END of a task.\n\n"+
				"Stores full trace in SQLite with FTS5 indexing and optional vector embedding. Returns episode ID."),
			mcp.WithString("problem", mcp.Description("The user's request or task description verbatim."), mcp.Required()),
			mcp.WithString("thinking_trace", mcp.Description("Full chain-of-thought reasoning text."), mcp.Required()),
			mcp.WithArray("tool_calls", mcp.Description("List of tool calling records, each with: tool (name), args, result_excerpt, outcome (success/failure).")),
			mcp.WithString("outcome", mcp.Description("Overall task outcome: success, partial, or failure."), mcp.Required()),
			mcp.WithArray("tags", mcp.Description("Domain tags e.g. [\"coding\", \"resilience\", \"retry\"].")),
			mcp.WithString("domain", mcp.Description("Broad domain: \"coding\" or \"agentic\". Defaults to \"coding\".")),
			mcp.WithNumber("duration_seconds", mcp.Description("Total task duration in seconds.")),
			mcp.WithString("model_id", mcp.Description("Model identifier e.g. \"claude-sonnet-4-20260514\".")),
		),
		handleCapture(es, cfg),
	)

	s.AddTool(
		mcp.NewTool("retrieve_reasoning",
			mcp.WithDescription("Search the local structured index for similar reasoning episodes."),
			mcp.WithString("problem", mcp.Description("Problem description to match against."), mcp.Required()),
			mcp.WithString("domain", mcp.Description("Filter by domain: \"coding\" or \"agentic\".")),
			mcp.WithString("outcome", mcp.Description("Filter by outcome: \"success\", \"partial\", or \"failure\".")),
			mcp.WithArray("tags", mcp.Description("Filter by tags (any match).")),
			mcp.WithNumber("top_k", mcp.Description("Max results (default 5, max 20).")),
		),
		handleRetrieve(es, cfg),
	)

	s.AddTool(
		mcp.NewTool("inject_reasoning_context",
			mcp.WithDescription("Retrieve relevant reasoning history and format it as context for a lite model.\n\n"+
				"Use this at the START of a task. Returns a formatted <reasoning_memory> block."),
			mcp.WithString("problem", mcp.Description("The task/problem description to match against."), mcp.Required()),
			mcp.WithNumber("top_k", mcp.Description("Number of past episodes to include (default 3, max 10).")),
			mcp.WithBoolean("include_traces", mcp.Description("Include full thinking traces (true) or just summaries (false).")),
		),
		handleInject(es, cfg),
	)

	s.AddTool(
		mcp.NewTool("consolidate_reasoning",
			mcp.WithDescription("Analyze all episodes to cluster patterns, prune duplicates, merge similar episodes, and rebuild the FTS5 index."),
			mcp.WithString("strategy", mcp.Description("Strategy: \"auto\" (default) -- cluster + merge + prune + index.")),
		),
		handleConsolidate(es, cfg),
	)

	s.AddTool(
		mcp.NewTool("polish_prompt",
			mcp.WithDescription("Take an unstructured user prompt and return a polished, structured version.\n\n"+
				"Detects task type, applies domain-specific architectural rules, optionally injects skill context."),
			mcp.WithString("raw_prompt", mcp.Description("The user's raw/unstructured input."), mcp.Required()),
			mcp.WithString("domain", mcp.Description("Optional override (\"coding\", \"agentic\", \"analysis\", \"general\"). Auto-detected if omitted.")),
			mcp.WithBoolean("include_context", mcp.Description("If true, search RMN for relevant past episodes (default true).")),
			mcp.WithNumber("top_k", mcp.Description("Number of context episodes to include (default 3, max 5).")),
			mcp.WithString("skill_name", mcp.Description("Optional skill name to load and inject.")),
		),
		handlePolish(es, cfg),
	)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func handleCapture(es *store.EpisodeStore, _ *models.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments

		problem := getString(args, "problem")
		thinkingTrace := getString(args, "thinking_trace")
		outcome := getString(args, "outcome")
		domain := getString(args, "domain")
		if domain == "" {
			domain = "coding"
		}
		tags := getStringSlice(args, "tags")

		var durationSeconds int
		if ds, err := getFloat64(args, "duration_seconds"); err == nil {
			durationSeconds = int(ds)
		}
		modelID := getString(args, "model_id")

		toolCalls := getToolCalls(args, "tool_calls")

		ep := &models.Episode{
			ID:              es.NextID(),
			Domain:          domain,
			Outcome:         outcome,
			Tags:            tags,
			Problem:         problem,
			ThinkingTrace:   thinkingTrace,
			Steps:           extractSteps(thinkingTrace),
			ToolCalls:       toolCalls,
			ModelID:         modelID,
			DurationSeconds: durationSeconds,
		}

		episodeID, err := es.CreateEpisode(ep)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("capture failed: %v", err)), nil
		}

		return mcp.NewToolResultText(episodeID), nil
	}
}

func handleRetrieve(es *store.EpisodeStore, _ *models.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments

		problem := getString(args, "problem")
		domain := getString(args, "domain")
		outcome := getString(args, "outcome")
		tags := getStringSlice(args, "tags")

		topK := 5
		if tk, err := getFloat64(args, "top_k"); err == nil {
			topK = int(tk)
		}
		if topK > 20 {
			topK = 20
		}

		results, err := es.SearchLocal(problem, domain, outcome, tags, topK)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}

		data, _ := json.Marshal(results)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleInject(es *store.EpisodeStore, _ *models.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments

		problem := getString(args, "problem")
		topK := 3
		if tk, err := getFloat64(args, "top_k"); err == nil {
			topK = int(tk)
		}
		if topK > 10 {
			topK = 10
		}
		includeTraces := true
		if b, ok := args["include_traces"].(bool); ok {
			includeTraces = b
		}

		results, err := es.SearchLocal(problem, "", "", nil, topK)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}

		var episodes []prompter.EpisodeContext
		for _, r := range results {
			if includeTraces {
				ep, _ := es.GetEpisode(r.ID)
				if ep != nil {
					episodes = append(episodes, prompter.EpisodeContext{
						Problem:       r.Problem,
						Domain:        r.Domain,
						Outcome:       r.Outcome,
						Tags:          r.Tags,
						ThinkingTrace: ep.ThinkingTrace,
					})
				}
			} else {
				episodes = append(episodes, prompter.EpisodeContext{
					Problem: r.Problem,
					Domain:  r.Domain,
					Outcome: r.Outcome,
					Tags:    r.Tags,
				})
			}
		}

		xmlBlock := prompter.BuildXMLEpisodeBlock(episodes)
		return mcp.NewToolResultText(xmlBlock), nil
	}
}

func handleConsolidate(es *store.EpisodeStore, cfg *models.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		strategy := getString(args, "strategy")
		if strategy == "" {
			strategy = "auto"
		}

		var report strings.Builder

		if strategy == "auto" || strategy == "clustered" || strategy == "merge" {
			candidates, err := es.FindMergeCandidates(cfg.Consolidation.MinEpisodesForPattern)
			if err != nil {
				fmt.Fprintf(&report, "⚠ find merge candidates: %v\n", err)
			} else {
				fmt.Fprintf(&report, "  Found %d merge candidates\n", len(candidates))
				for _, c := range candidates {
					pid, err := es.MergeToPattern(c)
					if err != nil {
						fmt.Fprintf(&report, "  ⚠ merge %s+%s: %v\n", c.A, c.B, err)
					} else {
						fmt.Fprintf(&report, "  ✓ merged → %s (score=%.3f)\n", pid, c.Score)
					}
				}
			}
		}

		if strategy == "auto" || strategy == "prune" {
			pruned, err := es.PruneFailures(cfg.Consolidation.PruneAfterDays)
			if err != nil {
				fmt.Fprintf(&report, "⚠ prune: %v\n", err)
			} else {
				fmt.Fprintf(&report, "  Pruned %d stale failure episodes\n", pruned)
			}
		}

		if strategy == "auto" || strategy == "index" {
			count, err := es.EpisodeCount()
			if err != nil {
				fmt.Fprintf(&report, "⚠ count: %v\n", err)
			} else {
				patCount, _ := es.PatternCount()
				fmt.Fprintf(&report, "  Index rebuilt: %d episodes, %d patterns\n", count, patCount)
			}
		}

		return mcp.NewToolResultText(report.String()), nil
	}
}

func handlePolish(es *store.EpisodeStore, cfg *models.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments

		rawPrompt := getString(args, "raw_prompt")
		domain := getString(args, "domain")
		skillName := getString(args, "skill_name")

		includeContext := true
		if b, ok := args["include_context"].(bool); ok {
			includeContext = b
		}
		topK := 3
		if tk, err := getFloat64(args, "top_k"); err == nil {
			topK = int(tk)
		}
		if topK > 5 {
			topK = 5
		}

		var contextStr string
		if includeContext {
			results, err := es.SearchLocal(rawPrompt, domain, "success", nil, topK)
			if err == nil && len(results) > 0 {
				var ctxEpisodes []prompter.EpisodeContext
				for _, r := range results {
					ctxEpisodes = append(ctxEpisodes, prompter.EpisodeContext{
						Problem: r.Problem,
						Domain:  r.Domain,
						Outcome: r.Outcome,
						Tags:    r.Tags,
					})
				}
				contextStr = prompter.BuildXMLEpisodeBlock(ctxEpisodes)
			}
		}

		result, _ := prompter.PolishPrompt(rawPrompt, domain, contextStr, skillName, false)

		data, _ := json.Marshal(map[string]interface{}{
			"polished_prompt": result.PolishedPrompt,
			"task_type":       result.TaskType,
			"language":        result.Language,
			"domain":          result.Domain,
			"skill_injected":  result.SkillInjected,
			"skill_name":      result.SkillName,
			"context_count":   result.ContextCount,
		})

		return mcp.NewToolResultText(string(data)), nil
	}
}

func getString(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getStringSlice(args map[string]interface{}, key string) []string {
	if v, ok := args[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			var result []string
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}

func getFloat64(args map[string]interface{}, key string) (float64, error) {
	if v, ok := args[key]; ok {
		if f, ok := v.(float64); ok {
			return f, nil
		}
	}
	return 0, fmt.Errorf("not found")
}

func getToolCalls(args map[string]interface{}, key string) []models.ToolCall {
	var result []models.ToolCall
	if v, ok := args[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					tc := models.ToolCall{
						Tool:    getString(m, "tool"),
						Outcome: getString(m, "outcome"),
					}
					if res, ok := m["result_excerpt"]; ok {
						if s, ok := res.(string); ok {
							tc.ResultExcerpt = s
						}
					}
					if a, ok := m["args"]; ok {
						tc.Args = a
					}
					result = append(result, tc)
				}
			}
		}
	}
	return result
}

func extractSteps(thinkingTrace string) []models.Step {
	lines := strings.Split(strings.TrimSpace(thinkingTrace), "\n")
	var steps []models.Step
	var current *models.Step

	stepTypes := map[string]string{
		"decide": "decision", "choose": "decision", "pick": "decision", "select": "decision",
		"option": "option_generation", "alternative": "option_generation", "consider": "option_generation", "approach": "option_generation",
		"implement": "implementation", "write": "implementation", "code": "implementation", "edit": "implementation", "create": "implementation",
		"verify": "verification", "test": "verification", "check": "verification", "validate": "verification",
		"error": "error", "bug": "error", "issue": "error", "problem": "error", "fail": "error",
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		stepType := "analysis"
		lower := strings.ToLower(line)
		for key, st := range stepTypes {
			if strings.Contains(lower, key) {
				stepType = st
				break
			}
		}

		if len(line) > 0 && line[0] >= '0' && line[0] <= '9' && len(line) > 3 && line[1] == '.' {
			if current != nil {
				steps = append(steps, *current)
			}
			current = &models.Step{
				ID:      fmt.Sprintf("s%d", len(steps)+1),
				Type:    stepType,
				Content: line,
			}
		} else if current != nil {
			current.Content += "\n" + line
		} else {
			current = &models.Step{
				ID:      fmt.Sprintf("s%d", len(steps)+1),
				Type:    stepType,
				Content: line,
			}
		}
	}

	if current != nil {
		steps = append(steps, *current)
	}

	if len(steps) == 0 {
		trace := thinkingTrace
		if len(trace) > 500 {
			trace = trace[:500]
		}
		steps = append(steps, models.Step{ID: "s1", Type: "analysis", Content: trace})
	}

	return steps
}

func configPath() string {
	if p := os.Getenv("REASONING_MEMORY_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".reasoning-memory", "config.yaml")
}

func dataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".reasoning-memory")
}

func init() {
	log.SetFlags(0)
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	_ = os.MkdirAll(filepath.Join(home, ".reasoning-memory"), 0700)
}

func reindexEpisodes(ctx context.Context, es *store.EpisodeStore, vec *store.VectorStore) {
	const batchSize = 10
	offset := 0
	total := 0
	for {
		summaries, err := es.ListEpisodes(batchSize, offset)
		if err != nil || len(summaries) == 0 {
			break
		}
		var contents []store.EpisodeContent
		for _, s := range summaries {
			ep, err := es.GetEpisode(s.ID)
			if err != nil || ep == nil {
				continue
			}
			contents = append(contents, store.EpisodeContent{
				ID:      ep.ID,
				Content: ep.Problem + "\n" + ep.ThinkingTrace,
			})
		}
		if len(contents) > 0 {
			if err := vec.AddEpisodes(ctx, contents); err != nil {
				log.Printf("⚠ reindex batch: %v", err)
			}
			total += len(contents)
		}
		offset += batchSize
	}
	log.Printf("✓ Reindexed %d episodes", total)
}
