package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
	"github.com/spf13/cobra"
)

func NewRetrieveCmd(es *store.EpisodeStore, _ *models.Config) *cobra.Command {
	var (
		problem    string
		domain     string
		outcome    string
		repo       string
		tags       []string
		topK       int
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "retrieve [problem]",
		Short: "Search reasoning episodes matching a problem description",
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
				topK = 5
			}
			if topK > 20 {
				topK = 20
			}

			results, err := es.SearchLocal(problem, domain, outcome, repo, tags, topK)
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}
			if results == nil {
				results = []models.EpisodeSummary{}
			}

			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(results)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Found %d matching episodes:\n", len(results))
			for _, r := range results {
				score := r.LocalScore
				if r.VectorScore > score {
					score = r.VectorScore
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  [%s] (score: %.3f, outcome: %s) %s\n", r.ID, score, r.Outcome, r.Problem)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&problem, "problem", "p", "", "Problem description to match against")
	cmd.Flags().StringVarP(&domain, "domain", "d", "", "Filter by domain (e.g. coding, agentic)")
	cmd.Flags().StringVarP(&outcome, "outcome", "o", "", "Filter by outcome (e.g. success, failure)")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "Filter by repository name")
	cmd.Flags().StringSliceVarP(&tags, "tags", "t", nil, "Filter by tags (comma-separated)")
	cmd.Flags().IntVarP(&topK, "top-k", "k", 5, "Maximum number of results to return")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	return cmd
}
