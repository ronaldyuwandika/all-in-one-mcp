#!/usr/bin/env python3
"""PR Reviewer — MCP Server

Automated Pull/Merge Request reviewer for GitHub and GitLab.
Supports manual review via URL and webhook-based auto-review.
Configurable LLM backends: Gemini or Claude.
"""

import json
import logging
import os
from pathlib import Path

import yaml
from mcp.server.fastmcp import FastMCP

from core.reviewer import Reviewer
from core.models import ReviewRequest

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
    return json.dumps({"status": "error", "error": msg}, indent=2)


@mcp.tool()
def review_pr(url: str) -> str:
    """Review a pull request or merge request by URL.

    Supports URLs like:
      https://github.com/owner/repo/pull/123
      https://gitlab.com/owner/repo/-/merge_requests/456

    Args:
        url: Full URL to the pull request or merge request.
    """
    if not url or not url.strip():
        return _err("url is required")

    try:
        result = reviewer.review(url.strip())
        return json.dumps(result.to_dict(), indent=2, default=str)
    except Exception as e:
        logger.exception("Review failed")
        return _err(f"review failed: {e}")


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
    if not title or not diff:
        return _err("title and diff are required")

    try:
        req = ReviewRequest(
            title=title,
            description=description,
            diff=diff,
            repo_url=repo_url or "",
            source="draft",
        )
        result = reviewer.review_request(req)
        return json.dumps(result.to_dict(), indent=2, default=str)
    except Exception as e:
        logger.exception("Draft review failed")
        return _err(f"draft review failed: {e}")


@mcp.tool()
def list_pending_reviews(repo_url: str, source: str = "github") -> str:
    """List open pull requests or merge requests awaiting review.

    Args:
        repo_url: Repository URL (e.g. https://github.com/owner/repo).
        source: Platform: 'github' or 'gitlab'.
    """
    if not repo_url:
        return _err("repo_url is required")

    try:
        pending = reviewer.list_pending(repo_url, source)
        return json.dumps(
            {
                "count": len(pending),
                "reviews": [p.to_dict() for p in pending],
            },
            indent=2,
            default=str,
        )
    except Exception as e:
        logger.exception("Failed to list pending reviews")
        return _err(f"failed to list pending reviews: {e}")


@mcp.tool()
def configure_provider(provider: str, token: str) -> str:
    """Set GitHub or GitLab token for API access.

    Args:
        provider: 'github' or 'gitlab'.
        token: Personal access token.
    """
    key = f"{provider}_token"
    os.environ[key.upper()] = token
    result = reviewer.configure(provider, token)
    return json.dumps({"configured": provider, "success": result}, indent=2)


@mcp.tool()
def configure_llm(provider: str, model: str, api_key: str = "") -> str:
    """Switch LLM backend for code analysis.

    Args:
        provider: 'gemini' or 'claude'.
        model: Model name (e.g. 'gemini-2.0-flash', 'claude-sonnet-4-20260514').
        api_key: API key (omit to use existing env var).
    """
    if api_key:
        env_key = {
            "gemini": "GEMINI_API_KEY",
            "claude": "ANTHROPIC_API_KEY",
        }.get(provider)
        if env_key:
            os.environ[env_key] = api_key

    result = reviewer.configure_llm(provider, model)
    return json.dumps({"configured": provider, "model": model, "success": result}, indent=2)


if __name__ == "__main__":
    mcp.run()
