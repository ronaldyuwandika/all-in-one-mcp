# Reasoning Memory Network — MCP Server

MCP server that captures, stores, searches, and consolidates LLM reasoning traces.

Written in **Go** using `modernc.org/sqlite` (SQLite FTS5) and `mark3labs/mcp-go` (stdio transport).

## Quick Start

```bash
go run .
```

Or via Makefile:

```bash
make run-mcp-reasoning-memory
```

## Tools

| Tool | Description |
|---|---|
| `capture_reasoning_episode` | Store reasoning trace at task end |
| `retrieve_reasoning` | Search episodes by keyword + metadata |
| `inject_reasoning_context` | Get formatted `<reasoning_memory>` XML for prompt injection |
| `consolidate_reasoning` | Cluster, merge, prune, rebuild index |
| `polish_prompt` | Structure raw prompts with domain rules + skill injection |

## Configuration

See `config.yaml` for retrieval thresholds and consolidation settings.

## Testing

```bash
go test -v ./...
```
