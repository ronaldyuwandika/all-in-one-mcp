"""Tests for core/reviewer.py"""

import pytest
from core.reviewer import Reviewer
from core.models import ReviewRequest


def test_url_parse_github():
    r = Reviewer({})
    source, owner, repo, number = r._parse_url("https://github.com/owner/repo/pull/42")
    assert source == "github"
    assert owner == "owner"
    assert repo == "repo"
    assert number == 42


@pytest.mark.skip(reason="Requires network")
def test_llm_review():
    r = Reviewer({})
    req = ReviewRequest(
        title="Test change",
        description="A simple test",
        diff="""--- a/main.py\n+++ b/main.py\n@@ -1,3 +1,4 @@\n def hello():\n-    print("hello")\n+    print("hello world")\n""",
        source="draft",
    )
    result = r.review_request(req)
    assert result.verdict in ("approved", "changes_requested", "needs_work")
    assert isinstance(result.comments, list)


def test_empty_diff():
    r = Reviewer({})
    req = ReviewRequest(title="Empty", description="", diff="", source="draft")
    result = r.review_request(req)
    assert result.verdict == "needs_work"
