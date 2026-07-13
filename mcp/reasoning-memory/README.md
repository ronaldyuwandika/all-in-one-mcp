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

## Benchmarking & Accuracy Suite

The `reasoning-memory` module includes a comprehensive suite of performance and accuracy/effectiveness benchmarks located in the `bench/` package.

### Targets and Scenarios

| Category | Benchmark | Metric | Target |
|---|---|---|---|
| **Performance** | FTS5 Search | p50/p99 Latency | &lt;5ms / &lt;20ms |
| | Vector Search | p50/p99 Latency | &lt;15ms / &lt;50ms |
| | Episode Insert | Throughput | &gt;5,000 ops/s |
| | Episode Insert + Vector | Throughput | &gt;3,000 ops/s |
| | Auto Consolidation (1k eps) | Duration | &lt;30 seconds |
| | Memory Footprint | RSS / Heap | &lt;200MB RSS |
| **Accuracy** | Retrieval Relevance | nDCG@10 | &gt;0.8000 |
| | Prompt Polish | Task Type Detection | Accuracy Rate |
| | Consolidation Quality | Human Evaluation | &gt;3.5 / 5.0 rating |

### Running the Suite

To run all benchmarks (performance + accuracy) and generate markdown reports:

```bash
make bench-go
```

This target runs:
1. Performance measurements and pipes results to `bench/report/gen_reports.go`.
2. Retrieval relevance, prompt task type detection accuracy, and consolidation pattern generator tests.

All reports are written to the `bench/results/` directory:
- `fts5-search.md` - Lexical search latencies
- `vector-search.md` - Semantic search latencies
- `insert-throughput.md` - Insertion write speed
- `consolidate.md` - Auto-consolidation run duration
- `memory.md` - Process memory usage
- `relevance-ndcg.md` - Retrieval relevance nDCG@10 scores
- `polish-accuracy.md` - Prompt task classifier accuracy breakdown
- `consolidation-quality.md` - Consolidated patterns review sheet for human evaluation

### Comparing Benchmarks

To compare performance of the current branch against a baseline (e.g. `main`), run:

```bash
./bench/benchstat.sh
```

This script will run standard Go `benchstat` to highlight performance deltas and regressions.

