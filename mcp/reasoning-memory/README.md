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
  "model_id": "claude-sonnet-4-20260514"
}
```

### `retrieve_reasoning`

Hybrid FTS5 + vector search returning ranked, deduplicated episode summaries.

```json
{
  "problem": "Go concurrency patterns",
  "domain": "coding",
  "outcome": "success",
  "tags": ["concurrency"],
  "top_k": 5
}
```

### `inject_reasoning_context`

Returns a `<reasoning_memory>` XML block ready for prompt prepending.

```json
{
  "problem": "Refactor a large Go service",
  "top_k": 3,
  "include_traces": true
}
```

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
│   │   ├── search.go          # Hybrid search, ranking
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
