"""Webhook event handlers for GitHub and GitLab."""

import logging
from typing import Optional

logger = logging.getLogger("pr-reviewer.handlers")


def parse_github_payload(payload: dict) -> Optional[dict]:
    event = payload.get("x-github-event", "")
    if event not in ("pull_request", "pull_request_target"):
        return None

    action = payload.get("action", "")
    if action not in ("opened", "synchronize", "reopened"):
        return None

    pr = payload.get("pull_request", {})
    return {
        "url": pr.get("html_url", ""),
        "title": pr.get("title", ""),
        "source": "github",
    }


def parse_gitlab_payload(payload: dict) -> Optional[dict]:
    event_type = payload.get("event_type", "")
    if event_type not in ("Merge Request Hook", "Merge Request"):
        return None

    mr = payload.get("object_attributes", {})
    action = mr.get("action", "")
    if action not in ("open", "reopen", "update"):
        return None

    return {
        "url": mr.get("url", ""),
        "title": mr.get("title", ""),
        "source": "gitlab",
    }
