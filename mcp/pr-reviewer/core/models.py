from dataclasses import dataclass, field, asdict
from typing import Optional


@dataclass
class Comment:
    file: str
    line: int
    severity: str  # critical | warning | info
    message: str
    rule: str = ""  # security | performance | style | correctness | error_handling

    def to_dict(self) -> dict:
        return {
            "file": self.file,
            "line": self.line,
            "severity": self.severity,
            "message": self.message,
            "rule": self.rule,
        }


@dataclass
class ReviewRequest:
    title: str
    description: str
    diff: str
    repo_url: str = ""
    source: str = "draft"  # draft | github | gitlab
    pr_number: Optional[int] = None
    pr_url: str = ""
    commit_sha: str = ""

    def to_dict(self) -> dict:
        return {
            "title": self.title,
            "description": self.description[:200] if self.description else "",
            "repo_url": self.repo_url,
            "source": self.source,
            "pr_number": self.pr_number,
            "pr_url": self.pr_url,
        }


@dataclass
class ReviewResult:
    summary: str
    verdict: str  # approved | changes_requested | needs_work
    confidence: float
    comments: list[Comment] = field(default_factory=list)
    review_time_s: float = 0.0
    llm_provider: str = ""

    def to_dict(self) -> dict:
        return {
            "summary": self.summary,
            "verdict": self.verdict,
            "confidence": self.confidence,
            "comments": [c.to_dict() for c in self.comments],
            "review_time_s": round(self.review_time_s, 1),
            "llm_provider": self.llm_provider,
        }


@dataclass
class PendingReview:
    title: str
    number: int
    url: str
    author: str
    created_at: str
    source: str  # github | gitlab

    def to_dict(self) -> dict:
        return asdict(self)
