package main

import (
	"os"

	"github.com/jingkaihe/kodelet/pkg/github"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/spf13/cobra"
)

// issueCmd represents the issue command
var issueCmd = &cobra.Command{
	Use:   "issue",
	Short: "GitHub issue operations",
	Long:  `Commands for fetching and processing GitHub issues.`,
}

// issueFetchCmd represents the issue fetch command
var issueFetchCmd = &cobra.Command{
	Use:   "fetch <github-issue-url>",
	Short: "Fetch GitHub issue and create ISSUE.md",
	Long: `Fetch a GitHub issue from its URL and create both ISSUE.md and .kodelet/issue.json files.

Examples:
  kodelet issue fetch https://github.com/owner/repo/issues/123
  
This command will:
- Fetch the issue details from GitHub API
- Create ISSUE.md with formatted issue content
- Create .kodelet/issue.json with issue metadata

Environment Variables:
  GITHUB_TOKEN - GitHub personal access token (optional but recommended for higher rate limits)`,
	Args: cobra.ExactArgs(1),
	RunE: runIssueFetch,
}

func init() {
	// Add subcommands to issue command
	issueCmd.AddCommand(issueFetchCmd)
}

func runIssueFetch(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	log := logger.G(ctx)
	
	issueURL := args[0]
	log.WithField("issue_url", issueURL).Info("Starting issue fetch")
	
	// Get GitHub token from environment
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Warn("GITHUB_TOKEN not set - using unauthenticated requests (lower rate limits)")
	}
	
	// Create GitHub client
	client := github.NewClient(ctx, token)
	
	// Create issue processor
	processor := github.NewIssueProcessor(client)
	
	// Fetch and process the issue
	issueData, err := processor.FetchAndProcess(ctx, issueURL)
	if err != nil {
		log.WithError(err).Error("Failed to fetch GitHub issue")
		return err
	}
	
	// Write ISSUE.md file
	if err := processor.WriteIssueFile(issueData); err != nil {
		log.WithError(err).Error("Failed to write ISSUE.md")
		return err
	}
	log.Info("Created ISSUE.md")
	
	// Write issue metadata JSON
	if err := processor.WriteIssueJSON(issueData); err != nil {
		log.WithError(err).Error("Failed to write issue metadata")
		return err
	}
	log.Info("Created .kodelet/issue.json")
	
	log.WithFields(map[string]interface{}{
		"title":  issueData.Title,
		"owner":  issueData.Owner,
		"repo":   issueData.Repo,
		"number": issueData.Number,
	}).Info("Successfully processed GitHub issue")
	
	return nil
}