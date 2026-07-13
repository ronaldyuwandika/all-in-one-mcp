# All-in-One MCP

Personal monorepo of Model Context Protocol (MCP) servers. Every MCP is self-contained under `mcp/<name>/` with its own README, AI_GUIDE, Makefile, and dependency management.

## Quick Start

```bash
make setup       # install all MCPs
make validate    # check all configs are present
make lint        # ruff linter
make test        # run all tests
```

## MCP Index

| MCP | Description | How to Run |
|---|---|---|
| [reasoning-memory](mcp/reasoning-memory/) | Captures, stores, and retrieves LLM reasoning traces; polishes raw prompts with skill injection | `make run-mcp-reasoning-memory` |
| [credential-vault](mcp/credential-vault/) | Encrypted credential vault with MCP, CLI, and TUI interfaces; audit-logged access | `make run-mcp-credential-vault` |
| [pr-reviewer](mcp/pr-reviewer/) | Automated PR/MR reviewer for GitHub & GitLab with Gemini or Claude analysis | `make run-mcp-pr-reviewer` |

## Architecture

```
.
├── Makefile               # Top-level orchestration
├── mcp/                   # All MCP servers
│   ├── reasoning-memory/  # Reasoning trace storage & prompt engineering
│   ├── credential-vault/  # Encrypted credential management
│   └── pr-reviewer/       # Pull/Merge Request automated reviews
├── docs/                  # Shared documentation
└── scripts/               # Helper scripts
```

Each MCP is an independent Python FastMCP server. Shared conventions:

- **Entry point:** `server.py` — exposes a `FastMCP` instance with `mcp.run(transport='stdio')`
- **Dependencies:** `pyproject.toml` with `[project]` and `[project.optional-dependencies]`
- **Python:** >=3.12
- **Environment:** Each MCP has its own `.venv/` (gitignored)

## Configuration

MCPs are configured via environment variables **only** — no credentials are stored in this repository. See each MCP's `AI_GUIDE.md` for the required variables.

## License

MIT
