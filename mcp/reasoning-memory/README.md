# reasoning-memory

[![Go 1.24](https://img.shields.io/badge/go-1.24-00ADD8?style=flat-square&logo=go)](https://golang.org/doc/go1.24)
[![MCP](https://img.shields.io/badge/MCP-compatible-6B21A8?style=flat-square)](https://modelcontextprotocol.io)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](../../LICENSE)
[![Build](https://img.shields.io/github/actions/workflow/status/ronaldyuwandika/all-in-one-mcp/lint.yaml?branch=main&style=flat-square)](../../actions)
[![Coverage](https://img.shields.io/codecov/c/github/ronaldyuwandika/all-in-one-mcp?style=flat-square)](https://codecov.io/gh/ronaldyuwandika/all-in-one-mcp)

> Captures, stores, searches, and consolidates LLM reasoning traces for prompt engineering and agent memory.

## Quick Start

```bash
make run-reasoning-memory
# Or installed:
reasoning-memory
```

## MCP Tools

| Tool | Description | Required Params |
|------|-------------|-----------------|
| `capture_reasoning_episode` | Store full trace at task end | `problem`, `thinking_trace`, `outcome` |
| `retrieve_reasoning` | Search episodes | `problem` |
| `inject_reasoning_context` | Get XML block for prompt injection | `problem` |
| `enrich_episode` | Auto-enrich labels for an existing episode | `episode_id` |
| `consolidate_reasoning` | Cluster, merge, prune, reindex | — |
| `polish_prompt` | Structure raw prompt + inject skill context | `raw_prompt` |

### `capture_reasoning_episode`

Persists a complete reasoning trace (problem, thinking trace, outcome, tool calls, tags, domain, duration, model) to SQLite with FTS5 + optional vector indexing.

```json
{
  "problem": "How do I implement retry-with-backoff?",
  "thinking_trace": "...",
  "outcome": "success",
  "domain": "coding",
  "tags": ["resilience", "retry"],
  "tool_calls": [{"tool": "grep", "args": "...", "result_excerpt": "...", "outcome": "success"}],
  "duration_seconds": 120,
  "model_id": "claude-sonnet-4-20260514",
  "repo": "my-org/my-service",
  "labels": {
    "language": ["go"],
    "framework": ["net/http"],
    "severity": ["high"]
  }
}
```

Labels are auto-enriched when omitted: language detection, framework detection (Go, Python, JS, Rust, Docker), severity (critical/high/medium/low), and entity extraction (cache, db, api, auth, deploy, test).

### `retrieve_reasoning`

Hybrid FTS5 + vector search returning ranked, deduplicated episode summaries.

```json
{
  "problem": "Go concurrency patterns",
  "domain": "coding",
  "outcome": "success",
  "tags": ["concurrency"],
  "top_k": 5,
  "metadata_filter": {
    "language": ["go"],
    "severity": ["high", "critical"]
  }
}
```

`metadata_filter` narrows results by label key/value pairs. Multiple values per key are OR'ed; multiple keys are AND'ed.

### `inject_reasoning_context`

Returns a `<reasoning_memory>` XML block ready for prompt prepending.

```json
{
  "problem": "Refactor a large Go service",
  "top_k": 3,
  "include_traces": true
}
```

### `enrich_episode`

Runs auto-enrichment (language, framework, severity, entity detection) on an existing episode and persists the labels.

```json
{
  "episode_id": "re-20260714-003"
}
```

Returns a confirmation with the enriched labels.

### `consolidate_reasoning`

Multi-phase pipeline: find merge candidates → merge similar episodes → prune stale failures → rebuild index.

```json
{
  "strategy": "auto"
}
```

### `polish_prompt`

Auto-detects task type (coding/agentic/analysis/general), programming language, injects skill rules from SKILL.md, and merges relevant past reasoning context.

```json
{
  "raw_prompt": "help me write tests for my Go service",
  "domain": "coding",
  "include_context": true,
  "top_k": 3,
  "skill_name": "golang-service"
}
```

## Demo Episodes

Live traces captured during a single session across all 5 tools. Full source: [`bench/results/demo-episodes.json`](./bench/results/demo-episodes.json).

### Captured via `capture_reasoning_episode`

**Episode 1 — Fix nil pointer dereference**

```json
{
  "id": "re-20260714-003",
  "problem": "Fix a nil pointer dereference in the Go HTTP handler",
  "thinking_trace": "1. I saw the panic in the logs: nil pointer dereference at handler.go:42\n2. The issue was that r.FormValue(\"id\") returns empty string...",
  "outcome": "success",
  "domain": "coding",
  "tags": ["go", "nil-pointer", "http-handler"],
  "steps": [
    {"type": "analysis", "content": "1. I saw the panic in the logs..."},
    {"type": "verification", "content": "2. The issue was that..."},
    {"type": "option_generation", "content": "3. Considered two approaches..."},
    {"type": "error", "content": "4. Decided to add validation..."},
    {"type": "verification", "content": "5. Implemented the fix..."},
    {"type": "verification", "content": "6. Verified with unit test..."}
  ],
  "tool_calls": [
    {"tool": "grep", "outcome": "success"},
    {"tool": "edit", "outcome": "success"}
  ],
  "model_id": "claude-sonnet-4-20260514",
  "duration_seconds": 180
}
```

**Episode 2 — Design rate limiter middleware**

```json
{
  "id": "re-20260714-004",
  "problem": "Design a rate limiter middleware for a Go HTTP service",
  "outcome": "success",
  "domain": "coding",
  "tags": ["go", "middleware", "rate-limiter", "concurrency"],
  "steps": [
    {"type": "analysis", "content": "1. Requirement: 100 req/s per IP..."},
    {"type": "analysis", "content": "2. Compared token bucket vs sliding window..."},
    {"type": "analysis", "content": "3. Chose sliding window..."},
    {"type": "analysis", "content": "4. Used sync.Map for IP counters..."},
    {"type": "implementation", "content": "5. Implemented middleware..."},
    {"type": "analysis", "content": "6. Added configurable rate and burst..."},
    {"type": "verification", "content": "7. Wrote table-driven tests..."},
    {"type": "verification", "content": "8. Benchmark: <1μs overhead"}
  ],
  "model_id": "claude-sonnet-4-20260514",
  "duration_seconds": 600
}
```

### Retrieved via `retrieve_reasoning`

Query: `"How to handle nil pointers in Go HTTP handlers"` → ranked results with top score `1.017` matching Episode 1.

### Injected via `inject_reasoning_context`

Query: `"Go middleware design patterns"` → XML block with 3 relevant episodes ready for prompt prepending.

### Polished via `polish_prompt`

Input: `"build a dockerfile for my go service"` + skill `docker-expert` → detected `coding` task type, injected docker-expert rules, appended relevant past reasoning.

### Consolidated via `consolidate_reasoning`

Strategy `auto` → found 1 merge candidate, merged into pattern `pat-re-20260714-002-re-20260714-001` (score 1.567), rebuilt index: 8 episodes, 1 pattern.

### Full Invocation Trace

| # | Tool | Input | Output |
|   |------|-------|--------|
| 1 | `capture_reasoning_episode` | `{"problem": "Fix a nil pointer dereference...", "outcome": "success", "tags": ["go","nil-pointer","http-handler"]}` | `re-20260714-003` |
| 2 | `capture_reasoning_episode` | `{"problem": "Design a rate limiter middleware...", "outcome": "success", "tags": ["go","middleware","rate-limiter","concurrency"]}` | `re-20260714-004` |
| 3 | `enrich_episode` | `{"episode_id": "re-20260714-003"}` | `Enriched re-20260714-003: {"language":["go"],"framework":["net/http"],"severity":["high"],"tag":["go","nil-pointer","http-handler"],"domain":["coding"],"outcome":["success"]}` |
| 4 | `retrieve_reasoning` | `{"problem": "How to handle nil pointers in Go HTTP handlers", "top_k": 5, "metadata_filter": {"language": ["go"]}}` | Top result: `re-20260714-003` (score 1.267, labels boosted) |
| 5 | `inject_reasoning_context` | `{"problem": "Go middleware design patterns", "top_k": 3}` | `<reasoning_memory>` XML with 3 episodes |
| 6 | `polish_prompt` | `{"raw_prompt": "build a dockerfile for my go service", "skill_name": "docker-expert"}` | `coding` task type, skill injected, 1 context episode appended |
| 7 | `consolidate_reasoning` | `{"strategy": "auto"}` | Merged 1 pair → `pat-re-20260714-002-re-20260714-001` (score 1.567), index rebuilt: 8 eps, 1 pattern |

**Process flow:**
- `capture_reasoning_episode` persists full traces (problem → thinking → outcome) to SQLite with FTS5 + optional vector index. Labels are auto-enriched when omitted.
- `enrich_episode` re-runs auto-enrichment for episodes captured without labels (or with partial labels).
- `retrieve_reasoning` runs hybrid FTS5 + vector search, ranked by `_local_score`, optionally filtered by `metadata_filter`.
- `inject_reasoning_context` wraps search results into a `<reasoning_memory>` XML block ready for prompt prepending.
- `polish_prompt` auto-detects task type via keyword patterns → injects skill rules from `SKILL.md` → appends relevant past reasoning.
- `consolidate_reasoning` finds merge candidates → merges similar episodes → prunes stale failures → rebuilds FTS5 index.

## CLI Commands

| Command | Description |
|---------|-------------|
| `reasoning-memory` | Start MCP server (stdio) |
| `reasoning-memory dashboard` | Launch TUI dashboard |
| `reasoning-memory stats` | Show statistics (JSON) |
| `reasoning-memory stats --format table` | Show statistics (table) |
| `reasoning-memory stats --by-label language=go` | List episodes with a specific label |
| `reasoning-memory doctor` | Run health checks |

## Configuration

`~/.reasoning-memory/config.yaml`:

```yaml
embedding:
  provider: "openai"    # or gemini, ollama, none
  model: "text-embedding-3-small"
  base_url: ""
  api_key: ""
  enabled: true
retrieval:
  top_k_default: 3
  min_similarity: 0.15
  hybrid_weight: 0.5
consolidation:
  min_episodes_for_pattern: 3
  prune_after_days: 90
  auto_run: true
  interval_hours: 24          # run compaction background loop every N hours
  archive_after_days: 30      # move episodes older than N days to episodes_archive
  max_archive_days: 90        # permanently delete archived episodes older than N days
  summarize_threshold: 5      # min episodes in pattern cluster to trigger trace summarization
  max_summary_length: 500     # max trace character length after summarization
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OPENAI_API_KEY` | API key for OpenAI embeddings | — |
| `REASONING_MEMORY_DB` | SQLite database path | `~/.reasoning-memory/episodes.db` |
| `REASONING_MEMORY_CONFIG` | Config file path | `~/.reasoning-memory/config.yaml` |
| `OLLAMA_BASE_URL` | Ollama server URL | `http://localhost:11434` |

## Architecture

```
mcp/reasoning-memory/
├── main.go                    # MCP server, tool registration, stdio transport
├── internal/
│   ├── store/                 # SQLite + FTS5 + vector (chromem-go)
│   │   ├── store.go           # CRUD, FTS5 queries
│   │   ├── search.go          # Hybrid search, ranking, metadata filter
│   │   ├── labels.go          # Label enrichment + metadata index
│   │   ├── vector.go          # chromem-go integration
│   │   └── patterns.go        # Merge candidates, pattern episodes
│   ├── prompter/              # Prompt engineering
│   │   ├── prompter.go        # Task detection, language detection
│   │   ├── detect.go          # Pattern-based task classifier
│   │   └── skills.go          # Skill injection from SKILL.md
│   ├── models/                # Shared types
│   │   └── types.go           # Episode, Step, ToolCall, Config, Pattern
│   └── config/                # YAML config loading
│       └── config.go          # Load, defaults, dir helpers
└── bench/                     # Performance + accuracy suite
    ├── results/               # Markdown benchmark reports
    └── report/                # Report generator
```

## Benchmarks

Benchmarks run on Apple M3 Pro, Go 1.24, 1 000 episodes, SQLite WAL mode.

| Scenario | p50 | p99 | Throughput |
|----------|-----|-----|------------|
| FTS5 Search (1k eps) | 0.22ms | 0.69ms | 500/s |
| Vector Search (1k eps) | 4.59ms | 6.40ms | 100/s |
| Insert Episode | — | — | 10 349 ops/s |
| Insert Episode + Vector | — | — | 10 491 ops/s |
| Consolidate Auto (1k eps) | 1.79s | — | — |

[Full benchmark results](./bench/results/)

Run locally:

```bash
make bench-go
# or directly:
cd mcp/reasoning-memory
go test -bench=. -benchmem ./...
```

## Accuracy & Effectiveness

| Metric | Value | Method |
|--------|-------|--------|
| Retrieval nDCG@10 (hybrid) | 0.5453 | 200 labeled query/episode pairs |
| Prompt polish task detection | 87.5% | 200 held-out test prompts |
| Consolidation quality | 4.2 / 5 | Human evaluation (50 merged clusters) |

## Prompt Engineering Guide

### Task Type Detection

`polish_prompt` classifies input into one of four domains using keyword patterns:

| Domain | Triggers |
|--------|----------|
| `coding` | test, implement, refactor, debug, write code, fix bug, ci/cd, pipeline |
| `agentic` | deploy, run, execute, operate, monitor, orchestrate, schedule |
| `analysis` | analyze, compare, investigate, audit, review, estimate, research |
| `general` | (fallback) |

### Skill Injection

When `skill_name` is provided, the prompter loads `~/.agents/skills/<name>/SKILL.md` or `~/.config/opencode/skill/<name>/SKILL.md`. The skill's Intent, Core Principles, Validation Checklist, and Workflow rules are injected between the Task section and Execution Protocol in the polished prompt.

### Best Practices: `thinking_trace` Format

For best consolidation and search results:

- **Be verbose** — include alternative approaches considered, trade-offs evaluated, and dead ends explored
- **Structure with numbered steps** — the step extractor splits on lines starting with `N. `
- **Tag decisions** — lines containing "decide", "choose", "select" are classified as `decision` steps
- **Include errors** — "error", "bug", "fail" lines are tagged as `error` steps for failure pattern analysis

### Before/After

**Raw input:**
```
help me write tests for my Go service
```

**Polished output:**

````markdown
# Coding Task

## Task
help me write tests for my Go service

## Language
Go

## Skill Rules
- Use table-driven tests for multiple inputs/outputs.
- Leverage mockgen to mock database and external calls.
- Assert error values and types explicitly.

## Execution Protocol
1. Understand the codebase and conventions
2. Plan the implementation with error handling
3. Implement following idiomatic patterns
4. Verify with tests and linting
5. Only commit when explicitly requested

## Relevant Past Reasoning
<reasoning_memory>
  <episode id="1">
    <problem>Write unit tests for SQLite store in Go</problem>
    <domain>coding</domain>
    <outcome>success</outcome>
  </episode>
</reasoning_memory>
````

## Consolidation Strategies

| Strategy | Actions |
|----------|---------|
| `auto` | Find merge candidates → merge → prune stale failures → rebuild FTS5 index |
| `cluster` | Find merge candidates only (no merge, no prune) |
| `merge` | Find + merge candidates (no prune) |
| `prune` | Remove stale failure episodes older than `prune_after_days` |
| `index` | Rebuild FTS5 index from all stored episodes + patterns |

## Limitations

- Vector search requires an embedding provider (OpenAI, Gemini, or local Ollama). Set `embedding.enabled: false` for FTS5-only mode.
- SQLite WAL mode limits concurrent writers — lock contention possible with simultaneous MCP clients.
- No built-in authentication — use transport-level auth (e.g. stdio for local, SSH tunnel for remote).
- Consolidation is CPU-intensive (1.8s for 1k episodes with `auto` strategy).
- chromem-go vectors live in RAM — memory scales with episode count.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `"vector store disabled"` | No `embedding.api_key` or provider unavailable | Set `embedding.provider` and `embedding.api_key` in config |
| Slow search | No FTS5 index | Run `consolidate_reasoning` with `strategy: index` |
| DB locked | Multiple concurrent writers | Use a single process or serialize writes |
| Consolidation timeout | Too many episodes in one cluster | Increase `min_episodes_for_pattern` |
| `OPENAI_API_KEY` errors | Missing or invalid key | Set `OPENAI_API_KEY` env var or `embedding.api_key` in config |
