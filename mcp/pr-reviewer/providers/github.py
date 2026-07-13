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
        )

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
