"""GitLab provider: fetch MR details, diff, and post comments."""

import logging
import os

from core.models import ReviewRequest, PendingReview

logger = logging.getLogger("pr-reviewer.gitlab")


class GitLabProvider:
    def __init__(self, config: dict):
        self.api_url = config.get("api_url", "https://gitlab.com")
        token = os.environ.get(config.get("token_env", "GITLAB_TOKEN"), "")
        self.token = token

    def _headers(self) -> dict:
        headers = {}
        if self.token:
            headers["PRIVATE-TOKEN"] = self.token
        return headers

    def _project_path(self, owner: str, repo: str) -> str:
        return f"{owner}/{repo}".replace("/", "%2F")

    def fetch_review(self, owner: str, repo: str, number: int) -> ReviewRequest:
        import httpx

        project = self._project_path(owner, repo)
        base_url = f"{self.api_url}/api/v4/projects/{project}/merge_requests/{number}"

        with httpx.Client() as client:
            mr_resp = client.get(base_url, headers=self._headers())
            mr_resp.raise_for_status()
            mr_data = mr_resp.json()

            diff_resp = client.get(f"{base_url}/diffs", headers=self._headers())
            diffs = diff_resp.json() if diff_resp.status_code == 200 else []

            diff_lines = []
            for d in diffs[:50]:
                diff_lines.append(f"--- a/{d.get('old_path', '')}")
                diff_lines.append(f"+++ b/{d.get('new_path', '')}")
                diff_lines.append(d.get("diff", ""))
            diff = "\n".join(diff_lines)

        return ReviewRequest(
            title=mr_data.get("title", ""),
            description=mr_data.get("description", ""),
            diff=diff,
            repo_url=f"https://gitlab.com/{owner}/{repo}",
            source="gitlab",
            pr_number=number,
            pr_url=mr_data.get("web_url", f"https://gitlab.com/{owner}/{repo}/-/merge_requests/{number}"),
        )

    def post_review(
        self, owner: str, repo: str, number: int, summary: str, verdict: str, comments: list, commit_sha: str = ""
    ) -> bool:
        import httpx

        project = self._project_path(owner, repo)
        base_url = f"{self.api_url}/api/v4/projects/{project}"

        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["PRIVATE-TOKEN"] = self.token

        success = True

        # Post inline comments on each reviewed file using MR discussions
        for c in comments[:10]:
            if c.file and c.line > 0:
                position = {
                    "position_type": "text",
                    "new_path": c.file,
                    "new_line": c.line,
                    "base_sha": commit_sha or "0" * 40,
                    "start_sha": commit_sha or "0" * 40,
                    "head_sha": commit_sha or "0" * 40,
                }
                inline_body = f"**{c.severity.upper()}** ({c.rule}): {c.message}"
                try:
                    resp = httpx.post(
                        f"{base_url}/merge_requests/{number}/discussions",
                        json={"body": inline_body, "position": position},
                        headers=headers,
                        timeout=30,
                    )
                    if resp.status_code >= 400:
                        logger.warning("Inline discussion failed on %s:%s: %s", c.file, c.line, resp.text[:200])
                except Exception as e:
                    logger.warning("Inline discussion error on %s:%s: %s", c.file, c.line, e)
                    success = False

        # Post summary as a regular note
        body_lines = [f"## MR Review ({verdict})", "", summary, ""]
        inline_count = sum(1 for c in comments[:10] if c.file and c.line > 0)
        if inline_count < len(comments[:10]):
            body_lines.append("### Additional comments")
            for c in comments[:10]:
                if not c.file or c.line <= 0:
                    body_lines.append(f"- **{c.severity}** — {c.message} ({c.rule})")
        body_lines.append("")
        body_lines.append(f"_{len(comments)} comment(s), reviewed via {verdict}_")
        note_body = "\n".join(body_lines)

        try:
            resp = httpx.post(
                f"{base_url}/merge_requests/{number}/notes",
                json={"body": note_body},
                headers=headers,
                timeout=30,
            )
            resp.raise_for_status()
            logger.info("Posted review summary to MR#%s", number)
        except Exception as e:
            logger.warning("Failed to post MR note: %s", e)
            return False

        return success

    def list_pending(self, owner: str, repo: str) -> list[PendingReview]:
        import httpx

        project = self._project_path(owner, repo)
        try:
            resp = httpx.get(
                f"{self.api_url}/api/v4/projects/{project}/merge_requests?state=opened",
                headers=self._headers(),
                timeout=30,
            )
            resp.raise_for_status()
            mrs = resp.json()
            return [
                PendingReview(
                    title=mr["title"],
                    number=mr["iid"],
                    url=mr["web_url"],
                    author=mr["author"]["name"],
                    created_at=mr["created_at"],
                    source="gitlab",
                )
                for mr in mrs
            ]
        except Exception as e:
            logger.warning("Failed to list pending MRs: %s", e)
            return []
