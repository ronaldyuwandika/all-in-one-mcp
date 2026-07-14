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

	if s.EpisodesByDomain != nil {
		fmt.Println(strings.Repeat("─", 50))
		for domain, count := range s.EpisodesByDomain {
			fmt.Printf("  %-30s %s\n", "Episodes by domain: "+domain, strconv.Itoa(count))
		}
	}
	if s.EpisodesByOutcome != nil {
		for outcome, count := range s.EpisodesByOutcome {
			fmt.Printf("  %-30s %s\n", "Episodes by outcome: "+outcome, strconv.Itoa(count))
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
	fmt.Printf("  %-30s %.0f\n", "Avg thinking trace (chars)", s.AvgThinkingTraceChars)
	fmt.Println(strings.Repeat("─", 50))

	if len(s.TopTags) > 0 {
		fmt.Println("  Top Tags:")
		for _, tc := range s.TopTags {
			fmt.Printf("    %-20s %d\n", tc.Tag, tc.Count)
		}
		fmt.Println(strings.Repeat("─", 50))
	}
}
