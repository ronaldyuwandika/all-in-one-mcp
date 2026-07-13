"""Tests for core/models.py"""

import pytest
from core.models import Comment, ReviewRequest, ReviewResult, PendingReview


def test_comment_creation():
    c = Comment(file="main.go", line=42, severity="critical", message="SQL injection risk", rule="security")
    d = c.to_dict()
    assert d["file"] == "main.go"
    assert d["line"] == 42
    assert d["severity"] == "critical"
    assert d["rule"] == "security"


def test_review_request():
    r = ReviewRequest(title="Fix bug", description="Fixes the login bug", diff="@@ -1,3 +1,4 @@", source="draft")
    d = r.to_dict()
    assert d["title"] == "Fix bug"
    assert d["source"] == "draft"


def test_review_result():
    comments = [Comment(file="a.go", line=1, severity="info", message="LGTM")]
    r = ReviewResult(summary="Looks good", verdict="approved", confidence=0.9, comments=comments)
    d = r.to_dict()
    assert d["verdict"] == "approved"
    assert d["confidence"] == 0.9
    assert len(d["comments"]) == 1


def test_pending_review():
    p = PendingReview(
        title="Add feature",
        number=42,
        url="https://github.com/owner/repo/pull/42",
        author="user1",
        created_at="2026-01-01T00:00:00Z",
        source="github",
    )
    d = p.to_dict()
    assert d["number"] == 42
    assert d["source"] == "github"
