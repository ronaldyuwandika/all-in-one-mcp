"""Shared pytest fixtures for pr-reviewer tests."""

import json
from pathlib import Path
from unittest.mock import MagicMock

import pytest

FIXTURES_DIR = Path(__file__).parent / "fixtures"


def _load_fixture(name: str) -> str:
    path = FIXTURES_DIR / name
    return path.read_text()


def _load_json_fixture(name: str) -> dict:
    return json.loads(_load_fixture(name))


@pytest.fixture
def github_pr_json() -> dict:
    return _load_json_fixture("github_pr.json")


@pytest.fixture
def github_diff() -> str:
    return _load_fixture("github_diff.diff")


@pytest.fixture
def gitlab_mr_json() -> dict:
    return _load_json_fixture("gitlab_mr.json")


@pytest.fixture
def gitlab_diff() -> str:
    return _load_fixture("gitlab_diff.diff")


@pytest.fixture
def llm_response_security() -> dict:
    return _load_json_fixture("llm_responses/security_finding.json")


@pytest.fixture
def llm_response_approved() -> dict:
    return _load_json_fixture("llm_responses/approved.json")


@pytest.fixture
def webhook_github_payload() -> dict:
    return _load_json_fixture("webhook_github.json")


@pytest.fixture
def webhook_gitlab_payload() -> dict:
    return _load_json_fixture("webhook_gitlab.json")


@pytest.fixture
def sample_config() -> dict:
    return {
        "llm": {
            "provider": "gemini",
            "gemini": {"model": "gemini-2.0-flash", "api_key_env": "GEMINI_API_KEY"},
            "claude": {
                "model": "claude-sonnet-4-20260514",
                "api_key_env": "ANTHROPIC_API_KEY",
            },
        },
        "github": {
            "api_url": "https://api.github.com",
            "token_env": "GITHUB_TOKEN",
        },
        "gitlab": {
            "api_url": "https://gitlab.com",
            "token_env": "GITLAB_TOKEN",
        },
        "review": {
            "min_confidence": 0.7,
            "max_comments": 10,
            "rules": ["security", "performance", "style", "correctness", "error_handling"],
        },
    }


@pytest.fixture
def mock_llm(llm_response_security):
    """Mock LLM that returns a pre-recorded response."""
    mock = MagicMock()
    mock.analyze.return_value = json.dumps(llm_response_security)
    mock.provider_name.return_value = "mock-gemini"
    return mock


@pytest.fixture
def mock_github_provider(github_pr_json, github_diff):
    """Mock GitHubProvider that returns fixture data."""
    from core.models import ReviewRequest

    provider = MagicMock()
    provider.fetch_review.return_value = ReviewRequest(
        title=github_pr_json["title"],
        description=github_pr_json.get("body", ""),
        diff=github_diff,
        repo_url="https://github.com/test-owner/test-repo",
        source="github",
        pr_number=42,
        pr_url=github_pr_json["html_url"],
        commit_sha=github_pr_json["head"]["sha"],
    )
    provider.post_review.return_value = True
    provider.list_pending.return_value = []
    return provider


@pytest.fixture
def mock_gitlab_provider(gitlab_mr_json, gitlab_diff):
    """Mock GitLabProvider that returns fixture data."""
    from core.models import ReviewRequest

    provider = MagicMock()
    provider.fetch_review.return_value = ReviewRequest(
        title=gitlab_mr_json["title"],
        description=gitlab_mr_json.get("description", ""),
        diff=gitlab_diff,
        repo_url="https://gitlab.com/test-owner/test-repo",
        source="gitlab",
        pr_number=99,
        pr_url=gitlab_mr_json["object_attributes"]["url"],
        commit_sha="",
    )
    provider.post_review.return_value = True
    provider.list_pending.return_value = []
    return provider


@pytest.fixture
def reviewer_with_mocks(mock_llm, mock_github_provider, mock_gitlab_provider, sample_config):
    """Reviewer with all providers and LLM mocked."""
    from core.reviewer import Reviewer

    r = Reviewer(sample_config)
    r._llm = mock_llm
    r.github_provider = mock_github_provider
    r.gitlab_provider = mock_gitlab_provider
    return r
