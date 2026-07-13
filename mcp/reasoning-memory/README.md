# Reasoning Memory Network

MCP server that captures, stores, and retrieves LLM reasoning traces. Augments lite models with past reasoning context and polishes raw prompts with domain-specific architectural rules and skill injection.

## Usage

```bash
# Server mode (stdio transport)
python server.py

# Seed episodes from installed skills
python seed.py --all

# Benchmark polish_prompt token overhead
python benchmark.py
```

## MCP Tools

| Tool | Description |
|---|---|
| `capture_reasoning_episode` | Store a completed reasoning trace |
| `inject_reasoning_context` | Retrieve past episodes for context injection |
| `retrieve_reasoning` | Search episodes by keyword, domain, tags |
| `consolidate_reasoning` | Cluster, merge, prune, and re-index episodes |
| `polish_prompt` | Structure raw prompts with domain rules + skills |

## Dependencies

- Python >=3.12
- `mcp[cli]`, `pyyaml`
