# Benchmark results

Generate current machine-specific results with:

```bash
go test -bench=. -benchmem ./bench/... | tee bench/results/latest.txt
```

This directory intentionally contains no fabricated latency or accuracy claims. CI artifacts are the source of measured results for each commit and platform.
