"""Secret redaction and input validation utilities for pr-reviewer."""

import re
import urllib.parse
from typing import Optional

MAX_DIFF_SIZE = 50_000
MAX_TITLE_LENGTH = 200
MAX_URL_LENGTH = 400
ALLOWED_PROVIDERS = {"github", "gitlab"}
ALLOWED_LLM_PROVIDERS = {"gemini", "claude", "openai", "deepseek"}
ALLOWED_SEVERITIES = {"critical", "warning", "info"}
ALLOWED_SOURCES = {"github", "gitlab", "draft"}


CRED_PATTERNS = [
    (
        re.compile(r"(AKIA|ASIA|AROA|AIDA|AIPA|ANPA|ANVA|APKA)[0-9A-Z]{16}"),
        "AWS_ACCESS_KEY_ID",
    ),
    (
        re.compile(r'(?i)(?:aws[_-])?secret[_-]access[_-]key[= ]+["\']?([^"\' \n]{8})([^"\' \n]+)'),
        lambda m: f"secret_access_key={m.group(1)}{'*' * min(len(m.group(2)), 32)}",
    ),
    (
        re.compile(r'(?i)(?:aws[_-])?session[_-]token[= ]+["\']?[^"\' \n]{10}([^"\' \n]+)'),
        "AWS_SESSION_TOKEN",
    ),
    (
        re.compile(
            r"-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----.*?-----END .*?PRIVATE KEY-----",
            re.DOTALL,
        ),
        "SSH_PRIVATE_KEY",
    ),
    (re.compile(r"gh[pousr]_[A-Za-z0-9_]{10,}"), "GITHUB_TOKEN"),
    (re.compile(r"github_pat_[A-Za-z0-9_]{10,}"), "GITHUB_PAT"),
    (re.compile(r"glpat-[A-Za-z0-9_\-]{10,}"), "GITLAB_TOKEN"),
    (re.compile(r"glsa_[A-Za-z0-9_]{10,}(?:_[A-Fa-f0-9]{8,})?"), "GRAFANA_TOKEN"),
    (
        re.compile(r"https://[a-f0-9]{8,40}@[^/]+/\d+"),
        "SENTRY_DSN",
    ),
    (re.compile(r"sntrys_[A-Za-z0-9_]{10,}"), "SENTRY_TOKEN"),
    (
        re.compile(r'(?i)datadog_api_key[= ]+["\']?[A-Za-z0-9]{20,}'),
        "DATADOG_API_KEY",
    ),
    (
        re.compile(r'(?i)new_relic_license_key[= ]+["\']?[A-Za-z0-9]{20,}'),
        "NEW_RELIC_LICENSE_KEY",
    ),
    (re.compile(r"(?i)sk_live_[A-Za-z0-9]{10,}"), "STRIPE_SECRET_KEY"),
    (re.compile(r"(?i)rk_live_[A-Za-z0-9]{10,}"), "STRIPE_SECRET_KEY"),
    (re.compile(r"(?i)whsec_[A-Za-z0-9]{10,}"), "STRIPE_WEBHOOK_SECRET"),
    (re.compile(r"cfat_[A-Za-z0-9_]{10,}"), "CLOUDFLARE_TOKEN"),
    (
        re.compile(r'(?i)(api[_-]?key|apikey|api_secret|secret_key)[=: ]+["\']?([^"\' \n]{8})([^"\' \n]+)'),
        lambda m: f"{m.group(1)}={m.group(2)}{'*' * min(len(m.group(3)), 32)}",
    ),
    (
        re.compile(r'(?i)(bearer|jwt)[=: ]+["\']?[^"\' \n]{10}([^"\' \n]{10,})'),
        lambda m: f"{m.group(1)}={'*' * 40}",
    ),
    (
        re.compile(r'(?i)(token|secret)[=: ]+["\']?([^"\' \n]{10})([^"\' \n]+)'),
        lambda m: f"{m.group(1)}={m.group(2)}{'*' * min(len(m.group(3)), 32)}",
    ),
    (
        re.compile(r'(?i)(password|passwd|pass|pwd)[=: ]+["\']?([^"\' \n]{3})([^"\' \n]+)'),
        lambda m: f"{m.group(1)}={m.group(2)}{'*' * min(len(m.group(3)), 32)}",
    ),
    (
        re.compile(r"(postgresql|mysql|redis|mongodb|rediss|amqp|rabbitmq)://[^@]+@"),
        lambda m: m.group(1) + "://__REDACTED__@",
    ),
    (
        re.compile(r'(DATABASE_URL|REDIS_URL|MONGO_URI|MONGODB_URI)[=: ]+["\']?[^"\' \n]{8}([^"\' \n]+)'),
        lambda m: f"{m.group(1)}={'*' * min(len(m.group(2)), 40)}",
    ),
    (
        re.compile(r'(?i)Authorization[=: ]+["\']?(Bearer|Basic|Bearer)\s+[^"\' \n]+'),
        lambda m: re.sub(r"\s+\S{10,}", " [REDACTED]", m.group(0)),
    ),
]


def mask_text(text: str) -> str:
    """Redact known credential patterns from text."""
    result = text
    for pattern, label in CRED_PATTERNS:
        if callable(label):
            result = pattern.sub(label, result)
        else:
            result = pattern.sub(f"[REDACTED:{label}]", result)
    result = re.sub(r"[A-Za-z0-9+/=]{40,}", "[REDACTED:LONG_KEY]", result)
    return result


def validate_url(url: str) -> Optional[str]:
    """Validate a PR/MR URL. Returns error message or None if valid."""
    if not url or not url.strip():
        return "url is required"

    url = url.strip()
    if len(url) > MAX_URL_LENGTH:
        return f"url exceeds {MAX_URL_LENGTH} characters"

    try:
        parsed = urllib.parse.urlparse(url)
    except Exception:
        return "invalid URL format"

    if parsed.scheme not in ("https", "http"):
        return "URL must use https:// scheme"

    hostname = (parsed.hostname or "").lower()
    if not hostname:
        return "URL has no hostname"

    allowed_hosts = {
        "github.com",
        "www.github.com",
        "gitlab.com",
    }
    if hostname not in allowed_hosts and not hostname.endswith(".gitlab.com"):
        return f"unsupported host: {hostname}"

    path = parsed.path
    if "/pull/" in path or "/merge_requests/" in path:
        return None

    return f"not a recognized PR/MR URL: {hostname}{path}"


def validate_title(title: str) -> Optional[str]:
    if not title or not title.strip():
        return "title is required"
    if len(title) > MAX_TITLE_LENGTH:
        return f"title exceeds {MAX_TITLE_LENGTH} characters"
    return None


def validate_diff(diff: str) -> Optional[str]:
    if not diff or not diff.strip():
        return "diff is required"
    if len(diff) > MAX_DIFF_SIZE:
        return f"diff exceeds {MAX_DIFF_SIZE} bytes"
    return None


def validate_provider(provider: str) -> Optional[str]:
    if provider not in ALLOWED_PROVIDERS:
        return f"provider must be one of: {', '.join(sorted(ALLOWED_PROVIDERS))}"
    return None


def validate_llm_provider(provider: str) -> Optional[str]:
    if provider not in ALLOWED_LLM_PROVIDERS:
        return f"llm provider must be one of: {', '.join(sorted(ALLOWED_LLM_PROVIDERS))}"
    return None


def validate_severity(severity: str) -> Optional[str]:
    if severity not in ALLOWED_SEVERITIES:
        return f"severity must be one of: {', '.join(sorted(ALLOWED_SEVERITIES))}"
    return None


def validate_model_name(model: str) -> Optional[str]:
    if not model or not model.strip():
        return "model name is required"
    if len(model) > 100:
        return "model name exceeds 100 characters"
    return None


def sanitize_string(value: str) -> str:
    return value.strip()
