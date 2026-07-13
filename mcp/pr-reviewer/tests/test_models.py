"""Tests for core/models.py"""

from core.models import ReviewRequest, ReviewResult, Comment, PendingReview


class TestComment:
    def test_comment_creation(self):
        c = Comment(file="main.go", line=42, severity="critical", message="SQL injection risk", rule="security")
        assert c.file == "main.go"
        assert c.line == 42
        assert c.severity == "critical"
        assert c.message == "SQL injection risk"
        assert c.rule == "security"

    def test_comment_default_rule(self):
        c = Comment(file="a.py", line=1, severity="info", message="default test")
        assert c.file == "a.py"
        assert c.line == 1
        assert c.severity == "info"
        assert c.rule == ""

    def test_comment_to_dict(self):
        c = Comment(file="a.py", line=10, severity="warning", message="unused import", rule="style")
        d = c.to_dict()
        assert d == {
            "file": "a.py",
            "line": 10,
            "severity": "warning",
            "message": "unused import",
            "rule": "style",
        }

    def test_comment_empty_file_and_line(self):
        c = Comment(file="", line=0, severity="info", message="general note", rule="")
        d = c.to_dict()
        assert d["file"] == ""
        assert d["line"] == 0

    def test_comment_negative_line(self):
        c = Comment(file="x.py", line=-1, severity="warning", message="test", rule="")
        assert c.line == -1


class TestReviewRequest:
    def test_to_dict(self):
        req = ReviewRequest(
            title="Fix bug",
            description="Fixes a thing",
            diff="diff content",
            repo_url="https://github.com/org/repo",
            source="github",
            pr_number=123,
            pr_url="https://github.com/org/repo/pull/123",
            commit_sha="abc123",
        )
        d = req.to_dict()
        assert d["title"] == "Fix bug"
        assert d["description"] == "Fixes a thing"
        assert d["source"] == "github"
        assert d["pr_number"] == 123
        assert "commit_sha" not in d

    def test_to_dict_excludes_diff(self):
        req = ReviewRequest(
            title="Fix bug",
            description="",
            diff="very long diff...",
            repo_url="https://github.com/org/repo",
            source="draft",
        )
        d = req.to_dict()
        assert "diff" not in d


class TestReviewResult:
    def test_to_dict_basic(self):
        result = ReviewResult(
            summary="All good",
            verdict="approved",
            confidence=0.9,
            comments=[],
            review_time_s=1.5,
            llm_provider="gemini",
        )
        d = result.to_dict()
        assert d["summary"] == "All good"
        assert d["verdict"] == "approved"
        assert d["confidence"] == 0.9
        assert d["comments"] == []
        assert d["review_time_s"] == 1.5
        assert d["llm_provider"] == "gemini"

    def test_to_dict_with_comments(self):
        result = ReviewResult(
            summary="Needs fixes",
            verdict="changes_requested",
            confidence=0.75,
            comments=[
                Comment(file="a.py", line=1, severity="critical", message="bad", rule="security"),
                Comment(file="b.py", line=2, severity="warning", message="meh", rule="style"),
            ],
        )
        d = result.to_dict()
        assert len(d["comments"]) == 2
        assert d["comments"][0]["file"] == "a.py"
        assert d["comments"][1]["line"] == 2


class TestPendingReview:
    def test_to_dict(self):
        p = PendingReview(
            title="Add feature",
            number=7,
            url="https://github.com/org/repo/pull/7",
            author="dev1",
            created_at="2026-07-01T12:00:00Z",
            source="github",
        )
        d = p.to_dict()
        assert d["title"] == "Add feature"
        assert d["number"] == 7
        assert d["author"] == "dev1"
        assert d["source"] == "github"
