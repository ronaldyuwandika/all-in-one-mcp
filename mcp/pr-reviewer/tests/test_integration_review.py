"""Integration tests for the reviewer flow (with mocks)."""

from core.models import ReviewRequest


class TestIntegrationReviewFlow:
    def test_full_review_flow_github(self, reviewer_with_mocks):
        """End-to-end mock: URL -> parse -> fetch -> LLM -> result."""
        result = reviewer_with_mocks.review("https://github.com/test-owner/test-repo/pull/42")
        assert result.verdict == "changes_requested"
        assert result.confidence == 0.95
        assert len(result.comments) == 3
        assert result.comments[0].severity == "critical"
        assert result.llm_provider == "mock-gemini"

    def test_full_review_flow_gitlab(self, reviewer_with_mocks):
        """End-to-end mock for GitLab MR."""
        result = reviewer_with_mocks.review("https://gitlab.com/test-owner/test-repo/-/merge_requests/99")
        assert result.verdict == "changes_requested"
        assert len(result.comments) == 3

    def test_review_and_post_flow(self, reviewer_with_mocks):
        """Verify review_and_post calls post_review."""
        result = reviewer_with_mocks.review_and_post("https://github.com/test-owner/test-repo/pull/42")
        assert result.verdict == "changes_requested"
        assert reviewer_with_mocks.github_provider.post_review.called

    def test_draft_review_flow(self, reviewer_with_mocks):
        """Test draft review without a live PR."""
        req = ReviewRequest(
            title="Add retry logic",
            description="Adds retry",
            diff="diff --git a/x.py b/x.py\n+def retry(): pass",
            source="draft",
        )
        result = reviewer_with_mocks.review_request(req)
        assert result.verdict == "changes_requested"
        assert result.llm_provider == "mock-gemini"

    def test_review_handles_empty_description(self, reviewer_with_mocks):
        """Draft with empty description should still work."""
        req = ReviewRequest(
            title="Fix",
            description="",
            diff="diff",
            source="draft",
        )
        result = reviewer_with_mocks.review_request(req)
        assert result.verdict in ("approved", "changes_requested", "needs_work")


class TestRedaction:
    def test_diff_with_github_token_is_redacted(self):
        """Secret patterns in diff are redacted in output."""
        from core.secrets import mask_text

        fake_token = "ghp_" + "a" * 20
        diff_with_secret = f"""diff --git a/.env b/.env
+ GITHUB_TOKEN={fake_token}
+ AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"""

        redacted = mask_text(diff_with_secret)
        assert fake_token not in redacted
        assert "[REDACTED:" in redacted

    def test_diff_with_private_key_is_redacted(self):
        from core.secrets import mask_text

        diff_with_key = """-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC
-----END PRIVATE KEY-----"""

        redacted = mask_text(diff_with_key)
        assert "-----BEGIN PRIVATE KEY-----" not in redacted
        assert "[REDACTED:SSH_PRIVATE_KEY]" in redacted

    def test_diff_with_database_url_is_redacted(self):
        from core.secrets import mask_text

        diff_with_url = "DATABASE_URL=postgresql://user:password@localhost/db"

        redacted = mask_text(diff_with_url)
        assert "password" not in redacted.lower() or "REDACTED" in redacted.upper()

    def test_clean_diff_is_unchanged(self):
        from core.secrets import mask_text

        clean = """diff --git a/main.py b/main.py
+ def hello():
+     print("hello world")"""

        assert mask_text(clean) == clean

    def test_generic_credentials_are_fully_redacted(self):
        from core.secrets import mask_text

        credentials = [
            "api_key=abcdefgh-super-secret-tail",
            "password=correct-horse-battery-staple",
            "token=abcdefghijklmnop-secret-tail",
        ]
        for credential in credentials:
            assert credential not in mask_text(credential)
            assert credential.split("=", 1)[1][:8] not in mask_text(credential)


class TestInputValidation:
    def test_validate_url_valid(self):
        from core.secrets import validate_url

        assert validate_url("https://github.com/owner/repo/pull/1") is None
        assert validate_url("https://gitlab.com/owner/repo/-/merge_requests/1") is None

    def test_validate_url_invalid(self):
        from core.secrets import validate_url

        assert validate_url("") is not None
        assert validate_url("https://evil.com/pull/1") is not None
        assert validate_url("not a url") is not None

    def test_validate_title(self):
        from core.secrets import validate_title

        assert validate_title("Good title") is None
        assert validate_title("") is not None
        assert validate_title("x" * 300) is not None

    def test_validate_diff(self):
        from core.secrets import validate_diff

        assert validate_diff("some diff") is None
        assert validate_diff("") is not None
        assert validate_diff("x" * 60_000) is not None

    def test_validate_provider(self):
        from core.secrets import validate_provider

        assert validate_provider("github") is None
        assert validate_provider("gitlab") is None
        assert validate_provider("bitbucket") is not None

    def test_validate_llm_provider(self):
        from core.secrets import validate_llm_provider

        assert validate_llm_provider("gemini") is None
        assert validate_llm_provider("deepseek") is None
        assert validate_llm_provider("unknown") is not None

    def test_sanitize_string(self):
        from core.secrets import sanitize_string

        assert sanitize_string("  hello  ") == "hello"


class TestAuditLogging:
    def test_log_operation_writes_entry(self, tmp_path):
        import core.audit as audit_mod
        from core.audit import log_operation, read_audit_log

        original = audit_mod.AUDIT_FILE
        audit_mod.AUDIT_FILE = tmp_path / "audit.jsonl"
        audit_mod._ensure_audit_dir()
        try:
            entry_id = log_operation(
                "review_pr",
                url="https://github.com/o/r/pull/1",
                verdict="approved",
                duration_ms=1234,
                comments_count=2,
            )
            assert entry_id
            entries = read_audit_log()
            assert len(entries) == 1
            assert entries[0]["operation"] == "review_pr"
            assert entries[0]["verdict"] == "approved"
        finally:
            audit_mod.AUDIT_FILE = original

    def test_clear_audit_log(self, tmp_path):
        import core.audit as audit_mod
        from core.audit import clear_audit_log, log_operation

        original = audit_mod.AUDIT_FILE
        audit_mod.AUDIT_FILE = tmp_path / "audit.jsonl"
        audit_mod._ensure_audit_dir()
        try:
            log_operation("test_op")
            assert len(audit_mod.read_audit_log()) == 1
            count = clear_audit_log()
            assert count >= 0
        finally:
            audit_mod.AUDIT_FILE = original


class TestValidator:
    def test_valid_config(self, sample_config):
        from core.validator import validate_config

        warnings = validate_config(sample_config)
        assert len(warnings) == 0

    def test_missing_required_key(self):
        from core.validator import validate_config

        warnings = validate_config({"llm": {"provider": "gemini"}})
        assert any("github" in w.lower() for w in warnings)
        assert any("review" in w.lower() for w in warnings)

    def test_invalid_llm_provider(self, sample_config):
        from core.validator import validate_config

        sample_config["llm"]["provider"] = "unknown"
        warnings = validate_config(sample_config)
        assert any("unknown" in w for w in warnings)

    def test_invalid_confidence(self, sample_config):
        from core.validator import validate_config

        sample_config["review"]["min_confidence"] = 2.0
        warnings = validate_config(sample_config)
        assert any("min_confidence" in w for w in warnings)

    def test_invalid_rule(self, sample_config):
        from core.validator import validate_config

        sample_config["review"]["rules"] = ["security", "invalid_rule"]
        warnings = validate_config(sample_config)
        assert any("invalid_rule" in w for w in warnings)

    def test_empty_rules(self, sample_config):
        from core.validator import validate_config

        sample_config["review"]["rules"] = []
        warnings = validate_config(sample_config)
        assert any("rule" in w.lower() for w in warnings)

    def test_validation_report(self, sample_config):
        from core.validator import validation_report

        report = validation_report(sample_config)
        assert report["valid"] is True
        assert report["warning_count"] == 0
        assert "config_summary" in report

    def test_non_dict_config(self):
        from core.validator import validate_config

        warnings = validate_config([])
        assert len(warnings) == 1
