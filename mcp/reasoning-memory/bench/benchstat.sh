#!/bin/bash
set -e

# Make sure we are in the parent module directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

# Check if benchstat is installed
if ! command -v benchstat &> /dev/null; then
    echo "Installing benchstat..."
    go install golang.org/x/perf/cmd/benchstat@latest
fi

# Run benchmarks for comparison (using fewer counts in normal CLI run for speed, but count=5 for stable stat)
echo "Running benchmarks on current branch..."
go test -bench="Search|Insert" -benchmem -count=5 ./bench/... > "$SCRIPT_DIR/bench_current.txt"

if [ -f "$SCRIPT_DIR/bench_base.txt" ]; then
    echo ""
    echo "=========================================================="
    echo " Comparing Current Benchmarks with Baseline (bench_base.txt)"
    echo "=========================================================="
    benchstat "$SCRIPT_DIR/bench_base.txt" "$SCRIPT_DIR/bench_current.txt"
else
    echo ""
    echo "No baseline (bench_base.txt) found. Storing current run as baseline."
    cp "$SCRIPT_DIR/bench_current.txt" "$SCRIPT_DIR/bench_base.txt"
    benchstat "$SCRIPT_DIR/bench_base.txt"
fi
