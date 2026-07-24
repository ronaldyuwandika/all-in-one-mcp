package cli

import (
	"context"
	"fmt"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
	"github.com/spf13/cobra"
)

var (
	compactDryRun bool
)

func NewCompactCmd(es *store.EpisodeStore, cfg *models.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Run database compaction, archiving, and pruning",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if compactDryRun {
				fmt.Println("Running compaction in DRY-RUN mode (preview)...")
			} else {
				fmt.Println("Running compaction...")
			}

			report, err := es.Compact(ctx, cfg.Consolidation, compactDryRun)
			if err != nil {
				return fmt.Errorf("compaction failed: %w", err)
			}

			if compactDryRun {
				fmt.Printf("Dry-run compaction report:\n")
			} else {
				fmt.Printf("Compaction completed successfully:\n")
			}
			fmt.Printf("  Archived episodes:  %d\n", report.ArchivedCount)
			fmt.Printf("  Pruned episodes:    %d\n", report.PrunedCount)
			fmt.Printf("  Summarized traces:  %d\n", report.SummarizedCount)
			fmt.Printf("  FTS index rebuilt:  %t\n", report.RebuiltFTS)

			return nil
		},
	}

	cmd.Flags().BoolVar(&compactDryRun, "dry-run", false, "Preview changes without modifying the database")
	return cmd
}
