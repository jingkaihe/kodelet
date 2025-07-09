package main

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// ResolveConfig holds configuration for the resolve command
type ResolveConfig struct {
	Provider   string
	IssueURL   string
	BotMention string
}

// NewResolveConfig creates a new ResolveConfig with default values
func NewResolveConfig() *ResolveConfig {
	return &ResolveConfig{
		Provider:   "github",
		IssueURL:   "",
		BotMention: "@kodelet",
	}
}

// Validate validates the ResolveConfig and returns an error if invalid
func (c *ResolveConfig) Validate() error {
	if c.Provider != "github" {
		return errors.New(fmt.Sprintf("unsupported provider: %s, only 'github' is supported", c.Provider))
	}

	if c.IssueURL == "" {
		return errors.New("issue URL cannot be empty")
	}

	return nil
}

var resolveCmd = &cobra.Command{
	Use:        "resolve",
	Short:      "[DEPRECATED] Use 'issue-resolve' instead - Resolve an issue autonomously",
	Long:       `[DEPRECATED] This command is deprecated. Please use 'kodelet issue-resolve' instead.`,
	Deprecated: "Use 'kodelet issue-resolve' instead",
	Run: func(cmd *cobra.Command, args []string) {
		// Forward to issue-resolve command
		issueResolveCmd.Run(cmd, args)
	},
}

func init() {
	defaults := NewResolveConfig()
	resolveCmd.Flags().StringP("provider", "p", defaults.Provider, "The issue provider to use")
	resolveCmd.Flags().String("issue-url", defaults.IssueURL, "Issue URL (required)")
	resolveCmd.Flags().String("bot-mention", defaults.BotMention, "Bot mention to look for in comments")
	resolveCmd.MarkFlagRequired("issue-url")
}
