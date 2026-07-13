# PR Reviewer — Security Model

## Overview

PR Reviewer is an MCP server that fetches pull request diffs, sends them to an LLM for analysis, and returns structured review comments. This document describes how secrets, input, and communications are protected.

## Secrets Handling

### In Transit

- **API tokens** (`GITHUB_TOKEN`, `GITLAB_TOKEN`, LLM API keys) are read from environment variables only. They are never written to disk, logs, or configuration files.
- All token values are resolved at startup. The `config.yaml` file references environment variable names (e.g., `api_key_env: GITHUB_TOKEN`), not the values themselves.

### In Responses

- All tool outputs pass through `mask_text()` which redacts 20+ credential patterns:
  - AWS access keys, secret keys, session tokens
  - GitHub/GitLab personal access tokens
  - SSH private keys
  - Sentry DSNs and tokens
  - Datadog/New Relic API keys
  - Stripe live keys
  - Cloudflare tokens
  - Bearer/JWT tokens
  - Database connection strings with embedded credentials
  - Generic `TOKEN=`, `SECRET=`, `PASSWORD=`, `API_KEY=` patterns
- Long base64 strings (>40 chars) are also redacted as a catch-all

### What Leaves the Server

| Data | Destinations |
|------|-------------|
| PR title + description | LLM provider API |
| PR diff (up to 8KB) | LLM provider API |
| Review result (JSON) | MCP client (e.g., opencode) |
| Inline review comments | GitHub/GitLab API (when using `review_and_post`) |

**Important**: The PR diff is sent to the configured LLM provider (Gemini, Claude, OpenAI, or DeepSeek). If the diff contains secrets embedded in code, they will be transmitted to the LLM API. Redaction happens at the response level, not the diff level, because the LLM needs to review the actual code.

### Recommendations

1. **Never commit secrets to source code** — use `.env` files, secret management services, or CI/CD variables
2. **Enable webhook HMAC validation** — set `GITHUB_WEBHOOK_SECRET` and `GITLAB_WEBHOOK_SECRET` environment variables
3. **Rotate API tokens regularly** — use fine-grained tokens with minimal scopes:
   - GitHub: `repo` (read-only on public repos, read+write on private)
   - GitLab: `api` (read-only on public repos)

## Input Validation

All tool inputs are validated and sanitized:

| Input | Validation |
|-------|-----------|
| `url` | HTTPS scheme, allowlisted hosts (`github.com`, `gitlab.com` + self-hosted GitLab), path must contain `/pull/` or `/merge_requests/`, max 400 chars |
| `title` | Required, max 200 chars, stripped |
| `diff` | Required, max 50KB, stripped |
| `provider` | Enum: `github` or `gitlab` |
| `llm provider` | Enum: `gemini`, `claude`, `openai`, `deepseek` |
| `model` | Required, max 100 chars |

Invalid inputs return structured error messages — no raw exception traces are exposed.

## Webhook Security

### GitHub Webhooks

- HMAC-SHA256 signature verification (`x-hub-signature-256` header)
- Enabled when `GITHUB_WEBHOOK_SECRET` environment variable is set
- Without the secret, all requests are accepted (permissive mode)
- Non-PR events (`push`, `issues`) are ignored with `{"status": "ignored"}`
- Closed/labeled/assigned PR actions are ignored — only `opened`, `synchronize`, `reopened` trigger review

### GitLab Webhooks

- Static token verification (`x-gitlab-token` header)
- Enabled when `GITLAB_WEBHOOK_SECRET` environment variable is set
- Non-MR events are ignored
- Non-open/reopen/update actions are ignored

## Audit Logging

All operations are logged to `~/.pr-reviewer/audit.jsonl`:

```
{"id":"uuid","timestamp":"...","operation":"review_pr","url":"...","verdict":"...","duration_ms":1234}
```

| Field | Description |
|-------|-------------|
| `id` | Unique entry identifier (UUID4) |
| `timestamp` | ISO 8601 UTC timestamp |
| `operation` | `review_pr`, `review_and_post`, `review_draft`, `list_pending`, `configure_provider`, `configure_llm` |
| `source` | `manual` (MCP tool) or `webhook_github` / `webhook_gitlab` |
| `url` | Reviewed PR/MR URL (redacted if contains secrets) |
| `verdict` | `approved`, `changes_requested`, or `needs_work` |
| `error` | Error message (only on failure) |
| `duration_ms` | Operation duration in milliseconds |
| `comments_count` | Number of review comments generated |

Audit log entries can be retrieved via the `get_audit_log` MCP tool or read directly from `~/.pr-reviewer/audit.jsonl`. The file is set to `0600` permissions.

## Configuration Validation

The `validate_configuration` MCP tool runs at startup and on demand, checking:
- Required top-level sections (`llm`, `github`, `review`) exist
- LLM provider is one of the known backends
- Each LLM section has `model` and `api_key_env` fields
- API URLs are valid
- `min_confidence` is between 0.0 and 1.0
- Review rules are from the known set

## Incident Response

If a security issue is discovered:

1. **Immediate**: Revoke all API tokens exposed through the MCP server
2. **Audit**: Check `~/.pr-reviewer/audit.jsonl` for the scope of affected operations
3. **Rotate**: Regenerate GitHub/GitLab/LLM API tokens
4. **Report**: File a [GitHub issue](https://github.com/ronaldyuwandika/all-in-one-mcp/issues/new)

## Threat Model

| Threat | Mitigation |
|--------|-----------|
| Secret in diff sent to LLM | Response-level regex redaction; operator should prevent secrets in code |
| Webhook spoofing | HMAC signature / token verification |
| Excessive diff size | 50KB limit on draft diffs; GitHub/GitLab APIs limit response sizes |
| Token leakage in logs | Tokens read from env vars only; response-level redaction; audit log does not store tokens |
| Malicious URL | Hostname allowlisting + URL structure validation |
| Config misconfiguration | Startup validation with warnings; `validate_configuration` tool |
