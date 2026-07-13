# AI Guide: Reasoning Memory Network

## Architecture

```
server.py          — FastMCP entry point, tool definitions
store.py           — EpisodeStore: file-based YAML episode storage + hybrid search
prompter.py        — Skill loader, task type detector, prompt polisher
seed.py            — Batch seed episodes from installed skill files
eval.py            — LLM-as-judge eval (optional, requires API key)
benchmark.py       — Token overhead measurement across domains/skills/lengths
config.yaml        — Retrieval thresholds, consolidation settings
```

## How It Works

1. **Capture**: At task end, call `capture_reasoning_episode()` with problem, thinking trace, tool calls, outcome, tags. Stored as YAML frontmatter markdown in `~/.reasoning-memory/episodes/`.

2. **Retrieve**: `inject_reasoning_context()` at task start searches episodes by keyword + metadata similarity. Returns a `<reasoning_memory>` XML block for prompt injection.

3. **Polish**: `polish_prompt()` detects task type (coding/agentic/analysis/general), detects language, injects domain-specific architectural rules, optional skill context, and past reasoning.

## Skill Injection

Scans these locations for SKILL.md files:
- `~/.claude/skills/<name>/SKILL.md`
- `~/.agents/skills/<name>/SKILL.md`
- `~/.config/opencode/skill/<name>/SKILL.md`

Use `skill_name` parameter in `polish_prompt()` to inject a specific skill's intent, principles, validation checklist, workflow, and constraints.

## Episode Lifecycle

- Episodes are written to `~/.reasoning-memory/episodes/re-YYYYMMDD-NNN.md`
- Index maintained at `~/.reasoning-memory/index/episodes.json`
- `consolidate_reasoning(strategy="auto")` clusters by domain, merges similar episodes into patterns, prunes stale failures (>90 days), rebuilds index

## Configuration (config.yaml)

- `retrieval.top_k_default`: 3 — default number of episodes to return
- `retrieval.min_similarity`: 0.15 — minimum match score threshold
- `consolidation.prune_after_days`: 90 — delete failures older than this
- `consolidation.auto_run`: true — auto-consolidate on operations
