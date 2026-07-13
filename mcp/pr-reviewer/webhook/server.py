"""FastAPI webhook server for auto-reviewing PRs/MRs via webhook events."""

import hashlib
import hmac
import logging
import os
import time
from pathlib import Path

import yaml
from fastapi import FastAPI, Request, HTTPException
from fastapi.middleware.cors import CORSMiddleware

from core.reviewer import Reviewer
from core.audit import log_operation

logger = logging.getLogger("pr-reviewer.webhook")

BASE_DIR = Path(__file__).parent.parent
CONFIG_PATH = BASE_DIR / "config.yaml"

with open(CONFIG_PATH) as f:
    config = yaml.safe_load(f) or {}

reviewer = Reviewer(config)
app = FastAPI(title="PR Reviewer Webhook")

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["POST", "GET"],
    allow_headers=["*"],
)

WEBHOOK_SECRET_GITHUB = os.environ.get("GITHUB_WEBHOOK_SECRET", "")
WEBHOOK_SECRET_GITLAB = os.environ.get("GITLAB_WEBHOOK_SECRET", "")


def _verify_github_signature(body: bytes, signature: str) -> bool:
    if not WEBHOOK_SECRET_GITHUB:
        return True
    if not signature:
        return False
    expected = "sha256=" + hmac.new(WEBHOOK_SECRET_GITHUB.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature)


def _verify_gitlab_token(token: str) -> bool:
    if not WEBHOOK_SECRET_GITLAB:
        return True
    if not token:
        return False
    return hmac.compare_digest(WEBHOOK_SECRET_GITLAB, token)


@app.post("/webhook/github")
async def github_webhook(request: Request):
    event = request.headers.get("x-github-event", "")
    if event not in ("pull_request", "pull_request_target"):
        return {"status": "ignored", "event": event}

    body = await request.body()
    signature = request.headers.get("x-hub-signature-256", "")
    if not _verify_github_signature(body, signature):
        raise HTTPException(status_code=403, detail="Invalid signature")

    try:
        payload = await _parse_json_body(body)
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid JSON payload")

    action = payload.get("action", "")
    if action not in ("opened", "synchronize", "reopened"):
        return {"status": "ignored", "action": action}

    pr = payload.get("pull_request", {})
    html_url = pr.get("html_url", "")
    if not html_url:
        return {"status": "error", "message": "No PR URL"}

    t0 = time.time()
    result = reviewer.review_and_post(html_url)
    duration_ms = int((time.time() - t0) * 1000)

    log_operation(
        "review_and_post",
        source="webhook_github",
        url=html_url,
        verdict=result.verdict,
        comments_count=len(result.comments),
        duration_ms=duration_ms,
    )
    logger.info("Auto-reviewed %s: %s", html_url, result.verdict)

    return {
        "status": "ok",
        "url": html_url,
        "verdict": result.verdict,
        "comments": len(result.comments),
    }


@app.post("/webhook/gitlab")
async def gitlab_webhook(request: Request):
    body = await request.body()
    token = request.headers.get("x-gitlab-token", "")
    if not _verify_gitlab_token(token):
        raise HTTPException(status_code=403, detail="Invalid token")

    try:
        payload = await _parse_json_body(body)
    except ValueError:
        raise HTTPException(status_code=400, detail="Invalid JSON payload")

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

    t0 = time.time()
    result = reviewer.review_and_post(url)
    duration_ms = int((time.time() - t0) * 1000)

    log_operation(
        "review_and_post",
        source="webhook_gitlab",
        url=url,
        verdict=result.verdict,
        comments_count=len(result.comments),
        duration_ms=duration_ms,
    )
    logger.info("Auto-reviewed %s: %s", url, result.verdict)

    return {
        "status": "ok",
        "url": url,
        "verdict": result.verdict,
        "comments": len(result.comments),
    }


@app.get("/health")
async def health():
    return {"status": "ok"}


async def _parse_json_body(body: bytes) -> dict:
    import json

    try:
        return json.loads(body)
    except json.JSONDecodeError:
        raise ValueError("Invalid JSON payload")


def run():
    import uvicorn

    port = int(os.environ.get("PR_REVIEWER_PORT", "8080"))
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="info")  # nosec B104


if __name__ == "__main__":
    run()
