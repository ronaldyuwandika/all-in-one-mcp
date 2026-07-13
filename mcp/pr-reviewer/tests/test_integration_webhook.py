"""Integration tests for the webhook server."""

import hashlib
import hmac
import json
from unittest.mock import patch

import pytest
from fastapi.testclient import TestClient


@pytest.fixture
def client(reviewer_with_mocks):
    with patch("webhook.server.reviewer", reviewer_with_mocks):
        from webhook.server import app

        yield TestClient(app)


class TestHealthEndpoint:
    def test_health(self, client):
        resp = client.get("/health")
        assert resp.status_code == 200
        assert resp.json() == {"status": "ok"}


class TestGitHubWebhook:
    def test_valid_opened_pr(self, client, webhook_github_payload):
        resp = client.post(
            "/webhook/github",
            json=webhook_github_payload,
            headers={"x-github-event": "pull_request"},
        )
        assert resp.status_code == 200
        data = resp.json()
        assert data["status"] == "ok"

    def test_ignored_event(self, client):
        resp = client.post(
            "/webhook/github",
            json={"action": "opened"},
            headers={"x-github-event": "push"},
        )
        assert resp.status_code == 200
        assert resp.json()["status"] == "ignored"

    def test_ignored_action(self, client):
        resp = client.post(
            "/webhook/github",
            json={"action": "closed", "pull_request": {"html_url": "https://github.com/o/r/pull/1"}},
            headers={"x-github-event": "pull_request"},
        )
        assert resp.status_code == 200
        assert resp.json()["status"] == "ignored"

    def test_no_pr_url(self, client):
        resp = client.post(
            "/webhook/github",
            json={"action": "opened", "pull_request": {}},
            headers={"x-github-event": "pull_request"},
        )
        assert resp.status_code == 200
        assert resp.json()["status"] == "error"

    def test_hmac_validation_bypass_when_no_secret(self, client, webhook_github_payload):
        """Without GITHUB_WEBHOOK_SECRET, HMAC validation is skipped."""
        with patch("webhook.server.WEBHOOK_SECRET_GITHUB", ""):
            resp = client.post(
                "/webhook/github",
                json=webhook_github_payload,
                headers={"x-github-event": "pull_request"},
            )
        assert resp.status_code == 200

    def test_hmac_validation_rejects_invalid(self, client, webhook_github_payload):
        """With GITHUB_WEBHOOK_SECRET set, invalid signature returns 403."""
        secret = "testsecret"
        with patch("webhook.server.WEBHOOK_SECRET_GITHUB", secret):
            resp = client.post(
                "/webhook/github",
                json=webhook_github_payload,
                headers={
                    "x-github-event": "pull_request",
                    "x-hub-signature-256": "sha256=invalid",
                },
            )
        assert resp.status_code == 403

    def test_hmac_validation_accepts_valid(self, client, webhook_github_payload):
        """With correct signature, request is accepted."""
        secret = "testsecret"
        body = json.dumps(webhook_github_payload).encode()
        sig = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()

        with patch("webhook.server.WEBHOOK_SECRET_GITHUB", secret):
            resp = client.post(
                "/webhook/github",
                content=body,
                headers={
                    "x-github-event": "pull_request",
                    "x-hub-signature-256": sig,
                },
            )
        assert resp.status_code == 200

    def test_invalid_json_body(self, client):
        with patch("webhook.server.WEBHOOK_SECRET_GITHUB", ""):
            resp = client.post(
                "/webhook/github",
                content=b"not json",
                headers={
                    "x-github-event": "pull_request",
                    "content-type": "application/json",
                },
            )
        assert resp.status_code == 400


class TestGitLabWebhook:
    def test_valid_mr(self, client, webhook_gitlab_payload):
        resp = client.post("/webhook/gitlab", json=webhook_gitlab_payload)
        assert resp.status_code == 200
        data = resp.json()
        assert data["status"] == "ok"

    def test_ignored_event(self, client):
        resp = client.post(
            "/webhook/gitlab",
            json={"event_type": "Push Hook"},
        )
        assert resp.status_code == 200
        assert resp.json()["status"] == "ignored"

    def test_ignored_action(self, client):
        resp = client.post(
            "/webhook/gitlab",
            json={
                "event_type": "Merge Request Hook",
                "object_attributes": {
                    "action": "close",
                    "url": "https://gitlab.com/o/r/-/merge_requests/1",
                },
            },
        )
        assert resp.status_code == 200
        assert resp.json()["status"] == "ignored"

    def test_no_mr_url(self, client):
        resp = client.post(
            "/webhook/gitlab",
            json={
                "event_type": "Merge Request Hook",
                "object_attributes": {"action": "open"},
            },
        )
        assert resp.status_code == 200
        assert resp.json()["status"] == "error"

    def test_token_validation_bypass_when_no_secret(self, client, webhook_gitlab_payload):
        """Without GITLAB_WEBHOOK_SECRET, token validation is skipped."""
        with patch("webhook.server.WEBHOOK_SECRET_GITLAB", ""):
            resp = client.post("/webhook/gitlab", json=webhook_gitlab_payload)
        assert resp.status_code == 200

    def test_token_validation_rejects_invalid(self, client, webhook_gitlab_payload):
        """With GITLAB_WEBHOOK_SECRET set, invalid token returns 403."""
        with patch("webhook.server.WEBHOOK_SECRET_GITLAB", "testsecret"):
            resp = client.post(
                "/webhook/gitlab",
                json=webhook_gitlab_payload,
                headers={"x-gitlab-token": "wrong"},
            )
        assert resp.status_code == 403

    def test_token_validation_accepts_valid(self, client, webhook_gitlab_payload):
        """With correct token, request is accepted."""
        with patch("webhook.server.WEBHOOK_SECRET_GITLAB", "testsecret"):
            resp = client.post(
                "/webhook/gitlab",
                json=webhook_gitlab_payload,
                headers={"x-gitlab-token": "testsecret"},
            )
        assert resp.status_code == 200
