"""Configuration validation for pr-reviewer."""

import urllib.parse
from typing import Any

ALLOWED_LLM_PROVIDERS = {"gemini", "claude", "openai", "deepseek"}
ALLOWED_REVIEW_RULES = {
    "security",
    "performance",
    "style",
    "correctness",
    "error_handling",
    "maintainability",
    "testing",
    "documentation",
}

REQUIRED_TOP_KEYS = {"llm", "github", "review"}

LLM_REQUIRED_FIELDS = {"model", "api_key_env"}
LLM_OPTIONAL_FIELDS = {"base_url"}


def validate_config(config: dict[str, Any]) -> list[str]:
    """Validate pr-reviewer configuration. Returns a list of warnings (empty = valid)."""
    warnings: list[str] = []

    if not isinstance(config, dict):
        return ["config must be a YAML dictionary"]

    for key in REQUIRED_TOP_KEYS:
        if key not in config:
            warnings.append(f"missing required top-level key: '{key}'")

    warnings.extend(_validate_llm_section(config.get("llm", {})))
    warnings.extend(_validate_github_section(config.get("github", {})))
    warnings.extend(_validate_gitlab_section(config.get("gitlab", {})))
    warnings.extend(_validate_review_section(config.get("review", {})))

    return warnings


def _validate_llm_section(llm: dict) -> list[str]:
    warnings: list[str] = []

    provider = llm.get("provider", "")
    if provider and provider not in ALLOWED_LLM_PROVIDERS:
        warnings.append(f"llm.provider '{provider}' is not one of {sorted(ALLOWED_LLM_PROVIDERS)}")

    for name in ALLOWED_LLM_PROVIDERS:
        section = llm.get(name)
        if section is None:
            continue
        if not isinstance(section, dict):
            warnings.append(f"llm.{name} must be a dictionary")
            continue
        for field in LLM_REQUIRED_FIELDS:
            val = section.get(field, "")
            if not val or not str(val).strip():
                warnings.append(f"llm.{name}.{field} is empty or missing")
        if "base_url" in section and section["base_url"]:
            url = section["base_url"]
            if not _is_valid_url(url):
                warnings.append(f"llm.{name}.base_url is not a valid URL: {url}")

    return warnings


def _validate_github_section(github: dict) -> list[str]:
    warnings: list[str] = []
    if not github:
        return warnings
    if not isinstance(github, dict):
        warnings.append("github section must be a dictionary")
        return warnings
    if not github.get("token_env", "").strip():
        warnings.append("github.token_env is empty")
    api_url = github.get("api_url", "")
    if api_url and not _is_valid_url(api_url):
        warnings.append(f"github.api_url is not a valid URL: {api_url}")
    return warnings


def _validate_gitlab_section(gitlab: dict) -> list[str]:
    warnings: list[str] = []
    if not gitlab:
        return warnings
    if not isinstance(gitlab, dict):
        warnings.append("gitlab section must be a dictionary")
        return warnings
    if not gitlab.get("token_env", "").strip():
        warnings.append("gitlab.token_env is empty")
    api_url = gitlab.get("api_url", "")
    if api_url and not _is_valid_url(api_url):
        warnings.append(f"gitlab.api_url is not a valid URL: {api_url}")
    return warnings


def _validate_review_section(review: dict) -> list[str]:
    warnings: list[str] = []
    if not review:
        warnings.append("review section is empty — no review rules configured")
        return warnings
    if not isinstance(review, dict):
        warnings.append("review section must be a dictionary")
        return warnings

    min_conf = review.get("min_confidence", 0.7)
    try:
        min_conf = float(min_conf)
        if not (0.0 <= min_conf <= 1.0):
            warnings.append(f"review.min_confidence must be between 0.0 and 1.0, got {min_conf}")
    except (TypeError, ValueError):
        warnings.append(f"review.min_confidence must be a number, got {min_conf}")

    max_comments = review.get("max_comments", 10)
    try:
        max_comments = int(max_comments)
        if max_comments < 1:
            warnings.append(f"review.max_comments must be >= 1, got {max_comments}")
    except (TypeError, ValueError):
        warnings.append(f"review.max_comments must be an integer, got {max_comments}")

    rules = review.get("rules", [])
    if not rules:
        warnings.append("review.rules is empty — no review rules configured")
    elif isinstance(rules, list):
        for rule in rules:
            if rule not in ALLOWED_REVIEW_RULES:
                warnings.append(f"review.rules contains unknown rule '{rule}'. Allowed: {sorted(ALLOWED_REVIEW_RULES)}")
    else:
        warnings.append("review.rules must be a list")

    return warnings


def _is_valid_url(url: str) -> bool:
    try:
        parsed = urllib.parse.urlparse(url)
        return parsed.scheme in ("http", "https") and bool(parsed.hostname)
    except Exception:
        return False


def validation_report(config: dict[str, Any]) -> dict:
    """Return a structured validation report."""
    warnings = validate_config(config)
    return {
        "valid": len(warnings) == 0,
        "warning_count": len(warnings),
        "warnings": warnings,
        "config_summary": {
            "llm_provider": config.get("llm", {}).get("provider", "unknown"),
            "github_enabled": bool(config.get("github", {}).get("token_env")),
            "gitlab_enabled": bool(config.get("gitlab", {}).get("token_env")),
            "rules_count": len(config.get("review", {}).get("rules", [])),
        },
    }
