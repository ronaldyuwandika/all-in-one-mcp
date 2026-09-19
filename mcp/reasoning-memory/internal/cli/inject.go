package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
	"github.com/spf13/cobra"
)

type InjectOutput struct {
	Context      string `json:"context"`
	EpisodeCount int    `json:"episode_count"`
	PatternCount int    `json:"pattern_count"`
}

func NewInjectCmd(es *store.EpisodeStore, cfg *models.Config) *cobra.Command {
	var (
		problem       string
		topK          int
		includeTraces bool
		jsonOutput    bool
	)

	cmd := &cobra.Command{
		Use:   "inject [problem]",
		Short: "Retrieve and format reasoning context for a problem",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if problem == "" && len(args) > 0 {
				problem = strings.Join(args, " ")
			}
			if problem == "" {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				problem = strings.TrimSpace(string(raw))
			}
			if problem == "" {
				return errors.New("problem is required (via --problem, argument, or stdin)")
			}

			if topK <= 0 {
				topK = 3
			}
			if topK > 10 {
				topK = 10
			}

			results, err := es.SearchLocal(problem, "", "", "", nil, topK)
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}

			var episodes []prompter.EpisodeContext
			for _, r := range results {
				ep, _ := es.GetEpisode(r.ID)
				var failed []models.FailedApproach
				trace := ""
				if ep != nil {
					failed = ep.FailedApproaches
					if includeTraces {
						trace = ep.ThinkingTrace
					}
				}
				episodes = append(episodes, prompter.EpisodeContext{
					Problem:          r.Problem,
					Domain:           r.Domain,
					Outcome:          r.Outcome,
					Tags:             r.Tags,
					ThinkingTrace:    trace,
					FailedApproaches: failed,
					EpisodeID:        r.ID,
				})
			}

			var promptPatterns []prompter.PatternContext
			if cfg != nil && cfg.Retrieval.IncludePatterns {
				pats, err := es.SearchPatterns(problem, "", nil, cfg.Retrieval.MaxPatterns)
				if err == nil {
					for _, p := range pats {
						promptPatterns = append(promptPatterns, prompter.PatternContext{
							ID:                 p.ID,
							Domain:             p.Domain,
							ConsolidatedPrompt: p.ConsolidatedPrompt,
							MasterThinkingPath: p.MasterThinkingPath,
							Tags:               p.Tags,
							MergeScore:         p.MergeScore,
						})
					}
				}
			}

			xmlBlock := prompter.BuildXMLReasoningMemoryBlock(episodes, promptPatterns)

			if jsonOutput {
				out := InjectOutput{
					Context:      xmlBlock,
					EpisodeCount: len(episodes),
					PatternCount: len(promptPatterns),
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), xmlBlock)
			return err
		},
	}

	cmd.Flags().StringVarP(&problem, "problem", "p", "", "Problem description")
	cmd.Flags().IntVarP(&topK, "top-k", "k", 3, "Number of past episodes to include")
	cmd.Flags().BoolVar(&includeTraces, "include-traces", false, "Include full thinking traces")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	return cmd
}
