package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: compare <base_metrics_file> <current_metrics_file>")
		os.Exit(1)
	}

	thresholdStr := os.Getenv("BENCH_REGRESSION_THRESHOLD")
	threshold := 20.0
	if thresholdStr != "" {
		if v, err := strconv.ParseFloat(thresholdStr, 64); err == nil {
			threshold = v
		}
	}

	baseMetrics := readMetrics(os.Args[1])
	currMetrics := readMetrics(os.Args[2])

	keys := []string{"FTS5_p99_ms", "Vector_p99_ms"}
	regressed := false

	for _, k := range keys {
		baseVal, baseOk := baseMetrics[k]
		currVal, currOk := currMetrics[k]

		if baseOk && currOk {
			diffPercent := ((currVal - baseVal) / baseVal) * 100.0
			fmt.Printf("%s: Base=%.3fms, Current=%.3fms (Diff=%.2f%%)\n", k, baseVal, currVal, diffPercent)

			if diffPercent > threshold {
				fmt.Printf("⚠ REGRESSION: %s regressed by > %.0f%% (%.2f%%)\n", k, threshold, diffPercent)
				regressed = true
			}
		}
	}

	if regressed {
		fmt.Printf("🔴 Pull request rejected due to performance regression (> %.0f%% on p99 latency).\n", threshold)
		os.Exit(1)
	}

	fmt.Printf("🟢 Performance verification passed (no regressions > %.0f%% on p99 latency).\n", threshold)
}

func readMetrics(path string) map[string]float64 {
	metrics := make(map[string]float64)
	file, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open %s: %v\n", path, err)
		return metrics
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "[METRIC]") {
			parts := strings.Split(strings.TrimPrefix(line, "[METRIC]"), ":")
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				valStr := strings.TrimSpace(parts[1])
				val, err := strconv.ParseFloat(valStr, 64)
				if err == nil {
					metrics[key] = val
				}
			}
		}
	}
	return metrics
}
