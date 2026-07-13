"""Tests for core/reviewer.py"""

import json

import pytest
from core.reviewer import Reviewer
from core.models import ReviewRequest


class TestUrlParsing:
    @pytest.fixture
    def reviewer(self):
        return Reviewer({})

    def test_parse_github_url(self, reviewer):
        source, owner, repo, number = reviewer._parse_url("https://github.com/owner/repo/pull/42")
        assert source == "github"
        assert owner == "owner"
        assert repo == "repo"
        assert number == 42

    def test_parse_github_url_trailing_slash(self, reviewer):
        source, owner, repo, number = reviewer._parse_url("https://github.com/owner/repo/pull/42/")
        assert source == "github"
        assert number == 42

    def test_parse_gitlab_url(self, reviewer):
        source, owner, repo, number = reviewer._parse_url("https://gitlab.com/owner/repo/-/merge_requests/99")
        assert source == "gitlab"
        assert owner == "owner"
        assert repo == "repo"
        assert number == 99

    def test_parse_gitlab_subgroup(self, reviewer):
        source, owner, repo, number = reviewer._parse_url("https://gitlab.com/group/subgroup/repo/-/merge_requests/5")
        assert source == "gitlab"
        assert repo == "repo"
        assert number == 5

    def test_parse_url_invalid(self, reviewer):
        with pytest.raises(ValueError, match="Unsupported URL"):
            reviewer._parse_url("https://bitbucket.org/owner/repo/pull/1")

    def test_parse_url_invalid_format(self, reviewer):
        with pytest.raises(ValueError):
            reviewer._parse_url("https://github.com/owner/pull/42")

    def test_parse_gitlab_url_no_merge_requests(self, reviewer):
        with pytest.raises(ValueError, match="Invalid GitLab MR URL"):
            reviewer._parse_url("https://gitlab.com/owner/repo/issues/1")


class TestUrlParsingSelfHosted:
    @pytest.fixture
    def reviewer(self):
        return Reviewer({})

    def test_parse_gitlab_self_hosted(self, reviewer):
        source, owner, repo, number = reviewer._parse_url("https://gitlab.example.com/owner/repo/-/merge_requests/42")
        assert source == "gitlab"
        assert owner == "owner"
        assert repo == "repo"
        assert number == 42


class TestBuildReviewPrompt:
    @pytest.fixture
    def reviewer(self):
        return Reviewer(
            {
                "review": {
                    "rules": ["security", "performance", "style"],
                },
            }
        )

    def test_build_prompt_includes_title_and_diff(self, reviewer):
        req = ReviewRequest(
            title="Add feature X",
            description="Implements feature X",
            diff="+ new line",
            repo_url="https://github.com/o/r",
        )
        prompt = reviewer._build_review_prompt(req)
        assert "Add feature X" in prompt
        assert "new line" in prompt
        assert "## Review Rules" in prompt
        assert "security" in prompt
        assert "Output Format" in prompt

    def test_build_prompt_no_description(self, reviewer):
        req = ReviewRequest(
            title="Fix",
            description="",
            diff="diff",
        )
        prompt = reviewer._build_review_prompt(req)
        assert "(no description)" in prompt

    def test_build_prompt_no_rules(self):
        reviewer = Reviewer({})
        req = ReviewRequest(title="Fix", description="", diff="diff")
        prompt = reviewer._build_review_prompt(req)
        assert "## Review Rules" in prompt


class TestParseLlmResponse:
    @pytest.fixture
    def reviewer(self):
        return Reviewer({})

    def test_parse_valid_json(self, reviewer, llm_response_approved):
        response = "```json\n" + json.dumps(llm_response_approved) + "\n```"
        result = reviewer._parse_llm_response(response)
        assert result.verdict == "approved"
        assert result.confidence == 0.88
        assert len(result.comments) == 1

    def test_parse_valid_json_no_markdown(self, reviewer, llm_response_security):
        response = json.dumps(llm_response_security)
        result = reviewer._parse_llm_response(response)
        assert result.verdict == "changes_requested"
        assert len(result.comments) == 3

    def test_parse_malformed_json(self, reviewer):
        result = reviewer._parse_llm_response("not json at all")
        assert result.verdict == "needs_work"
        assert "Failed to parse" in result.summary

    def test_parse_missing_fields(self, reviewer):
        result = reviewer._parse_llm_response('{"verdict": "approved"}')
        assert result.verdict == "approved"
        assert result.comments == []

    def test_parse_json_with_extra_text(self, reviewer):
        response = (
            'Some text before {"verdict": "approved", "summary": "OK", "confidence": 0.9, "comments": []} more text'
        )
        result = reviewer._parse_llm_response(response)
        assert result.verdict == "approved"

    def test_parse_comments_with_missing_fields(self, reviewer):
        response = json.dumps(
            {
                "summary": "OK",
                "verdict": "approved",
                "confidence": 0.5,
                "comments": [
                    {"file": "x.py"},
                    {},
                ],
            }
        )
        result = reviewer._parse_llm_response(response)
        assert len(result.comments) == 2
        assert result.comments[0].file == "x.py"

    def test_parse_empty_response(self, reviewer):
        result = reviewer._parse_llm_response("")
        assert result.verdict == "needs_work"


class TestReviewerOperations:
    def test_review_with_mocks(self, reviewer_with_mocks):
        result = reviewer_with_mocks.review("https://github.com/test-owner/test-repo/pull/42")
        assert result.verdict in ("approved", "changes_requested", "needs_work")
        assert isinstance(result.comments, list)

    def test_review_and_post(self, reviewer_with_mocks):
        result = reviewer_with_mocks.review_and_post("https://github.com/test-owner/test-repo/pull/42")
        assert result.verdict in ("approved", "changes_requested", "needs_work")
        assert reviewer_with_mocks.github_provider.post_review.called

    def test_list_pending(self, reviewer_with_mocks):
        pending = reviewer_with_mocks.list_pending("https://github.com/owner/repo", "github")
        assert isinstance(pending, list)

    def test_list_pending_invalid_source(self, reviewer_with_mocks):
        with pytest.raises(ValueError, match="Unknown source"):
            reviewer_with_mocks.list_pending("https://github.com/owner/repo", "bitbucket")

    def test_configure_provider(self, reviewer_with_mocks):
        result = reviewer_with_mocks.configure("github", "test-token")
        assert result is True

    def test_configure_provider_invalid(self, reviewer_with_mocks):
        result = reviewer_with_mocks.configure("bitbucket", "token")
        assert result is False

    def test_configure_llm(self, reviewer_with_mocks):
        result = reviewer_with_mocks.configure_llm("gemini", "gemini-2.0-flash")
        assert result is True

    def test_configure_llm_deepseek(self, reviewer_with_mocks):
        result = reviewer_with_mocks.configure_llm("deepseek", "deepseek-v4-pro")
        assert result is True

    def test_configure_llm_invalid(self, reviewer_with_mocks):
        result = reviewer_with_mocks.configure_llm("unknown", "model")
        assert result is False

    def test_empty_diff(self, reviewer_with_mocks):
        req = ReviewRequest(title="Empty", description="", diff="", source="draft")
        result = reviewer_with_mocks.review_request(req)
        assert result is not None
