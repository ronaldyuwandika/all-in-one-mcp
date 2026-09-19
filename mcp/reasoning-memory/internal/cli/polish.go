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

func NewPolishCmd(es *store.EpisodeStore, cfg *models.Config) *cobra.Command {
	var (
		rawPrompt      string
		targetAgent    string
		domain         string
		repo           string
		skillName      string
		outputFormat   string
		topK           int
		includeContext bool
		jsonOutput     bool
	)

	cmd := &cobra.Command{
		Use:   "polish [prompt]",
		Short: "Polish and structure an unstructured user prompt with reasoning context",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rawPrompt == "" && len(args) > 0 {
				rawPrompt = strings.Join(args, " ")
			}
			if rawPrompt == "" {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				rawPrompt = strings.TrimSpace(string(raw))
			}
			if rawPrompt == "" {
				return errors.New("prompt is required (via --prompt, argument, or stdin)")
			}

			if targetAgent == "" && cfg != nil {
				targetAgent = cfg.PromptPolishing.DefaultTargetAgent
			}
			if targetAgent == "" {
				targetAgent = "codex"
			}
			if outputFormat == "" && cfg != nil {
				outputFormat = cfg.PromptPolishing.DefaultOutputFormat
			}
			if outputFormat == "" {
				outputFormat = "markdown"
			}

			maxChars := 4000
			if cfg != nil && cfg.PromptPolishing.MaxPromptChars > 0 {
				maxChars = cfg.PromptPolishing.MaxPromptChars
			}

			var contextStr string
			var promptEpisodes []prompter.EpisodeContext
			var promptPatterns []prompter.PatternContext
			contextCount := 0

			if includeContext && es != nil {
				if topK <= 0 {
					topK = 3
				}
				results, err := es.SearchLocal(rawPrompt, domain, "success", repo, nil, topK)
				if err == nil {
					for _, r := range results {
						ep, _ := es.GetEpisode(r.ID)
						var failed []models.FailedApproach
						if ep != nil {
							failed = ep.FailedApproaches
						}
						promptEpisodes = append(promptEpisodes, prompter.EpisodeContext{
							Problem:          r.Problem,
							Domain:           r.Domain,
							Outcome:          r.Outcome,
							Tags:             r.Tags,
							FailedApproaches: failed,
							EpisodeID:        r.ID,
						})
					}
					if cfg != nil && cfg.PromptPolishing.IncludePatterns {
						pats, err := es.SearchPatterns(rawPrompt, domain, nil, cfg.PromptPolishing.MaxPatterns)
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
					contextCount = len(promptEpisodes) + len(promptPatterns)
					contextStr = prompter.BuildXMLReasoningMemoryBlock(promptEpisodes, promptPatterns)
				}
			}

			result, err := prompter.PolishPromptWithOptions(prompter.Options{
				RawPrompt:    rawPrompt,
				TargetAgent:  targetAgent,
				Domain:       domain,
				Repo:         repo,
				Context:      contextStr,
				SkillName:    skillName,
				OutputFormat: outputFormat,
				MaxChars:     maxChars,
				ContextCount: contextCount,
				Episodes:     promptEpisodes,
				Patterns:     promptPatterns,
			})
			if err != nil {
				return fmt.Errorf("polish failed: %w", err)
			}

			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}

			fmt.Fprintln(cmd.OutOrStdout(), result.PolishedPrompt)
			return nil
		},
	}

	cmd.Flags().StringVarP(&rawPrompt, "prompt", "p", "", "Raw prompt text")
	cmd.Flags().StringVarP(&targetAgent, "agent", "a", "", "Target agent profile (codex, claude, generic)")
	cmd.Flags().StringVarP(&domain, "domain", "d", "", "Domain override")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "Repository scope")
	cmd.Flags().StringVarP(&skillName, "skill", "s", "", "Skill name to load")
	cmd.Flags().StringVarP(&outputFormat, "format", "f", "markdown", "Output format (markdown, json, xml)")
	cmd.Flags().IntVarP(&topK, "top-k", "k", 3, "Number of context memories to include")
	cmd.Flags().BoolVar(&includeContext, "include-context", true, "Include relevant past episodes")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output full result as JSON")

	return cmd
}
