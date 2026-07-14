package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
	"github.com/spf13/cobra"
)

var statsFormat string

func NewStatsCmd(es *store.EpisodeStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show reasoning-memory statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStats(es)
		},
	}
	cmd.Flags().StringVar(&statsFormat, "format", "json", "Output format: json (default) or table")
	return cmd
}

func runStats(es *store.EpisodeStore) error {
	epTotal, _ := es.EpisodeCount()
	patTotal, _ := es.PatternCount()
	byDomain, _ := es.EpisodesByDomain()
	byOutcome, _ := es.EpisodesByOutcome()
	topTags, _ := es.TopTags(10)
	avgProb, avgTrace, _ := es.AvgEpisodeLengths()
	dbSize, _ := es.DBSizeMB()
	ftsSize, _ := es.FTSSizeMB()
	lastConsolidation, _ := es.LastConsolidationTS()
	summary, _ := es.SummaryStats()
	epByDay, _ := es.EpisodesByDay(7)

	var vecSize float64
	var vecCount int
	vs := es.VectorStore()
	if vs != nil {
		vecCount = vs.Count()
		vecSize = 0
	}

	result := models.StatsResult{
		EpisodesTotal:         epTotal,
		PatternsTotal:         patTotal,
		EpisodesByDomain:      byDomain,
		EpisodesByOutcome:     byOutcome,
		TopTags:               topTags,
		VectorIndexSizeMB:     vecSize,
		VectorCount:           vecCount,
		FTSSizeMB:             ftsSize,
		DBSizeMB:              dbSize,
		ConsolidationsTotal:   patTotal,
		AvgEpisodeLenChars:    avgProb,
		AvgThinkingTraceChars: avgTrace,
	}
	if summary != nil {
		result.SuccessRate = summary.SuccessRate
		result.ConsolidationRatio = summary.ConsolidationRatio
		result.TopDomain = summary.TopDomain
		result.AvgDurationSec = summary.AvgDurationSec
	}
	if epByDay != nil {
		result.EpisodesByDay = epByDay
	}

	if lastConsolidation != nil {
		ts := lastConsolidation.Format("2006-01-02T15:04:05Z")
		result.LastConsolidationTS = &ts
	}

	if statsFormat == "table" {
		renderStatsTable(result)
	} else {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	}
	return nil
}

func renderStatsTable(s models.StatsResult) {
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("  %-30s %s\n", "Metric", "Value")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("  %-30s %d\n", "Episodes (total)", s.EpisodesTotal)
	fmt.Printf("  %-30s %d\n", "Patterns (total)", s.PatternsTotal)
	fmt.Printf("  %-30s %s\n", "Top domain", s.TopDomain)
	fmt.Printf("  %-30s %.1f%%\n", "Success rate", s.SuccessRate*100)
	fmt.Printf("  %-30s %.1f%%\n", "Consolidation ratio", s.ConsolidationRatio*100)
	fmt.Printf("  %-30s %.1f s\n", "Avg duration", s.AvgDurationSec)

	if s.EpisodesByDomain != nil {
		fmt.Println(strings.Repeat("─", 50))
		for domain, count := range s.EpisodesByDomain {
			fmt.Printf("  %-30s %s\n", "Domain: "+domain, strconv.Itoa(count))
		}
	}
	if s.EpisodesByOutcome != nil {
		for outcome, count := range s.EpisodesByOutcome {
			fmt.Printf("  %-30s %s\n", "Outcome: "+outcome, strconv.Itoa(count))
		}
	}
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("  %-30s %.2f MB\n", "DB size", s.DBSizeMB)
	fmt.Printf("  %-30s %.2f MB\n", "FTS5 index size", s.FTSSizeMB)
	if s.VectorCount > 0 {
		fmt.Printf("  %-30s %d\n", "Vector count", s.VectorCount)
		fmt.Printf("  %-30s %.2f MB\n", "Vector index size", s.VectorIndexSizeMB)
	}
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("  %-30s %d\n", "Consolidations (total)", s.ConsolidationsTotal)
	if s.LastConsolidationTS != nil {
		fmt.Printf("  %-30s %s\n", "Last consolidation", *s.LastConsolidationTS)
	}
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("  %-30s %.0f\n", "Avg episode length (chars)", s.AvgEpisodeLenChars)
	fmt.Printf("  %-30s %.0f\n", "Avg trace length (chars)", s.AvgThinkingTraceChars)

	if len(s.EpisodesByDay) > 0 {
		fmt.Println(strings.Repeat("─", 50))
		fmt.Println("  Last 7 Days:")
		for _, d := range s.EpisodesByDay {
			fmt.Printf("    %s: %d eps, %d ok, %.0f s avg\n", d.Date, d.Count, d.Successes, d.AvgDuration)
		}
	}
	fmt.Println(strings.Repeat("─", 50))

	if len(s.TopTags) > 0 {
		fmt.Println("  Top Tags:")
		for _, tc := range s.TopTags {
			fmt.Printf("    %-20s %d\n", tc.Tag, tc.Count)
		}
		fmt.Println(strings.Repeat("─", 50))
	}
}
