package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/security"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
	"github.com/spf13/cobra"
)

type CaptureOutput struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Outcome string `json:"outcome"`
}

func stdinAvailable(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return true
	}
	info, err := file.Stat()
	if err != nil {
		return true
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func NewCaptureCmd(es *store.EpisodeStore, _ *models.Config) *cobra.Command {
	var (
		problem         string
		thinkingTrace   string
		outcome         string
		domain          string
		repo            string
		tier            string
		modelID         string
		durationSeconds int
		tags            []string
		jsonOutput      bool
	)

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture a reasoning episode at the completion of a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			var ep models.Episode

			if stdinAvailable(cmd.InOrStdin()) {
				stdinData, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				trimmed := strings.TrimSpace(string(stdinData))
				if trimmed != "" && strings.HasPrefix(trimmed, "{") {
					if err := json.Unmarshal(stdinData, &ep); err != nil || ep.Problem == "" {
						ep.Problem = trimmed
					}
				} else if trimmed != "" {
					ep.Problem = trimmed
				}
			}

			if cmd.Flags().Changed("problem") {
				ep.Problem = problem
			} else if ep.Problem == "" && len(args) > 0 {
				ep.Problem = strings.Join(args, " ")
			}
			if ep.Problem == "" {
				return errors.New("problem is required (via --problem, JSON stdin, or text stdin)")
			}

			if cmd.Flags().Changed("thinking-trace") {
				ep.ThinkingTrace = thinkingTrace
			}
			if cmd.Flags().Changed("outcome") {
				if norm, ok := models.NormalizeOutcome(outcome); ok {
					ep.Outcome = norm
				} else {
					ep.Outcome = outcome
				}
			} else if ep.Outcome == "" {
				ep.Outcome = "success"
			}
			if cmd.Flags().Changed("domain") {
				ep.Domain = domain
			} else if ep.Domain == "" {
				ep.Domain = "coding"
			}
			if cmd.Flags().Changed("tags") {
				ep.Tags = tags
			}
			if cmd.Flags().Changed("repo") {
				ep.Repo = repo
			}
			if cmd.Flags().Changed("tier") {
				ep.Tier = models.MemoryTier(tier)
			} else if ep.Tier == "" {
				ep.Tier = models.TierEpisodic
			}
			if cmd.Flags().Changed("model") {
				ep.ModelID = modelID
			}
			if cmd.Flags().Changed("duration") {
				ep.DurationSeconds = durationSeconds
			}

			generatedID := ep.ID == ""
			security.Episode(&ep)
			ep.Steps = models.ExtractSteps(ep.ThinkingTrace)

			var (
				createdID string
				err       error
			)
			for attempt := 0; attempt < 3; attempt++ {
				if generatedID {
					ep.ID = es.NextID()
				}
				createdID, err = es.CreateEpisodeContext(ctx, &ep)
				if err == nil {
					break
				}
				if !generatedID || !strings.Contains(err.Error(), "UNIQUE constraint failed: episodes.id") {
					return fmt.Errorf("create episode: %w", err)
				}
			}
			if err != nil {
				return fmt.Errorf("create episode after ID retries: %w", err)
			}
			ep.ID = createdID

			if jsonOutput {
				out := CaptureOutput{
					ID:      ep.ID,
					Status:  "captured",
					Outcome: ep.Outcome,
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			}

			fmt.Fprintln(cmd.OutOrStdout(), ep.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&problem, "problem", "p", "", "Task or problem description")
	cmd.Flags().StringVarP(&thinkingTrace, "thinking-trace", "t", "", "Chain-of-thought reasoning trace")
	cmd.Flags().StringVarP(&outcome, "outcome", "o", "", "Outcome (success, partial, failure)")
	cmd.Flags().StringVarP(&domain, "domain", "d", "", "Broad domain (coding, agentic)")
	cmd.Flags().StringSliceVar(&tags, "tags", nil, "Tags (comma-separated)")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "Repository scope")
	cmd.Flags().StringVar(&tier, "tier", "", "Memory tier (episodic, semantic)")
	cmd.Flags().StringVarP(&modelID, "model", "m", "", "Model identifier")
	cmd.Flags().IntVar(&durationSeconds, "duration", 0, "Task duration in seconds")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	return cmd
}
