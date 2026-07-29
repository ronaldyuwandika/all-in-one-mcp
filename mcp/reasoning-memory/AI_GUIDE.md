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

## Verification Memory (Issue #103)

- Model: ordered `VerificationRecord` values with canonical types `tests`, `lint`, `builds`, `benchmarks`, `deployments`, `smoke_tests`, `review`, and `observation`; fields are `type`, `command`, `result`, `success`, and `evidence`.
- Required evidence: executable types require a command; every record requires result or evidence; `verified_success` requires at least one valid successful record. Updates cannot retain `verified_success` after removing its final successful record. Compatibility input `success` normalizes to `unverified_success`.
- Commands: store the commands actually run, especially `go test ./...`, `go vet ./...`, and `go test -race ./...`, with bounded result or evidence text.
- Retrieval: `verification_fts` indexes `type`, `command`, `result`, and `evidence` (with SQL `LIKE` fallback if tables are missing). Search filters by verification presence, successful evidence (`verification_success`), and canonical types (`verification_types`). Verification matches contribute to local score alongside metadata and lexical/vector relevance. Prompt rendering emits sanitized, bounded summaries with command presence and result/evidence excerpts.
- Migration: legacy verification JSON strings and objects become sanitized unsuccessful `observation` rows only when no structured rows exist. Legacy `verified_success` without valid successful evidence becomes `unverified_success` and gains an unsuccessful conversion observation. Rebuild `verification_fts` after backfill.
- Vector consistency: vector replacement and deletion use durable `vector_reconcile` rows; empty problem and trace form a deletion tombstone. Failed vector replacement restores the complete database episode, including verification rows, before retry reconciliation.
- Retention: compaction copies active verification rows to archive rows transactionally in position order. Active deletion cascades active rows, archived records survive normal retention, and permanent archive pruning cascades archived rows.

## Technology

- **Go 1.25+** with **github.com/mark3labs/mcp-go** (stdio transport)
- **modernc.org/sqlite** (pure Go SQLite, no CGo, FTS5 full-text search)
- **chromem-go** (embedded vector DB — Ollama, OpenAI, OpenAI-compatible)
- **gopkg.in/yaml.v3** for config parsing

## Hybrid Search

Two-layer retrieval combines structured failure memory with episode text:

1. **FTS5 / SQL**: Searches `problem` + `thinking_trace`, structured `failed_approaches`, and `verification_fts` (`type`, `command`, `result`, `evidence`). Any of these indices can admit an episode. Missing or corrupt FTS5 tables fall back to bounded SQL `LIKE` queries over the same fields.
2. **Vector**: Semantic similarity over problem, trace, serialized failed approaches, and bounded verification text containing `type`, `success`, `result`, and `evidence` (commands are omitted); candidates below `0.3` similarity are discarded.
3. **Merged**: The raw local score combines episode-text admission, verification admission, failure matches, metadata boosts, and verification boosts. Raw local scores are not exposed. When vectors are available, `_local_score` stores and displays the final combined score: `raw_local_score × 0.5 + vector_similarity × 0.5`; `_vector_score` remains the unweighted similarity. Local-only candidates expose half their raw local score as `_local_score`, and vector-only candidates contribute half their similarity. Equal scores sort by newest `created_at`, then ascending episode ID.
4. **Filters**: Domain, outcome, repository, tags, and metadata are applied to FTS and vector candidates. Repository matching is exact and case-insensitive; metadata values OR within a key and keys AND together.

Results expose `_local_score`, `_vector_score`, and `failure_matches` with the four failure fields plus match score. When vector embeddings are disabled or unavailable, retrieval remains FTS/SQL-only.

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

When embeddings are enabled and the configured vector store initializes successfully, startup opens SQLite through `NewWithVector`. That path drains the durable SQLite `vector_reconcile` queue into the configured vector store before the server becomes available. Workers conditionally claim rows using unique owner tokens and 30-second expiries, delete only rows they own, immediately release failed claims, and retry expired claims. ReconcileVectorStore returns `ErrVectorReconciliationPending` after 10 bounded batches if queue items remain. Migration upserts preserve existing content, tombstones, claims, and each existing row's `queue_generation`, raising only `migration_version`. Producer updates through `enqueueVectorReconcileTx`, its `enqueueVectorReconcileDB` wrapper, or `enqueueVectorDeleteTx` increment `store_metadata.vector_queue_generation` and stamp the resulting generation on the inserted or updated row. Readiness checks call the same reconciliation path, so an unresolved vector operation or pending queue items make readiness fail with `ErrVectorReconciliationPending` instead of silently reporting a synchronized store.

`store_metadata.vector_content_version` advances only after a transaction observes zero total queue rows across all generations; normal update rows and active claims therefore block version completion. Producer queue helpers (`enqueueVectorReconcileTx` and `enqueueVectorDeleteTx`) increment `store_metadata.vector_queue_generation` atomically and stamp that generation on new/updated queue rows. Diagnose stalls by querying `vector_reconcile` for `migration_version`, `claim_owner`, `claim_expires_at`, and `queue_generation`. An unexpired claim indicates current work; an expired claim is retryable. Verification backfill and FTS rebuild commit atomically before `verification_migration_phase` is set to `verification_backfill_complete`; schema completion uses `schema_migrations_complete`. Graph, concept, and decision families run as independently retryable phases; each completion marker is written after its callback succeeds and uses the exact markers `graph_migration_complete`, `concept_migration_complete`, and `decision_migration_complete`.

After reconciliation, startup performs a full reindex only when SQLite contains episodes and the configured vector collection is empty. A non-empty vector collection is not rebuilt automatically.

## Rich Episode Model

`models.Episode` supports lifecycle timestamps and reusable structured memory:

- Canonical outcomes: `verified_success`, `unverified_success`, `partial_success`, `failure`, and `abandoned`.
- Compatibility mappings: input and search filter `success` becomes `unverified_success`; `partial` becomes `partial_success`.
- Scope: `repo` identifies the repository used by repository filters; matching is exact and case-insensitive (`strings.EqualFold` / `LOWER(repo) = LOWER(?)`); `project` is a distinct optional project scope and is not a repo alias.
- Failure memory: `failed_approaches` accepts up to 20 objects (`approach`, `failure_mode`, `root_cause`, `lesson`), validated to non-blank strings up to 2,000 code points each, with exact duplicate objects deduplicated after trimming whitespace. Failure content is indexed in FTS5/SQLite, concatenated to vector documents, rendered as warnings in `polish_prompt`, and protected from archive hard pruning.
- Attribution: `provenance` records the episode origin; optional `confidence` must be finite and in `[0, 1]`.
- Structured fields: `objectives`, `decisions`, `alternatives`, and `lessons` are string arrays. `verification` is an ordered array of `VerificationRecord` objects and is normalized into relational active/archive tables.
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

Validation rejects blank problems, unsupported outcomes, invalid tiers, non-finite or out-of-range confidence, and malformed `failed_approaches`. The MCP create tools additionally require `thinking_trace`. Updates use replacement semantics, preserve the original creation time, update FTS5 and metadata indexes, and synchronize the vector document.

All array arguments declare explicit item schemas for Gemini-compatible clients. Rich string arrays use string items; `tool_calls` uses structured objects; `failed_approaches` uses objects with four required string properties and no partial entries. Create/capture/update/get accept or return `failed_approaches`; retrieve summaries return query-specific `failure_matches` instead of the entire failure list.

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
- Adds missing `repo`, `labels`, `tier`, `updated_at`, `project`, `provenance`, `confidence`, `objectives`, `decisions`, `alternatives`, `verification`, and `lessons` columns to `episodes` and `episodes_archive` (rich JSON array columns provide schema compatibility while verification data is migrated to structured `episode_verifications`/`episode_verifications_archive` tables).
- Backfills legacy outcomes (`success` → `unverified_success`, `partial` → `partial_success`) and sets `updated_at = created_at` when missing.
- Rebuilds the `episodes_fts` table after backfills.
- Creates `vector_reconcile` as a durable SQLite queue table (`episode_id`, `problem`, `thinking_trace`, `updated_at`) to record pending vector operations during primary SQLite writes.
- Creates `metadata_idx` for fast `metadata_filter` matching, `episode_sources` for link metadata, and decision, concept, and graph tables.

Vector operations coordinate through this queue:
- Creates and updates record pending vector content in `vector_reconcile` within the primary SQLite transaction. Their configured vector-store operation runs after commit and clears the queue entry when synchronization succeeds.
- Deletes and compaction archive moves enqueue a tombstone (`problem = '', thinking_trace = ''`) in `vector_reconcile`. If vector deletion fails or the provider is unavailable, the queue row persists to ensure durable reconciliation.
- `NewWithVector` flushes pending `vector_reconcile` entries during store initialization (deleting vectors when problem and trace are empty, replacing when non-empty). `Readiness` repeats reconciliation before reporting ready.
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
