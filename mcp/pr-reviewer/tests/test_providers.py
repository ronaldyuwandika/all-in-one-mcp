"""Tests for providers."""

import pytest
from providers.github import GitHubProvider
from providers.gitlab import GitLabProvider


def test_github_headers_no_token():
    p = GitHubProvider({"api_url": "https://api.github.com", "token_env": "GITHUB_TOKEN"})
    headers = p._headers()
    assert "Authorization" not in headers


def test_gitlab_headers_no_token():
    p = GitLabProvider({"api_url": "https://gitlab.com", "token_env": "GITLAB_TOKEN"})
    headers = p._headers()
    assert "PRIVATE-TOKEN" not in headers
