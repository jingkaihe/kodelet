package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

// PRRespondConfig holds configuration for the pr-respond command
type PRRespondConfig struct {
	Provider        string
	PRURL           string
	ReviewCommentID string
	IssueCommentID  string
}

// PRData holds prefetched PR information
type PRData struct {
	BasicInfo         string
	FocusedComment    string // Focused comment when comment-id is specified
	RelatedDiscussion string // Related discussions for the focused comment
	GitDiff           string // Git diff of the PR
}

// PRBasicInfo represents the JSON structure returned by 'gh pr view --json'
type PRBasicInfo struct {
	Title    string      `json:"title"`
	Author   PRAuthor    `json:"author"`
	Body     string      `json:"body"`
	Comments []PRComment `json:"comments"`
}

// PRAuthor represents the author of the PR
type PRAuthor struct {
	Login string `json:"login"`
}

// PRComment represents a comment on the PR
type PRComment struct {
	Author    PRAuthor  `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// NewPRRespondConfig creates a new PRRespondConfig with default values
func NewPRRespondConfig() *PRRespondConfig {
	return &PRRespondConfig{
		Provider:        "github",
		PRURL:           "",
		ReviewCommentID: "",
		IssueCommentID:  "",
	}
}

// Validate validates the PRRespondConfig and returns an error if invalid
func (c *PRRespondConfig) Validate() error {
	if c.Provider != "github" {
		return errors.New(fmt.Sprintf("unsupported provider: %s, only 'github' is supported", c.Provider))
	}

	if c.PRURL == "" {
		return errors.New("PR URL cannot be empty")
	}

	// Check that exactly one comment ID is provided
	reviewCommentProvided := c.ReviewCommentID != ""
	issueCommentProvided := c.IssueCommentID != ""

	if !reviewCommentProvided && !issueCommentProvided {
		return errors.New("either --review-id or --issue-comment-id must be provided")
	}

	if reviewCommentProvided && issueCommentProvided {
		return errors.New("only one of --review-id or --issue-comment-id can be provided, not both")
	}

	return nil
}

var prRespondCmd = &cobra.Command{
	Use:   "pr-respond",
	Short: "Intelligently respond to PR comments based on their type",
	Long: `Respond to pull request comments using appropriate workflows based on comment type:

CODE CHANGE REQUESTS (bug fixes, feature updates, implementation feedback):
- Analyzes the specific comment and requirements
- Makes targeted code changes to the PR branch
- Commits changes and pushes updates
- Responds to the comment with a summary of changes

QUESTION REQUESTS (clarifications, explanations, discussions):
- Analyzes the codebase and PR context to understand the question
- Researches relevant code and documentation
- Provides comprehensive answers directly in comment replies
- No code changes or commits made

CODE REVIEW REQUESTS (code quality assessment, security analysis, best practices):
- Conducts comprehensive code review of the PR changes
- Analyzes for security vulnerabilities, performance issues, and best practices
- Provides detailed feedback with specific recommendations
- Organizes findings by category and severity
- No code changes or commits made

The command automatically detects comment type and applies the appropriate workflow.
You must provide either --review-id for review comments or --issue-comment-id for issue comments.
Currently supports GitHub PRs only.

Examples:
  kodelet pr-respond --pr-url https://github.com/user/repo/pull/123 --review-id 456789
  kodelet pr-respond --pr-url https://github.com/user/repo/pull/123 --issue-comment-id 987654`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		// Set up signal handling
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			presenter.Warning("Cancellation requested, shutting down...")
			cancel()
		}()

		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create MCP manager")
			return
		}

		s := tools.NewBasicState(ctx, tools.WithMCPTools(mcpManager))

		// Get pr-respond config from flags
		config := getPRRespondConfigFromFlags(cmd)

		// Validate configuration
		if err := config.Validate(); err != nil {
			presenter.Error(err, "Configuration validation failed")
			os.Exit(1)
		}

		// Prerequisites checking
		if !isGitRepository() {
			presenter.Error(errors.New("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		if !isGhCliInstalled() {
			presenter.Error(errors.New("GitHub CLI not installed"), "Please install GitHub CLI first")
			presenter.Info("Visit https://cli.github.com/ for installation instructions")
			os.Exit(1)
		}

		if !isGhAuthenticated() {
			presenter.Error(errors.New("not authenticated with GitHub"), "Please run 'gh auth login' first")
			os.Exit(1)
		}

		bin, err := os.Executable()
		if err != nil {
			presenter.Error(err, "Failed to get executable path")
			os.Exit(1)
		}

		// Determine which comment ID to use
		var commentID string
		if config.ReviewCommentID != "" {
			commentID = config.ReviewCommentID
		} else {
			commentID = config.IssueCommentID
		}

		// Prefetch PR data
		presenter.Info("Prefetching PR data...")

		prData, err := prefetchPRData(ctx, config.PRURL, commentID, config.ReviewCommentID != "")
		if err != nil {
			presenter.Error(err, "Failed to prefetch PR data")
			os.Exit(1)
		}

		// Generate comprehensive prompt using builtin fragment
		prompt, err := loadPRResponsePrompt(ctx, bin, config.PRURL, prData)
		if err != nil {
			presenter.Error(err, "Failed to load PR response prompt")
			os.Exit(1)
		}
		logger.G(ctx).WithField("prompt_length", len(prompt)).Debug("Generated PR respond prompt")

		// Send to LLM using existing architecture
		presenter.Info("Analyzing PR comment and determining appropriate response workflow...")
		presenter.Separator()

		out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt,
			llm.GetConfigFromViper(), false, llmtypes.MessageOpt{
				PromptCache: true,
			})

		fmt.Println(out)
		presenter.Separator()

		// Display usage statistics
		usageStats := presenter.ConvertUsageStats(&usage)
		presenter.Stats(usageStats)
	},
}

func init() {
	defaults := NewPRRespondConfig()
	prRespondCmd.Flags().StringP("provider", "p", defaults.Provider, "The PR provider to use")
	prRespondCmd.Flags().String("pr-url", defaults.PRURL, "PR URL (required)")
	prRespondCmd.Flags().String("review-id", defaults.ReviewCommentID, "Specific review comment ID to respond to")
	prRespondCmd.Flags().String("issue-comment-id", defaults.IssueCommentID, "Specific issue comment ID to respond to")
	prRespondCmd.MarkFlagRequired("pr-url")
}

// getPRRespondConfigFromFlags extracts pr-respond configuration from command flags
func getPRRespondConfigFromFlags(cmd *cobra.Command) *PRRespondConfig {
	config := NewPRRespondConfig()

	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
	}
	if prURL, err := cmd.Flags().GetString("pr-url"); err == nil {
		config.PRURL = prURL
	}
	if reviewCommentID, err := cmd.Flags().GetString("review-id"); err == nil {
		config.ReviewCommentID = reviewCommentID
	}
	if issueCommentID, err := cmd.Flags().GetString("issue-comment-id"); err == nil {
		config.IssueCommentID = issueCommentID
	}

	return config
}

// prefetchPRData fetches PR information, comments, and reviews using gh CLI
// If commentID is provided, it also fetches focused comment and related discussions
func prefetchPRData(ctx context.Context, prURL, commentID string, isReviewComment bool) (*PRData, error) {
	data := &PRData{}

	// Get basic PR information
	cmd := exec.Command("gh", "pr", "view", prURL, "--json", "title,author,body,comments")
	basicInfoOutput, err := cmd.CombinedOutput()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get PR basic info: %s", string(basicInfoOutput))
	}

	// Format JSON output to human-readable markdown
	formattedInfo, err := formatPRBasicInfoToMarkdown(strings.TrimSpace(string(basicInfoOutput)))
	if err != nil {
		// Fallback to raw JSON if formatting fails
		logger.G(ctx).WithError(err).Warn("Failed to format PR basic info to markdown, using raw JSON")
		data.BasicInfo = strings.TrimSpace(string(basicInfoOutput))
	} else {
		data.BasicInfo = formattedInfo
	}

	// Get git diff of the PR
	diffCmd := exec.Command("gh", "pr", "diff", prURL)
	logger.G(ctx).WithField("cmd", diffCmd.String()).Debug("Fetching PR git diff")
	diffOutput, err := diffCmd.CombinedOutput()
	if err != nil {
		// Don't fail completely, just log the error and continue
		logger.G(ctx).WithError(err).WithField("output", string(diffOutput)).Warn("Failed to fetch PR git diff")
		data.GitDiff = "Failed to fetch git diff"
	} else {
		data.GitDiff = strings.TrimSpace(string(diffOutput))
		logger.G(ctx).WithField("diff_length", len(data.GitDiff)).Debug("PR git diff fetched successfully")
	}

	// Fetch focused comment and related discussions
	var focusedComment, relatedDiscussion string

	if isReviewComment {
		focusedComment, relatedDiscussion, err = fetchFocusedReviewComment(ctx, prURL, commentID)
	} else {
		focusedComment, relatedDiscussion, err = fetchFocusedIssueComment(ctx, prURL, commentID)
	}

	if err != nil {
		// Don't fail completely, just log the error and continue
		logger.G(ctx).WithError(err).WithFields(map[string]interface{}{
			"is_review_comment": isReviewComment,
			"comment_id":        commentID,
		}).Warn("Failed to fetch focused comment data")
		data.FocusedComment = "Failed to fetch focused comment"
		data.RelatedDiscussion = "Failed to fetch related discussions"
	} else {
		data.FocusedComment = focusedComment
		data.RelatedDiscussion = relatedDiscussion
		logger.G(ctx).WithField("is_review_comment", isReviewComment).Debug("Focused comment data fetched successfully")
	}

	return data, nil
}

// parseGitHubURL extracts owner, repo, and PR number from GitHub PR URL
// Expected URL format: https://github.com/owner/repo/pull/123
// When split by "/", the parts array becomes:
//
//	parts[0]: "https:"
//	parts[1]: "" (empty string)
//	parts[2]: "github.com"
//	parts[3]: "owner" (GitHub username/organization)
//	parts[4]: "repo" (repository name)
//	parts[5]: "pull" (literal "pull")
//	parts[6]: "123" (PR number)
func parseGitHubURL(prURL string) (owner, repo, prNumber string, err error) {
	parts := strings.Split(prURL, "/")
	if len(parts) < 7 {
		return "", "", "", errors.New("invalid PR URL format")
	}
	// Extract: owner (parts[3]), repo (parts[4]), prNumber (parts[6])
	return parts[3], parts[4], parts[6], nil
}

// formatPRBasicInfoToMarkdown converts JSON PR data to human-readable markdown
func formatPRBasicInfoToMarkdown(jsonData string) (string, error) {
	var prInfo PRBasicInfo
	err := json.Unmarshal([]byte(jsonData), &prInfo)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse PR JSON data")
	}

	// Create function map for template
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}

	tmpl, err := template.New("prBasicInfo").Funcs(funcMap).Parse(prBasicInfoTemplate)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse PR basic info template")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, prInfo); err != nil {
		return "", errors.Wrap(err, "failed to execute PR basic info template")
	}

	return buf.String(), nil
}

const prBasicInfoTemplate = `# {{.Title}}

**Author:** @{{.Author.Login}}

{{if .Body}}## Description

{{.Body}}

{{end}}{{if .Comments}}## Comments

{{range $i, $comment := .Comments}}### Comment {{add $i 1}}
**Author:** @{{$comment.Author.Login}}
**Created:** {{$comment.CreatedAt.Format "2006-01-02 15:04:05"}}

{{$comment.Body}}

---

{{end}}{{end}}`

// fetchFocusedReviewComment fetches specific review comment details and related discussions using GitHub API
func fetchFocusedReviewComment(ctx context.Context, prURL, commentID string) (string, string, error) {
	owner, repo, prNumber, err := parseGitHubURL(prURL)
	if err != nil {
		return "", "", err
	}

	// Fetch review comment
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/%s/pulls/%s/reviews/%s", owner, repo, prNumber, commentID), "--jq", "{body: .body, author: .user.login, submitted_at: .submitted_at}")
	logger.G(ctx).WithField("cmd", cmd.String()).Debug("Fetching review comment data")
	commentOutput, err := cmd.CombinedOutput()
	if err != nil {
		logger.G(ctx).WithField("cmd", cmd.String()).WithError(err).WithField("output", string(commentOutput)).Error("Failed to fetch review comment details")
		return "", "", errors.Wrapf(err, "failed to fetch review comment details: %s", string(commentOutput))
	}

	focusedComment := fmt.Sprintf("Review Comment ID %s:\n%s", commentID, strings.TrimSpace(string(commentOutput)))

	// For related discussions
	cmd = exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/%s/pulls/%s/reviews/%s/comments", owner, repo, prNumber, commentID),
		"--jq", "[.[] | {id: .id, author: .user.login, body: .body, line: .line, created_at: .created_at, diff_hunk: .diff_hunk}]")
	logger.G(ctx).WithField("cmd", cmd.String()).Debug("Fetching related review discussions")
	discussionOutput, err := cmd.CombinedOutput()
	if err != nil {
		logger.G(ctx).WithField("cmd", cmd.String()).WithError(err).WithField("output", string(discussionOutput)).Error("Failed to fetch related review discussions")
		relatedDiscussion := "No related discussions found or failed to fetch"
		return focusedComment, relatedDiscussion, nil
	}

	// turn discussionOutput into from json to yaml
	var discussion any
	err = json.Unmarshal(discussionOutput, &discussion)
	if err != nil {
		return "", "", errors.Wrapf(err, "failed to unmarshal discussion output: %s", string(discussionOutput))
	}

	// turn discussion into yaml
	discussionYaml, err := yaml.Marshal(discussion)
	if err != nil {
		return "", "", errors.Wrapf(err, "failed to marshal discussion output: %s", string(discussionOutput))
	}

	relatedDiscussion := fmt.Sprintf("Related review discussions for comment %s:\n%s", commentID, strings.TrimSpace(string(discussionYaml)))

	return focusedComment, relatedDiscussion, nil
}

// fetchFocusedIssueComment fetches specific issue comment details using GitHub API
func fetchFocusedIssueComment(ctx context.Context, prURL, commentID string) (string, string, error) {
	owner, repo, _, err := parseGitHubURL(prURL)
	if err != nil {
		return "", "", err
	}

	// Fetch issue comment
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/%s/issues/comments/%s", owner, repo, commentID),
		"--jq", "{author: .user.login, body: .body, created_at: .created_at}")
	logger.G(ctx).WithField("cmd", cmd.String()).Debug("Fetching issue comment data")
	commentOutput, err := cmd.CombinedOutput()
	if err != nil {
		logger.G(ctx).WithField("cmd", cmd.String()).WithError(err).WithField("output", string(commentOutput)).Error("Failed to fetch issue comment details")
		return "", "", errors.Wrapf(err, "failed to fetch issue comment details: %s", string(commentOutput))
	}

	focusedComment := fmt.Sprintf("Issue Comment ID %s:\n%s", commentID, strings.TrimSpace(string(commentOutput)))
	relatedDiscussion := "Issue comments don't have related discussions like review comments"

	return focusedComment, relatedDiscussion, nil
}

// loadPRResponsePrompt loads the PR response prompt using the builtin fragment system
func loadPRResponsePrompt(ctx context.Context, bin, prURL string, prData *PRData) (string, error) {
	// Create fragment processor with builtin fragments enabled
	processor, err := fragments.NewFragmentProcessor(fragments.WithBuiltinFragments())
	if err != nil {
		return "", errors.Wrap(err, "failed to create fragment processor")
	}

	// Extract branch name from PR URL for potential code changes
	_, _, prNumber, err := parseGitHubURL(prURL)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse GitHub URL")
	}
	
	// Default branch name format - this could be enhanced to fetch actual branch name
	branchName := fmt.Sprintf("pr-%s", prNumber)

	// Prepare fragment arguments
	args := map[string]string{
		"PullRequestURL": prURL,
		"BotMention":     "@kodelet", // Default bot mention
		"BranchName":     branchName,
		"BinPath":        bin,
	}

	// Add PR data as context
	var contextParts []string
	
	// Add basic PR info
	if prData.BasicInfo != "" {
		contextParts = append(contextParts, fmt.Sprintf("PR Info:\n%s", prData.BasicInfo))
	}
	
	// Add focused comment
	if prData.FocusedComment != "" {
		contextParts = append(contextParts, fmt.Sprintf("Focused Comment:\n%s", prData.FocusedComment))
	}
	
	// Add related discussion
	if prData.RelatedDiscussion != "" && prData.RelatedDiscussion != "Issue comments don't have related discussions like review comments" {
		contextParts = append(contextParts, fmt.Sprintf("Related Discussion:\n%s", prData.RelatedDiscussion))
	}
	
	// Add git diff
	if prData.GitDiff != "" && prData.GitDiff != "Failed to fetch git diff" {
		contextParts = append(contextParts, fmt.Sprintf("Git Diff:\n%s", prData.GitDiff))
	}

	if len(contextParts) > 0 {
		args["Context"] = strings.Join(contextParts, "\n\n")
	}

	// Load and process the builtin pr-response fragment
	fragmentConfig := &fragments.Config{
		FragmentName: "pr-response",
		Arguments:    args,
	}

	fragment, err := processor.LoadFragment(ctx, fragmentConfig)
	if err != nil {
		return "", errors.Wrap(err, "failed to load pr-response fragment")
	}

	return fragment.Content, nil
}
