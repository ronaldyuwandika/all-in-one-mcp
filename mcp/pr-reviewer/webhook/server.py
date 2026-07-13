"""FastAPI webhook server for auto-reviewing PRs/MRs via webhook events."""

import json
import logging
import os
from pathlib import Path

import yaml
from fastapi import FastAPI, Request, Response

from core.reviewer import Reviewer

logger = logging.getLogger("pr-reviewer.webhook")

BASE_DIR = Path(__file__).parent.parent
CONFIG_PATH = BASE_DIR / "config.yaml"

with open(CONFIG_PATH) as f:
    config = yaml.safe_load(f) or {}

reviewer = Reviewer(config)
app = FastAPI(title="PR Reviewer Webhook")


@app.post("/webhook/github")
async def github_webhook(request: Request):
    event = request.headers.get("x-github-event", "")
    if event not in ("pull_request", "pull_request_target"):
        return {"status": "ignored", "event": event}

    payload = await request.json()
    action = payload.get("action", "")
    if action not in ("opened", "synchronize", "reopened"):
        return {"status": "ignored", "action": action}

    pr = payload.get("pull_request", {})
    html_url = pr.get("html_url", "")
    if not html_url:
        return {"status": "error", "message": "No PR URL"}

    result = reviewer.review(html_url)
    logger.info("Auto-reviewed %s: %s", html_url, result.verdict)

    return {"status": "ok", "url": html_url, "verdict": result.verdict, "comments": len(result.comments)}


@app.post("/webhook/gitlab")
async def gitlab_webhook(request: Request):
    payload = await request.json()
    event_type = payload.get("event_type", "")

    if event_type not in ("Merge Request Hook", "Merge Request"):
        return {"status": "ignored", "event": event_type}

    mr = payload.get("object_attributes", {})
    action = mr.get("action", "")
    if action not in ("open", "reopen", "update"):
        return {"status": "ignored", "action": action}

    url = mr.get("url", "")
    if not url:
        return {"status": "error", "message": "No MR URL"}

    result = reviewer.review(url)
    logger.info("Auto-reviewed %s: %s", url, result.verdict)

    return {"status": "ok", "url": url, "verdict": result.verdict, "comments": len(result.comments)}


@app.get("/health")
async def health():
    return {"status": "ok"}


def run():
    import uvicorn

    port = int(os.environ.get("PR_REVIEWER_PORT", "8080"))
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="info")


if __name__ == "__main__":
    run()
