"""Tests for providers (GitHub/GitLab) with mocked HTTP."""

from unittest.mock import patch, MagicMock

import pytest
from core.models import ReviewRequest, PendingReview
from providers.github import GitHubProvider
from providers.gitlab import GitLabProvider


class TestGitHubProvider:
    @pytest.fixture
    def provider(self):
        return GitHubProvider(
            {
                "api_url": "https://api.github.com",
                "token_env": "GITHUB_TOKEN",
            }
        )

    @pytest.fixture
    def mock_client(self):
        with patch("httpx.Client") as mock:
            yield mock

    def test_fetch_review_success(self, provider, github_pr_json, github_diff):
        pr_response = MagicMock()
        pr_response.status_code = 200
        pr_response.json.return_value = github_pr_json

        diff_response = MagicMock()
        diff_response.status_code = 200
        diff_response.text = github_diff

        with patch("httpx.Client") as mock_client_cls:
            client = MagicMock()
            client.get.side_effect = [pr_response, diff_response]
            mock_client_cls.return_value.__enter__.return_value = client

            result = provider.fetch_review("test-owner", "test-repo", 42)

            assert isinstance(result, ReviewRequest)
            assert result.title == "Fix SQL injection in user query handler"
            assert result.source == "github"
            assert result.pr_number == 42
            assert result.diff == github_diff

    def test_fetch_review_http_error(self, provider):
        with patch("httpx.Client") as mock_client_cls:
            client = MagicMock()
            response = MagicMock()
            response.status_code = 404
            response.raise_for_status.side_effect = Exception("HTTP 404")
            client.get.return_value = response
            mock_client_cls.return_value.__enter__.return_value = client

            with pytest.raises(Exception):
                provider.fetch_review("owner", "repo", 1)

    def test_post_review_success(self, provider):
        with patch("httpx.post") as mock_post:
            mock_response = MagicMock()
            mock_response.status_code = 201
            mock_post.return_value = mock_response

            from core.models import Comment

            comments = [
                Comment(file="a.py", line=10, severity="critical", message="bad", rule="security"),
            ]

            result = provider.post_review("owner", "repo", 42, "summary", "approved", comments, commit_sha="abc123")
            assert result is True

    def test_list_pending(self, provider):
        with patch("httpx.get") as mock_get:
            response = MagicMock()
            response.status_code = 200
            response.json.return_value = [
                {
                    "title": "PR 1",
                    "number": 1,
                    "html_url": "https://github.com/o/r/pull/1",
                    "user": {"login": "dev1"},
                    "created_at": "2026-01-01T00:00:00Z",
                }
            ]
            mock_get.return_value = response

            result = provider.list_pending("owner", "repo")
            assert len(result) == 1
            assert isinstance(result[0], PendingReview)
            assert result[0].title == "PR 1"

    def test_list_pending_error(self, provider):
        with patch("httpx.get") as mock_get:
            mock_get.side_effect = Exception("Network error")
            result = provider.list_pending("owner", "repo")
            assert result == []

    def test_fetch_review_no_token(self, github_pr_json, github_diff):
        provider = GitHubProvider({"api_url": "https://api.github.com"})
        pr_response = MagicMock()
        pr_response.status_code = 200
        pr_response.json.return_value = github_pr_json
        diff_response = MagicMock()
        diff_response.status_code = 200
        diff_response.text = github_diff

        with patch("httpx.Client") as mock_client_cls:
            client = MagicMock()
            client.get.side_effect = [pr_response, diff_response]
            mock_client_cls.return_value.__enter__.return_value = client

            result = provider.fetch_review("test-owner", "test-repo", 42)
            assert result.title == "Fix SQL injection in user query handler"


class TestGitLabProvider:
    @pytest.fixture
    def provider(self):
        return GitLabProvider(
            {
                "api_url": "https://gitlab.com",
                "token_env": "GITLAB_TOKEN",
            }
        )

    def test_fetch_review_success(self, provider, gitlab_mr_json, gitlab_diff):
        mr_response = MagicMock()
        mr_response.status_code = 200
        mr_response.json.return_value = gitlab_mr_json

        diff_response = MagicMock()
        diff_response.status_code = 200
        diff_response.text = gitlab_diff

        with patch("httpx.Client") as mock_client_cls:
            client = MagicMock()
            client.get.side_effect = [mr_response, diff_response]
            mock_client_cls.return_value.__enter__.return_value = client

            result = provider.fetch_review("test-owner", "test-repo", 99)

            assert isinstance(result, ReviewRequest)
            assert result.title == "Add retry logic to external API calls"
            assert result.source == "gitlab"
            assert result.pr_number == 99

    def test_fetch_review_http_error(self, provider):
        with patch("httpx.Client") as mock_client_cls:
            client = MagicMock()
            response = MagicMock()
            response.status_code = 500
            response.raise_for_status.side_effect = Exception("HTTP 500")
            client.get.return_value = response
            mock_client_cls.return_value.__enter__.return_value = client

            with pytest.raises(Exception):
                provider.fetch_review("owner", "repo", 1)

    def test_list_pending(self, provider):
        with patch("httpx.get") as mock_get:
            response = MagicMock()
            response.status_code = 200
            response.json.return_value = [
                {
                    "title": "MR 1",
                    "iid": 1,
                    "web_url": "https://gitlab.com/o/r/-/merge_requests/1",
                    "author": {"username": "dev1", "name": "dev1"},
                    "created_at": "2026-01-01T00:00:00Z",
                }
            ]
            mock_get.return_value = response

            result = provider.list_pending("owner", "repo")
            assert len(result) == 1
            assert isinstance(result[0], PendingReview)

    def test_list_pending_error(self, provider):
        with patch("httpx.get") as mock_get:
            mock_get.side_effect = Exception("Timeout")
            result = provider.list_pending("owner", "repo")
            assert result == []
