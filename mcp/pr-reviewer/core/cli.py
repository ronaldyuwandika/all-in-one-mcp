#!/usr/bin/env python3
"""CLI interface for PR Reviewer."""

import argparse
import json
import os
import sys
import time
from pathlib import Path

import yaml

from core.audit import clear_audit_log, log_operation, read_audit_log
from core.models import ReviewRequest
from core.reviewer import Reviewer
from core.secrets import mask_text, sanitize_string, validate_diff, validate_title, validate_url
from core.validator import validation_report


def load_config(config_path: str | None = None) -> dict:
    """Load configuration from specified path or default locations."""
    if config_path:
        p = Path(config_path)
        if p.exists():
            with open(p) as f:
                return yaml.safe_load(f) or {}

    env_path = os.environ.get("PR_REVIEWER_CONFIG")
    if env_path and Path(env_path).exists():
        with open(env_path) as f:
            return yaml.safe_load(f) or {}

    # Check project root or core directory parent
    default_path = Path(__file__).resolve().parent.parent / "config.yaml"
    if default_path.exists():
        with open(default_path) as f:
            return yaml.safe_load(f) or {}

    return {}


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="pr-reviewer",
        description="Automated PR/MR Reviewer CLI",
    )
    parser.add_argument(
        "--config",
        "-c",
        help="Path to config.yaml",
        default=None,
    )

    subparsers = parser.add_subparsers(dest="subcommand", required=True)

    # review subcommand
    review_parser = subparsers.add_parser("review", help="Review a pull request, merge request, or raw diff")
    review_parser.add_argument("--url", help="PR or MR URL to review")
    review_parser.add_argument("--stdin", action="store_true", help="Read diff or ReviewRequest JSON from stdin")
    review_parser.add_argument("--title", default="Draft Review", help="Title for the review request")
    review_parser.add_argument("--description", default="", help="Description or context for review")
    review_parser.add_argument("--repo-url", default="", help="Repository URL for context")
    review_parser.add_argument("--post", action="store_true", help="Post review comment back to PR/MR (URL only)")

    # audit subcommand
    audit_parser = subparsers.add_parser("audit", help="Inspect or clear audit log entries")
    audit_parser.add_argument("--limit", type=int, default=50, help="Maximum number of entries to return")
    audit_parser.add_argument("--clear", action="store_true", help="Clear the audit log")

    # validate subcommand
    subparsers.add_parser("validate", help="Validate current configuration")

    return parser


def handle_review(args: argparse.Namespace, reviewer: Reviewer) -> int:
    t0 = time.time()

    # Case 1: Live PR/MR review via URL
    if args.url:
        url = sanitize_string(args.url)
        err = validate_url(url)
        if err:
            print(mask_text(json.dumps({"status": "error", "error": err}, indent=2)), file=sys.stderr)
            return 1
        try:
            if args.post:
                result = reviewer.review_and_post(url)
                resp = {"posted": True, **result.to_dict()}
            else:
                result = reviewer.review(url)
                resp = result.to_dict()

            duration_ms = int((time.time() - t0) * 1000)
            log_operation(
                "cli_review",
                url=url,
                verdict=result.verdict,
                comments_count=len(result.comments),
                duration_ms=duration_ms,
            )
            print(mask_text(json.dumps(resp, indent=2, default=str)))
            return 0
        except Exception as e:
            duration_ms = int((time.time() - t0) * 1000)
            log_operation("cli_review", url=url, error=str(e), duration_ms=duration_ms)
            print(mask_text(json.dumps({"status": "error", "error": str(e)}, indent=2)), file=sys.stderr)
            return 1

    # Case 2: Stdin or piped diff review
    raw_input = ""
    if args.stdin or not sys.stdin.isatty():
        raw_input = sys.stdin.read()

    if not raw_input.strip():
        print(
            mask_text(
                json.dumps(
                    {"status": "error", "error": "Either --url or diff via stdin (--stdin) is required"},
                    indent=2,
                )
            ),
            file=sys.stderr,
        )
        return 1

    # Check if raw_input is a JSON payload representing ReviewRequest
    stripped = raw_input.strip()
    title = sanitize_string(args.title)
    description = sanitize_string(args.description)
    repo_url = sanitize_string(args.repo_url)
    diff = stripped

    if stripped.startswith("{"):
        try:
            parsed = json.loads(stripped)
            if isinstance(parsed, dict) and ("diff" in parsed or "title" in parsed):
                diff = sanitize_string(parsed.get("diff", ""))
                title = sanitize_string(parsed.get("title", title))
                description = sanitize_string(parsed.get("description", description))
                repo_url = sanitize_string(parsed.get("repo_url", repo_url))
        except json.JSONDecodeError:
            pass

    t_err = validate_title(title)
    if t_err:
        print(mask_text(json.dumps({"status": "error", "error": t_err}, indent=2)), file=sys.stderr)
        return 1

    d_err = validate_diff(diff)
    if d_err:
        print(mask_text(json.dumps({"status": "error", "error": d_err}, indent=2)), file=sys.stderr)
        return 1

    try:
        req = ReviewRequest(
            title=title,
            description=description,
            diff=diff,
            repo_url=repo_url,
            source="cli",
        )
        result = reviewer.review_request(req)
        duration_ms = int((time.time() - t0) * 1000)
        log_operation(
            "cli_review_stdin",
            url=repo_url,
            verdict=result.verdict,
            comments_count=len(result.comments),
            duration_ms=duration_ms,
        )
        print(mask_text(json.dumps(result.to_dict(), indent=2, default=str)))
        return 0
    except Exception as e:
        duration_ms = int((time.time() - t0) * 1000)
        log_operation("cli_review_stdin", url=repo_url, error=str(e), duration_ms=duration_ms)
        print(mask_text(json.dumps({"status": "error", "error": str(e)}, indent=2)), file=sys.stderr)
        return 1


def handle_audit(args: argparse.Namespace) -> int:
    try:
        if args.clear:
            count = clear_audit_log()
            print(json.dumps({"cleared": count}, indent=2))
            return 0
        limit = max(1, min(args.limit, 500))
        entries = read_audit_log(limit=limit)
        print(json.dumps({"count": len(entries), "entries": entries}, indent=2, default=str))
        return 0
    except Exception as e:
        print(json.dumps({"status": "error", "error": str(e)}, indent=2), file=sys.stderr)
        return 1


def handle_validate(config: dict) -> int:
    try:
        report = validation_report(config)
        print(mask_text(json.dumps(report, indent=2, default=str)))
        if not report.get("valid", False):
            return 1
        return 0
    except Exception as e:
        print(json.dumps({"status": "error", "error": str(e)}, indent=2), file=sys.stderr)
        return 1


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)

    config = load_config(args.config)

    if args.subcommand == "review":
        reviewer = Reviewer(config)
        return handle_review(args, reviewer)
    elif args.subcommand == "audit":
        return handle_audit(args)
    elif args.subcommand == "validate":
        return handle_validate(config)
    else:
        parser.print_help(sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
