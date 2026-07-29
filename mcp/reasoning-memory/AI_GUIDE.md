# AI Guide: Reasoning Memory Network

## Architecture

```
main.go                    — MCP server entry point (mark3labs/mcp-go, stdio transport)
internal/
  models/types.go          — Shared data types (Episode, ToolCall, Pattern, Config, etc.)
  config/config.go         — YAML config loader with defaults
  store/
    store.go               — SQLite EpisodeStore (CRUD, FTS5 triggers)
    decisions.go           — SQLite DecisionStore (Create, Get, Search with repository isolation)
    search.go              — Hybrid search (FTS5 keyword + chromem-go semantic)
    patterns.go            — Pattern consolidation, merge, prune
    vector.go              — chromem-go vector DB wrapper (Ollama, OpenAI, compat)
  prompter/
    detect.go              — Task type + language detection
    skills.go              — SKILL.md loader from 3 scan paths
    prompter.go            — Domain-specific prompt builders + XML context
migrate.py                 — One-shot migration: YAML frontmatter → SQLite
config.yaml                — Retrieval, embedding, consolidation settings
.golangci.yml              — Linter configuration
```

## Technology

- **Go 1.25+** with **github.com/mark3labs/mcp-go** (stdio transport)
- **modernc.org/sqlite** (pure Go SQLite, no CGo, FTS5 full-text search)
- **chromem-go** (embedded vector DB — Ollama, OpenAI, OpenAI-compatible)
- **gopkg.in/yaml.v3** for config parsing

## Hybrid Search

Two-layer retrieval with configurable weighting (`retrieval.hybrid_weight`):

1. **FTS5**: Full-text search on problem + thinking_trace, scored by term frequency + metadata match
2. **Vector**: Semantic similarity via chromem-go embeddings (cosine similarity)
3. **Merged**: Hybrid score = vector_score × weight + fts_score × (1-weight)

When vector embeddings are disabled, falls back to FTS5-only search.

## Vector Embeddings

Configure via `config.yaml`:

```yaml
embedding:
  provider: ollama          # ollama | openai | openai-compat
  model: nomic-embed-text   # embedding model name
  base_url: http://localhost:11434
  enabled: true             # false to disable
```

**Supported providers:**
- **ollama** — local, requires Ollama running (`ollama pull nomic-embed-text`)
- **openai** — cloud, set `OPENAI_API_KEY` env var
- **openai-compat** — any OpenAI-compatible API (LocalAI, llama.cpp, etc.)
- **mock** — deterministic in-memory vector testing provider

Vector data stored in `~/.reasoning-memory/vector/` (chromem-go persistent DB).

## Startup Reconciliation and Conditional Reindex

When embeddings are enabled and the configured vector store initializes successfully, startup opens SQLite through `NewWithVector`. That path drains the durable SQLite `vector_reconcile` queue into the configured vector store before the server becomes available. Readiness checks call the same reconciliation path, so an unresolved vector operation makes readiness fail instead of silently reporting a synchronized store.

After reconciliation, startup performs a full reindex only when SQLite contains episodes and the configured vector collection is empty. A non-empty vector collection is not rebuilt automatically.

## Rich Episode Model

`models.Episode` supports lifecycle timestamps and reusable structured memory:

- Canonical outcomes: `verified_success`, `unverified_success`, `partial_success`, `failure`, and `abandoned`.
- Compatibility mappings: input and search filter `success` becomes `unverified_success`; `partial` becomes `partial_success`.
- Scope: `repo` identifies the repository used by repository filters; `project` is a distinct optional project scope and is not a repo alias.
- Attribution: `provenance` records the episode origin; optional `confidence` must be finite and in `[0, 1]`.
- Structured fields: `objectives`, `decisions`, `alternatives`, `verification`, and `lessons` are string arrays.
- Lifecycle: creation initializes `created_at` and `updated_at`; replacement updates preserve `created_at` and advance `updated_at`; archive records retain all rich fields.

Capture auto-detects `repo` only when it is omitted. Detection uses the current Git origin's repository basename, falling back to the current directory basename. `project` is never auto-populated from `repo`.

## Episode MCP API

| Tool | Contract |
|---|---|
| `create_episode` | Creates a validated rich episode. Requires `problem`, `thinking_trace`, and `outcome`. |
| `capture_reasoning_episode` | Compatibility alias with the same schema and handler as `create_episode`. |
| `get_episode` | Reads by `episode_id`; `include_archived: true` enables archive fallback. |
| `list_episodes` | Lists active summaries using `limit` and `offset`; non-positive limits use the store default and limits above 1,000 are capped. |
| `update_episode` | Replaces an active record from the required complete `episode` object. The object must contain an existing `id`. |
| `delete_episode` | Deletes an active record by `episode_id`; archived records are not deleted. |

Validation rejects blank problems, unsupported outcomes, invalid tiers, and non-finite or out-of-range confidence. The MCP create tools additionally require `thinking_trace`. Updates use replacement semantics, preserve the original creation time, update FTS5 and metadata indexes, and synchronize the vector document.

All array arguments declare explicit item schemas for Gemini-compatible clients. Rich string arrays use string items, while `tool_calls` uses structured objects.

## How It Works

1. **Capture**: At task end, `capture_reasoning_episode()` writes episode to SQLite with FTS5 indexing + optional vector embedding.

2. **Retrieve**: `inject_reasoning_context()` at task start uses hybrid search (FTS5 + vector). Returns `<reasoning_memory>` XML for prompt injection.

3. **Polish**: `polish_prompt()` detects task type (coding/agentic/analysis), detects language, injects skill context from SKILL.md files, and optionally embeds past reasoning.

4. **Consolidate**: `consolidate_reasoning()` clusters episodes by domain, merges similar pairs into patterns, prunes stale failures, and rebuilds the search index.

## Skill Injection

Scans these locations for SKILL.md files:
- `~/.claude/skills/<name>/SKILL.md`
- `~/.agents/skills/<name>/SKILL.md`
- `~/.config/opencode/skill/<name>/SKILL.md`

## Migrations and Vector Reconciliation

Opening SQLite applies idempotent migrations automatically:
- Adds missing `repo`, `labels`, `tier`, `updated_at`, `project`, `provenance`, `confidence`, `objectives`, `decisions`, `alternatives`, `verification`, and `lessons` columns to `episodes` and `episodes_archive`.
- Backfills legacy outcomes (`success` → `unverified_success`, `partial` → `partial_success`) and sets `updated_at = created_at` when missing.
- Rebuilds the `episodes_fts` table after backfills.
- Creates `vector_reconcile` as a durable SQLite queue table (`episode_id`, `problem`, `thinking_trace`, `updated_at`) to record pending vector operations during primary SQLite writes.
- Creates `metadata_idx` for fast `metadata_filter` matching, `episode_sources` for link metadata, and decision, concept, and graph tables.

Vector operations coordinate through this queue:
- Creates and updates record pending vector content in `vector_reconcile` within the primary SQLite transaction. Their configured vector-store operation runs after commit and clears the queue entry when synchronization succeeds.
- Deletes remove SQLite episode data and any stale reconciliation entry transactionally, then delete the configured vector document when vector storage is enabled.
- `NewWithVector` flushes pending `vector_reconcile` entries during store initialization. `Readiness` repeats reconciliation before reporting ready.
- When embeddings are disabled or vector initialization fails, startup uses the SQLite-only store. Queued reconciliation resumes on a later successful `NewWithVector` startup.

## Legacy Frontmatter Migration (`migrate.py`)

`migrate.py` is a one-shot helper for importing legacy YAML frontmatter Markdown files from `~/.reasoning-memory/episodes/*.md` and pattern files from `~/.reasoning-memory/patterns/*.yaml` into SQLite (`~/.reasoning-memory/store.db`).

- Defaults: `created_at` uses current UTC time when omitted; `domain` defaults to `'coding'`; missing outcomes are stored as `'unknown'`; `duration_seconds` defaults to `0`.
- Limits: The script scans local directory structures without built-in record caps.
- Redaction: Pass input payloads through `pkg/secretdetect` (`go run ./cmd/secretdetect`) before inserting into SQLite.
- Post-migration vector sync: Running `migrate.py` writes directly to SQLite and does not write vector embeddings directly. Opening the Go server with vector embeddings enabled will reindex all episodes into the vector DB if the vector collection is currently empty.

## Storage

- **SQLite**: `~/.reasoning-memory/store.db` — episodes, archive, decisions, concepts, graph, FTS5 index, metadata index, and vector reconciliation queue
- **Vector DB**: `~/.reasoning-memory/vector/` — chromem-go persistent collection
