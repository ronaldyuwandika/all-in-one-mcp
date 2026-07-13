#!/usr/bin/env python3
"""PR Reviewer — MCP Server

Automated Pull/Merge Request reviewer for GitHub and GitLab.
Supports manual review via URL and webhook-based auto-review.
Configurable LLM backends: Gemini, Claude, OpenAI, or DeepSeek.
"""

import json
import logging
import os
import time
from pathlib import Path

import yaml
from mcp.server.fastmcp import FastMCP

from core.reviewer import Reviewer
from core.models import ReviewRequest
from core.secrets import (
    mask_text,
    validate_url,
    validate_title,
    validate_diff,
    validate_provider,
    validate_llm_provider,
    validate_model_name,
    sanitize_string,
)
from core.audit import log_operation, read_audit_log as _read_audit_log, clear_audit_log as _clear_audit_log
from core.validator import validate_config, validation_report

BASE_DIR = Path(__file__).parent
CONFIG_PATH = BASE_DIR / "config.yaml"

logging.basicConfig(
    level=logging.INFO,
    format="%(name)s: %(levelname)s %(message)s",
)
logger = logging.getLogger("pr-reviewer")


def load_config() -> dict:
    if CONFIG_PATH.exists():
        with open(CONFIG_PATH) as f:
            return yaml.safe_load(f) or {}
    return {}


config = load_config()
reviewer = Reviewer(config)

mcp = FastMCP(
    "pr-reviewer",
    instructions="""PR Reviewer: Automated Pull/Merge Request reviewer.

Accepts a PR/MR URL, fetches the diff, analyzes it with an LLM (Gemini or Claude),
and returns structured review comments. Supports both GitHub and GitLab.

Also exposes a webhook listener for auto-review on new PR/MR events.
""",
)


def _err(msg: str) -> str:
    return mask_text(json.dumps({"status": "error", "error": msg}, indent=2))


def _safe_json(obj) -> str:
    return mask_text(json.dumps(obj, indent=2, default=str))


def _validate_startup() -> list[str]:
    warnings = validate_config(config)
    for w in warnings:
        logger.warning("Config: %s", w)
    return warnings


_startup_warnings = _validate_startup()


@mcp.tool()
def review_pr(url: str) -> str:
    """Review a pull request or merge request by URL.

    Supports URLs like:
      https://github.com/owner/repo/pull/123
      https://gitlab.com/owner/repo/-/merge_requests/456

    Args:
        url: Full URL to the pull request or merge request.
    """
    url = sanitize_string(url)
    err = validate_url(url)
    if err:
        return _err(err)

    t0 = time.time()
    try:
        result = reviewer.review(url)
        duration_ms = int((time.time() - t0) * 1000)
        log_operation(
            "review_pr",
            url=url,
            verdict=result.verdict,
            comments_count=len(result.comments),
            duration_ms=duration_ms,
        )
        return _safe_json(result.to_dict())
    except Exception as e:
        duration_ms = int((time.time() - t0) * 1000)
        log_operation("review_pr", url=url, error=str(e), duration_ms=duration_ms)
        logger.exception("Review failed")
        return _err(f"review failed: {e}")


@mcp.tool()
def review_and_post(url: str) -> str:
    """Review a pull request or merge request and post the result as a PR comment.

    Fetches the diff, analyzes it with the configured LLM, and posts
    the structured review as a comment on the PR/MR.

    Args:
        url: Full URL to the pull request or merge request.
    """
    url = sanitize_string(url)
    err = validate_url(url)
    if err:
        return _err(err)

    t0 = time.time()
    try:
        result = reviewer.review_and_post(url)
        duration_ms = int((time.time() - t0) * 1000)
        log_operation(
            "review_and_post",
            url=url,
            verdict=result.verdict,
            comments_count=len(result.comments),
            duration_ms=duration_ms,
        )
        return _safe_json({"posted": True, **result.to_dict()})
    except Exception as e:
        duration_ms = int((time.time() - t0) * 1000)
        log_operation("review_and_post", url=url, error=str(e), duration_ms=duration_ms)
        logger.exception("Review + post failed")
        return _err(f"review + post failed: {e}")


@mcp.tool()
def review_draft(title: str, description: str, diff: str, repo_url: str = "") -> str:
    """Review a draft change without a live PR.

    Useful for pre-submission reviews or code snippets.

    Args:
        title: Title of the proposed change.
        description: Description or context for the change.
        diff: The diff content (unified format) or code to review.
        repo_url: Optional repository URL for context.
    """
    title = sanitize_string(title)
    diff = sanitize_string(diff)

    t_err = validate_title(title)
    d_err = validate_diff(diff)
    if t_err:
        return _err(t_err)
    if d_err:
        return _err(d_err)

    t0 = time.time()
    try:
        req = ReviewRequest(
            title=title,
            description=description or "",
            diff=diff,
            repo_url=repo_url or "",
            source="draft",
        )
        result = reviewer.review_request(req)
        duration_ms = int((time.time() - t0) * 1000)
        log_operation(
            "review_draft",
            url=repo_url,
            verdict=result.verdict,
            comments_count=len(result.comments),
            duration_ms=duration_ms,
        )
        return _safe_json(result.to_dict())
    except Exception as e:
        duration_ms = int((time.time() - t0) * 1000)
        log_operation("review_draft", url=repo_url, error=str(e), duration_ms=duration_ms)
        logger.exception("Draft review failed")
        return _err(f"draft review failed: {e}")


@mcp.tool()
def list_pending_reviews(repo_url: str, source: str = "github") -> str:
    """List open pull requests or merge requests awaiting review.

    Args:
        repo_url: Repository URL (e.g. https://github.com/owner/repo).
        source: Platform: 'github' or 'gitlab'.
    """
    repo_url = sanitize_string(repo_url)
    source = sanitize_string(source)

    p_err = validate_provider(source)
    if p_err:
        return _err(p_err)

    if not repo_url:
        return _err("repo_url is required")

    t0 = time.time()
    try:
        pending = reviewer.list_pending(repo_url, source)
        duration_ms = int((time.time() - t0) * 1000)
        log_operation(
            "list_pending",
            source=source,
            url=repo_url,
            duration_ms=duration_ms,
        )
        return _safe_json(
            {
                "count": len(pending),
                "reviews": [p.to_dict() for p in pending],
            }
        )
    except Exception as e:
        duration_ms = int((time.time() - t0) * 1000)
        log_operation("list_pending", source=source, url=repo_url, error=str(e), duration_ms=duration_ms)
        logger.exception("Failed to list pending reviews")
        return _err(f"failed to list pending reviews: {e}")


@mcp.tool()
def configure_provider(provider: str, token: str) -> str:
    """Set GitHub or GitLab token for API access.

    Args:
        provider: 'github' or 'gitlab'.
        token: Personal access token.
    """
    provider = sanitize_string(provider)
    p_err = validate_provider(provider)
    if p_err:
        return _err(p_err)

    key = f"{provider}_token"
    os.environ[key.upper()] = token
    result = reviewer.configure(provider, token)
    return _safe_json({"configured": provider, "success": result})


@mcp.tool()
def configure_llm(provider: str, model: str, api_key: str = "") -> str:
    """Switch LLM backend for code analysis.

    Args:
        provider: 'gemini' or 'claude'.
        model: Model name (e.g. 'gemini-2.0-flash', 'claude-sonnet-4-20260514').
        api_key: API key (omit to use existing env var).
    """
    provider = sanitize_string(provider)
    model = sanitize_string(model)

    p_err = validate_llm_provider(provider)
    if p_err:
        return _err(p_err)
    m_err = validate_model_name(model)
    if m_err:
        return _err(m_err)

    if api_key:
        env_map = {
            "gemini": "GEMINI_API_KEY",
            "claude": "ANTHROPIC_API_KEY",
            "openai": "OPENAI_API_KEY",
            "deepseek": "DEEPSEEK_API_KEY",
        }
        env_key = env_map.get(provider)
        if env_key:
            os.environ[env_key] = api_key

    result = reviewer.configure_llm(provider, model)
    return _safe_json({"configured": provider, "model": model, "success": result})


@mcp.tool()
def get_audit_log(limit: int = 50) -> str:
    """Retrieve recent audit log entries.

    Args:
        limit: Maximum number of entries to return (default 50).
    """
    try:
        entries = _read_audit_log(limit=max(1, min(limit, 500)))
        return _safe_json({"count": len(entries), "entries": entries})
    except Exception as e:
        return _err(f"failed to read audit log: {e}")


@mcp.tool()
def clear_audit_log() -> str:
    """Clear all audit log entries."""
    try:
        count = _clear_audit_log()
        return _safe_json({"cleared": count})
    except Exception as e:
        return _err(f"failed to clear audit log: {e}")


@mcp.tool()
def validate_configuration() -> str:
    """Validate the current pr-reviewer configuration and return a report."""
    try:
        report = validation_report(config)
        return _safe_json(report)
    except Exception as e:
        return _err(f"validation failed: {e}")


if __name__ == "__main__":
    mcp.run()
