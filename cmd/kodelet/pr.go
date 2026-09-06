package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type PRConfig struct {
	Provider     string
	Target       string
	TemplateFile string
	Draft        bool
	ResultOnly   bool
}

func NewPRConfig() *PRConfig {
	return &PRConfig{
		Provider:     "github",
		Target:       "main",
		TemplateFile: "",
		Draft:        false,
		ResultOnly:   false,
	}
}

func (c *PRConfig) Validate() error {
	if c.Provider != "github" {
		return fmt.Errorf("unsupported provider: %s, only 'github' is supported", c.Provider)
	}

	if c.Target == "" {
		return errors.New("target branch cannot be empty")
	}

	return nil
}

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Create a pull request with AI-generated title and description",
	Long: `Create a pull request for the changes you have made on the current branch.

This command analyzes the current branch changes compared to the target branch and generates an appropriate PR title and description.

Use the --draft flag to create a draft pull request that is not ready for review.`,
	RunE: func(cmd *cobra.Command, _ []string) error { return runRemotePR(cmd) },
}

func init() {
	defaults := NewPRConfig()
	addRemoteRunFlags(prCmd)
	prCmd.Flags().String("cwd", "", "Repository directory on the selected runner")
	prCmd.Flags().StringP("provider", "p", defaults.Provider, "The CVS provider to use")
	prCmd.Flags().StringP("target", "t", defaults.Target, "The target branch to create the pull request on")
	prCmd.Flags().String("template-file", defaults.TemplateFile, "The path to the template file for the pull request")
	prCmd.Flags().BoolP("draft", "d", defaults.Draft, "Create the pull request as a draft")
	prCmd.Flags().Bool("result-only", defaults.ResultOnly, "Only print the final agent message, suppressing all intermediate output and usage statistics")
}

func remotePRRequest(cmd *cobra.Command) (chat.ChatRequest, error) {
	var request chat.ChatRequest
	if err := validateRemoteChatFlags(cmd); err != nil {
		return request, errors.Wrap(err, "invalid pull request options")
	}
	provider, _ := cmd.Flags().GetString("provider")
	if provider != "github" {
		return request, errors.New("--provider must be github for pull requests; use --profile to select the AI provider")
	}
	target, _ := cmd.Flags().GetString("target")
	if strings.TrimSpace(target) == "" {
		return request, errors.New("target branch cannot be empty")
	}
	// PR's --provider names the VCS, not the model provider. All other typed
	// execution flags keep their ordinary validation and presence semantics.
	options, err := remoteRunExecutionOptions(cmd, "provider")
	if err != nil {
		return request, err
	}
	request.Options = options
	request.CWD, _ = cmd.Flags().GetString("cwd")
	request.EnvironmentProfile, _ = cmd.Flags().GetString("runner-profile")
	if cmd.Flags().Changed("profile") {
		request.Profile, _ = cmd.Flags().GetString("profile")
	}
	template, _ := cmd.Flags().GetString("template-file")
	draft, _ := cmd.Flags().GetBool("draft")
	arguments := map[string]string{"target": target, "draft": strconv.FormatBool(draft)}
	if template != "" {
		arguments["template_file"] = template
	}
	request.Message = "/github/pr " + formatFragmentDisplayArgs(arguments)
	return request, nil
}

func runRemotePR(cmd *cobra.Command) error {
	request, err := remotePRRequest(cmd)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, _ := serverFlagOrConfig(cmd)
	token, _, err := resolveControlPlaneAuthToken(cmd, server)
	if err != nil {
		return err
	}
	runner, err := prepareOneShotRunner(ctx, cmd, server, token, &request)
	if err != nil {
		return err
	}
	resultOnly, _ := cmd.Flags().GetBool("result-only")
	if !resultOnly {
		ctx = extensions.ContextWithUIInputBroker(ctx, extensions.NewTerminalUIInputBroker(os.Stdin, cmd.ErrOrStderr()))
	}
	return executeRemoteRun(ctx, runner, request, resultOnly, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func getPRConfigFromFlags(cmd *cobra.Command) *PRConfig {
	config := NewPRConfig()

	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
	}
	if target, err := cmd.Flags().GetString("target"); err == nil {
		config.Target = target
	}
	if templateFile, err := cmd.Flags().GetString("template-file"); err == nil {
		config.TemplateFile = templateFile
	}
	if draft, err := cmd.Flags().GetBool("draft"); err == nil {
		config.Draft = draft
	}
	if resultOnly, err := cmd.Flags().GetBool("result-only"); err == nil {
		config.ResultOnly = resultOnly
	}

	return config
}
