#!/usr/bin/env python3
"""Reasoning Memory Network — MCP Server

Captures and retrieves LLM reasoning traces for augmenting lite models.
Stores episodes as YAML files and maintains a local structured index.

Dependencies: mcp, pyyaml
"""

import json
import logging
import os
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Literal, Optional

from mcp.server.fastmcp import FastMCP

from prompter import polish_prompt as run_polish
from store import EpisodeStore

BASE_DIR = Path.home() / ".reasoning-memory"
EPISODES_DIR = BASE_DIR / "episodes"
INDEX_DIR = BASE_DIR / "index"
PATTERNS_DIR = BASE_DIR / "patterns"

for d in [BASE_DIR, EPISODES_DIR, INDEX_DIR, PATTERNS_DIR]:
    d.mkdir(parents=True, exist_ok=True)

logging.basicConfig(
    level=logging.WARNING,
    format="%(name)s: %(levelname)s %(message)s",
)
logger = logging.getLogger("reasoning-memory")

store = EpisodeStore(BASE_DIR)
mcp = FastMCP(
    "reasoning-memory",
    instructions="""Reasoning Memory Network for LLM reasoning trace capture and retrieval.

Use capture_reasoning_episode at the END of each completed task to store the
reasoning trace (thinking, tool calls, decisions, outcome). Use
inject_reasoning_context at the START of a task to retrieve relevant past
reasoning. Use retrieve_reasoning for raw search results. Use
consolidate_reasoning to batch-cluster episodes into patterns.

The MCP local index handles keyword + metadata matching. For full semantic
search across the YAML episode files, use lean-ctx ctx_semantic_search with
path="~/.reasoning-memory/episodes/".
""",
)


def _valid_err(msg: str) -> str:
    return json.dumps({"status": "error", "error": msg}, indent=2)


@mcp.tool()
def capture_reasoning_episode(
    problem: str,
    thinking_trace: str,
    tool_calls: list[dict],
    outcome: Literal["success", "partial", "failure"],
    tags: list[str],
    domain: str = "coding",
    duration_seconds: Optional[int] = None,
    model_id: Optional[str] = None,
) -> str:
    """Capture a completed reasoning episode at the END of a task.

    Stores full trace as YAML + summary in local index. Returns episode ID.

    Args:
        problem: The user's request or task description verbatim.
        thinking_trace: Full chain-of-thought reasoning text.
        tool_calls: List of dicts, each with 'tool' (name), 'args', 'result_excerpt', 'outcome' (success/failure).
        outcome: Overall task outcome: success, partial, or failure.
        tags: Domain tags e.g. ["coding", "resilience", "retry"].
        domain: Broad domain: "coding" or "agentic". Defaults to "coding".
        duration_seconds: Total task duration in seconds.
        model_id: Model identifier e.g. "claude-sonnet-4-20260514".
    """
    if not problem or not problem.strip():
        return _valid_err("problem is required")
    if not thinking_trace or not thinking_trace.strip():
        return _valid_err("thinking_trace is required")
    if not isinstance(tool_calls, list):
        return _valid_err("tool_calls must be a list")
    if outcome not in ("success", "partial", "failure"):
        return _valid_err("outcome must be 'success', 'partial', or 'failure'")

    episode_id = store.create_episode(
        problem=problem,
        thinking_trace=thinking_trace,
        tool_calls=tool_calls,
        outcome=outcome,
        tags=tags,
        domain=domain,
        duration_seconds=duration_seconds,
        model_id=model_id,
    )

    lean_ctx_cmd = (
        f'ctx_knowledge(action="remember", '
        f'category="reasoning_episode", '
        f'key="{episode_id}", '
        f'value="domain={domain}, outcome={outcome}, tags={tags}", '
        f'query="{problem[:100]}"'
        f")"
    )

    return json.dumps(
        {
            "episode_id": episode_id,
            "status": "captured",
            "episode_path": str(EPISODES_DIR / f"{episode_id}.md"),
            "_lean_ctx_commands": [
                {
                    "tool": "ctx_knowledge",
                    "args": {
                        "action": "remember",
                        "category": "reasoning_episode",
                        "key": episode_id,
                        "value": f"domain={domain}, outcome={outcome}, tags={tags}",
                    },
                },
                {
                    "tool": "ctx_knowledge",
                    "args": {
                        "action": "relate",
                        "key": episode_id,
                        "value": f"problem: {problem[:200]}",
                    },
                },
            ],
        },
        indent=2,
    )


@mcp.tool()
def retrieve_reasoning(
    problem: str,
    domain: Optional[str] = None,
    outcome: Optional[str] = None,
    tags: Optional[list[str]] = None,
    top_k: int = 5,
) -> str:
    """Search the local structured index for similar reasoning episodes.

    Returns episodes matching by keyword, domain, outcome, and tags.

    Args:
        problem: Problem description to match against.
        domain: Filter by domain: "coding" or "agentic".
        outcome: Filter by outcome: "success", "partial", or "failure".
        tags: Filter by tags (any match).
        top_k: Max results (default 5, max 20).
    """
    if not problem or not problem.strip():
        return json.dumps({"results": [], "count": 0, "source": "mcp_local"})
    top_k = min(top_k, 20)
    results = store.search_local(
        query=problem,
        domain=domain,
        outcome=outcome,
        tags=tags,
        top_k=top_k,
    )

    return json.dumps(
        {
            "results": results,
            "count": len(results),
            "source": "mcp_local",
            "hybrid_hint": (
                "For semantic search, also call ctx_semantic_search("
                f'query="{problem[:100]}", '
                'path="~/.reasoning-memory/episodes/", '
                'mode="hybrid")'
            ),
        },
        indent=2,
        default=str,
    )


@mcp.tool()
def inject_reasoning_context(
    problem: str,
    top_k: int = 3,
    include_traces: bool = True,
) -> str:
    """Retrieve relevant reasoning history and format it as context for a lite model.

    Use this at the START of a task. Returns a formatted <reasoning_memory>
    block to inject into the model's system prompt or context.

    Args:
        problem: The task/problem description to match against.
        top_k: Number of past episodes to include (default 3, max 10).
        include_traces: Include full thinking traces (True) or just summaries (False).
    """
    top_k = min(top_k, 10)
    results = store.search_local(query=problem, top_k=top_k)

    if not results:
        return "<reasoning_memory>\n  No matching reasoning history found.\n</reasoning_memory>"

    lines = ["<reasoning_memory>"]
    for i, ep in enumerate(results, 1):
        lines.append(f'  <episode id="{ep["id"]}" score="{ep.get("_local_score", 0)}">')
        lines.append(f"    <problem>{ep['problem'][:200]}</problem>")
        lines.append(f"    <domain>{ep['domain']}</domain>")
        lines.append(f"    <outcome>{ep['outcome']}</outcome>")
        lines.append(f"    <tags>{','.join(ep['tags'])}</tags>")
        lines.append(
            f"    <steps>{ep['step_count']} steps ({', '.join(set(ep['step_types']))})</steps>"
        )
        lines.append(f"    <tools>{ep['tool_count']} tool calls</tools>")

        if include_traces:
            full = store.get_episode(ep["id"])
            if full and full.get("thinking_trace"):
                trace = full["thinking_trace"][:2000]
                lines.append(f"    <thinking_trace>\n{trace}\n    </thinking_trace>")

            if full and full.get("tool_calls"):
                tc_lines = []
                for tc in full["tool_calls"][:5]:
                    excerpt = str(tc.get("args", ""))[:120]
                    tc_lines.append(
                        f"      tool={tc.get('tool')} args={excerpt} \u2192 {tc.get('outcome')}"
                    )
                if tc_lines:
                    lines.append(
                        "    <tool_calls>\n"
                        + "\n".join(tc_lines)
                        + "\n    </tool_calls>"
                    )

        lines.append(f"  </episode>")

    lines.append("</reasoning_memory>")
    lines.append("")
    lines.append(
        "<!-- Hybrid retrieval complete: MCP local (keyword+metadata) results above."
    )
    lines.append(f"     For broader semantic recall, also run:")
    lines.append(
        f'     ctx_semantic_search(query="{problem[:80]}", path="~/.reasoning-memory/episodes/", mode="hybrid")'
    )
    lines.append("     and merge with the MCP results (dedup by episode id). -->")

    return "\n".join(lines)


@mcp.tool()
def consolidate_reasoning(
    strategy: str = "auto",
) -> str:
    """Analyze all episodes to cluster patterns, prune duplicates, merge similar episodes, and rebuild the local index.

    Uses the Knowledge Graph Architect merge pattern to combine similar episodes
    into Master Memory Nodes stored in the patterns/ directory.

    Args:
        strategy: "auto" (default) — cluster + merge + prune + index.
                  "cluster" — only group similar episodes.
                  "merge" — only merge similar episodes into patterns.
                  "prune" — only remove low-value entries.
                  "index" — only rebuild the local search index.
    """
    from collections import defaultdict

    all_episodes = store.list_episodes(limit=1000)
    report = {
        "total_episodes": len(all_episodes),
        "strategy": strategy,
        "actions": [],
    }

    if strategy in ("auto", "cluster"):
        clusters = defaultdict(list)
        for ep in all_episodes:
            domain = ep.get("domain", "unknown")
            clusters[domain].append(ep["id"])

        report["actions"].append(
            f"Clustered into {len(clusters)} domain groups: {dict(clusters)}"
        )

    if strategy in ("auto", "merge"):
        candidates = store.find_merge_candidates(min_tag_overlap=1)
        merged = 0
        for a, b, score in candidates:
            pid = store.merge_to_pattern(a, b, score)
            merged += 1
        report["actions"].append(
            f"Merged {merged} episode pairs into patterns (found {len(candidates)} candidates)"
        )

    if strategy in ("auto", "prune"):
        pruned = 0
        for ep in all_episodes:
            eid = ep["id"]
            full = store.get_episode(eid)
            if not full:
                continue
            created = full.get("created_at", "")
            if created:
                try:
                    created_dt = datetime.fromisoformat(created)
                    delta = (datetime.now(timezone.utc) - created_dt).days
                    if delta > 90 and ep.get("outcome") == "failure":
                        store.delete_episode(eid)
                        pruned += 1
                except (ValueError, TypeError):
                    pass

        report["actions"].append(f"Pruned {pruned} stale failure episodes (>90 days)")

    if strategy in ("auto", "index"):
        store._load_local_index()
        store._save_local_index()
        report["actions"].append("Rebuilt local search index")

    report["episodes_remaining"] = len(store.list_episodes(limit=1000))
    report["patterns"] = store.pattern_count()

    return json.dumps(report, indent=2)


@mcp.tool()
def polish_prompt(
    raw_prompt: str,
    domain: Optional[str] = None,
    include_context: bool = True,
    top_k: int = 3,
    skill_name: Optional[str] = None,
) -> str:
    """Take an unstructured user prompt and return a polished, structured version.

    Detects task type (coding, agentic, analysis, general), applies domain-specific
    architectural rules, optionally injects relevant reasoning context from past
    episodes, and returns a ready-to-use structured prompt.

    Args:
        raw_prompt: The user's raw/unstructured input.
        domain: Optional override ("coding", "agentic", "analysis", "general").
                Auto-detected if omitted.
        include_context: If True, search RMN for relevant past episodes (default True).
        top_k: Number of context episodes to include (default 3, max 5).
        skill_name: Optional skill name to load and inject. Scans
                    ~/.claude/skills/, ~/.agents/skills/, and
                    ~/.config/opencode/skill/ for the skill file.
    """
    if not raw_prompt or not raw_prompt.strip():
        return _valid_err("raw_prompt is required")

    context = ""
    similar = []
    if include_context:
        similar = store.search_local(query=raw_prompt, top_k=min(top_k, 5))
        if similar:
            parts = ["<reasoning_memory>"]
            for ep in similar:
                full = store.get_episode(ep["id"])
                trace = (full.get("thinking_trace", "")[:800] if full else "") or ""
                parts.append(
                    f'  <episode id="{ep["id"]}" domain="{ep["domain"]}" outcome="{ep["outcome"]}">\n'
                    f"    <problem>{ep['problem'][:200]}</problem>\n"
                    f"    <trace_summary>{trace[:300]}</trace_summary>\n"
                    f"  </episode>"
                )
            parts.append("</reasoning_memory>")
            context = "\n".join(parts)

    result = run_polish(
        raw_prompt=raw_prompt,
        domain=domain,
        context=context,
        skill_name=skill_name or "",
    )

    output = {
        "task_type": result["task_type"],
        "language": result.get("language"),
        "raw_length": result["raw_length"],
        "polished_length": result["polished_length"],
        "context_episodes": len(similar) if include_context and context else 0,
        "skill_loaded": result.get("skill_loaded", False),
        "skill_name": result.get("skill_name"),
        "polished_prompt": result["polished"],
    }
    return json.dumps(output, indent=2)


if __name__ == "__main__":
    mcp.run()
