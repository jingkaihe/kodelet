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

// PRRespondTemplateData holds data for the PR respond prompt template
type PRRespondTemplateData struct {
	BinPath         string
	PRURL           string
	CommentID       string
	PRData          *PRData
	FocusedSections string
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
	Short: "Respond to a specific PR comment with code changes",
	Long: `Respond to a specific pull request comment by analyzing the feedback and implementing the requested changes.

This command focuses on addressing a specific comment or review feedback within a PR. You must provide either --review-id for review comments or --issue-comment-id for issue comments. Currently supports GitHub PRs only.`,
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

		// Generate comprehensive prompt with prefetched data
		prompt := generatePRRespondPrompt(bin, config.PRURL, commentID, prData)
		logger.G(ctx).WithField("prompt_length", len(prompt)).Debug("Generated PR respond prompt")

		// Send to LLM using existing architecture
		presenter.Info("Analyzing specific PR comment and implementing response...")
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

// formatFocusedSections creates the focused sections format used in prompts
func formatFocusedSections(comment, discussion string) string {
	return fmt.Sprintf(`
<pr_focused_comment>
	<pr_comment>
%s
	</pr_comment>

	<pr_discussions>
%s
	</pr_discussions>
</pr_focused_comment>
`,
		comment, discussion)
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

const prRespondPromptTemplate = `Here is the information for pull request {{.PRURL}}:

<pr_basic_info>
{{.PRData.BasicInfo}}
</pr_basic_info>

<git_diff>
{{.PRData.GitDiff}}
</git_diff>

{{.FocusedSections}}

Please respond to the comment and discussions in <pr_focused_comment> section following the steps below:

1. Check the current state of the PR branch:
   - Use "git checkout <pr-branch>" to switch to the PR branch
   - Run "git pull origin <pr-branch>" to ensure latest changes
   - Check current working directory state

2. Analyze the specific comment request:
   - Review the PR comments section above to understand exactly what is being asked for
   - Determine if it requires code changes, documentation, tests, or clarification
   - Create a focused todo list for this specific request
   - If the request is unclear, ask for clarification in your comment response, do not implement any changes

3. Implement the specific change:
   - Focus only on what was requested in the comment
   - Make precise, targeted changes
   - Avoid scope creep or unrelated improvements

4. Respond appropriately:
   - Make necessary code changes if requested
	 - Make sure that you run 'git add ...' to add the changes to the staging area before you commit.
   - Ask subagent to run "{{.BinPath}} commit --short --no-confirm" for changes
   - Push updates with "git push origin <pr-branch>"
   - Reply to the specific comment with a summary of actions taken using "gh pr comment <pr-number> --body <summary>". Keep the summary short, concise and to the point.

IMPORTANT:
- !!!CRITICAL!!!: You should never update user's git config under any circumstances.
- Focus ONLY on the specific comment/request - don't address other feedback
- Be precise and targeted in your response
- If the request is unclear, ask for clarification in your comment response
- Always acknowledge the specific comment you're responding to
`

func generatePRRespondPrompt(bin, prURL, commentID string, prData *PRData) string {
	focusedSections := formatFocusedSections(prData.FocusedComment, prData.RelatedDiscussion)

	data := PRRespondTemplateData{
		BinPath:         bin,
		PRURL:           prURL,
		CommentID:       commentID,
		PRData:          prData,
		FocusedSections: focusedSections,
	}

	tmpl, err := template.New("prRespond").Parse(prRespondPromptTemplate)
	if err != nil {
		// Fallback to the old approach if template parsing fails
		return fmt.Sprintf("Error parsing template: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		// Fallback to the old approach if template execution fails
		return fmt.Sprintf("Error executing template: %v", err)
	}

	return buf.String()
}
