import os
import re
import json
import hashlib
import logging
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

import yaml

logger = logging.getLogger("reasoning-memory")

EPISODE_ID_PATTERN = re.compile(r"^re-\d{8}-\d{3}$")


def new_episode_id(episodes_dir: Path) -> str:
    today = datetime.now(timezone.utc).strftime("%Y%m%d")
    prefix = f"re-{today}-"
    max_seq = 0
    if episodes_dir.exists():
        for f in episodes_dir.iterdir():
            if f.name.startswith(prefix) and f.suffix == ".md":
                try:
                    seq = int(f.stem.split("-")[-1])
                    max_seq = max(max_seq, seq)
                except (ValueError, IndexError):
                    pass
    return f"{prefix}{max_seq + 1:03d}"


def _parse_frontmatter(text: str) -> tuple[dict, str]:
    """Parse YAML frontmatter from markdown text. Returns (metadata, body)."""
    if text.startswith("---"):
        parts = text.split("---", 2)
        if len(parts) >= 3:
            meta = yaml.safe_load(parts[1]) or {}
            body = parts[2].strip()
            return meta, body
    return {}, text.strip()


def _build_episode_md(episode: dict, steps_body: str, tools_body: str) -> str:
    """Build markdown with YAML frontmatter for lean-ctx indexing."""
    meta = {
        k: episode[k]
        for k in (
            "id",
            "created_at",
            "domain",
            "outcome",
            "tags",
            "steps",
            "tool_calls",
        )
        if k in episode
    }
    meta["model_id"] = episode.get("model_id", "")
    meta["duration_seconds"] = episode.get("duration_seconds", 0)
    meta["problem"] = episode["problem"][:200]
    meta["thinking_trace"] = episode["thinking_trace"][:2000]
    frontmatter = yaml.dump(
        meta, default_flow_style=False, sort_keys=False, allow_unicode=True
    )

    return (
        f"---\n{frontmatter}---\n\n"
        f"## Problem\n\n{episode['problem']}\n\n"
        f"## Thinking Trace\n\n{episode['thinking_trace']}\n\n"
        f"## Steps\n\n{steps_body}\n\n"
        f"## Tool Calls\n\n{tools_body}\n"
    )


class EpisodeStore:
    def __init__(self, base_dir: Path):
        self.base_dir = base_dir
        self.episodes_dir = base_dir / "episodes"
        self.index_dir = base_dir / "index"
        self.patterns_dir = base_dir / "patterns"
        self.local_index_path = self.index_dir / "episodes.json"

        self.episodes_dir.mkdir(parents=True, exist_ok=True)
        self.index_dir.mkdir(parents=True, exist_ok=True)
        self.patterns_dir.mkdir(parents=True, exist_ok=True)

        self._local_index: dict[str, dict] = {}
        self._load_local_index()

    def _load_local_index(self):
        if self.local_index_path.exists():
            try:
                with open(self.local_index_path) as f:
                    self._local_index = json.load(f)
            except (json.JSONDecodeError, IOError):
                self._local_index = {}

    def _save_local_index(self):
        with open(self.local_index_path, "w") as f:
            json.dump(self._local_index, f, indent=2)

    def create_episode(
        self,
        problem: str,
        thinking_trace: str,
        tool_calls: list[dict],
        outcome: str,
        tags: list[str],
        domain: str = "coding",
        duration_seconds: Optional[int] = None,
        model_id: Optional[str] = None,
        steps: Optional[list[dict]] = None,
    ) -> str:
        episode_id = new_episode_id(self.episodes_dir)
        now = datetime.now(timezone.utc).isoformat()

        if steps is None:
            steps = self._extract_steps(thinking_trace)

        episode = {
            "id": episode_id,
            "created_at": now,
            "model_id": model_id or "",
            "duration_seconds": duration_seconds or 0,
            "domain": domain,
            "problem": problem,
            "thinking_trace": thinking_trace,
            "steps": steps,
            "tool_calls": tool_calls,
            "outcome": outcome,
            "tags": tags,
        }

        steps_body = "\n".join(
            f"{i + 1}. **[{s.get('type', 'step')}]** {s.get('content', '')[:300]}"
            for i, s in enumerate(steps)
        )
        tools_body = "\n".join(
            f"- `{tc.get('tool')}` args={json.dumps(tc.get('args', {}))[:200]} \u2192 {tc.get('outcome')}"
            for tc in tool_calls
        )

        md_content = _build_episode_md(episode, steps_body, tools_body)

        episode_path = self.episodes_dir / f"{episode_id}.md"
        with open(episode_path, "w") as f:
            f.write(md_content)

        summary = {
            "id": episode_id,
            "created_at": now,
            "problem": problem[:200],
            "domain": domain,
            "outcome": outcome,
            "tags": tags,
            "step_count": len(steps),
            "tool_count": len(tool_calls),
            "step_types": [s.get("type", "unknown") for s in steps],
            "model_id": model_id or "",
            "duration_seconds": duration_seconds or 0,
        }

        self._local_index[episode_id] = summary
        self._save_local_index()

        logger.info("Created episode %s", episode_id)
        return episode_id

    def get_episode(self, episode_id: str) -> Optional[dict]:
        path = self.episodes_dir / f"{episode_id}.md"
        if path.exists():
            with open(path) as f:
                content = f.read()
            meta, body = _parse_frontmatter(content)
            return meta
        return None

    def get_summary(self, episode_id: str) -> Optional[dict]:
        return self._local_index.get(episode_id)

    def list_episodes(self, limit: int = 50, offset: int = 0) -> list[dict]:
        sorted_ids = sorted(self._local_index.keys(), reverse=True)
        batch = sorted_ids[offset : offset + limit]
        return [self._local_index[eid] for eid in batch]

    def search_local(
        self,
        query: str,
        domain: Optional[str] = None,
        outcome: Optional[str] = None,
        tags: Optional[list[str]] = None,
        top_k: int = 5,
    ) -> list[dict]:
        query_lower = query.lower()
        query_terms = set(query_lower.split())

        meta_scored = []
        for eid, summary in self._local_index.items():
            score = 0.0

            problem_text = summary.get("problem", "").lower()
            tag_list = summary.get("tags", [])

            term_matches = sum(1 for t in query_terms if t in problem_text)
            if term_matches > 0:
                score += term_matches / len(query_terms) * 0.6

            exact_phrase = query_lower in problem_text
            if exact_phrase:
                score += 0.3

            tag_matches = sum(1 for t in (tags or []) if t in tag_list)
            if tag_matches > 0:
                score += tag_matches / max(len(tags or []), 1) * 0.3

            if domain and summary.get("domain") == domain:
                score += 0.2
            if outcome and summary.get("outcome") == outcome:
                score += 0.15

            if domain and summary.get("domain") != domain:
                continue
            if outcome and summary.get("outcome") != outcome:
                continue

            meta_scored.append((eid, score))

        content_scored = {}
        for eid, _ in meta_scored:
            full = self.get_episode(eid)
            if not full:
                continue
            trace = full.get("thinking_trace", "")
            content = (trace + " " + full.get("problem", "")).lower()

            ct_matches = sum(1 for t in query_terms if t in content)
            ct_exact = query_lower in content
            ct_score = 0.0
            if ct_matches > 0:
                ct_score = ct_matches / len(query_terms) * 0.5
            if ct_exact:
                ct_score += 0.3
            if ct_score > 0:
                content_scored[eid] = ct_score

        merged: dict[str, float] = {}
        for eid, mscore in meta_scored:
            cscore = content_scored.get(eid, 0.0)
            total = mscore + cscore
            if total > 0:
                merged[eid] = total

        sorted_eids = sorted(merged.keys(), key=lambda eid: -merged[eid])

        results = []
        for eid in sorted_eids[:top_k]:
            summary = dict(self._local_index[eid])
            summary["_local_score"] = round(merged[eid], 3)
            results.append(summary)

        return results

    def find_merge_candidates(
        self, min_tag_overlap: int = 1
    ) -> list[tuple[dict, dict, float]]:
        episodes = list(self._local_index.values())
        candidates = []

        for i in range(len(episodes)):
            for j in range(i + 1, len(episodes)):
                a, b = episodes[i], episodes[j]
                if a.get("domain") != b.get("domain"):
                    continue

                tags_a = set(a.get("tags", []))
                tags_b = set(b.get("tags", []))
                overlap = len(tags_a & tags_b)

                if overlap >= min_tag_overlap:
                    a_text = a.get("problem", "").lower()
                    b_text = b.get("problem", "").lower()
                    a_terms = set(a_text.split())
                    b_terms = set(b_text.split())
                    text_overlap = len(a_terms & b_terms) / max(
                        len(a_terms | b_terms), 1
                    )
                    score = (overlap * 0.3) + (text_overlap * 0.4)
                    candidates.append((a, b, round(score, 3)))

        candidates.sort(key=lambda x: -x[2])
        return candidates

    def merge_to_pattern(self, ep_a: dict, ep_b: dict, merge_score: float) -> str:
        a_full = self.get_episode(ep_a["id"])
        b_full = self.get_episode(ep_b["id"])
        a_trace = a_full.get("thinking_trace", "") if a_full else ""
        b_trace = b_full.get("thinking_trace", "") if b_full else ""

        combined_prompt = f"{ep_a['problem']}\n\n(Combined with: {ep_b['problem']})"

        merged_trace_lines = []
        a_lines = a_trace.strip().split("\n")
        b_lines = b_trace.strip().split("\n")

        a_phases = set()
        for line in a_lines:
            stripped = line.strip()
            key = stripped[:60].lower()
            if key not in a_phases:
                a_phases.add(key)
                merged_trace_lines.append(stripped)

        for line in b_lines:
            stripped = line.strip()
            key = stripped[:60].lower()
            if key not in a_phases:
                a_phases.add(key)
                merged_trace_lines.append(stripped)

        a_tools = set()
        merged_tools = []
        for tc in a_full.get("tool_calls", []):
            key = (
                tc.get("tool", "")
                + json.dumps(tc.get("args", {}), sort_keys=True)[:100]
            )
            if key not in a_tools:
                a_tools.add(key)
                merged_tools.append(tc)
        for tc in b_full.get("tool_calls", []):
            key = (
                tc.get("tool", "")
                + json.dumps(tc.get("args", {}), sort_keys=True)[:100]
            )
            if key not in a_tools:
                a_tools.add(key)
                merged_tools.append(tc)

        all_tags = list(set(ep_a.get("tags", []) + ep_b.get("tags", [])))
        domain = ep_a.get("domain", "unknown")

        pattern_id = f"pat-{ep_a['id']}-{ep_b['id']}"
        pattern_path = self.patterns_dir / f"{pattern_id}.yaml"

        pattern = {
            "id": pattern_id,
            "created_at": datetime.now(timezone.utc).isoformat(),
            "domain": domain,
            "merge_score": merge_score,
            "sources": [ep_a["id"], ep_b["id"]],
            "consolidated_prompt": combined_prompt,
            "master_thinking_path": "\n".join(merged_trace_lines),
            "master_tool_calls": merged_tools,
            "tags": all_tags,
        }

        with open(pattern_path, "w") as f:
            yaml.dump(
                pattern,
                f,
                default_flow_style=False,
                sort_keys=False,
                allow_unicode=True,
            )

        return pattern_id

    def _extract_steps(self, thinking_trace: str) -> list[dict]:
        lines = thinking_trace.strip().split("\n")
        steps = []
        current_step = None
        step_types = [
            "analysis",
            "option_generation",
            "decision",
            "implementation",
            "verification",
            "error",
        ]

        for line in lines:
            line_stripped = line.strip()
            if not line_stripped:
                continue

            step_type = "analysis"
            lower = line_stripped.lower()
            if any(
                w in lower for w in ["decide", "choose", "pick", "select", "opt for"]
            ):
                step_type = "decision"
            elif any(
                w in lower for w in ["option", "alternative", "consider", "approach"]
            ):
                step_type = "option_generation"
            elif any(
                w in lower for w in ["implement", "write", "code", "edit", "create"]
            ):
                step_type = "implementation"
            elif any(w in lower for w in ["verify", "test", "check", "validate"]):
                step_type = "verification"
            elif any(w in lower for w in ["error", "bug", "issue", "problem", "fail"]):
                step_type = "error"

            if line_stripped[0].isdigit() and "." in line_stripped[:4]:
                if current_step:
                    steps.append(current_step)
                current_step = {
                    "id": f"s{len(steps) + 1}",
                    "type": step_type,
                    "content": line_stripped,
                }
            elif current_step:
                current_step["content"] += "\n" + line_stripped
            else:
                current_step = {
                    "id": f"s{len(steps) + 1}",
                    "type": step_type,
                    "content": line_stripped,
                }

        if current_step:
            steps.append(current_step)

        if not steps:
            steps.append(
                {"id": "s1", "type": "analysis", "content": thinking_trace[:500]}
            )

        return steps

    def pattern_count(self) -> int:
        return len(list(self.patterns_dir.glob("*.yaml")))

    def get_pattern(self, pattern_id: str) -> Optional[dict]:
        path = self.patterns_dir / f"{pattern_id}.yaml"
        if path.exists():
            with open(path) as f:
                return yaml.safe_load(f)
        return None

    def list_patterns(self) -> list[dict]:
        patterns = []
        for path in sorted(self.patterns_dir.glob("*.yaml")):
            with open(path) as f:
                patterns.append(yaml.safe_load(f))
        return patterns

    def delete_episode(self, episode_id: str) -> bool:
        path = self.episodes_dir / f"{episode_id}.md"
        if path.exists():
            path.unlink()
            self._local_index.pop(episode_id, None)
            self._save_local_index()
            return True
        return False
