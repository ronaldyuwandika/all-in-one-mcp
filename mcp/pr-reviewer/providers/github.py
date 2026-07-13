"""GitHub provider: fetch PR details, diff, and post comments."""

import logging
import os

from core.models import ReviewRequest, PendingReview

logger = logging.getLogger("pr-reviewer.github")


class GitHubProvider:
    def __init__(self, config: dict):
        self.api_url = config.get("api_url", "https://api.github.com")
        token = os.environ.get(config.get("token_env", "GITHUB_TOKEN"), "")
        self.token = token

    def _headers(self) -> dict:
        headers = {"Accept": "application/vnd.github.v3.diff"}
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        return headers

    def fetch_review(self, owner: str, repo: str, number: int) -> ReviewRequest:
        import httpx

        base_url = f"{self.api_url}/repos/{owner}/{repo}"

        with httpx.Client() as client:
            pr_resp = client.get(f"{base_url}/pulls/{number}", headers=self._headers())
            pr_resp.raise_for_status()
            pr_data = pr_resp.json()

            diff_resp = client.get(
                f"{base_url}/pulls/{number}",
                headers={**self._headers(), "Accept": "application/vnd.github.v3.diff"},
            )
            diff = diff_resp.text if diff_resp.status_code == 200 else ""

        return ReviewRequest(
            title=pr_data.get("title", ""),
            description=pr_data.get("body", ""),
            diff=diff,
            repo_url=f"https://github.com/{owner}/{repo}",
            source="github",
            pr_number=number,
            pr_url=pr_data.get("html_url", f"https://github.com/{owner}/{repo}/pull/{number}"),
            commit_sha=(pr_data.get("head") or {}).get("sha", ""),
        )

    def post_review(
        self, owner: str, repo: str, number: int, summary: str, verdict: str, comments: list, commit_sha: str = ""
    ) -> bool:
        import httpx

        base_url = f"{self.api_url}/repos/{owner}/{repo}"
        headers = {
            "Accept": "application/vnd.github.v3+json",
            "Content-Type": "application/json",
        }
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"

        success = True

        # Post inline comments on each reviewed file
        for c in comments[:10]:
            if c.file and c.line > 0 and commit_sha:
                inline_body = f"**{c.severity.upper()}** ({c.rule}): {c.message}"
                try:
                    resp = httpx.post(
                        f"{base_url}/pulls/{number}/comments",
                        json={"commit_id": commit_sha, "path": c.file, "body": inline_body, "line": c.line},
                        headers=headers,
                        timeout=30,
                    )
                    if resp.status_code == 422:
                        logger.warning("Inline comment failed (line may not be in diff): %s:%s", c.file, c.line)
                    else:
                        resp.raise_for_status()
                except Exception as e:
                    logger.warning("Inline comment error on %s:%s: %s", c.file, c.line, e)
                    success = False

        # Post summary as an issue comment
        body_lines = [f"## PR Review ({verdict})", "", summary, ""]
        inline_count = sum(1 for c in comments[:10] if c.file and c.line > 0)
        if inline_count < len(comments[:10]):
            body_lines.append("### Additional comments")
            for c in comments[:10]:
                if not c.file or c.line <= 0:
                    body_lines.append(f"- **{c.severity}** — {c.message} ({c.rule})")
        body_lines.append("")
        body_lines.append(f"_{len(comments)} comment(s), reviewed via {verdict}_")
        comment_body = "\n".join(body_lines)

        try:
            resp = httpx.post(
                f"{base_url}/issues/{number}/comments",
                json={"body": comment_body},
                headers=headers,
                timeout=30,
            )
            resp.raise_for_status()
            logger.info("Posted review summary to PR#%s", number)
        except Exception as e:
            logger.warning("Failed to post review summary: %s", e)
            return False

        return success

    def list_pending(self, owner: str, repo: str) -> list[PendingReview]:
        import httpx

        try:
            resp = httpx.get(
                f"{self.api_url}/repos/{owner}/{repo}/pulls?state=open",
                headers=self._headers(),
                timeout=30,
            )
            resp.raise_for_status()
            prs = resp.json()
            return [
                PendingReview(
                    title=pr["title"],
                    number=pr["number"],
                    url=pr["html_url"],
                    author=pr["user"]["login"],
                    created_at=pr["created_at"],
                    source="github",
                )
                for pr in prs
            ]
        except Exception as e:
            logger.warning("Failed to list pending PRs: %s", e)
            return []
