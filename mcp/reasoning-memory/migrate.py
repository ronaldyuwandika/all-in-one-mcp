#!/usr/bin/env python3
"""Migrate existing reasoning-memory episodes from YAML frontmatter markdown files to SQLite.

Reads ~/.reasoning-memory/episodes/*.md and ~/.reasoning-memory/index/episodes.json,
writes into ~/.reasoning-memory/store.db (SQLite with FTS5).

Usage:
    python3 migrate.py [--dry-run]
"""

import json
import os
import sqlite3
import sys
import re
from datetime import datetime, timezone
from pathlib import Path

import yaml

BASE_DIR = Path.home() / ".reasoning-memory"
DB_PATH = BASE_DIR / "store.db"
EPISODES_DIR = BASE_DIR / "episodes"
INDEX_PATH = BASE_DIR / "index" / "episodes.json"
PATTERNS_DIR = BASE_DIR / "patterns"


def parse_frontmatter(text: str) -> tuple[dict, str]:
    if text.startswith("---"):
        parts = text.split("---", 2)
        if len(parts) >= 3:
            return yaml.safe_load(parts[1]) or {}, parts[2].strip()
    return {}, text.strip()


def safe_json(data) -> str:
    return json.dumps(data or [], ensure_ascii=False)


def migrate(dry_run: bool = False):
    if dry_run:
        print("[DRY RUN]")

    episodes = []
    if EPISODES_DIR.exists():
        for md_file in sorted(EPISODES_DIR.glob("*.md")):
            content = md_file.read_text()
            meta, _ = parse_frontmatter(content)
            if meta.get("id"):
                episodes.append(meta)
    print(f"Found {len(episodes)} episodes")

    if INDEX_PATH.exists():
        with open(INDEX_PATH) as f:
            old_index = json.load(f)
        print(f"Loaded index with {len(old_index)} entries")

    patterns = []
    if PATTERNS_DIR.exists():
        for yaml_file in sorted(PATTERNS_DIR.glob("*.yaml")):
            pat = yaml.safe_load(yaml_file.read_text())
            if pat and pat.get("id"):
                patterns.append(pat)
    print(f"Found {len(patterns)} patterns")

    if dry_run:
        return

    db = sqlite3.connect(str(DB_PATH))

    db.executescript("""
        CREATE TABLE IF NOT EXISTS episodes (
            id TEXT PRIMARY KEY,
            created_at TEXT NOT NULL,
            domain TEXT NOT NULL DEFAULT 'coding',
            outcome TEXT NOT NULL,
            tags TEXT NOT NULL DEFAULT '[]',
            problem TEXT NOT NULL,
            thinking_trace TEXT NOT NULL,
            steps TEXT NOT NULL DEFAULT '[]',
            tool_calls TEXT NOT NULL DEFAULT '[]',
            model_id TEXT NOT NULL DEFAULT '',
            duration_seconds INTEGER NOT NULL DEFAULT 0
        );
        CREATE VIRTUAL TABLE IF NOT EXISTS episodes_fts USING fts5(
            id UNINDEXED,
            problem,
            thinking_trace,
            domain,
            outcome,
            tags,
            content='episodes',
            content_rowid='rowid'
        );
        CREATE TABLE IF NOT EXISTS patterns (
            id TEXT PRIMARY KEY,
            created_at TEXT NOT NULL,
            domain TEXT NOT NULL,
            merge_score REAL NOT NULL DEFAULT 0,
            sources TEXT NOT NULL DEFAULT '[]',
            consolidated_prompt TEXT NOT NULL,
            master_thinking_path TEXT NOT NULL,
            master_tool_calls TEXT NOT NULL DEFAULT '[]',
            tags TEXT NOT NULL DEFAULT '[]'
        );
        CREATE TRIGGER IF NOT EXISTS episodes_ai AFTER INSERT ON episodes BEGIN
            INSERT INTO episodes_fts(rowid, problem, thinking_trace, domain, outcome, tags)
            VALUES (new.rowid, new.problem, new.thinking_trace, new.domain, new.outcome, new.tags);
        END;
        CREATE TRIGGER IF NOT EXISTS episodes_ad AFTER DELETE ON episodes BEGIN
            INSERT INTO episodes_fts(episodes_fts, rowid, problem, thinking_trace, domain, outcome, tags)
            VALUES ('delete', old.rowid, old.problem, old.thinking_trace, old.domain, old.outcome, old.tags);
        END;
        CREATE TRIGGER IF NOT EXISTS episodes_au AFTER UPDATE ON episodes BEGIN
            INSERT INTO episodes_fts(episodes_fts, rowid, problem, thinking_trace, domain, outcome, tags)
            VALUES ('delete', old.rowid, old.problem, old.thinking_trace, old.domain, old.outcome, old.tags);
            INSERT INTO episodes_fts(rowid, problem, thinking_trace, domain, outcome, tags)
            VALUES (new.rowid, new.problem, new.thinking_trace, new.domain, new.outcome, new.tags);
        END;
    """)

    imported = 0
    for ep in episodes:
        try:
            db.execute(
                """INSERT OR IGNORE INTO episodes
                   (id, created_at, domain, outcome, tags, problem, thinking_trace,
                    steps, tool_calls, model_id, duration_seconds)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    ep["id"],
                    ep.get("created_at", datetime.now(timezone.utc).isoformat()),
                    ep.get("domain", "coding"),
                    ep.get("outcome", "unknown"),
                    safe_json(ep.get("tags", [])),
                    ep.get("problem", ""),
                    ep.get("thinking_trace", ""),
                    safe_json(ep.get("steps", [])),
                    safe_json(ep.get("tool_calls", [])),
                    ep.get("model_id", ""),
                    ep.get("duration_seconds", 0),
                ),
            )
            imported += 1
        except sqlite3.IntegrityError:
            pass
    print(f"Imported {imported} episodes")

    pat_count = 0
    for pat in patterns:
        try:
            db.execute(
                """INSERT OR IGNORE INTO patterns
                   (id, created_at, domain, merge_score, sources,
                    consolidated_prompt, master_thinking_path, master_tool_calls, tags)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    pat["id"],
                    pat.get("created_at", datetime.now(timezone.utc).isoformat()),
                    pat.get("domain", "unknown"),
                    pat.get("merge_score", 0.0),
                    safe_json(pat.get("sources", [])),
                    pat.get("consolidated_prompt", ""),
                    pat.get("master_thinking_path", ""),
                    safe_json(pat.get("master_tool_calls", [])),
                    safe_json(pat.get("tags", [])),
                ),
            )
            pat_count += 1
        except sqlite3.IntegrityError:
            pass
    print(f"Imported {pat_count} patterns")

    db.commit()
    db.close()
    print(f"Migration complete → {DB_PATH}")


if __name__ == "__main__":
    dry_run = "--dry-run" in sys.argv
    migrate(dry_run=dry_run)
