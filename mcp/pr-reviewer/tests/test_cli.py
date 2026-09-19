"""Tests for core/cli.py"""

import json
from unittest.mock import patch

from core.cli import main
from core.models import ReviewResult


def test_cli_validate(capsys):
    exit_code = main(["validate"])
    assert exit_code == 0
    captured = capsys.readouterr()
    report = json.loads(captured.out)
    assert "valid" in report
    assert "warnings" in report


def test_cli_audit(capsys):
    exit_code = main(["audit", "--limit", "10"])
    assert exit_code == 0
    captured = capsys.readouterr()
    data = json.loads(captured.out)
    assert "count" in data
    assert "entries" in data


def test_cli_audit_clear(capsys):
    exit_code = main(["audit", "--clear"])
    assert exit_code == 0
    captured = capsys.readouterr()
    data = json.loads(captured.out)
    assert "cleared" in data


def test_cli_review_missing_input(capsys):
    with patch("sys.stdin.read", return_value="   "), patch("sys.stdin.isatty", return_value=True):
        exit_code = main(["review"])
        assert exit_code == 1
        captured = capsys.readouterr()
        assert "error" in captured.err


def test_cli_review_stdin(capsys):
    diff_content = """--- a/main.py\n+++ b/main.py\n@@ -1,3 +1,3 @@\n-print('hello')\n+print('hello world')\n"""
    mock_result = ReviewResult(
        summary="Clean change",
        verdict="APPROVED",
        confidence=0.95,
        comments=[],
    )

    with (
        patch("core.cli.Reviewer.review_request", return_value=mock_result),
        patch("sys.stdin.read", return_value=diff_content),
        patch("sys.stdin.isatty", return_value=False),
    ):
        exit_code = main(["review", "--stdin", "--title", "Test Change"])
        assert exit_code == 0
        captured = capsys.readouterr()
        res = json.loads(captured.out)
        assert res["verdict"] == "APPROVED"
        assert res["summary"] == "Clean change"


def test_cli_review_url(capsys):
    mock_result = ReviewResult(
        summary="PR approved",
        verdict="APPROVED",
        confidence=0.9,
        comments=[],
    )

    with patch("core.cli.Reviewer.review", return_value=mock_result):
        exit_code = main(["review", "--url", "https://github.com/owner/repo/pull/123"])
        assert exit_code == 0
        captured = capsys.readouterr()
        res = json.loads(captured.out)
        assert res["verdict"] == "APPROVED"
