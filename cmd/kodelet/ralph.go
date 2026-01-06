package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

const (
	DefaultPRDFile          = "prd.json"
	DefaultProgressFile     = "progress.txt"
	DefaultIterations       = 10
	DefaultCompletionSignal = "COMPLETE"
)

type RalphConfig struct {
	PRDFile          string
	ProgressFile     string
	Iterations       int
	CompletionSignal string
}

func NewRalphConfig() *RalphConfig {
	return &RalphConfig{
		PRDFile:          DefaultPRDFile,
		ProgressFile:     DefaultProgressFile,
		Iterations:       DefaultIterations,
		CompletionSignal: DefaultCompletionSignal,
	}
}

func (c *RalphConfig) GetCompletionMarker() string {
	return fmt.Sprintf("<promise>%s</promise>", c.CompletionSignal)
}

func (c *RalphConfig) Validate() error {
	if c.PRDFile == "" {
		return errors.New("PRD file path cannot be empty")
	}

	if c.ProgressFile == "" {
		return errors.New("progress file path cannot be empty")
	}

	if c.Iterations < 1 {
		return errors.New("iterations must be at least 1")
	}

	if _, err := os.Stat(c.PRDFile); os.IsNotExist(err) {
		return errors.Errorf("PRD file not found: %s", c.PRDFile)
	}

	return nil
}

var ralphCmd = &cobra.Command{
	Use:   "ralph",
	Short: "Run autonomous feature development loop (Ralph pattern)",
	Long: `Ralph is an outer loop harness for autonomous software development.

It iteratively runs an AI agent to implement features from a PRD (Product Requirements Document),
tracking progress between iterations. Each iteration:

1. Reads the PRD and progress files
2. Implements the highest-priority incomplete feature
3. Runs type checking and tests
4. Updates the PRD and progress file
5. Makes a git commit

The loop exits when all features are complete (signaled by <promise>SIGNAL</promise>)
or when the maximum number of iterations is reached. The signal keyword is configurable via --signal.

Based on the "Ralph" pattern from ghuntley.com/ralph and Anthropic's guidance on
effective harnesses for long-running agents.

Examples:
  # Run with default settings (prd.json, progress.txt, 10 iterations)
  kodelet ralph

  # Run with custom files
  kodelet ralph --prd features.json --progress dev-log.txt

  # Run for more iterations
  kodelet ralph --iterations 50

  # Initialize a new PRD file
  kodelet ralph init
`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			presenter.Warning("Cancellation requested, shutting down after current iteration...")
			cancel()
		}()

		config := getRalphConfigFromFlags(cmd)

		if err := config.Validate(); err != nil {
			presenter.Error(err, "Configuration validation failed")
			os.Exit(1)
		}

		if !isGitRepository() {
			presenter.Error(errors.New("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		ensureProgressFileExists(config.ProgressFile)

		presenter.Section("Ralph - Autonomous Development Loop")
		presenter.Info(fmt.Sprintf("PRD: %s | Progress: %s | Max Iterations: %d",
			config.PRDFile, config.ProgressFile, config.Iterations))
		presenter.Separator()

		for i := 1; i <= config.Iterations; i++ {
			select {
			case <-ctx.Done():
				presenter.Warning("Loop cancelled by user")
				return
			default:
			}

			presenter.Section(fmt.Sprintf("Iteration %d of %d", i, config.Iterations))

			result, err := runRalphIteration(ctx, cmd, config)
			if err != nil {
				presenter.Error(err, fmt.Sprintf("Iteration %d failed", i))
				continue
			}

			if strings.Contains(result, config.GetCompletionMarker()) {
				presenter.Success(fmt.Sprintf("PRD complete after %d iterations!", i))
				return
			}

			presenter.Separator()
		}

		presenter.Warning(fmt.Sprintf("Reached maximum iterations (%d). PRD may not be fully complete.", config.Iterations))
	},
}

var ralphInitCmd = &cobra.Command{
	Use:   "init [extra instructions]",
	Short: "Initialize a new PRD file from project analysis",
	Long: `Analyze the current project and generate a PRD (Product Requirements Document)
with features to implement. This creates a starting point for the ralph loop.

You can provide extra instructions as arguments to guide the PRD generation:
  kodelet ralph init "take a look at the design doc in ./design.md"
  kodelet ralph init "focus on authentication and API features"

Can also be invoked as a standalone recipe:
  kodelet run -r ralph-init --arg prd=features.json "check ./specs/feature-spec.md"`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		prdFile, _ := cmd.Flags().GetString("prd")
		progressFile, _ := cmd.Flags().GetString("progress")

		extraInstructions := strings.Join(args, " ")

		if _, err := os.Stat(prdFile); err == nil {
			presenter.Warning(fmt.Sprintf("PRD file already exists: %s", prdFile))
			response := presenter.Prompt("Overwrite?", "y/N")
			if strings.ToLower(response) != "y" {
				presenter.Info("Aborted.")
				return
			}
		}

		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil && !errors.Is(err, tools.ErrMCPDisabled) {
			presenter.Error(err, "Failed to create MCP manager")
			return
		}

		customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create custom tool manager")
			return
		}

		llmConfig, err := llm.GetConfigFromViperWithCmd(cmd)
		if err != nil {
			presenter.Error(err, "Failed to load configuration")
			return
		}
		s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig), tools.WithMCPTools(mcpManager), tools.WithCustomTools(customManager))

		processor, err := fragments.NewFragmentProcessor()
		if err != nil {
			presenter.Error(err, "Failed to create fragment processor")
			return
		}

		fragment, err := processor.LoadFragment(ctx, &fragments.Config{
			FragmentName: "ralph-init",
			Arguments: map[string]string{
				"prd":      prdFile,
				"progress": progressFile,
			},
		})
		if err != nil {
			presenter.Error(err, "Failed to load ralph-init recipe")
			return
		}

		prompt := fragment.Content
		if extraInstructions != "" {
			prompt = fmt.Sprintf("%s\n\n## Instructions\n\n%s", prompt, extraInstructions)
		}

		presenter.Info("Analyzing repository and generating PRD...")
		presenter.Separator()

		out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt,
			llmConfig, false, llmtypes.MessageOpt{
				PromptCache: true,
			})

		presenter.Info(out)
		presenter.Separator()

		usageStats := presenter.ConvertUsageStats(&usage)
		presenter.Stats(usageStats)
	},
}

func init() {
	defaults := NewRalphConfig()

	ralphCmd.Flags().String("prd", defaults.PRDFile, "Path to the PRD (Product Requirements Document) JSON file")
	ralphCmd.Flags().String("progress", defaults.ProgressFile, "Path to the progress tracking file")
	ralphCmd.Flags().Int("iterations", defaults.Iterations, "Maximum number of iterations to run")
	ralphCmd.Flags().String("signal", defaults.CompletionSignal, "Completion signal keyword (wrapped in <promise>...</promise>)")

	ralphInitCmd.Flags().String("prd", defaults.PRDFile, "Path for the PRD file to create")
	ralphInitCmd.Flags().String("progress", defaults.ProgressFile, "Path for the progress file to create")

	ralphCmd.AddCommand(ralphInitCmd)
}

func getRalphConfigFromFlags(cmd *cobra.Command) *RalphConfig {
	config := NewRalphConfig()

	if prd, err := cmd.Flags().GetString("prd"); err == nil && prd != "" {
		config.PRDFile = prd
	}
	if progress, err := cmd.Flags().GetString("progress"); err == nil && progress != "" {
		config.ProgressFile = progress
	}
	if iterations, err := cmd.Flags().GetInt("iterations"); err == nil {
		config.Iterations = iterations
	}
	if signal, err := cmd.Flags().GetString("signal"); err == nil && signal != "" {
		config.CompletionSignal = signal
	}

	return config
}

func ensureProgressFileExists(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		header := "# Progress Log\n\nThis file tracks progress across Ralph iterations.\n\n---\n\n"
		if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
			presenter.Warning(fmt.Sprintf("Could not create progress file: %v", err))
		}
	}
}

func runRalphIteration(ctx context.Context, cmd *cobra.Command, config *RalphConfig) (string, error) {
	mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
	if err != nil && !errors.Is(err, tools.ErrMCPDisabled) {
		return "", errors.Wrap(err, "failed to create MCP manager")
	}

	customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to create custom tool manager")
	}

	llmConfig, err := llm.GetConfigFromViperWithCmd(cmd)
	if err != nil {
		return "", errors.Wrap(err, "failed to load configuration")
	}
	s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig), tools.WithMCPTools(mcpManager), tools.WithCustomTools(customManager))

	processor, err := fragments.NewFragmentProcessor()
	if err != nil {
		return "", errors.Wrap(err, "failed to create fragment processor")
	}

	fragment, err := processor.LoadFragment(ctx, &fragments.Config{
		FragmentName: "ralph",
		Arguments: map[string]string{
			"prd":      config.PRDFile,
			"progress": config.ProgressFile,
			"signal":   config.CompletionSignal,
		},
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to load ralph recipe")
	}

	out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, fragment.Content,
		llmConfig, false, llmtypes.MessageOpt{
			PromptCache: true,
		})

	presenter.Info(out)
	presenter.Separator()

	usageStats := presenter.ConvertUsageStats(&usage)
	presenter.Stats(usageStats)

	return out, nil
}
