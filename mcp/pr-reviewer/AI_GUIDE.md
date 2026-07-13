# PR Reviewer — Operator Guide

## Prerequisites

- **Python 3.12+**
- API tokens for the platforms you want to review:
  - GitHub: [Personal access token](https://github.com/settings/tokens) with `repo` scope
  - GitLab: [Personal access token](https://gitlab.com/-/profile/personal_access_tokens) with `api` scope
- LLM API key (at least one):
  - Gemini: `GEMINI_API_KEY` from [Google AI Studio](https://aistudio.google.com/apikey)
  - Claude: `ANTHROPIC_API_KEY` from [Anthropic Console](https://console.anthropic.com/)
  - OpenAI: `OPENAI_API_KEY` from [OpenAI Platform](https://platform.openai.com/)
  - DeepSeek: `DEEPSEEK_API_KEY` from [DeepSeek Platform](https://platform.deepseek.com/)

## Quick Start

```bash
# Clone and install
git clone https://github.com/ronaldyuwandika/all-in-one-mcp.git
cd all-in-one-mcp
make setup

# Set environment variables
export GITHUB_TOKEN=ghp_xxxxxxxxxxxx
export DEEPSEEK_API_KEY=sk-xxxxxxxxxxxx

# Run as MCP server
cd mcp/pr-reviewer
.venv/bin/python server.py
```

## Configuration

Edit `mcp/pr-reviewer/config.yaml`:

```yaml
llm:
  provider: deepseek                    # gemini | claude | openai | deepseek
  gemini:
    model: gemini-2.0-flash
    api_key_env: GEMINI_API_KEY
  claude:
    model: claude-sonnet-4-20260514
    api_key_env: ANTHROPIC_API_KEY
  openai:
    model: gpt-4o
    api_key_env: OPENAI_API_KEY
    base_url: https://api.openai.com/v1
  deepseek:
    model: deepseek-v4-pro
    api_key_env: DEEPSEEK_API_KEY
    base_url: https://api.deepseek.com/v1

github:
  api_url: https://api.github.com
  token_env: GITHUB_TOKEN

gitlab:
  api_url: https://gitlab.com
  token_env: GITLAB_TOKEN

review:
  min_confidence: 0.7
  max_comments: 10
  auto_review_on_webhook: true
  auto_post_comment: true
  rules:
    - security
    - performance
    - style
    - correctness
    - error_handling
```

### Review Rules Reference

| Rule | Scope |
|------|-------|
| `security` | SQL injection, XSS, hardcoded secrets, unsafe dependencies |
| `performance` | N+1 queries, memory leaks, blocking I/O |
| `style` | Naming conventions, code organization, readability |
| `correctness` | Logic errors, edge cases, missing validation |
| `error_handling` | Proper exception handling, error propagation |
| `maintainability` | DRY, complexity, documentation |
| `testing` | Test coverage, test quality |
| `documentation` | Docstrings, comments, API docs |

## Environment Variables

| Variable | Purpose | Required |
|----------|---------|----------|
| `GITHUB_TOKEN` | GitHub API authentication | For GitHub reviews |
| `GITLAB_TOKEN` | GitLab API authentication | For GitLab reviews |
| `GEMINI_API_KEY` | Google Gemini API key | If using Gemini |
| `ANTHROPIC_API_KEY` | Anthropic Claude API key | If using Claude |
| `OPENAI_API_KEY` | OpenAI API key | If using OpenAI |
| `DEEPSEEK_API_KEY` | DeepSeek API key | If using DeepSeek |
| `GITHUB_WEBHOOK_SECRET` | HMAC secret for GitHub webhooks | For webhook server |
| `GITLAB_WEBHOOK_SECRET` | Token for GitLab webhooks | For webhook server |
| `PR_REVIEWER_PORT` | Webhook server port (default: 8080) | For webhook server |

## Deployment Options

### Option 1: MCP stdio (mcp.json / opencode.json)

```json
{
  "mcpServers": {
    "pr-reviewer": {
      "command": "python3",
      "args": ["mcp/pr-reviewer/server.py"],
      "cwd": "/path/to/all-in-one-mcp",
      "env": {
        "GITHUB_TOKEN": "${GITHUB_TOKEN}",
        "DEEPSEEK_API_KEY": "${DEEPSEEK_API_KEY}"
      }
    }
  }
}
```

### Option 2: systemd Service

Create `/etc/systemd/system/pr-reviewer-webhook.service`:

```ini
[Unit]
Description=PR Reviewer Webhook Server
After=network.target

[Service]
Type=simple
User=pr-reviewer
WorkingDirectory=/opt/all-in-one-mcp
ExecStart=/opt/all-in-one-mcp/mcp/pr-reviewer/.venv/bin/python webhook/server.py
Environment=PR_REVIEWER_PORT=8080
EnvironmentFile=/opt/all-in-one-mcp/.env
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pr-reviewer-webhook
```

### Option 3: Docker Compose

```yaml
version: "3.8"
services:
  pr-reviewer:
    build:
      context: .
      dockerfile: Dockerfile.pr-reviewer
    ports:
      - "8080:8080"
    environment:
      - GITHUB_TOKEN=${GITHUB_TOKEN}
      - GITLAB_TOKEN=${GITLAB_TOKEN}
      - DEEPSEEK_API_KEY=${DEEPSEEK_API_KEY}
      - GITHUB_WEBHOOK_SECRET=${GITHUB_WEBHOOK_SECRET}
      - PR_REVIEWER_PORT=8080
    volumes:
      - ./mcp/pr-reviewer/config.yaml:/app/config.yaml:ro
      - pr-reviewer-data:/root/.pr-reviewer
    restart: unless-stopped

  credential-vault:
    build:
      context: .
      dockerfile: Dockerfile.credential-vault
    environment:
      - VAULT_KEYCHAIN_SERVICE=com.credential-vault
    volumes:
      - credential-vault-data:/root/.credential-vault
    restart: unless-stopped

volumes:
  pr-reviewer-data:
  credential-vault-data:
```

## Config Validation

Validate your configuration at any time:

```bash
cd mcp/pr-reviewer && .venv/bin/python -c "
from core.validator import validation_report
import yaml, json
config = yaml.safe_load(open('config.yaml'))
print(json.dumps(validation_report(config), indent=2))
"
```

Output example:

```json
{
  "valid": true,
  "warning_count": 0,
  "warnings": [],
  "config_summary": {
    "llm_provider": "deepseek",
    "github_enabled": true,
    "gitlab_enabled": false,
    "rules_count": 5
  }
}
```

## Audit Log

All operations are logged to `~/.pr-reviewer/audit.jsonl`. Each entry contains:

```json
{
  "id": "uuid",
  "timestamp": "2026-07-13T12:00:00+00:00",
  "operation": "review_pr",
  "url": "https://github.com/owner/repo/pull/42",
  "verdict": "changes_requested",
  "duration_ms": 2345,
  "comments_count": 3
}
```

View recent entries with the `get_audit_log` MCP tool, or read the file directly:

```bash
tail -20 ~/.pr-reviewer/audit.jsonl | jq .
```

## Webhook Setup

### GitHub

1. Go to **Repo Settings → Webhooks → Add webhook**
2. Payload URL: `https://your-server:8080/webhook/github`
3. Content type: `application/json`
4. Secret: same as `GITHUB_WEBHOOK_SECRET` env var
5. Events: **Pull requests**

### GitLab

1. Go to **Repo Settings → Webhooks**
2. URL: `https://your-server:8080/webhook/gitlab`
3. Secret token: same as `GITLAB_WEBHOOK_SECRET` env var
4. Triggers: **Merge request events**
