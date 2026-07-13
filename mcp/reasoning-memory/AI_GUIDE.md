# AI Guide: Reasoning Memory Network

## Architecture

```
main.go                    — MCP server entry point (mark3labs/mcp-go, stdio transport)
internal/
  models/types.go          — Shared data types (Episode, ToolCall, Pattern, Config, etc.)
  config/config.go         — YAML config loader with defaults
  store/
    store.go               — SQLite EpisodeStore (CRUD, FTS5 triggers)
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

- **Go 1.22+** with **github.com/mark3labs/mcp-go** (stdio transport)
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

Vector data stored in `~/.reasoning-memory/vector/` (chromem-go persistent DB).

## Auto-Reindex

On startup, if vector DB is enabled and has zero documents but SQLite has episodes, the server automatically reindexes all episodes to build the vector index. Progress is logged.

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

## Data Migration

```bash
python3 migrate.py          # Migrate existing ~/.reasoning-memory/episodes/*.md → SQLite
python3 migrate.py --dry-run  # Preview only
```

After migration, run the Go server once with `embedding.enabled: true` to auto-reindex all episodes into the vector DB.

## Storage

- **SQLite**: `~/.reasoning-memory/store.db` — episodes, patterns, FTS5 index
- **Vector DB**: `~/.reasoning-memory/vector/` — chromem-go persistent collection
