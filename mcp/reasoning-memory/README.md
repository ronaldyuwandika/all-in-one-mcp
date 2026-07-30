# reasoning-memory

[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?style=flat-square&logo=go)](https://golang.org/doc/go1.25)
[![MCP](https://img.shields.io/badge/MCP-compatible-6B21A8?style=flat-square)](https://modelcontextprotocol.io)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](../../LICENSE)
[![Build](https://img.shields.io/github/actions/workflow/status/ronaldyuwandika/all-in-one-mcp/lint.yaml?branch=main&style=flat-square)](../../actions)
[![Coverage](https://img.shields.io/codecov/c/github/ronaldyuwandika/all-in-one-mcp?style=flat-square)](https://codecov.io/gh/ronaldyuwandika/all-in-one-mcp)

> Captures, stores, searches, and consolidates LLM reasoning traces for prompt engineering and agent memory.

## Verification Memory (Issue #103)

Verification is structured evidence, not a list of claims. `verification` uses ordered records with canonical types `tests`, `lint`, `builds`, `benchmarks`, `deployments`, `smoke_tests`, `review`, and `observation`; each record contains `type`, optional `command`, `result`, `success`, and optional `evidence`. Executable types require a command, every record requires a result or evidence, and `verified_success` requires at least one valid successful record. Removing the final successful record requires changing the outcome to `unverified_success`; legacy `success` maps to `unverified_success`.

Capture actual commands and bounded result evidence, including `go test ./...`, `go vet ./...`, and `go test -race ./...`. Search supports verification-presence, success, and type filters via `verification_types` (enum of canonical types) and `verification_success` (boolean) in `retrieve_reasoning`; verification matches and verified-success records receive fixed ranking bonuses. Prompt rendering includes bounded verification summaries with command presence and sanitized result/evidence excerpts, never unbounded command transcripts.

Migration converts legacy verification JSON strings or objects into sanitized `observation` records prefixed with `Legacy verification payload converted:` only when structured rows do not already exist. The exact durable phase markers are `verification_backfill_complete`, `schema_migrations_complete`, `graph_migration_complete`, `concept_migration_complete`, and `decision_migration_complete`. Each family is independently retryable and idempotent; its completion marker is written after callback returns, so a later-family failure resumes without rolling back a completed phase. `verification_backfill_complete` is written only after active/archive backfills, outcome correction, and the `verification_fts` rebuild complete in the same transaction. A legacy `verified_success` without valid successful evidence becomes `unverified_success` with an unsuccessful conversion observation.

On startup/migration, a durable `store_metadata` version check (`vector_content_version`) queues active episodes into `vector_reconcile` without replacing existing updates, tombstones, claims, or their per-row `queue_generation`. Producer queue helpers `enqueueVectorReconcileTx`, `enqueueVectorReconcileDB`, and `enqueueVectorDeleteTx` increment `store_metadata.vector_queue_generation` and store the new generation on each inserted or updated row in the same transaction. Migration upserts preserve an existing row's `queue_generation`; on conflict, the only updated column is `migration_version`. Applying a migration version requires the migration target generation to match the current producer generation. Workers claim rows with owner tokens and 30-second expiries, delete only rows they own, release failed claims immediately, and retry expired claims. If the queue remains non-empty after the bounded batch retry budget is exhausted, reconciliation returns `ErrVectorReconciliationPending` to surface pending state during startup or readiness. The applied vector version advances only when the entire queue is empty across migration and normal generations. For diagnostics, inspect `vector_reconcile` fields `migration_version`, `queue_generation`, `claim_owner`, and `claim_expires_at`, then compare `store_metadata.vector_content_version`; active unexpired claims indicate current work, expired claims are retryable, and any pending row prevents version completion. Active verification rows move transactionally to archive rows in position order. Verification rows follow their episode: active rows are deleted with active episodes, archived rows survive normal retention, and permanent archive pruning removes them by foreign-key cascade.

## Quick Start

From the repository root:

```bash
make run-mcp-reasoning-memory
```

Or run an installed binary:

```bash
reasoning-memory
```

## Episode records

Episodes retain the original problem and trace while adding structured fields for later retrieval and maintenance:

| Field | Meaning |
|---|---|
| `created_at`, `updated_at` | UTC timestamps. Creation initializes both; every successful update preserves `created_at` and advances `updated_at`. |
| `problem`, `thinking_trace` | Required capture text. |
| `outcome` | One of `verified_success`, `unverified_success`, `partial_success`, `failure`, or `abandoned`. |
| `objectives`, `decisions`, `alternatives`, `lessons` | Optional string arrays that preserve reusable parts of an episode without parsing the trace. |
| `verification` | Optional ordered array of structured verification records. Each record contains `type`, `success`, and optional `command`, `result`, and `evidence`, subject to the validation rules above. |
| `failed_approaches` | Optional array of failure-memory objects. Each object requires valid UTF-8 and non-blank `approach`, `failure_mode`, `root_cause`, and `lesson`; at most 20 objects are accepted and each field is limited to 2,000 Unicode code points. Exact duplicate objects are removed after whitespace is trimmed. |
| `repo` | Repository identity used by repository filters. Matching is exact and case-insensitive. When omitted during capture, it is detected from the current Git origin; detection returns the repository basename, or the working-directory basename when no origin is available. |
| `project` | Optional scope within or across repositories. It is stored independently and does not replace or populate `repo`. |
| `provenance` | Optional origin or producer, such as an agent, import, or workflow name. |
| `confidence` | Optional finite number from `0` through `1`. |
| `tier` | `episodic` by default, or `semantic`. |
| `tags`, `labels`, `domain`, `steps`, `tool_calls`, `model_id`, `duration_seconds` | Existing classification, trace, and execution metadata. |

Legacy outcome inputs remain accepted at compatibility boundaries: `success` maps to `unverified_success`, and `partial` maps to `partial_success`. New clients should send and persist only the five canonical values above.

Validation requires a non-blank `problem`, a canonical or supported legacy outcome, a confidence in the inclusive `[0, 1]` range, and a tier of `episodic` or `semantic` when supplied. `thinking_trace` remains required by the MCP create tools. Invalid records are rejected rather than partially stored.

## MCP Tools

| Tool | Description | Required parameters |
|---|---|---|
| `create_episode` | Create a rich episode | `problem`, `thinking_trace`, `outcome` |
| `capture_reasoning_episode` | Compatibility alias for `create_episode` | `problem`, `thinking_trace`, `outcome` |
| `get_episode` | Read an active episode, or an archived episode when requested | `episode_id` |
| `list_episodes` | List active episode summaries with pagination | — |
| `update_episode` | Replace an active episode with a complete validated record | `episode` |
| `delete_episode` | Delete an active episode | `episode_id` |
| `record_decision` | Store a decision with rationale, trade-offs, assumptions, evidence, and rejected alternatives | `episode_id`, `title`, `selected`, `rationale` |
| `retrieve_decisions` | Retrieve repository-scoped decisions | `query`, `repo` |
| `retrieve_reasoning` | Search episodes using hybrid FTS5 and vector retrieval (`verification_types` and `verification_success` supported) | `problem` |
| `inject_reasoning_context` | Get `<reasoning_memory>` XML context for prompt injection | `problem` |
| `enrich_episode` | Auto-enrich metadata labels for an existing episode | `episode_id` |
| `consolidate_reasoning` | Cluster, merge, prune, and reindex episodes | — |
| `polish_prompt` | Build a secret-safe agent prompt with optional memory and skill context | `raw_prompt` |
| `memorize_concept` | Store a standalone semantic concept | `entity_name`, `description` |
| `recall_semantic` | Search semantic concepts by similarity | `query` |
| `link_entities` | Create a directed relationship between concepts or episodes | `source_id`, `target_id`, `relationship` |
| `traverse_concepts` | Graph-traverse related concepts | `start_id` |

*(17 registered MCP tools total)*

### Create and capture

`create_episode` and `capture_reasoning_episode` use the same schema and handler. The capture name remains available so existing MCP clients do not need an immediate rename.

```json
{
  "problem": "Make retry behavior observable",
  "thinking_trace": "Compared counters and traces, implemented both, then ran tests.",
  "outcome": "verified_success",
  "domain": "coding",
  "tier": "episodic",
  "repo": "retry-service",
  "project": "worker-runtime",
  "provenance": "coding-agent",
  "confidence": 0.95,
  "objectives": ["Expose retry attempts"],
  "decisions": ["Use a counter and a trace attribute"],
  "alternatives": ["Log-only instrumentation"],
  "verification": [
    {
      "type": "tests",
      "command": "go test ./...",
      "result": "pass",
      "success": true
    },
    {
      "type": "tests",
      "command": "go test -race ./...",
      "result": "pass",
      "success": true
    }
  ],
  "lessons": ["Keep metric labels bounded"],
  "failed_approaches": [
    {
      "approach": "Global lock for rate counter",
      "failure_mode": "Lock contention under concurrency",
      "root_cause": "Single mutex serialized all requests",
      "lesson": "Use sharded counters or atomics"
    }
  ],
  "tags": ["go", "observability"],
  "labels": {"language": ["go"]},
  "tool_calls": [
    {
      "tool": "go_test",
      "args": {"packages": "./..."},
      "result_excerpt": "pass",
      "outcome": "success"
    }
  ],
  "duration_seconds": 120,
  "model_id": "example-model"
}
```

The response contains the generated episode ID. Labels are auto-enriched when omitted.

### Read, list, update, and delete

```json
{"episode_id": "re-20260729-001", "include_archived": true}
```

`get_episode` checks active storage first. Set `include_archived` to `true` to fall back to the archive. `list_episodes` accepts numeric `limit` and `offset` and returns active summaries; the store defaults a non-positive limit and caps it at 1,000.

`update_episode` is replacement, not patch, semantics. Send the complete episode object, including its `id`; omitted optional fields are cleared. The store rejects a missing ID or unknown active episode, preserves the original `created_at`, sets a new `updated_at`, refreshes FTS5 and metadata indexes, and reconciles the vector document.

```json
{
  "episode": {
    "id": "re-20260729-001",
    "problem": "Make retry behavior observable",
    "thinking_trace": "Updated trace",
    "outcome": "verified_success",
    "domain": "coding",
    "tier": "episodic",
    "repo": "retry-service",
    "project": "worker-runtime",
    "provenance": "reviewed-by-human",
    "confidence": 1,
    "objectives": ["Expose retry attempts"],
    "decisions": ["Use a counter and a trace attribute"],
    "alternatives": [],
    "verification": [
      {
        "type": "tests",
        "command": "go test ./...",
        "result": "pass",
        "success": true
      }
    ],
    "lessons": ["Keep metric labels bounded"],
    "tags": ["go", "observability"],
    "labels": {"language": ["go"]},
    "steps": [],
    "tool_calls": [],
    "model_id": "example-model",
    "duration_seconds": 140
  }
}
```

```json
{"episode_id": "re-20260729-001"}
```

`delete_episode` removes the active episode and associated SQLite metadata. It does not delete archived episodes.

### Upgrade behavior

Opening `~/.reasoning-memory/store.db` applies the rich-episode migration automatically. It adds missing rich fields to active and archived episodes, maps persisted `success` to `unverified_success` and `partial` to `partial_success`, backfills missing `updated_at` from `created_at`, and rebuilds FTS5 after those backfills.

When embeddings are configured and initialize successfully, startup uses `NewWithVector`. Creates and updates place pending vector content in the durable SQLite `vector_reconcile` queue as part of the primary transaction. Replacement documents include the problem, trace, serialized failed approaches, and bounded verification text containing each included record's `type`, `success`, `result`, and `evidence`; verification commands are omitted. The configured vector store synchronizes after commit and completed entries are cleared. `NewWithVector` drains remaining entries during initialization, and readiness repeats reconciliation before reporting ready. A full vector reindex runs only when SQLite has episodes and the configured vector collection is empty.

Vector reconciliation uses the same durable queue for replacement and deletion. A queue row with empty `problem` and `thinking_trace` is a deletion tombstone; failed deletes remain queued and are retried during vector-enabled startup or readiness. Archive compaction enqueues the tombstone in the same SQLite transaction as removing the active episode. Reconciliation must drain every row through the target queue generation, including migration rows, producer updates, tombstones, and active claims, before advancing `store_metadata.vector_content_version`; any remaining row prevents advancement.

The separate `migrate.py` helper imports legacy episode Markdown and pattern YAML without a record cap. Missing episode values default to current UTC `created_at`, `coding` domain, `unknown` outcome, empty collections/strings, and zero duration. It imports legacy `failed_approaches` into the normalized failure tables. It writes SQLite only; a later vector-enabled startup reindexes imported episodes only when the vector collection is empty.

### `record_decision`

Stores a decision with its selected approach, rationale, trade-offs, assumptions, evidence, and rejected alternatives linked to a parent episode.

```json
{
  "episode_id": "re-20260726-001",
  "repo": "my-service",
  "title": "Choose SQLite for Embedded Storage",
  "selected": "SQLite in WAL mode",
  "rationale": "Avoids an external process while supporting concurrent reads.",
  "tradeoffs": ["single writer restriction"],
  "assumptions": ["low concurrent write frequency"],
  "evidence": ["benchmark shows 10k ops/s"],
  "alternatives": [{"name": "Postgres", "rejection_reason": "Operational overhead", "tradeoffs": ["external service"]}]
}
```

### `retrieve_reasoning`

Hybrid search indexes episode `problem` and `thinking_trace`, structured `failed_approaches`, and verification `type`, `command`, `result`, and `evidence` through `verification_fts`. Missing or corrupt FTS5 tables fall back to bounded SQL `LIKE` searches over the same fields. Local retrieval combines episode-text admission, verification admission, failure matches, metadata boosts, and verification boosts into one raw local score. With vector results available, `_local_score` is the final combined ranking score: `raw_local_score × 0.5 + vector_similarity × 0.5`; `_vector_score` reports the unweighted similarity. A local-only candidate therefore exposes half its raw local score, while a vector-only candidate contributes half its similarity. Candidates are filtered identically, deduplicated by episode ID, and ties use newest `created_at`, then ascending ID. Results also include matching `failure_matches` objects (`approach`, `failure_mode`, `root_cause`, `lesson`, `score`) plus `updated_at`, `project`, `provenance`, and `confidence`.

```json
{
  "problem": "Go concurrency patterns",
  "domain": "coding",
  "outcome": "verified_success",
  "repo": "my-service",
  "tags": ["concurrency"],
  "verification_types": ["tests"],
  "verification_success": true,
  "top_k": 5,
  "metadata_filter": {"language": ["go"], "severity": ["high", "critical"]}
}
```

Legacy outcome filters are normalized using the same mappings as capture. `top_k` defaults to 5 and is capped at 20. `metadata_filter` ORs values within one key and ANDs separate keys. `verification_types` accepts canonical verification types and requires every requested type to be present. `verification_success: true` requires at least one successful verification record; `false` requires at least one unsuccessful record.

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

Multi-phase pipeline: find merge candidates → merge similar episodes → archive old episodic memories → prune eligible archive rows → rebuild index. `failed_approaches` move with their episode into archive; archived episodes containing any failed approach are never hard-pruned, regardless of `max_archive_days`.

```json
{
  "strategy": "auto"
}
```

### `polish_prompt`

Auto-detects task type (coding/agentic/analysis/general), programming language, injects skill rules from SKILL.md, renders failure warnings from past negative experiences, and merges relevant past reasoning context within structured character budgets.

```json
{
  "raw_prompt": "help me write tests for my Go service",
  "domain": "coding",
  "include_context": true,
  "top_k": 3,
  "skill_name": "golang-service"
}
```

#### Link ingestion

Link ingestion applies only to HTTP and HTTPS URLs found as whitespace-delimited tokens in `raw_prompt` or, when `include_thinking_trace: true`, the thinking trace. Surrounding punctuation is removed, fragments are discarded, duplicate normalized URLs are processed once, and only the first `max_links` URLs are used. URLs containing user information, such as `https://user:password@example.com/issue`, are rejected.

MCP `polish_prompt` fetches eligible links and asks the connected MCP client to return a structured summary. Each result uses one of these statuses: `summarized`, `blocked`, `fetch_failed`, `unsupported_content`, `extraction_failed`, or `summary_failed`. Source text is treated as untrusted data; instructions embedded in a page cannot override the summarization request.

```json
{
  "raw_prompt": "Implement https://issues.example.com/browse/APP-104?view=full#comments",
  "include_context": true
}
```

The fragment is ignored. The query remains part of URL identity, so a supplied summary for `?view=compact` does not satisfy `?view=full`.

The REST `POST /api/polish` endpoint never performs outbound link fetches. With the default `rest_require_pre_summarized: true`, every extracted URL must have a matching `linked_sources` entry whose `status` is `summarized` and whose `summary` is non-empty. The aggregate title, summary, instructions, acceptance criteria, and constraints must fit within `max_summary_chars`.

```json
{
  "raw_prompt": "Implement https://issues.example.com/browse/APP-104?view=full#comments",
  "linked_sources": [
    {
      "source_url": "https://issues.example.com/browse/APP-104?view=full",
      "source_type": "jira",
      "title": "APP-104: Ingest linked requirements",
      "summary": "Add bounded, SSRF-safe link ingestion to prompt polishing.",
      "instructions": ["Fetch links only through the MCP path"],
      "acceptance_criteria": ["REST accepts matching pre-summarized sources"],
      "constraints": ["Do not fetch links from REST"],
      "status": "summarized"
    }
  ]
}
```

Failure policy controls incomplete ingestion:

- `warn` continues polishing. MCP results retain their non-`summarized` status and stable warning; REST adds warnings such as `link_summary_required: https://issues.example.com/browse/APP-104?view=full`.
- `fail` rejects the operation if any requested URL is not summarized. REST returns HTTP 400 with `{"error": "polish failed: link_summary_required"}` when summaries are absent, or `{"error": "polish failed: link ingestion unavailable"}` when supplied summaries are missing, mismatched, or invalid.

Persisted and returned `source_url` values are safe presentation URLs. Harmless query values remain visible, while query keys containing `accesskey`, `apikey`, `authorization`, `credential`, `jwt`, `password`, `secret`, `signature`, or `token` after separator removal have every value replaced with `[REDACTED]`:

```text
https://example.com/task?id=104&api_token=private-value#notes
https://example.com/task?api_token=%5BREDACTED%5D&id=104
```

The second line is the safe stored and returned form; the fragment is removed. Episode capture writes the episode, metadata index, and all link-source rows in one SQLite transaction. A failure rolls back all SQLite rows. If vector indexing fails after commit, the store compensates by deleting the episode and its associated SQLite records instead of leaving a partial capture.

#### SSRF protections

Outbound MCP fetches enforce all of these checks:

- Only `http` and `https` are accepted; HTTPS redirects cannot downgrade to HTTP.
- URL user information is rejected.
- Literal and DNS-resolved loopback, private, link-local, multicast, unspecified, and IPv4 metadata-range addresses are blocked.
- Every redirect target is revalidated, with at most `max_redirects` redirects.
- The dialer connects to a validated resolved IP and verifies the connection's actual remote peer IP belongs to that resolved set. A mismatch fails with the stable `source fetch failed` warning, preventing DNS rebinding between validation and connection.
- Requests use `request_timeout_seconds`; response bodies use `max_response_bytes`; extracted text uses `max_extracted_chars`; content types must appear in `allowed_content_types`; concurrency is capped by `max_concurrency`.

For example, `http://127.0.0.1/admin`, `http://169.254.169.254/latest/meta-data/`, and a public hostname whose connected peer resolves to a private or different IP are blocked. Redirects receive the same validation as the original URL.

## Demo Episodes

Demo traces exercise five core capture, enrichment, retrieval, injection, and consolidation workflows. Full source: [`bench/results/demo-episodes.json`](./bench/results/demo-episodes.json).

### Captured via `capture_reasoning_episode`

**Episode 1 — Fix nil pointer dereference**

```json
{
  "id": "re-20260714-003",
  "problem": "Fix a nil pointer dereference in the Go HTTP handler",
  "thinking_trace": "1. I saw the panic in the logs: nil pointer dereference at handler.go:42\n2. The issue was that r.FormValue(\"id\") returns empty string...",
  "outcome": "verified_success",
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
  "outcome": "unverified_success",
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

Input: `"build a dockerfile for my go service"` + target `codex` + skill `docker-expert` → detected the task type, applied the Codex profile, injected actionable skill rules, and appended concise relevant experience.

### Consolidated via `consolidate_reasoning`

Strategy `auto` → found 1 merge candidate, merged into pattern `pat-re-20260714-002-re-20260714-001` (score 1.567), rebuilt index: 8 episodes, 1 pattern.

### Full Invocation Trace

| # | Tool | Input | Output |
|   |------|-------|--------|
| 1 | `capture_reasoning_episode` | `{"problem": "Fix a nil pointer dereference...", "outcome": "verified_success", "tags": ["go","nil-pointer","http-handler"]}` | `re-20260714-003` |
| 2 | `capture_reasoning_episode` | `{"problem": "Design a rate limiter middleware...", "outcome": "verified_success", "tags": ["go","middleware","rate-limiter","concurrency"]}` | `re-20260714-004` |
| 3 | `enrich_episode` | `{"episode_id": "re-20260714-003"}` | `Enriched re-20260714-003: {"language":["go"],"framework":["net/http"],"severity":["high"],"tag":["go","nil-pointer","http-handler"],"domain":["coding"],"outcome":["verified_success"]}` |
| 4 | `retrieve_reasoning` | `{"problem": "How to handle nil pointers in Go HTTP handlers", "top_k": 5, "metadata_filter": {"language": ["go"]}}` | Top result: `re-20260714-003` (score 1.267, labels boosted) |
| 5 | `inject_reasoning_context` | `{"problem": "Go middleware design patterns", "top_k": 3}` | `<reasoning_memory>` XML with 3 episodes |
| 6 | `polish_prompt` | `{"raw_prompt": "build a dockerfile for my go service", "skill_name": "docker-expert"}` | `coding` task type, skill injected, 1 context episode appended |
| 7 | `consolidate_reasoning` | `{"strategy": "auto"}` | Merged 1 pair → `pat-re-20260714-002-re-20260714-001` (score 1.567), index rebuilt: 8 eps, 1 pattern |

**Process flow:**
- `capture_reasoning_episode` persists full traces (problem → thinking → outcome) to SQLite with FTS5 + optional vector index. Labels are auto-enriched when omitted.
- `enrich_episode` re-runs auto-enrichment for episodes captured without labels (or with partial labels).
- `retrieve_reasoning` runs hybrid FTS5 + vector search, ranked by `_local_score`, optionally filtered by `metadata_filter`.
- `inject_reasoning_context` wraps search results into a `<reasoning_memory>` XML block ready for prompt prepending.
- `polish_prompt` redacts input → classifies the task → retrieves concise relevant experience → extracts skill rules → renders a `codex`, `claude`, or `generic` profile → applies final redaction and the prompt budget.
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
  provider: "openai"    # openai | openai-compat | ollama | mock
  model: "text-embedding-3-small"
  base_url: ""
  api_key: ""
  enabled: true
retrieval:
  top_k_default: 3
  min_similarity: 0.15
  hybrid_weight: 0.5
security:
  redact_secrets: true
  redact_before_embedding: true
  redact_on_retrieval: true
  redact_polished_prompts: true
  replacement: "[REDACTED]"
  audit_detection: true
prompt_polishing:
  enabled: true
  default_target_agent: generic
  default_output_format: markdown
  include_memory_by_default: true
  max_memories: 3
  max_prompt_chars: 20000
  include_failure_lessons: true
  include_full_traces: false
  deduplicate_context: true
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

### Secret redaction

Secret redaction is enabled by default and runs before normalization, SQLite/FTS5 storage, vector indexing, and external embedding requests. Retrieval also redacts legacy rows, and prompt rendering applies a final pass across raw requests, memory summaries, and skill guidance. The original secret is not retained by reasoning-memory.

Detection is deterministic and pattern-based. It covers common provider tokens, JWTs, CLI credential arguments, environment and structured YAML/Terraform assignments, authorization headers, private keys, credential-bearing connection strings, and conservative high-entropy candidates. Findings expose only type, byte range, confidence, and a truncated SHA-256 fingerprint—not the detected value. Git hashes, UUIDs, ordinary checksums, and low-diversity identifiers are excluded, but no pattern-based detector can identify every custom credential format. Avoid intentionally including secrets in reasoning traces or prompts.

The legacy `migrate.py` utility also passes imported episode and pattern JSON through the shared Go detector before opening SQLite. Running that migration therefore requires the Go toolchain in addition to Python.

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OPENAI_API_KEY` | API key for OpenAI embeddings | — |
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
│   │   ├── prompter.go        # Agent profiles, formats, and budgeting
│   │   ├── detect.go          # Pattern-based task classifier
│   │   └── skills.go          # Skill injection from SKILL.md
│   ├── models/                # Shared types
│   │   └── types.go           # Episode, Step, ToolCall, Config, Pattern
│   ├── security/              # Shared-detector model sanitization
│   └── config/                # YAML config loading
│       └── config.go          # Load, defaults, dir helpers
└── bench/                     # Performance + accuracy suite
    ├── results/               # Markdown benchmark reports
    └── report/                # Report generator
```

## Testing

Run quick unit tests locally (runs in short mode to skip heavy data generation benchmarks):

```bash
make test-reasoning-memory
# Or directly:
go test -v -count=1 -short ./...
```

To run full benchmarks and accuracy tests (including heavy database generation):

```bash
make bench-reasoning-memory
```

## Benchmarks

Benchmarks run on Apple M3 Pro, Go 1.25, 1 000 episodes, SQLite WAL mode.

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
make bench-reasoning-memory
```

## Accuracy & Effectiveness

| Metric | Value | Method |
| Retrieval nDCG@10 (hybrid) | 0.5453 | 200 labeled query/episode pairs |
| Prompt polish task detection | 87.5% | 200 held-out test prompts |
| Consolidation quality | 4.2 / 5 | Human evaluation (50 merged clusters) |

## Prompt Engineering Guide

### Task Type Detection

`polish_prompt` deterministically classifies common coding-agent work using keyword patterns:

| Domain | Triggers |
|--------|----------|
| `coding`, `bug_fix`, `refactor`, `testing`, `debugging` | implementation and code-change wording |
| `code_review`, `analysis` | review, analysis, comparison, investigation |
| `infrastructure`, `database`, `documentation` | domain-specific wording |
| `agentic` | orchestration, workflow, automation, monitoring |
| `general` | (fallback) |

### Skill Injection

When `skill_name` is provided, the prompter searches the supported local Claude, Agents, and OpenCode skill directories. It extracts actionable intent, principles, validation, workflow, and constraint rules; deduplicates prompt guidance; and redacts it before rendering. Skill content cannot override built-in security constraints.

The `target_agent` field supports `codex`, `claude`, and `generic`. `output_format` supports `markdown`, `json`, and `xml`; all profiles contain equivalent objective, requirement, constraint, acceptance, validation, and deliverable fields. `max_prompt_chars` bounds output, with prior-memory and skill context removed before mandatory instructions.

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
    <outcome>unverified_success</outcome>
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
