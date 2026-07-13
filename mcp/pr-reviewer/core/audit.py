"""Audit logging for pr-reviewer operations.

Writes JSONL entries to ~/.pr-reviewer/audit.jsonl (one JSON object per line).
"""

import json
import logging
import os
from datetime import datetime, timezone
from pathlib import Path
from uuid import uuid4

logger = logging.getLogger("pr-reviewer.audit")

AUDIT_DIR = Path.home() / ".pr-reviewer"
AUDIT_FILE = AUDIT_DIR / "audit.jsonl"


def _ensure_audit_dir():
    AUDIT_DIR.mkdir(parents=True, exist_ok=True)
    if not AUDIT_FILE.exists():
        AUDIT_FILE.touch()
    os.chmod(AUDIT_FILE, 0o600)


def log_operation(
    operation: str,
    *,
    source: str = "manual",
    url: str = "",
    repo: str = "",
    verdict: str = "",
    error: str = "",
    duration_ms: int = 0,
    comments_count: int = 0,
    extra: dict | None = None,
) -> str:
    """Write an audit entry. Returns the entry ID."""
    entry_id = str(uuid4())
    entry: dict = {
        "id": entry_id,
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "operation": operation,
        "source": source,
    }
    if url:
        entry["url"] = url
    if repo:
        entry["repo"] = repo
    if verdict:
        entry["verdict"] = verdict
    if error:
        entry["error"] = error
    if duration_ms:
        entry["duration_ms"] = duration_ms
    if comments_count:
        entry["comments_count"] = comments_count
    if extra:
        entry["extra"] = extra

    try:
        _ensure_audit_dir()
        with open(AUDIT_FILE, "a") as f:
            f.write(json.dumps(entry, default=str) + "\n")
    except Exception as e:
        logger.warning("Failed to write audit entry: %s", e)

    return entry_id


def read_audit_log(limit: int = 100) -> list[dict]:
    """Read the most recent audit entries."""
    if not AUDIT_FILE.exists():
        return []
    try:
        with open(AUDIT_FILE) as f:
            lines = f.readlines()
        entries = []
        for line in lines[-limit:]:
            line = line.strip()
            if line:
                entries.append(json.loads(line))
        return entries
    except Exception:
        return []


def clear_audit_log() -> int:
    """Delete the audit file. Returns number of entries removed."""
    entries = read_audit_log(limit=10_000)
    count = len(entries)
    if AUDIT_FILE.exists():
        AUDIT_FILE.unlink()
    return count
