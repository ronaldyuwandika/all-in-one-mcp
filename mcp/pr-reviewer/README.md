# PR Reviewer

Automated pull request and merge request review for GitHub and GitLab. The component provides three interfaces over the same review core:

- a standalone MCP stdio server (`server.py`);
- a headless CLI (`python -m core.cli` or the installed `pr-reviewer` console script);
- an HTTP webhook service for automatic reviews.

The MCP server remains backward compatible; the CLI is the machine-oriented entrypoint used by the TypeScript plugin bridge.

## Prerequisites

- Python 3.12+
- A supported LLM provider API key configured through the environment
- A GitHub or GitLab token when reviewing live PR/MR URLs

See [AI_GUIDE.md](./AI_GUIDE.md) for provider, webhook, and deployment configuration.

## Installation

From the repository root:

```bash
make setup
```

This creates `mcp/pr-reviewer/.venv` and installs the package in editable mode, including the `pr-reviewer` console script.

## Standalone MCP Server

```bash
make run-mcp-pr-reviewer
```

Equivalent direct invocation:

```bash
cd mcp/pr-reviewer
.venv/bin/python server.py
```

## Headless CLI

Run the module from `mcp/pr-reviewer`:

```bash
.venv/bin/python -m core.cli --help
```

After installation, the equivalent console entrypoint is:

```bash
.venv/bin/pr-reviewer --help
```

Use `--config PATH` before the subcommand to load a specific YAML configuration. Otherwise the CLI checks `PR_REVIEWER_CONFIG`, then `mcp/pr-reviewer/config.yaml`.

### Review a diff from stdin

`review --stdin` accepts either a raw unified diff or a JSON `ReviewRequest`-style payload. Results are emitted as JSON.

```bash
# Raw diff
printf '%s' "$DIFF" \
  | .venv/bin/python -m core.cli review --stdin \
      --title "Validate authentication change" \
      --description "Check security and error handling"

# Structured JSON input
printf '%s' '{"title":"Validate authentication change","description":"Check security and error handling","repo_url":"https://github.com/example/service","diff":"diff --git a/app.py b/app.py\n..."}' \
  | .venv/bin/python -m core.cli review --stdin
```

When stdin is piped, `--stdin` may be omitted, but the plugin client passes it explicitly. Validation rejects an empty input, invalid title, or invalid/oversized diff before review. Output and errors are masked for recognized secrets.

### Review a live PR or MR

```bash
.venv/bin/python -m core.cli review \
  --url https://github.com/owner/repository/pull/123
```

Add `--post` to post the generated review back to the PR/MR:

```bash
.venv/bin/python -m core.cli review \
  --url https://gitlab.com/owner/repository/-/merge_requests/123 \
  --post
```

`--post` is supported only with `--url`.

### Inspect the audit log

```bash
.venv/bin/python -m core.cli audit --limit 50
```

Clear the audit log explicitly:

```bash
.venv/bin/python -m core.cli audit --clear
```

The limit is clamped to the range `1..500`.

### Validate configuration

```bash
.venv/bin/python -m core.cli validate
```

The command prints a JSON validation report and exits with status `1` when the report is invalid.

## CLI Reference

| Command | Key options | Description |
|---|---|---|
| `review` | `--url`, `--stdin`, `--title`, `--description`, `--repo-url`, `--post` | Review a live PR/MR, raw diff, or JSON request |
| `audit` | `--limit`, `--clear` | Read or clear local audit entries |
| `validate` | — | Validate the loaded reviewer configuration |

Global option:

| Option | Description |
|---|---|
| `--config`, `-c` | Path to a configuration YAML file; place before the subcommand |

## Plugin Integration

`plugins/src/clients/reviewer-client.ts` resolves the CLI in this order:

1. an explicitly configured executable;
2. `mcp/pr-reviewer/.venv/bin/pr-reviewer`;
3. `mcp/pr-reviewer/.venv/bin/python -m core.cli`;
4. `python3 -m core.cli` from a detected project directory;
5. `pr-reviewer` from `PATH`.

The client streams raw diffs to `review --stdin`, applies a 30-second default timeout to review operations, and parses JSON responses into typed results. Audit and validation calls use 5-second timeouts. All subprocess calls pass through the shared plugin circuit breaker, output bounds, and error masking.

## Testing

From the repository root:

```bash
make test-pr-reviewer
```

Or directly:

```bash
cd mcp/pr-reviewer
.venv/bin/python -m pytest tests/ -v
```
