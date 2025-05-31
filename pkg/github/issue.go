package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/jingkaihe/kodelet/pkg/logger"
)

// CommentData represents a GitHub issue comment
type CommentData struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	HTMLURL   string    `json:"html_url"`
}

// IssueData represents the structured data of a GitHub issue
type IssueData struct {
	Owner         string        `json:"owner"`
	Repo          string        `json:"repo"`
	Number        int           `json:"number"`
	Title         string        `json:"title"`
	Body          string        `json:"body"`
	Author        string        `json:"author"`
	State         string        `json:"state"`
	Labels        []string      `json:"labels"`
	Assignees     []string      `json:"assignees"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	URL           string        `json:"url"`
	HTMLURL       string        `json:"html_url"`
	Comments      int           `json:"comments"`
	Locked        bool          `json:"locked"`
	Milestone     string        `json:"milestone,omitempty"`
	IssueComments []CommentData `json:"issue_comments"`
}

// IssueProcessor handles fetching and processing GitHub issues
type IssueProcessor struct {
	client *Client
}

// NewIssueProcessor creates a new issue processor
func NewIssueProcessor(client *Client) *IssueProcessor {
	return &IssueProcessor{
		client: client,
	}
}

// parseIssueURL extracts owner, repo, and issue number from a GitHub issue URL
func parseIssueURL(issueURL string) (owner, repo string, number int, err error) {
	// Regex to match GitHub issue URLs
	re := regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/issues/(\d+)/?$`)
	matches := re.FindStringSubmatch(strings.TrimSpace(issueURL))
	
	if len(matches) != 4 {
		return "", "", 0, fmt.Errorf("invalid GitHub issue URL format: %s", issueURL)
	}
	
	owner = matches[1]
	repo = matches[2]
	number, err = strconv.Atoi(matches[3])
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid issue number: %s", matches[3])
	}
	
	return owner, repo, number, nil
}

// FetchAndProcess fetches a GitHub issue and converts it to IssueData
func (p *IssueProcessor) FetchAndProcess(ctx context.Context, issueURL string) (*IssueData, error) {
	log := logger.G(ctx)
	
	// Parse the issue URL
	owner, repo, number, err := parseIssueURL(issueURL)
	if err != nil {
		return nil, err
	}
	
	log.WithFields(map[string]interface{}{
		"owner":  owner,
		"repo":   repo,
		"number": number,
	}).Info("Fetching GitHub issue")
	
	// Fetch the issue
	issue, _, err := p.client.client.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch issue: %w", err)
	}
	
	// Convert to IssueData
	issueData := &IssueData{
		Owner:     owner,
		Repo:      repo,
		Number:    number,
		Title:     issue.GetTitle(),
		Body:      issue.GetBody(),
		Author:    issue.GetUser().GetLogin(),
		State:     issue.GetState(),
		URL:       issue.GetURL(),
		HTMLURL:   issue.GetHTMLURL(),
		Comments:  issue.GetComments(),
		Locked:    issue.GetLocked(),
		CreatedAt: issue.GetCreatedAt().Time,
		UpdatedAt: issue.GetUpdatedAt().Time,
	}
	
	// Extract labels
	for _, label := range issue.Labels {
		issueData.Labels = append(issueData.Labels, label.GetName())
	}
	
	// Extract assignees
	for _, assignee := range issue.Assignees {
		issueData.Assignees = append(issueData.Assignees, assignee.GetLogin())
	}
	
	// Extract milestone if present
	if milestone := issue.GetMilestone(); milestone != nil {
		issueData.Milestone = milestone.GetTitle()
	}
	
	// Fetch comments if any exist
	if issue.GetComments() > 0 {
		log.WithField("comment_count", issue.GetComments()).Info("Fetching issue comments")
		comments, err := p.fetchComments(ctx, owner, repo, number)
		if err != nil {
			log.WithError(err).Warn("Failed to fetch comments, continuing without them")
		} else {
			issueData.IssueComments = comments
			log.WithField("comments_fetched", len(comments)).Info("Successfully fetched issue comments")
		}
	}
	
	log.WithField("title", issueData.Title).Info("Successfully fetched GitHub issue")
	return issueData, nil
}

// fetchComments fetches all comments for a GitHub issue
func (p *IssueProcessor) fetchComments(ctx context.Context, owner, repo string, number int) ([]CommentData, error) {
	var allComments []CommentData
	
	// GitHub API pagination options
	opts := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{
			PerPage: 100, // Maximum per page
		},
	}
	
	for {
		comments, resp, err := p.client.client.Issues.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch comments: %w", err)
		}
		
		// Convert GitHub comments to our CommentData format
		for _, comment := range comments {
			commentData := CommentData{
				ID:        comment.GetID(),
				Author:    comment.GetUser().GetLogin(),
				Body:      comment.GetBody(),
				CreatedAt: comment.GetCreatedAt().Time,
				UpdatedAt: comment.GetUpdatedAt().Time,
				HTMLURL:   comment.GetHTMLURL(),
			}
			allComments = append(allComments, commentData)
		}
		
		// Check if there are more pages
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	
	return allComments, nil
}

// WriteIssueFile creates an ISSUE.md file with the formatted issue content
func (p *IssueProcessor) WriteIssueFile(issueData *IssueData) error {
	content := p.formatIssueAsMarkdown(issueData)
	
	if err := os.WriteFile("ISSUE.md", []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write ISSUE.md: %w", err)
	}
	
	return nil
}

// WriteIssueJSON creates a .kodelet/issue.json file with the issue metadata
func (p *IssueProcessor) WriteIssueJSON(issueData *IssueData) error {
	// Ensure .kodelet directory exists
	if err := os.MkdirAll(".kodelet", 0755); err != nil {
		return fmt.Errorf("failed to create .kodelet directory: %w", err)
	}
	
	// Write JSON metadata
	jsonData, err := json.MarshalIndent(issueData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal issue data: %w", err)
	}
	
	jsonPath := filepath.Join(".kodelet", "issue.json")
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write issue.json: %w", err)
	}
	
	return nil
}

// formatIssueAsMarkdown formats the issue data as a readable markdown file
func (p *IssueProcessor) formatIssueAsMarkdown(issue *IssueData) string {
	var sb strings.Builder
	
	// Header
	sb.WriteString(fmt.Sprintf("# %s\n\n", issue.Title))
	
	// Metadata
	sb.WriteString("## Issue Information\n\n")
	sb.WriteString(fmt.Sprintf("- **Repository:** %s/%s\n", issue.Owner, issue.Repo))
	sb.WriteString(fmt.Sprintf("- **Issue Number:** #%d\n", issue.Number))
	sb.WriteString(fmt.Sprintf("- **Author:** @%s\n", issue.Author))
	sb.WriteString(fmt.Sprintf("- **State:** %s\n", issue.State))
	sb.WriteString(fmt.Sprintf("- **Created:** %s\n", issue.CreatedAt.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("- **Updated:** %s\n", issue.UpdatedAt.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("- **URL:** %s\n", issue.HTMLURL))
	
	// Labels
	if len(issue.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("- **Labels:** %s\n", strings.Join(issue.Labels, ", ")))
	}
	
	// Assignees
	if len(issue.Assignees) > 0 {
		sb.WriteString(fmt.Sprintf("- **Assignees:** %s\n", strings.Join(issue.Assignees, ", ")))
	}
	
	// Milestone
	if issue.Milestone != "" {
		sb.WriteString(fmt.Sprintf("- **Milestone:** %s\n", issue.Milestone))
	}
	
	// Comments count
	if issue.Comments > 0 {
		sb.WriteString(fmt.Sprintf("- **Comments:** %d\n", issue.Comments))
	}
	
	sb.WriteString("\n")
	
	// Issue Body
	sb.WriteString("## Description\n\n")
	if issue.Body != "" {
		sb.WriteString(issue.Body)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("*No description provided.*\n\n")
	}
	
	// Comments section
	if len(issue.IssueComments) > 0 {
		sb.WriteString("## Comments\n\n")
		for i, comment := range issue.IssueComments {
			// Comment header
			sb.WriteString(fmt.Sprintf("### Comment #%d by @%s\n\n", i+1, comment.Author))
			sb.WriteString(fmt.Sprintf("**Posted:** %s  \n", comment.CreatedAt.Format("2006-01-02 15:04:05")))
			if !comment.UpdatedAt.Equal(comment.CreatedAt) {
				sb.WriteString(fmt.Sprintf("**Updated:** %s  \n", comment.UpdatedAt.Format("2006-01-02 15:04:05")))
			}
			sb.WriteString(fmt.Sprintf("**URL:** %s\n\n", comment.HTMLURL))
			
			// Comment body
			if comment.Body != "" {
				sb.WriteString(comment.Body)
				sb.WriteString("\n\n")
			} else {
				sb.WriteString("*No comment text.*\n\n")
			}
			
			// Add separator between comments (except for the last one)
			if i < len(issue.IssueComments)-1 {
				sb.WriteString("---\n\n")
			}
		}
	}
	
	return sb.String()
}