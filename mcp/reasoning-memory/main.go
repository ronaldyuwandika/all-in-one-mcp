package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "embed"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/cli"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/config"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/linkcontent"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/security"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
)

var es *store.EpisodeStore
var cfg *models.Config
var cfgPath string
var linkService *linkcontent.Service
var mcpServer *server.MCPServer

//go:embed issues/visualization.html
var visualizationHTML string

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
	security.Configure(cfg.Security.Replacement)

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

	linkConfig := linkcontent.Config{
		Enabled:                  cfg.LinkIngestion.Enabled,
		MaxLinks:                 cfg.LinkIngestion.MaxLinks,
		RequestTimeoutSeconds:    cfg.LinkIngestion.RequestTimeoutSeconds,
		MaxRedirects:             cfg.LinkIngestion.MaxRedirects,
		MaxResponseBytes:         cfg.LinkIngestion.MaxResponseBytes,
		MaxExtractedChars:        cfg.LinkIngestion.MaxExtractedChars,
		MaxSummaryChars:          cfg.LinkIngestion.MaxSummaryChars,
		MaxConcurrency:           cfg.LinkIngestion.MaxConcurrency,
		CacheTTLMinutes:          cfg.LinkIngestion.CacheTTLMinutes,
		AllowedContentTypes:      cfg.LinkIngestion.AllowedContentTypes,
		FailurePolicy:            cfg.LinkIngestion.FailurePolicy,
		IncludeThinkingTrace:     cfg.LinkIngestion.IncludeThinkingTrace,
		RestRequirePreSummarized: cfg.LinkIngestion.RestRequirePreSummarized,
	}
	linkService = linkcontent.NewService(linkConfig, linkcontent.NewHTTPFetcher(linkConfig, linkConfig.AllowedContentTypes), samplingSummarizer{maxChars: cfg.LinkIngestion.MaxSummaryChars})
	defer func() { _ = linkService.Close() }()

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

var failedApproachSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"approach":     map[string]any{"type": "string"},
		"failure_mode": map[string]any{"type": "string"},
		"root_cause":   map[string]any{"type": "string"},
		"lesson":       map[string]any{"type": "string"},
	},
	"required": []string{"approach", "failure_mode", "root_cause", "lesson"},
}

func decodeFailedApproachesStrict(args map[string]interface{}) ([]models.FailedApproach, error) {
	raw, ok := args["failed_approaches"]
	if !ok || raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid failed_approaches array: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var values []models.FailedApproach
	if err := dec.Decode(&values); err != nil {
		return nil, fmt.Errorf("invalid failed_approaches array: %w", err)
	}
	return models.NormalizeFailedApproaches(values)
}

var toolCallSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"tool":           map[string]any{"type": "string"},
		"args":           map[string]any{"type": "object", "additionalProperties": true},
		"result_excerpt": map[string]any{"type": "string"},
		"outcome":        map[string]any{"type": "string"},
	},
}

var alternativeSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"name":             map[string]any{"type": "string"},
		"description":      map[string]any{"type": "string"},
		"rejection_reason": map[string]any{"type": "string"},
		"tradeoffs":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
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
	s.EnableSampling()
	mcpServer = s

	s.AddTool(mcp.NewTool("record_decision", mcp.WithDescription("Store a decision with rationale, trade-offs, assumptions, evidence, and rejected alternatives."),
		mcp.WithString("episode_id", mcp.Required()), mcp.WithString("repo"), mcp.WithString("title", mcp.Required()), mcp.WithString("selected", mcp.Required()), mcp.WithString("rationale", mcp.Required()), mcp.WithArray("tradeoffs", mcp.WithStringItems()), mcp.WithArray("assumptions", mcp.WithStringItems()), mcp.WithArray("evidence", mcp.WithStringItems()), mcp.WithArray("alternatives", mcp.Items(alternativeSchema))), handleCreateDecision(es))
	s.AddTool(mcp.NewTool("retrieve_decisions", mcp.WithDescription("Retrieve repository-scoped decision records explaining selected and rejected approaches."), mcp.WithString("query", mcp.Required()), mcp.WithString("repo", mcp.Required()), mcp.WithNumber("limit")), handleSearchDecisions(es))

	s.AddTool(
		mcp.NewTool("capture_reasoning_episode",
			mcp.WithDescription("Capture a completed reasoning episode at the END of a task.\n\n"+
				"Stores full trace in SQLite with FTS5 indexing and optional vector embedding. Returns episode ID."),
			mcp.WithString("problem", mcp.Description("The user's request or task description verbatim."), mcp.Required()),
			mcp.WithString("thinking_trace", mcp.Description("Full chain-of-thought reasoning text."), mcp.Required()),
			mcp.WithArray("tool_calls", mcp.Description("List of tool calling records, each with: tool (name), args, result_excerpt, outcome (success/failure)."), mcp.Items(toolCallSchema)),
			mcp.WithString("outcome", mcp.Description("Overall task outcome: success, partial, or failure."), mcp.Required()),
			mcp.WithArray("tags", mcp.Description("Domain tags e.g. [\"coding\", \"resilience\", \"retry\"]."), mcp.WithStringItems()),
			mcp.WithString("domain", mcp.Description("Broad domain: \"coding\" or \"agentic\". Defaults to \"coding\".")),
			mcp.WithString("tier", mcp.Description("Memory tier: \"episodic\" (default, short-term) or \"semantic\" (long-term, survives pruning).")),
			mcp.WithNumber("duration_seconds", mcp.Description("Total task duration in seconds.")),
			mcp.WithString("model_id", mcp.Description("Model identifier e.g. \"claude-sonnet-4-20260514\".")),
			mcp.WithString("repo", mcp.Description("Optional repository/project name for filtering. Auto-detected from git remote if omitted.")),
			mcp.WithObject("labels", mcp.Description("Optional metadata labels (key → [values]) for VectorDB-style mapping. Auto-enriched if omitted.")),
			mcp.WithString("project", mcp.Description("Optional project scope distinct from repository.")),
			mcp.WithString("provenance", mcp.Description("Optional origin or producer of this episode.")),
			mcp.WithNumber("confidence", mcp.Description("Optional finite confidence from 0 to 1.")),
			mcp.WithArray("objectives", mcp.WithStringItems()),
			mcp.WithArray("decisions", mcp.WithStringItems()),
			mcp.WithArray("alternatives", mcp.WithStringItems()),
			mcp.WithArray("verification", mcp.WithStringItems()),
			mcp.WithArray("lessons", mcp.WithStringItems()),
			mcp.WithArray("failed_approaches", mcp.Description("Optional failed approaches records containing approach, failure_mode, root_cause, lesson."), mcp.Items(failedApproachSchema)),
		),
		handleCapture(es, cfg),
	)
	s.AddTool(
		mcp.NewTool("create_episode",
			mcp.WithDescription("Alias for capture_reasoning_episode."),
			mcp.WithString("problem", mcp.Required()),
			mcp.WithString("thinking_trace", mcp.Required()),
			mcp.WithArray("tool_calls", mcp.Items(toolCallSchema)),
			mcp.WithString("outcome", mcp.Required()),
			mcp.WithArray("tags", mcp.WithStringItems()),
			mcp.WithString("domain"), mcp.WithString("tier"), mcp.WithNumber("duration_seconds"),
			mcp.WithString("model_id"), mcp.WithString("repo"), mcp.WithObject("labels"),
			mcp.WithString("project"), mcp.WithString("provenance"), mcp.WithNumber("confidence"),
			mcp.WithArray("objectives", mcp.WithStringItems()), mcp.WithArray("decisions", mcp.WithStringItems()),
			mcp.WithArray("alternatives", mcp.WithStringItems()), mcp.WithArray("verification", mcp.WithStringItems()),
			mcp.WithArray("lessons", mcp.WithStringItems()),
			mcp.WithArray("failed_approaches", mcp.Items(failedApproachSchema)),
		),
		handleCapture(es, cfg),
	)

	s.AddTool(mcp.NewTool("get_episode", mcp.WithDescription("Read one active or archived episode by ID."), mcp.WithString("episode_id", mcp.Required()), mcp.WithBoolean("include_archived")), handleGetEpisode(es))
	s.AddTool(mcp.NewTool("list_episodes", mcp.WithDescription("List active episode summaries."), mcp.WithNumber("limit"), mcp.WithNumber("offset")), handleListEpisodes(es))
	s.AddTool(mcp.NewTool("update_episode", mcp.WithDescription("Replace an active episode with a complete validated episode record."), mcp.WithObject("episode", mcp.Required())), handleUpdateEpisode(es))
	s.AddTool(mcp.NewTool("delete_episode", mcp.WithDescription("Delete one active episode by ID."), mcp.WithString("episode_id", mcp.Required())), handleDeleteEpisode(es))

	s.AddTool(
		mcp.NewTool("retrieve_reasoning",
			mcp.WithDescription("Search the local structured index for similar reasoning episodes."),
			mcp.WithString("problem", mcp.Description("Problem description to match against."), mcp.Required()),
			mcp.WithString("domain", mcp.Description("Filter by domain: \"coding\" or \"agentic\".")),
			mcp.WithString("outcome", mcp.Description("Filter by outcome: \"success\", \"partial\", or \"failure\".")),
			mcp.WithString("repo", mcp.Description("Filter by repository/project name.")),
			mcp.WithArray("tags", mcp.Description("Filter by tags (any match)."), mcp.WithStringItems()),
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
			mcp.WithBoolean("include_traces", mcp.Description("Include full thinking traces (default false) or just summaries.")),
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
				"Redacts secrets, detects task type, applies an agent profile, and optionally injects concise memory and skill context."),
			mcp.WithString("raw_prompt", mcp.Description("The user's raw/unstructured input."), mcp.Required()),
			mcp.WithString("target_agent", mcp.Description("Target profile: \"codex\", \"claude\", or \"generic\" (default).")),
			mcp.WithString("domain", mcp.Description("Optional task/domain override. Auto-detected if omitted.")),
			mcp.WithString("repo", mcp.Description("Optional repository or project scope.")),
			mcp.WithBoolean("include_context", mcp.Description("If true, search RMN for relevant past episodes (default true).")),
			mcp.WithNumber("top_k", mcp.Description("Number of context episodes to include (default 3, max 5).")),
			mcp.WithString("skill_name", mcp.Description("Optional skill name to load and inject.")),
			mcp.WithString("output_format", mcp.Description("Output format: \"markdown\" (default), \"json\", or \"xml\".")),
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
			mcp.WithArray("tags", mcp.Description("Optional tags for filtering."), mcp.WithStringItems()),
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
				Content: ep.Problem + "\n" + ep.ThinkingTrace + store.FailedApproachesText(ep.FailedApproaches),
			})
		}
		if len(contents) > 0 {
			if err := vec.AddEpisodes(ctx, contents); err != nil {
				log.Printf("⚠ reindex batch: %s", security.Text(err.Error()))
			}
			total += len(contents)
		}
		offset += batchSize
	}
	slog.Info("reindex complete", "total", total)
}

func getAlternatives(args map[string]interface{}, key string) []models.Alternative {
	value, ok := args[key]
	if !ok {
		return nil
	}
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	var alternatives []models.Alternative
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		tradeoffs := getStringSlice(m, "tradeoffs")
		alternatives = append(alternatives, models.Alternative{
			Name:            getString(m, "name"),
			Description:     getString(m, "description"),
			RejectionReason: getString(m, "rejection_reason"),
			Tradeoffs:       tradeoffs,
		})
	}
	return alternatives
}

func handleCreateDecision(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := toolArguments(req)
		d := &models.Decision{
			EpisodeID:    getString(a, "episode_id"),
			Repo:         getString(a, "repo"),
			Title:        getString(a, "title"),
			Selected:     getString(a, "selected"),
			Rationale:    getString(a, "rationale"),
			Tradeoffs:    getStringSlice(a, "tradeoffs"),
			Assumptions:  getStringSlice(a, "assumptions"),
			Evidence:     getStringSlice(a, "evidence"),
			Alternatives: getAlternatives(a, "alternatives"),
		}
		id, err := es.CreateDecision(ctx, d)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(id), nil
	}
}

func handleSearchDecisions(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := toolArguments(req)
		limit := 10
		if n, err := getFloat64(a, "limit"); err == nil {
			limit = int(n)
		}
		results, err := es.SearchDecisions(ctx, getString(a, "query"), getString(a, "repo"), limit)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		b, err := json.Marshal(results)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal results: %v", err)), nil
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

func linkIngestionFailed(sources []linkcontent.Source) bool {
	for _, source := range sources {
		if source.Status != linkcontent.StatusSummarized {
			return true
		}
	}
	return false
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
		var linkedSources []linkcontent.Source
		if linkService != nil {
			processed, err := linkService.Process(ctx, problem)
			if cfg.LinkIngestion.FailurePolicy == linkcontent.FailurePolicyFail && (err != nil || linkIngestionFailed(processed)) {
				return mcp.NewToolResultError("capture failed: link ingestion unavailable"), nil
			}
			linkedSources = processed
			if len(processed) > 0 {
				encoded, _ := json.Marshal(processed)
				problem += "\n\n<linked_sources>\n" + string(encoded) + "\n</linked_sources>"
			}
		}
		outcome, ok := models.NormalizeOutcome(getString(args, "outcome"))
		if !ok {
			return mcp.NewToolResultError("capture failed: invalid outcome"), nil
		}
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
		failedApproaches, err := decodeFailedApproachesStrict(args)
		if err != nil {
			return mcp.NewToolResultError("capture failed: " + err.Error()), nil
		}
		var confidence *float64
		if value, exists := args["confidence"]; exists {
			parsed, err := getFloat64(map[string]interface{}{"confidence": value}, "confidence")
			if err != nil {
				return mcp.NewToolResultError("capture failed: invalid confidence"), nil
			}
			confidence = &parsed
		}

		ep := &models.Episode{
			ID:               es.NextID(),
			Domain:           domain,
			Outcome:          outcome,
			Tier:             models.MemoryTier(tier),
			Tags:             tags,
			Repo:             repo,
			Project:          getString(args, "project"),
			Provenance:       getString(args, "provenance"),
			Confidence:       confidence,
			Labels:           labels,
			Problem:          problem,
			Objectives:       getStringSlice(args, "objectives"),
			Decisions:        getStringSlice(args, "decisions"),
			Alternatives:     getStringSlice(args, "alternatives"),
			Verification:     getStringSlice(args, "verification"),
			Lessons:          getStringSlice(args, "lessons"),
			FailedApproaches: failedApproaches,
			ThinkingTrace:    thinkingTrace,
			ToolCalls:        toolCalls,
			ModelID:          modelID,
			DurationSeconds:  durationSeconds,
		}
		security.Episode(ep)
		ep.Steps = extractSteps(ep.ThinkingTrace)

		episodeID, err := es.CreateEpisodeWithSourcesContext(ctx, ep, linkedSources)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("capture failed: %v", err)), nil
		}

		return mcp.NewToolResultText(episodeID), nil
	}
}

func handleGetEpisode(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := toolArguments(req)
		ep, err := es.GetEpisode(getString(args, "episode_id"))
		if err == nil && ep == nil {
			if include, _ := args["include_archived"].(bool); include {
				ep, err = es.GetArchivedEpisode(getString(args, "episode_id"))
			}
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if ep == nil {
			return mcp.NewToolResultError("episode not found"), nil
		}
		data, err := json.Marshal(ep)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleListEpisodes(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := toolArguments(req)
		limit, offset := 20, 0
		if value, err := getFloat64(args, "limit"); err == nil && value > 0 && value <= 100 {
			limit = int(value)
		}
		if value, err := getFloat64(args, "offset"); err == nil && value >= 0 {
			offset = int(value)
		}
		episodes, err := es.ListEpisodes(limit, offset)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		data, err := json.Marshal(episodes)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleUpdateEpisode(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, ok := toolArguments(req)["episode"]
		if !ok {
			return mcp.NewToolResultError("episode is required"), nil
		}
		data, err := json.Marshal(raw)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var ep models.Episode
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&ep); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if rawMap, ok := raw.(map[string]interface{}); ok {
			if idVal, exists := rawMap["id"]; exists {
				if idStr, isString := idVal.(string); isString && strings.TrimSpace(idStr) != "" && ep.ID != "" && strings.TrimSpace(idStr) != ep.ID {
					return mcp.NewToolResultError("ID mutation forbidden"), nil
				}
			}
		}
		if err := es.UpdateEpisode(ctx, &ep); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(ep.ID), nil
	}
}

func handleDeleteEpisode(es *store.EpisodeStore) server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := getString(toolArguments(req), "episode_id")
		if err := es.DeleteEpisode(id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(id), nil
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
			Outcome:       string(ep.Outcome),
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

func handleInject(es *store.EpisodeStore, cfg *models.Config) server.ToolHandlerFunc {
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
		includeTraces := cfg.PromptPolishing.IncludeFullTraces
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
						Problem:          r.Problem,
						Domain:           r.Domain,
						Outcome:          r.Outcome,
						Tags:             r.Tags,
						ThinkingTrace:    ep.ThinkingTrace,
						FailedApproaches: ep.FailedApproaches,
						EpisodeID:        ep.ID,
					})
				}
			} else {
				ep, _ := es.GetEpisode(r.ID)
				var failed []models.FailedApproach
				if ep != nil {
					failed = ep.FailedApproaches
				}
				episodes = append(episodes, prompter.EpisodeContext{
					Problem:          r.Problem,
					Domain:           r.Domain,
					Outcome:          r.Outcome,
					Tags:             r.Tags,
					FailedApproaches: failed,
					EpisodeID:        r.ID,
				})
			}
		}

		var promptPatterns []prompter.PatternContext
		if cfg.Retrieval.IncludePatterns {
			maxPats := cfg.Retrieval.MaxPatterns
			if maxPats <= 0 {
				maxPats = 2
			}
			pats, err := es.SearchPatterns(problem, "", nil, maxPats)
			if err == nil {
				for _, p := range pats {
					promptPatterns = append(promptPatterns, prompter.PatternContext{
						ID:                 p.ID,
						Domain:             p.Domain,
						ConsolidatedPrompt: p.ConsolidatedPrompt,
						MasterThinkingPath: p.MasterThinkingPath,
						Tags:               p.Tags,
						MergeScore:         p.MergeScore,
					})
				}
			}
		}

		xmlBlock := prompter.BuildXMLReasoningMemoryBlock(episodes, promptPatterns)
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
		targetAgent := getString(args, "target_agent")
		repo := getString(args, "repo")
		outputFormat := getString(args, "output_format")
		if targetAgent == "" {
			targetAgent = cfg.PromptPolishing.DefaultTargetAgent
		}
		if outputFormat == "" {
			outputFormat = cfg.PromptPolishing.DefaultOutputFormat
		}

		includeContext := cfg.PromptPolishing.IncludeMemoryByDefault
		if b, ok := args["include_context"].(bool); ok {
			includeContext = b
		}
		topK := cfg.PromptPolishing.MaxMemories
		if tk, err := getFloat64(args, "top_k"); err == nil {
			topK = int(tk)
		}
		if topK <= 0 {
			topK = 3
		}
		maxMemories := cfg.PromptPolishing.MaxMemories
		if maxMemories <= 0 || maxMemories > 5 {
			maxMemories = 5
		}
		if topK > maxMemories {
			topK = maxMemories
		}

		var contextStr string
		var promptEpisodes []prompter.EpisodeContext
		var promptPatterns []prompter.PatternContext
		var linkedContextStr string
		linkedWarnings := []string{}
		if linkService != nil {
			processed, lerr := linkService.Process(ctx, rawPrompt)
			if cfg.LinkIngestion.FailurePolicy == linkcontent.FailurePolicyFail && (lerr != nil || linkIngestionFailed(processed)) {
				return mcp.NewToolResultError("polish failed: link ingestion unavailable"), nil
			}
			if len(processed) > 0 {
				for _, source := range processed {
					if source.Status != linkcontent.StatusSummarized && source.Warning != "" {
						linkedWarnings = append(linkedWarnings, source.SourceURL+": "+source.Warning)
					}
				}
				rendered, werr := renderLinkedSources(processed)
				if werr != nil {
					slog.Warn("render linked sources", "error", werr)
				}
				linkedContextStr = rendered
			}
		}
		contextCount := 0
		if includeContext {
			results, err := es.SearchLocal(rawPrompt, domain, "success", repo, nil, topK)
			if err == nil {
				var ctxEpisodes []prompter.EpisodeContext
				for _, r := range results {
					ep, _ := es.GetEpisode(r.ID)
					var failed []models.FailedApproach
					if ep != nil {
						failed = ep.FailedApproaches
					}
					ctxEpisodes = append(ctxEpisodes, prompter.EpisodeContext{
						Problem:          r.Problem,
						Domain:           r.Domain,
						Outcome:          r.Outcome,
						Tags:             r.Tags,
						FailedApproaches: failed,
						EpisodeID:        r.ID,
					})
				}
				if cfg.PromptPolishing.IncludeFailureLessons && len(ctxEpisodes) < topK {
					failures, failureErr := es.SearchLocal(rawPrompt, domain, "failure", repo, nil, 1)
					if failureErr == nil && len(failures) > 0 {
						r := failures[0]
						ep, _ := es.GetEpisode(r.ID)
						var failed []models.FailedApproach
						if ep != nil {
							failed = ep.FailedApproaches
						}
						ctxEpisodes = append(ctxEpisodes, prompter.EpisodeContext{
							Problem:          r.Problem,
							Domain:           r.Domain,
							Outcome:          r.Outcome,
							Tags:             r.Tags,
							FailedApproaches: failed,
							EpisodeID:        r.ID,
						})
					}
				}
				if cfg.PromptPolishing.IncludePatterns {
					maxPats := cfg.PromptPolishing.MaxPatterns
					if maxPats <= 0 {
						maxPats = 2
					}
					pats, err := es.SearchPatterns(rawPrompt, domain, nil, maxPats)
					if err == nil {
						for _, p := range pats {
							promptPatterns = append(promptPatterns, prompter.PatternContext{
								ID:                 p.ID,
								Domain:             p.Domain,
								ConsolidatedPrompt: p.ConsolidatedPrompt,
								MasterThinkingPath: p.MasterThinkingPath,
								Tags:               p.Tags,
								MergeScore:         p.MergeScore,
							})
						}
					}
				}
				contextCount = len(ctxEpisodes) + len(promptPatterns)
				promptEpisodes = ctxEpisodes
				contextStr = prompter.BuildXMLReasoningMemoryBlock(ctxEpisodes, promptPatterns)
			}
		}

		result, err := prompter.PolishPromptWithOptions(prompter.Options{
			RawPrompt: rawPrompt, TargetAgent: targetAgent, Domain: domain,
			Repo: repo, Context: contextStr, LinkedContext: linkedContextStr, SkillName: skillName,
			OutputFormat: outputFormat, MaxChars: cfg.PromptPolishing.MaxPromptChars,
			ContextCount: contextCount, Episodes: promptEpisodes, Patterns: promptPatterns,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("polish failed: %v", err)), nil
		}
		if len(linkedWarnings) > 0 {
			result.Warnings = append(result.Warnings, linkedWarnings...)
		}

		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}
}

func toolArguments(req mcp.CallToolRequest) map[string]interface{} {
	data, err := json.Marshal(req.Params.Arguments)
	if err != nil {
		return map[string]interface{}{}
	}
	var args map[string]interface{}
	if err := json.Unmarshal(data, &args); err != nil || args == nil {
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(visualizationHTML))
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

	// JSON REST API endpoints (Issue #130)
	mux.HandleFunc("/api/episodes", jsonMiddleware(handleAPIEpisodes))
	mux.HandleFunc("/api/patterns", jsonMiddleware(handleAPIPatterns))
	mux.HandleFunc("/api/stats", jsonMiddleware(handleAPIStats))
	mux.HandleFunc("/api/polish", jsonMiddleware(handleAPIPolish))
	mux.HandleFunc("/api/graph", jsonMiddleware(handleAPIGraph))

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

// CORS and Content-Type Middleware
func jsonMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		next(w, r)
	}
}

func handleAPIEpisodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")
	tagFilter := r.URL.Query().Get("tag")
	repoFilter := r.URL.Query().Get("repo")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	limit := 20
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}
	offset := (page - 1) * limit

	var total int
	baseQuery := "FROM episodes WHERE 1=1"
	args := []interface{}{}
	if repoFilter != "" {
		baseQuery += " AND repo = ?"
		args = append(args, repoFilter)
	}
	if tagFilter != "" {
		baseQuery += " AND tags LIKE ?"
		args = append(args, "%\""+tagFilter+"\"%")
	}

	err := es.DB().QueryRow("SELECT COUNT(*) "+baseQuery, args...).Scan(&total)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "count query: %v"}`, err), http.StatusInternalServerError)
		return
	}

	selectQuery := "SELECT id, created_at, problem, domain, outcome, tier, tags, repo, labels, steps, tool_calls, model_id, duration_seconds " + baseQuery + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := es.DB().Query(selectQuery, args...)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "select query: %v"}`, err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

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
			http.Error(w, fmt.Sprintf(`{"error": "scan: %v"}`, err), http.StatusInternalServerError)
			return
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
		security.Summary(&s)
		summaries = append(summaries, s)
	}

	resp := map[string]interface{}{
		"episodes": summaries,
		"total":    total,
		"page":     page,
		"limit":    limit,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func handleAPIPatterns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	patterns, err := es.ListPatterns()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "%v"}`, err), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(patterns)
}

func handleAPIStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	epTotal, _ := es.EpisodeCount()
	patTotal, _ := es.PatternCount()
	byDomain, _ := es.EpisodesByDomain()
	byOutcome, _ := es.EpisodesByOutcome()
	byRepo, _ := es.EpisodesByRepo()
	topTags, _ := es.TopTags(10)
	avgProb, avgTrace, _ := es.AvgEpisodeLengths()
	dbSize, _ := es.DBSizeMB()
	ftsSize, _ := es.FTSSizeMB()
	lastConsolidation, _ := es.LastConsolidationTS()
	summary, _ := es.SummaryStats()
	epByDay, _ := es.EpisodesByDay(7)
	labelKeys, _ := es.TopLabelKeys(10)

	var vecSize float64
	var vecCount int
	vs := es.VectorStore()
	if vs != nil {
		vecCount = vs.Count()
		vecSize = 0
	}

	result := models.StatsResult{
		EpisodesTotal:         epTotal,
		PatternsTotal:         patTotal,
		EpisodesByDomain:      byDomain,
		EpisodesByOutcome:     byOutcome,
		EpisodesByRepo:        byRepo,
		TopTags:               topTags,
		VectorIndexSizeMB:     vecSize,
		VectorCount:           vecCount,
		FTSSizeMB:             ftsSize,
		DBSizeMB:              dbSize,
		ConsolidationsTotal:   patTotal,
		AvgEpisodeLenChars:    avgProb,
		AvgThinkingTraceChars: avgTrace,
	}
	if summary != nil {
		result.SuccessRate = summary.SuccessRate
		result.ConsolidationRatio = summary.ConsolidationRatio
		result.TopDomain = summary.TopDomain
		result.TopRepo = summary.TopRepo
		result.AvgDurationSec = summary.AvgDurationSec
		result.TopLabelKey = summary.TopLabelKey
		result.LabelCardinality = summary.LabelCardinality
		result.UnlabeledCount = summary.UnlabeledCount
		result.ArchivedTotal = summary.TotalArchived
		result.PrunedTotal = summary.TotalPruned
	}
	if epByDay != nil {
		result.EpisodesByDay = epByDay
	}
	if len(labelKeys) > 0 {
		lb := make([]models.LabelCount, len(labelKeys))
		for i, tc := range labelKeys {
			lb[i] = models.LabelCount{Key: tc.Tag, Value: "", Count: tc.Count}
		}
		result.EpisodesByLabel = lb
	}

	if lastConsolidation != nil {
		ts := lastConsolidation.Format("2006-01-02T15:04:05Z")
		result.LastConsolidationTS = &ts
	}

	_ = json.NewEncoder(w).Encode(result)
}

type polishRequest struct {
	RawPrompt     string               `json:"raw_prompt"`
	Domain        string               `json:"domain"`
	Repo          string               `json:"repo"`
	TargetAgent   string               `json:"target_agent"`
	OutputFormat  string               `json:"output_format"`
	MaxChars      int                  `json:"max_chars"`
	SkillName     string               `json:"skill_name"`
	Compact       bool                 `json:"compact"`
	LinkedSources []linkcontent.Source `json:"linked_sources"`
}

func handleAPIPolish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req polishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	if req.RawPrompt == "" {
		http.Error(w, `{"error": "raw_prompt is required"}`, http.StatusBadRequest)
		return
	}
	maxLinks := cfg.LinkIngestion.MaxLinks
	if maxLinks <= 0 {
		maxLinks = 5
	}
	if len(req.LinkedSources) > maxLinks {
		http.Error(w, `{"error": "too many linked_sources"}`, http.StatusBadRequest)
		return
	}

	linkedContext := ""
	var linkedWarnings []string
	if linkService != nil {
		if len(req.LinkedSources) > 0 {
			processed, warnings, err := linkService.ProcessProvided(req.RawPrompt, req.LinkedSources)
			if cfg.LinkIngestion.FailurePolicy == linkcontent.FailurePolicyFail && (err != nil || linkIngestionFailed(processed)) {
				http.Error(w, `{"error": "polish failed: link ingestion unavailable"}`, http.StatusBadRequest)
				return
			}
			linkedWarnings = append(linkedWarnings, warnings...)
			if len(processed) > 0 {
				rendered, rerr := renderLinkedSources(processed)
				if rerr != nil {
					http.Error(w, fmt.Sprintf(`{"error": "%v"}`, rerr), http.StatusBadRequest)
					return
				}
				linkedContext = rendered
			}
		} else if cfg.LinkIngestion.RestRequirePreSummarized {
			urls := linkcontent.ExtractURLs(req.RawPrompt, cfg.LinkIngestion.MaxLinks)
			if len(urls) > 0 {
				if cfg.LinkIngestion.FailurePolicy == linkcontent.FailurePolicyFail {
					http.Error(w, `{"error": "polish failed: link_summary_required"}`, http.StatusBadRequest)
					return
				}
				for _, u := range urls {
					linkedWarnings = append(linkedWarnings, "link_summary_required: "+linkcontent.SafeSourceURL(u))
				}
			}
		}
	}

	result, err := prompter.PolishPromptWithOptions(prompter.Options{
		RawPrompt:     req.RawPrompt,
		TargetAgent:   req.TargetAgent,
		Domain:        req.Domain,
		Repo:          req.Repo,
		SkillName:     req.SkillName,
		CompactSkill:  req.Compact,
		OutputFormat:  req.OutputFormat,
		MaxChars:      req.MaxChars,
		LinkedContext: linkedContext,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "%v"}`, err), http.StatusInternalServerError)
		return
	}
	if len(linkedWarnings) > 0 {
		result.Warnings = append(result.Warnings, linkedWarnings...)
	}

	_ = json.NewEncoder(w).Encode(result)
}

type nodeJSON struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`
}
type edgeJSON struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Label  string  `json:"label"`
	Weight float64 `json:"weight"`
}
type graphJSON struct {
	Nodes []nodeJSON `json:"nodes"`
	Edges []edgeJSON `json:"edges"`
}

func handleAPIGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	concepts, err := es.ListConcepts(1000, 0, "")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "list concepts: %v"}`, err), http.StatusInternalServerError)
		return
	}

	edges, err := es.ListEdges("")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "list edges: %v"}`, err), http.StatusInternalServerError)
		return
	}

	nodesMap := make(map[string]nodeJSON)

	for _, c := range concepts {
		nodesMap[c.ID] = nodeJSON{ID: c.ID, Label: c.EntityName, Type: "concept"}
	}

	for _, e := range edges {
		if strings.HasPrefix(e.SourceID, "re-") {
			if _, ok := nodesMap[e.SourceID]; !ok {
				ep, err := es.GetSummary(e.SourceID)
				if err == nil && ep != nil {
					nodesMap[e.SourceID] = nodeJSON{ID: ep.ID, Label: ep.Problem, Type: "episode"}
				} else {
					nodesMap[e.SourceID] = nodeJSON{ID: e.SourceID, Label: e.SourceID, Type: "episode"}
				}
			}
		}
		if strings.HasPrefix(e.TargetID, "re-") {
			if _, ok := nodesMap[e.TargetID]; !ok {
				ep, err := es.GetSummary(e.TargetID)
				if err == nil && ep != nil {
					nodesMap[e.TargetID] = nodeJSON{ID: ep.ID, Label: ep.Problem, Type: "episode"}
				} else {
					nodesMap[e.TargetID] = nodeJSON{ID: e.TargetID, Label: e.TargetID, Type: "episode"}
				}
			}
		}
	}

	var nodes []nodeJSON
	for _, n := range nodesMap {
		nodes = append(nodes, n)
	}

	var outEdges []edgeJSON
	for _, e := range edges {
		outEdges = append(outEdges, edgeJSON{
			From:   e.SourceID,
			To:     e.TargetID,
			Label:  e.Relationship,
			Weight: e.Weight,
		})
	}

	resp := graphJSON{
		Nodes: nodes,
		Edges: outEdges,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
