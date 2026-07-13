"""Review orchestrator: fetch diff, analyze with LLM, return structured comments."""

import logging
import time
from typing import Optional

from core.models import ReviewRequest, ReviewResult, Comment, PendingReview
from providers.github import GitHubProvider
from providers.gitlab import GitLabProvider
from llm.base import BaseLLM
from llm.gemini import GeminiLLM
from llm.claude import ClaudeLLM
from llm.openai import OpenAILLM

logger = logging.getLogger("pr-reviewer.reviewer")


class Reviewer:
    def __init__(self, config: dict):
        self.config = config
        self.github_provider = GitHubProvider(config.get("github", {}))
        self.gitlab_provider = GitLabProvider(config.get("gitlab", {}))
        self._llm: Optional[BaseLLM] = None
        self._init_llm()

    def _init_llm(self):
        llm_config = self.config.get("llm", {})
        provider = llm_config.get("provider", "gemini")
        if provider == "claude":
            self._llm = ClaudeLLM(llm_config.get("claude", {}))
        elif provider in ("openai", "deepseek"):
            self._llm = OpenAILLM(llm_config.get(provider, {}))
        else:
            self._llm = GeminiLLM(llm_config.get("gemini", {}))

    def _parse_url(self, url: str) -> tuple[str, str, str, int]:
        """Parse a PR/MR URL into (source, owner, repo, number)."""
        import urllib.parse

        parsed = urllib.parse.urlparse(url)
        hostname = (parsed.hostname or "").lower()

        if "github.com" in hostname:
            parts = parsed.path.strip("/").split("/")
            try:
                pull_idx = parts.index("pull")
            except ValueError:
                raise ValueError(f"Invalid GitHub PR URL: {url}")
            if pull_idx < 2 or pull_idx + 1 >= len(parts):
                raise ValueError(f"Invalid GitHub PR URL: {url}")
            owner = parts[pull_idx - 2]
            repo = parts[pull_idx - 1]
            number = int(parts[pull_idx + 1])
            return "github", owner, repo, number

        elif "gitlab" in hostname:
            parts = parsed.path.strip("/").split("/")
            try:
                mr_idx = parts.index("merge_requests")
            except ValueError:
                raise ValueError(f"Invalid GitLab MR URL: {url}")
            if mr_idx < 2 or mr_idx + 1 >= len(parts):
                raise ValueError(f"Invalid GitLab MR URL: {url}")
            if parts[mr_idx - 1] == "-" and mr_idx >= 3:
                owner = "/".join(parts[: mr_idx - 2])
                repo = parts[mr_idx - 2]
            else:
                owner = "/".join(parts[: mr_idx - 1])
                repo = parts[mr_idx - 1]
            number = int(parts[mr_idx + 1])
            return "gitlab", owner, repo, number

        raise ValueError(f"Unsupported URL: {url}. Use github.com or gitlab.com URLs.")

    def _get_provider(self, source: str):
        if source == "github":
            return self.github_provider
        elif source == "gitlab":
            return self.gitlab_provider
        raise ValueError(f"Unknown source: {source}")

    def review(self, url: str) -> ReviewResult:
        source, owner, repo, number = self._parse_url(url)
        provider = self._get_provider(source)

        logger.info("Fetching %s PR#%s from %s/%s", source, number, owner, repo)
        req = provider.fetch_review(owner, repo, number)

        return self.review_request(req)

    def review_and_post(self, url: str) -> ReviewResult:
        source, owner, repo, number = self._parse_url(url)
        provider = self._get_provider(source)

        logger.info("Fetching %s PR#%s from %s/%s", source, number, owner, repo)
        req = provider.fetch_review(owner, repo, number)

        result = self.review_request(req)

        provider.post_review(
            owner, repo, number, result.summary, result.verdict, result.comments, commit_sha=req.commit_sha
        )
        return result

    def review_request(self, req: ReviewRequest) -> ReviewResult:
        start = time.time()

        prompt = self._build_review_prompt(req)
        llm_response = self._llm.analyze(prompt)

        result = self._parse_llm_response(llm_response)
        result.review_time_s = time.time() - start
        result.llm_provider = self._llm.provider_name()

        return result

    def list_pending(self, repo_url: str, source: str) -> list[PendingReview]:
        provider = self._get_provider(source)
        owner, repo = self._parse_repo_url(repo_url)
        return provider.list_pending(owner, repo)

    def configure(self, provider: str, token: str) -> bool:
        if provider == "github":
            self.github_provider = GitHubProvider({"token_env": "", "api_url": "https://api.github.com"})
            return True
        elif provider == "gitlab":
            self.gitlab_provider = GitLabProvider({"token_env": "", "api_url": "https://gitlab.com"})
            return True
        return False

    def configure_llm(self, provider: str, model: str) -> bool:
        if provider == "gemini":
            self._llm = GeminiLLM({"model": model, "api_key_env": "GEMINI_API_KEY"})
            return True
        elif provider == "claude":
            self._llm = ClaudeLLM({"model": model, "api_key_env": "ANTHROPIC_API_KEY"})
            return True
        elif provider == "openai":
            self._llm = OpenAILLM(
                {"model": model, "api_key_env": "OPENAI_API_KEY", "base_url": "https://api.openai.com/v1"}
            )
            return True
        elif provider == "deepseek":
            self._llm = OpenAILLM(
                {"model": model, "api_key_env": "DEEPSEEK_API_KEY", "base_url": "https://api.deepseek.com/v1"}
            )
            return True
        return False

    def _parse_repo_url(self, url: str) -> tuple[str, str]:
        parts = url.rstrip("/").split("/")
        return parts[-2], parts[-1]

    def _build_review_prompt(self, req: ReviewRequest) -> str:
        rules = self.config.get("review", {}).get("rules", [])
        rules_str = "\n".join(f"- {r}" for r in rules)

        return f"""Review the following code change.

Title: {req.title}
Description: {req.description or "(no description)"}
Repository: {req.repo_url}

## Diff
```diff
{req.diff[:8000]}
```

## Review Rules
{rules_str}

## Output Format
Return ONLY a JSON object with:
1. "summary": a 2-3 sentence overview of the change
2. "verdict": "approved" | "changes_requested" | "needs_work"
3. "confidence": a float between 0.0 and 1.0
4. "comments": array of objects with:
   - "file": filename
   - "line": line number (int, 0 if inline not possible)
   - "severity": "critical" | "warning" | "info"
   - "message": specific actionable feedback
   - "rule": which rule triggered (security, performance, style, correctness, error_handling)

Example:
{{"summary": "...", "verdict": "changes_requested", "confidence": 0.85,
  "comments": [{{"file": "main.go", "line": 42, "severity": "critical",
                 "message": "Potential SQL injection in query builder",
                 "rule": "security"}}]}}

Return ONLY valid JSON, no other text."""

    def _parse_llm_response(self, response: str) -> ReviewResult:
        import json
        import re

        json_match = re.search(r"\{[\s\S]*\}", response)
        if not json_match:
            return ReviewResult(
                summary="Failed to parse LLM response",
                verdict="needs_work",
                confidence=0.0,
                comments=[],
            )

        try:
            data = json.loads(json_match.group())
            comments = []
            for c in data.get("comments", []):
                comments.append(
                    Comment(
                        file=c.get("file", ""),
                        line=c.get("line", 0),
                        severity=c.get("severity", "info"),
                        message=c.get("message", ""),
                        rule=c.get("rule", ""),
                    )
                )
            return ReviewResult(
                summary=data.get("summary", ""),
                verdict=data.get("verdict", "needs_work"),
                confidence=float(data.get("confidence", 0.0)),
                comments=comments,
            )
        except (json.JSONDecodeError, ValueError, TypeError) as e:
            return ReviewResult(
                summary=f"Parse error: {e}",
                verdict="needs_work",
                confidence=0.0,
                comments=[],
            )
