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

var (
	statsFormat string
	byLabel     string
)

func NewStatsCmd(es *store.EpisodeStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show reasoning-memory statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			if byLabel != "" {
				parts := strings.SplitN(byLabel, "=", 2)
				key := parts[0]
				value := ""
				if len(parts) == 2 {
					value = parts[1]
				}
				ids, err := es.EpisodesByLabel(key, value)
				if err != nil {
					return err
				}
				fmt.Printf("Episodes with label %q: %d\n", byLabel, len(ids))
				for _, id := range ids {
					fmt.Printf("  %s\n", id)
				}
				return nil
			}
			return runStats(es)
		},
	}
	cmd.Flags().StringVar(&statsFormat, "format", "json", "Output format: json (default) or table")
	cmd.Flags().StringVar(&byLabel, "by-label", "", "List episodes matching a label (e.g. language=go)")
	return cmd
}

func runStats(es *store.EpisodeStore) error {
	epTotal, _ := es.EpisodeCount()
	patTotal, _ := es.PatternCount()
	byDomain, _ := es.EpisodesByDomain()
	byOutcome, _ := es.EpisodesByOutcome()
	byRepo, _ := es.EpisodesByRepo()
	topTags, _ := es.TopTags(10)
	avgProb, avgTrace, _ := es.AvgEpisodeLengths()
	dbSize, _ := es.DBSizeMB()
	ftsSize, _ := es.FTSSizeMB()
	lastConsolidation, _ := es.LastConsolidationTS()
	summary, _ := es.SummaryStats()
	epByDay, _ := es.EpisodesByDay(7)
	labelKeys, _ := es.TopLabelKeys(10)

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
		EpisodesByRepo:        byRepo,
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
		result.TopRepo = summary.TopRepo
		result.AvgDurationSec = summary.AvgDurationSec
		result.TopLabelKey = summary.TopLabelKey
		result.LabelCardinality = summary.LabelCardinality
		result.UnlabeledCount = summary.UnlabeledCount
	}
	if epByDay != nil {
		result.EpisodesByDay = epByDay
	}
	if len(labelKeys) > 0 {
		lb := make([]models.LabelCount, len(labelKeys))
		for i, tc := range labelKeys {
			lb[i] = models.LabelCount{Key: tc.Tag, Value: "", Count: tc.Count}
		}
		result.EpisodesByLabel = lb
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
	if s.TopRepo != "" {
		fmt.Printf("  %-30s %s\n", "Top repo", s.TopRepo)
	}
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
	if s.EpisodesByRepo != nil {
		fmt.Println(strings.Repeat("─", 50))
		for repo, count := range s.EpisodesByRepo {
			fmt.Printf("  %-30s %s\n", "Repo: "+repo, strconv.Itoa(count))
		}
	}
	if s.TopLabelKey != "" {
		fmt.Printf("  %-30s %s\n", "Top label key", s.TopLabelKey)
		fmt.Printf("  %-30s %d\n", "Label cardinality", s.LabelCardinality)
	}
	if s.UnlabeledCount > 0 {
		fmt.Printf("  %-30s %d\n", "Unlabeled episodes", s.UnlabeledCount)
	}
	if len(s.EpisodesByLabel) > 0 {
		fmt.Println(strings.Repeat("─", 50))
		for _, kc := range s.EpisodesByLabel {
			fmt.Printf("  %-30s %d\n", "Label: "+kc.Key, kc.Count)
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
