package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/cli"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/config"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
)

var es *store.EpisodeStore
var cfg *models.Config
var cfgPath string

func main() {
	store.SetupLogger()

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dataDir := filepath.Join(home, ".reasoning-memory")
	_ = os.MkdirAll(dataDir, 0700)
	dbPath := filepath.Join(dataDir, "store.db")
	cfgPath = configPath()

	var loadErr error
	cfg, loadErr = config.Load(cfgPath)
	if loadErr != nil {
		log.Fatalf("load config: %v", loadErr)
	}

	vecDataDir := dataDir
	vec, vecErr := store.NewVectorStore(
		vecDataDir,
		cfg.Embedding.Provider,
		cfg.Embedding.Model,
		cfg.Embedding.BaseURL,
		cfg.Embedding.APIKey,
		cfg.Embedding.Enabled,
	)
	if vecErr != nil {
		slog.Warn("vector store disabled", "error", vecErr)
		vec = nil
	}

	if vec != nil && vec.Enabled() {
		es, loadErr = store.NewWithVector(dbPath, vec)
	} else {
		es, loadErr = store.New(dbPath)
	}
	if loadErr != nil {
		log.Fatalf("open store: %v", loadErr)
	}
	store.SetGlobalStore(es)
	defer func() { _ = es.Close() }()

	if vec != nil {
		slog.Info("vector search enabled", "provider", cfg.Embedding.Provider, "model", cfg.Embedding.Model)
		epCount, _ := es.EpisodeCount()
		if epCount > 0 && vec.Count() == 0 {
			slog.Info("reindexing episodes into vector DB", "count", epCount)
			ctx := context.Background()
			reindexEpisodes(ctx, es, vec)
		}
	}

	go startMetricsEndpoint()

	rootCmd := &cobra.Command{
		Use:   "reasoning-memory",
		Short: "Reasoning Memory Network — MCP server + CLI tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPServer()
		},
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP stdio server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPServer()
		},
	}

	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(cli.NewStatsCmd(es))
	rootCmd.AddCommand(cli.NewDoctorCmd(es, cfgPath))
	rootCmd.AddCommand(cli.NewDashboardCmd(es, cfgPath, cfg))
	rootCmd.AddCommand(cli.NewCompactCmd(es, cfg))

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runMCPServer() error {
	go handleSignals()

	ctx, cancel := context.WithCancel(context.Background())
	es.CompactionCancel = cancel
	es.StartCompactionLoop(ctx, cfg.Consolidation)

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
			mcp.WithString("tier", mcp.Description("Memory tier: \"episodic\" (default, short-term) or \"semantic\" (long-term, survives pruning).")),
			mcp.WithNumber("duration_seconds", mcp.Description("Total task duration in seconds.")),
			mcp.WithString("model_id", mcp.Description("Model identifier e.g. \"claude-sonnet-4-20260514\".")),
			mcp.WithString("repo", mcp.Description("Optional repository/project name for filtering. Auto-detected from git remote if omitted.")),
			mcp.WithObject("labels", mcp.Description("Optional metadata labels (key → [values]) for VectorDB-style mapping. Auto-enriched if omitted.")),
		),
		handleCapture(es, cfg),
	)

	s.AddTool(
		mcp.NewTool("retrieve_reasoning",
			mcp.WithDescription("Search the local structured index for similar reasoning episodes."),
			mcp.WithString("problem", mcp.Description("Problem description to match against."), mcp.Required()),
			mcp.WithString("domain", mcp.Description("Filter by domain: \"coding\" or \"agentic\".")),
			mcp.WithString("outcome", mcp.Description("Filter by outcome: \"success\", \"partial\", or \"failure\".")),
			mcp.WithString("repo", mcp.Description("Filter by repository/project name.")),
			mcp.WithArray("tags", mcp.Description("Filter by tags (any match).")),
			mcp.WithObject("metadata_filter", mcp.Description("Filter by metadata labels e.g. {\"language\": \"go\", \"severity\": \"bug\"}")),
			mcp.WithNumber("top_k", mcp.Description("Max results (default 5, max 20).")),
		),
		handleRetrieve(es, cfg),
	)

	s.AddTool(
		mcp.NewTool("enrich_episode",
			mcp.WithDescription("Run auto-enrichment on an existing episode to populate its metadata labels."),
			mcp.WithString("episode_id", mcp.Description("The episode ID to enrich."), mcp.Required()),
		),
		handleEnrich(es, cfg),
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

	s.AddTool(
		mcp.NewTool("memorize_concept",
			mcp.WithDescription("Store an entity/concept as a standalone semantic memory (not a full episode).\n\n"+
				"Use for atomic facts, entities, or definitions that don't need a full reasoning trace."),
			mcp.WithString("entity_name", mcp.Description("The entity or concept name."), mcp.Required()),
			mcp.WithString("concept_type", mcp.Description("Concept type/category e.g. 'tool', 'service', 'library', 'pattern'.")),
			mcp.WithString("description", mcp.Description("Description or definition of the concept."), mcp.Required()),
			mcp.WithArray("tags", mcp.Description("Optional tags for filtering.")),
			mcp.WithString("source_episode_id", mcp.Description("Optional source episode ID this concept was extracted from.")),
		),
		handleMemorizeConcept(es),
	)

	s.AddTool(
		mcp.NewTool("recall_semantic",
			mcp.WithDescription("Retrieve top-k semantic concepts by semantic similarity to a query string."),
			mcp.WithString("query", mcp.Description("Query string to match against concepts."), mcp.Required()),
			mcp.WithNumber("limit", mcp.Description("Max results (default 5, max 20).")),
			mcp.WithString("type_filter", mcp.Description("Optional filter by concept type.")),
		),
		handleRecallSemantic(es),
	)

	s.AddTool(
		mcp.NewTool("link_entities",
			mcp.WithDescription("Create a directed relationship between two semantic concepts or episodes."),
			mcp.WithString("source_id", mcp.Description("Source concept/episode ID."), mcp.Required()),
			mcp.WithString("target_id", mcp.Description("Target concept/episode ID."), mcp.Required()),
			mcp.WithString("relationship", mcp.Description("Relationship type e.g. 'depends_on', 'implements', 'fixes', 'references'."), mcp.Required()),
			mcp.WithNumber("weight", mcp.Description("Relationship weight (default 1.0).")),
		),
		handleLinkEntities(es),
	)

	s.AddTool(
		mcp.NewTool("traverse_concepts",
			mcp.WithDescription("Traverse the knowledge graph from a starting entity up to max_hops, returning reachable concepts."),
			mcp.WithString("start_id", mcp.Description("Starting entity/concept/episode ID."), mcp.Required()),
			mcp.WithString("relationship", mcp.Description("Optional filter by relationship type. Empty to match all.")),
			mcp.WithNumber("max_hops", mcp.Description("Maximum traversal depth (default 3, max 10).")),
		),
		handleTraverseConcepts(es),
	)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server: %v", err)
	}
	return nil
}

func configPath() string {
	if p := os.Getenv("REASONING_MEMORY_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".reasoning-memory", "config.yaml")
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
	slog.Info("reindex complete", "total", total)
}

func handleCapture(es *store.EpisodeStore, _ *models.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		defer func() {
			store.GlobalMetrics.CaptureDurations.Record(time.Since(start))
			store.GlobalMetrics.EpisodesCaptured.Add(1)
		}()

		args := toolArguments(req)

		problem := getString(args, "problem")
		thinkingTrace := getString(args, "thinking_trace")
		outcome := getString(args, "outcome")
		domain := getString(args, "domain")
		if domain == "" {
			domain = "coding"
		}
		tags := getStringSlice(args, "tags")
		repo := getString(args, "repo")
		labels := getStringMap(args, "labels")
		tier := getString(args, "tier")
		if tier == "" {
			tier = "episodic"
		}

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
			Tier:            models.MemoryTier(tier),
			Tags:            tags,
			Repo:            repo,
			Labels:          labels,
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
		start := time.Now()
		defer func() {
			store.GlobalMetrics.SearchDurations.Record(time.Since(start))
			store.GlobalMetrics.SearchesPerformed.Add(1)
		}()

		args := toolArguments(req)

		problem := getString(args, "problem")
		domain := getString(args, "domain")
		outcome := getString(args, "outcome")
		repo := getString(args, "repo")
		tags := getStringSlice(args, "tags")
		metadataFilter := getStringMap(args, "metadata_filter")

		topK := 5
		if tk, err := getFloat64(args, "top_k"); err == nil {
			topK = int(tk)
		}
		if topK > 20 {
			topK = 20
		}

		results, err := es.SearchLocal(problem, domain, outcome, repo, tags, topK, metadataFilter)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}

		data, _ := json.Marshal(results)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleEnrich(es *store.EpisodeStore, _ *models.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := toolArguments(req)
		episodeID := getString(args, "episode_id")

		ep, err := es.GetEpisode(episodeID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get episode: %v", err)), nil
		}
		if ep == nil {
			return mcp.NewToolResultError(fmt.Sprintf("episode not found: %s", episodeID)), nil
		}

		tcJSON, _ := json.Marshal(ep.ToolCalls)
		ec := store.EnrichCtx{
			Problem:       ep.Problem,
			ThinkingTrace: ep.ThinkingTrace,
			ToolCalls:     string(tcJSON),
			Outcome:       ep.Outcome,
			Domain:        ep.Domain,
			ExistingTags:  ep.Tags,
			ExistingRepo:  ep.Repo,
		}
		labels := store.EnrichLabels(ec)
		if err := es.SetLabels(episodeID, labels); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("enrich failed: %v", err)), nil
		}

		lj, _ := json.Marshal(labels)
		return mcp.NewToolResultText(fmt.Sprintf("Enriched %s: %s", episodeID, string(lj))), nil
	}
}

func handleInject(es *store.EpisodeStore, _ *models.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := toolArguments(req)

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

		results, err := es.SearchLocal(problem, "", "", "", nil, topK)
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
		start := time.Now()
		defer func() {
			store.GlobalMetrics.ConsolidationDurs.Record(time.Since(start))
			store.GlobalMetrics.ConsolidationsRan.Add(1)
		}()

		args := toolArguments(req)
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
		args := toolArguments(req)

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
			results, err := es.SearchLocal(rawPrompt, domain, "success", "", nil, topK)
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

func toolArguments(req mcp.CallToolRequest) map[string]interface{} {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return args
}

func getString(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getStringMap(args map[string]interface{}, key string) map[string][]string {
	if v, ok := args[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			result := make(map[string][]string)
			for k, val := range m {
				switch arr := val.(type) {
				case []interface{}:
					for _, item := range arr {
						if s, ok := item.(string); ok {
							result[k] = append(result[k], s)
						}
					}
				case string:
					result[k] = []string{arr}
				}
			}
			return result
		}
	}
	return nil
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

func handleMemorizeConcept(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		store.GlobalMetrics.ConceptsMemorized.Add(1)

		args := toolArguments(req)
		entityName := getString(args, "entity_name")
		conceptType := getString(args, "concept_type")
		description := getString(args, "description")
		tags := getStringSlice(args, "tags")
		sourceEpisodeID := getString(args, "source_episode_id")

		id, err := es.MemorizeConcept(ctx, entityName, conceptType, description, tags, sourceEpisodeID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("memorize failed: %v", err)), nil
		}
		return mcp.NewToolResultText(id), nil
	}
}

func handleRecallSemantic(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := toolArguments(req)
		query := getString(args, "query")
		limit := 5
		if v, err := getFloat64(args, "limit"); err == nil {
			limit = int(v)
		}
		typeFilter := getString(args, "type_filter")

		results, err := es.RecallSemantic(ctx, query, limit, typeFilter)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("recall failed: %v", err)), nil
		}
		data, _ := json.Marshal(results)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleLinkEntities(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		store.GlobalMetrics.EdgesCreated.Add(1)

		args := toolArguments(req)
		sourceID := getString(args, "source_id")
		targetID := getString(args, "target_id")
		relationship := getString(args, "relationship")
		weight := 1.0
		if v, err := getFloat64(args, "weight"); err == nil && v > 0 {
			weight = v
		}
		if weight > 1.0 {
			weight = 1.0
		}

		id, err := es.AddEdge(sourceID, targetID, relationship, weight)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("link failed: %v", err)), nil
		}
		return mcp.NewToolResultText(id), nil
	}
}

func handleTraverseConcepts(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := toolArguments(req)
		startID := getString(args, "start_id")
		relationship := getString(args, "relationship")
		maxHops := 3
		if v, err := getFloat64(args, "max_hops"); err == nil && v > 0 {
			maxHops = int(v)
		}

		results, err := es.Traverse(startID, relationship, maxHops)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("traverse failed: %v", err)), nil
		}
		data, _ := json.Marshal(results)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func startMetricsEndpoint() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", store.MetricsHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if es != nil {
			if err := es.Readiness(); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9464"
	}
	slog.Info("metrics endpoint starting", "addr", ":"+port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("metrics server", "error", err)
	}
}

func handleSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	slog.Info("received signal, shutting down", "signal", sig.String())
	if es != nil {
		if err := es.Shutdown(); err != nil {
			slog.Error("shutdown error", "error", err)
		}
	}
	os.Exit(0)
}
