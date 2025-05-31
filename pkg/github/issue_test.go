package github

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// GitHubIssuesServiceInterface defines the interface for GitHub Issues service operations
type GitHubIssuesServiceInterface interface {
	Get(ctx context.Context, owner string, repo string, number int) (*github.Issue, *github.Response, error)
	ListComments(ctx context.Context, owner string, repo string, number int, opts *github.IssueListCommentsOptions) ([]*github.IssueComment, *github.Response, error)
}

// MockIssuesService mocks the GitHub Issues service
type MockIssuesService struct {
	mock.Mock
}

// Get mocks the Issues.Get method
func (m *MockIssuesService) Get(ctx context.Context, owner string, repo string, number int) (*github.Issue, *github.Response, error) {
	args := m.Called(ctx, owner, repo, number)
	if args.Get(0) == nil {
		return nil, args.Get(1).(*github.Response), args.Error(2)
	}
	return args.Get(0).(*github.Issue), args.Get(1).(*github.Response), args.Error(2)
}

// ListComments mocks the Issues.ListComments method
func (m *MockIssuesService) ListComments(ctx context.Context, owner string, repo string, number int, opts *github.IssueListCommentsOptions) ([]*github.IssueComment, *github.Response, error) {
	args := m.Called(ctx, owner, repo, number, opts)
	return args.Get(0).([]*github.IssueComment), args.Get(1).(*github.Response), args.Error(2)
}

// TestableIssueProcessor extends IssueProcessor to allow for dependency injection
type TestableIssueProcessor struct {
	*IssueProcessor
	issuesService GitHubIssuesServiceInterface
}

// NewTestableIssueProcessor creates a new testable issue processor
func NewTestableIssueProcessor(client *Client, issuesService GitHubIssuesServiceInterface) *TestableIssueProcessor {
	return &TestableIssueProcessor{
		IssueProcessor: &IssueProcessor{client: client},
		issuesService:  issuesService,
	}
}

// FetchAndProcess overrides the original method to use the injected service
func (p *TestableIssueProcessor) FetchAndProcess(ctx context.Context, issueURL string) (*IssueData, error) {
	// Parse the issue URL
	owner, repo, number, err := parseIssueURL(issueURL)
	if err != nil {
		return nil, err
	}

	// Fetch the issue using the injected service
	issue, _, err := p.issuesService.Get(ctx, owner, repo, number)
	if err != nil {
		return nil, err
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
		comments, err := p.fetchComments(ctx, owner, repo, number)
		if err != nil {
			// Log warning but continue without comments
		} else {
			issueData.IssueComments = comments
		}
	}

	return issueData, nil
}

// fetchComments uses the injected service
func (p *TestableIssueProcessor) fetchComments(ctx context.Context, owner, repo string, number int) ([]CommentData, error) {
	var allComments []CommentData

	// GitHub API pagination options
	opts := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{
			PerPage: 100, // Maximum per page
		},
	}

	for {
		comments, resp, err := p.issuesService.ListComments(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, err
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

// Helper function to create a mock GitHub issue
func createMockIssue() *github.Issue {
	createdAt := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC)

	return &github.Issue{
		ID:     github.Int64(123),
		Number: github.Int(456),
		Title:  github.String("Test Issue"),
		Body:   github.String("This is a test issue body"),
		State:  github.String("open"),
		User: &github.User{
			Login: github.String("testuser"),
		},
		URL:       github.String("https://api.github.com/repos/testowner/testrepo/issues/456"),
		HTMLURL:   github.String("https://github.com/testowner/testrepo/issues/456"),
		Comments:  github.Int(2),
		Locked:    github.Bool(false),
		CreatedAt: &github.Timestamp{Time: createdAt},
		UpdatedAt: &github.Timestamp{Time: updatedAt},
		Labels: []*github.Label{
			{Name: github.String("bug")},
			{Name: github.String("high-priority")},
		},
		Assignees: []*github.User{
			{Login: github.String("assignee1")},
			{Login: github.String("assignee2")},
		},
		Milestone: &github.Milestone{
			Title: github.String("v1.0.0"),
		},
	}
}

// Helper function to create mock comments
func createMockComments() []*github.IssueComment {
	createdAt := time.Date(2023, 1, 3, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2023, 1, 3, 12, 30, 0, 0, time.UTC)

	return []*github.IssueComment{
		{
			ID:   github.Int64(789),
			Body: github.String("First comment"),
			User: &github.User{
				Login: github.String("commenter1"),
			},
			CreatedAt: &github.Timestamp{Time: createdAt},
			UpdatedAt: &github.Timestamp{Time: createdAt},
			HTMLURL:   github.String("https://github.com/testowner/testrepo/issues/456#issuecomment-789"),
		},
		{
			ID:   github.Int64(790),
			Body: github.String("Second comment with update"),
			User: &github.User{
				Login: github.String("commenter2"),
			},
			CreatedAt: &github.Timestamp{Time: createdAt},
			UpdatedAt: &github.Timestamp{Time: updatedAt},
			HTMLURL:   github.String("https://github.com/testowner/testrepo/issues/456#issuecomment-790"),
		},
	}
}

func TestParseIssueURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		expectOwner string
		expectRepo  string
		expectNum   int
		expectError bool
	}{
		{
			name:        "Valid URL",
			url:         "https://github.com/owner/repo/issues/123",
			expectOwner: "owner",
			expectRepo:  "repo",
			expectNum:   123,
			expectError: false,
		},
		{
			name:        "Valid URL with trailing slash",
			url:         "https://github.com/owner/repo/issues/123/",
			expectOwner: "owner",
			expectRepo:  "repo",
			expectNum:   123,
			expectError: false,
		},
		{
			name:        "URL with hyphens and underscores",
			url:         "https://github.com/owner-name/repo_name/issues/456",
			expectOwner: "owner-name",
			expectRepo:  "repo_name",
			expectNum:   456,
			expectError: false,
		},
		{
			name:        "Invalid URL format",
			url:         "https://github.com/owner/repo/pull/123",
			expectError: true,
		},
		{
			name:        "Invalid domain",
			url:         "https://gitlab.com/owner/repo/issues/123",
			expectError: true,
		},
		{
			name:        "Invalid issue number",
			url:         "https://github.com/owner/repo/issues/abc",
			expectError: true,
		},
		{
			name:        "Missing issue number",
			url:         "https://github.com/owner/repo/issues/",
			expectError: true,
		},
		{
			name:        "Empty URL",
			url:         "",
			expectError: true,
		},
		{
			name:        "URL with spaces",
			url:         "  https://github.com/owner/repo/issues/123  ",
			expectOwner: "owner",
			expectRepo:  "repo",
			expectNum:   123,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, number, err := parseIssueURL(tt.url)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectOwner, owner)
				assert.Equal(t, tt.expectRepo, repo)
				assert.Equal(t, tt.expectNum, number)
			}
		})
	}
}

func TestNewIssueProcessor(t *testing.T) {
	client := &Client{}
	processor := NewIssueProcessor(client)

	assert.NotNil(t, processor)
	assert.Equal(t, client, processor.client)
}

func TestIssueProcessor_FetchAndProcess(t *testing.T) {
	// Create a mock client
	mockIssuesService := &MockIssuesService{}

	// Create the mock issue and comments
	mockIssue := createMockIssue()
	mockComments := createMockComments()

	// Set up expectations
	mockIssuesService.On("Get", mock.Anything, "testowner", "testrepo", 456).
		Return(mockIssue, &github.Response{}, nil)

	mockIssuesService.On("ListComments", mock.Anything, "testowner", "testrepo", 456, mock.AnythingOfType("*github.IssueListCommentsOptions")).
		Return(mockComments, &github.Response{NextPage: 0}, nil)

	// Create a testable processor with mocked service
	processor := NewTestableIssueProcessor(&Client{}, mockIssuesService)

	ctx := context.Background()
	url := "https://github.com/testowner/testrepo/issues/456"

	issueData, err := processor.FetchAndProcess(ctx, url)

	require.NoError(t, err)
	require.NotNil(t, issueData)

	// Verify the parsed data
	assert.Equal(t, "testowner", issueData.Owner)
	assert.Equal(t, "testrepo", issueData.Repo)
	assert.Equal(t, 456, issueData.Number)
	assert.Equal(t, "Test Issue", issueData.Title)
	assert.Equal(t, "This is a test issue body", issueData.Body)
	assert.Equal(t, "testuser", issueData.Author)
	assert.Equal(t, "open", issueData.State)
	assert.Equal(t, []string{"bug", "high-priority"}, issueData.Labels)
	assert.Equal(t, []string{"assignee1", "assignee2"}, issueData.Assignees)
	assert.Equal(t, "v1.0.0", issueData.Milestone)
	assert.Equal(t, 2, issueData.Comments)
	assert.False(t, issueData.Locked)
	assert.Equal(t, "https://api.github.com/repos/testowner/testrepo/issues/456", issueData.URL)
	assert.Equal(t, "https://github.com/testowner/testrepo/issues/456", issueData.HTMLURL)

	// Verify comments
	require.Len(t, issueData.IssueComments, 2)
	assert.Equal(t, int64(789), issueData.IssueComments[0].ID)
	assert.Equal(t, "commenter1", issueData.IssueComments[0].Author)
	assert.Equal(t, "First comment", issueData.IssueComments[0].Body)
	assert.Equal(t, int64(790), issueData.IssueComments[1].ID)
	assert.Equal(t, "commenter2", issueData.IssueComments[1].Author)
	assert.Equal(t, "Second comment with update", issueData.IssueComments[1].Body)

	// Verify mock expectations
	mockIssuesService.AssertExpectations(t)
}

func TestIssueProcessor_FetchAndProcess_InvalidURL(t *testing.T) {
	processor := &IssueProcessor{
		client: &Client{},
	}

	ctx := context.Background()
	url := "invalid-url"

	_, err := processor.FetchAndProcess(ctx, url)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid GitHub issue URL format")
}

func TestIssueProcessor_FetchAndProcess_APIError(t *testing.T) {
	mockIssuesService := &MockIssuesService{}

	// Set up expectation for API error
	mockIssuesService.On("Get", mock.Anything, "testowner", "testrepo", 456).
		Return((*github.Issue)(nil), (*github.Response)(nil), errors.New("API error"))

	processor := NewTestableIssueProcessor(&Client{}, mockIssuesService)

	ctx := context.Background()
	url := "https://github.com/testowner/testrepo/issues/456"

	_, err := processor.FetchAndProcess(ctx, url)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API error")

	mockIssuesService.AssertExpectations(t)
}

func TestIssueProcessor_WriteIssueFile(t *testing.T) {
	processor := &IssueProcessor{}

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	os.Chdir(tempDir)

	// Create test issue data
	issueData := &IssueData{
		Owner:     "testowner",
		Repo:      "testrepo",
		Number:    123,
		Title:     "Test Issue",
		Body:      "This is a test issue",
		Author:    "testuser",
		State:     "open",
		Labels:    []string{"bug", "enhancement"},
		HTMLURL:   "https://github.com/testowner/testrepo/issues/123",
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
	}

	err := processor.WriteIssueFile(issueData)
	require.NoError(t, err)

	// Verify the file was created
	content, err := os.ReadFile("ISSUE.md")
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "# Test Issue")
	assert.Contains(t, contentStr, "testowner/testrepo")
	assert.Contains(t, contentStr, "#123")
	assert.Contains(t, contentStr, "@testuser")
	assert.Contains(t, contentStr, "open")
	assert.Contains(t, contentStr, "bug, enhancement")
	assert.Contains(t, contentStr, "This is a test issue")
}

func TestIssueProcessor_WriteIssueJSON(t *testing.T) {
	processor := &IssueProcessor{}

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	os.Chdir(tempDir)

	// Create test issue data
	issueData := &IssueData{
		Owner:     "testowner",
		Repo:      "testrepo",
		Number:    123,
		Title:     "Test Issue",
		Body:      "This is a test issue",
		Author:    "testuser",
		State:     "open",
		Labels:    []string{"bug", "enhancement"},
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
	}

	err := processor.WriteIssueJSON(issueData)
	require.NoError(t, err)

	// Verify the .kodelet directory was created
	_, err = os.Stat(".kodelet")
	require.NoError(t, err)

	// Verify the JSON file was created
	content, err := os.ReadFile(filepath.Join(".kodelet", "issue.json"))
	require.NoError(t, err)

	// Parse and verify the JSON content
	var parsedData IssueData
	err = json.Unmarshal(content, &parsedData)
	require.NoError(t, err)

	assert.Equal(t, issueData.Owner, parsedData.Owner)
	assert.Equal(t, issueData.Repo, parsedData.Repo)
	assert.Equal(t, issueData.Number, parsedData.Number)
	assert.Equal(t, issueData.Title, parsedData.Title)
	assert.Equal(t, issueData.Labels, parsedData.Labels)
}

func TestIssueProcessor_FormatIssueAsMarkdown(t *testing.T) {
	processor := &IssueProcessor{}

	// Create test issue data with comments
	issueData := &IssueData{
		Owner:     "testowner",
		Repo:      "testrepo",
		Number:    123,
		Title:     "Test Issue",
		Body:      "This is a test issue body\nwith multiple lines",
		Author:    "testuser",
		State:     "open",
		Labels:    []string{"bug", "enhancement"},
		Assignees: []string{"assignee1", "assignee2"},
		Milestone: "v1.0.0",
		Comments:  2,
		Locked:    false,
		HTMLURL:   "https://github.com/testowner/testrepo/issues/123",
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
		IssueComments: []CommentData{
			{
				ID:        789,
				Author:    "commenter1",
				Body:      "First comment",
				CreatedAt: time.Date(2023, 1, 3, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, 1, 3, 12, 0, 0, 0, time.UTC),
				HTMLURL:   "https://github.com/testowner/testrepo/issues/123#comment-789",
			},
			{
				ID:        790,
				Author:    "commenter2",
				Body:      "Second comment",
				CreatedAt: time.Date(2023, 1, 4, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, 1, 4, 14, 0, 0, 0, time.UTC),
				HTMLURL:   "https://github.com/testowner/testrepo/issues/123#comment-790",
			},
		},
	}

	markdown := processor.formatIssueAsMarkdown(issueData)

	// Test header
	assert.Contains(t, markdown, "# Test Issue")

	// Test metadata section
	assert.Contains(t, markdown, "## Issue Information")
	assert.Contains(t, markdown, "**Repository:** testowner/testrepo")
	assert.Contains(t, markdown, "**Issue Number:** #123")
	assert.Contains(t, markdown, "**Author:** @testuser")
	assert.Contains(t, markdown, "**State:** open")
	assert.Contains(t, markdown, "**Labels:** bug, enhancement")
	assert.Contains(t, markdown, "**Assignees:** assignee1, assignee2")
	assert.Contains(t, markdown, "**Milestone:** v1.0.0")
	assert.Contains(t, markdown, "**Comments:** 2")
	assert.Contains(t, markdown, "**URL:** https://github.com/testowner/testrepo/issues/123")

	// Test description section
	assert.Contains(t, markdown, "## Description")
	assert.Contains(t, markdown, "This is a test issue body\nwith multiple lines")

	// Test comments section
	assert.Contains(t, markdown, "## Comments")
	assert.Contains(t, markdown, "### Comment #1 by @commenter1")
	assert.Contains(t, markdown, "First comment")
	assert.Contains(t, markdown, "### Comment #2 by @commenter2")
	assert.Contains(t, markdown, "Second comment")
	assert.Contains(t, markdown, "**Updated:** 2023-01-04 14:00:00")
}

func TestIssueProcessor_FormatIssueAsMarkdown_EmptyBody(t *testing.T) {
	processor := &IssueProcessor{}

	issueData := &IssueData{
		Owner:     "testowner",
		Repo:      "testrepo",
		Number:    123,
		Title:     "Test Issue",
		Body:      "", // Empty body
		Author:    "testuser",
		State:     "open",
		HTMLURL:   "https://github.com/testowner/testrepo/issues/123",
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
	}

	markdown := processor.formatIssueAsMarkdown(issueData)

	assert.Contains(t, markdown, "## Description")
	assert.Contains(t, markdown, "*No description provided.*")
}

func TestIssueProcessor_FormatIssueAsMarkdown_NoComments(t *testing.T) {
	processor := &IssueProcessor{}

	issueData := &IssueData{
		Owner:         "testowner",
		Repo:          "testrepo",
		Number:        123,
		Title:         "Test Issue",
		Body:          "Test body",
		Author:        "testuser",
		State:         "open",
		Comments:      0,
		HTMLURL:       "https://github.com/testowner/testrepo/issues/123",
		CreatedAt:     time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
		IssueComments: []CommentData{},
	}

	markdown := processor.formatIssueAsMarkdown(issueData)

	// Should not contain comments section
	assert.NotContains(t, markdown, "## Comments")
	assert.NotContains(t, markdown, "### Comment")
}

func TestIssueProcessor_FormatIssueAsMarkdown_CommentWithEmptyBody(t *testing.T) {
	processor := &IssueProcessor{}

	issueData := &IssueData{
		Owner:     "testowner",
		Repo:      "testrepo",
		Number:    123,
		Title:     "Test Issue",
		Body:      "Test body",
		Author:    "testuser",
		State:     "open",
		HTMLURL:   "https://github.com/testowner/testrepo/issues/123",
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
		IssueComments: []CommentData{
			{
				ID:        789,
				Author:    "commenter1",
				Body:      "", // Empty comment body
				CreatedAt: time.Date(2023, 1, 3, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, 1, 3, 12, 0, 0, 0, time.UTC),
				HTMLURL:   "https://github.com/testowner/testrepo/issues/123#comment-789",
			},
		},
	}

	markdown := processor.formatIssueAsMarkdown(issueData)

	assert.Contains(t, markdown, "## Comments")
	assert.Contains(t, markdown, "### Comment #1 by @commenter1")
	assert.Contains(t, markdown, "*No comment text.*")
}

// Test data structures for JSON marshaling/unmarshaling
func TestCommentData_JSONSerialization(t *testing.T) {
	comment := CommentData{
		ID:        123,
		Author:    "testuser",
		Body:      "Test comment",
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
		HTMLURL:   "https://github.com/owner/repo/issues/1#comment-123",
	}

	// Marshal to JSON
	data, err := json.Marshal(comment)
	require.NoError(t, err)

	// Unmarshal back
	var parsed CommentData
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, comment.ID, parsed.ID)
	assert.Equal(t, comment.Author, parsed.Author)
	assert.Equal(t, comment.Body, parsed.Body)
	assert.True(t, comment.CreatedAt.Equal(parsed.CreatedAt))
	assert.True(t, comment.UpdatedAt.Equal(parsed.UpdatedAt))
	assert.Equal(t, comment.HTMLURL, parsed.HTMLURL)
}

func TestIssueData_JSONSerialization(t *testing.T) {
	issue := IssueData{
		Owner:     "testowner",
		Repo:      "testrepo",
		Number:    123,
		Title:     "Test Issue",
		Body:      "Test body",
		Author:    "testuser",
		State:     "open",
		Labels:    []string{"bug", "enhancement"},
		Assignees: []string{"assignee1"},
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
		URL:       "https://api.github.com/repos/testowner/testrepo/issues/123",
		HTMLURL:   "https://github.com/testowner/testrepo/issues/123",
		Comments:  1,
		Locked:    false,
		Milestone: "v1.0.0",
		IssueComments: []CommentData{
			{
				ID:     789,
				Author: "commenter",
				Body:   "Comment body",
			},
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(issue)
	require.NoError(t, err)

	// Unmarshal back
	var parsed IssueData
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, issue.Owner, parsed.Owner)
	assert.Equal(t, issue.Repo, parsed.Repo)
	assert.Equal(t, issue.Number, parsed.Number)
	assert.Equal(t, issue.Title, parsed.Title)
	assert.Equal(t, issue.Labels, parsed.Labels)
	assert.Equal(t, issue.Assignees, parsed.Assignees)
	assert.Equal(t, issue.Milestone, parsed.Milestone)
	assert.Len(t, parsed.IssueComments, 1)
	assert.Equal(t, issue.IssueComments[0].ID, parsed.IssueComments[0].ID)
}

// Test edge cases and error scenarios
func TestIssueProcessor_FetchAndProcess_NoComments(t *testing.T) {
	mockIssuesService := &MockIssuesService{}

	// Create a mock issue with no comments
	mockIssue := createMockIssue()
	mockIssue.Comments = github.Int(0) // No comments

	mockIssuesService.On("Get", mock.Anything, "testowner", "testrepo", 456).
		Return(mockIssue, &github.Response{}, nil)

	processor := NewTestableIssueProcessor(&Client{}, mockIssuesService)

	ctx := context.Background()
	url := "https://github.com/testowner/testrepo/issues/456"

	issueData, err := processor.FetchAndProcess(ctx, url)

	require.NoError(t, err)
	require.NotNil(t, issueData)
	assert.Equal(t, 0, issueData.Comments)
	assert.Empty(t, issueData.IssueComments)

	mockIssuesService.AssertExpectations(t)
}

func TestIssueProcessor_FetchAndProcess_CommentsError(t *testing.T) {
	mockIssuesService := &MockIssuesService{}

	// Create a mock issue with comments
	mockIssue := createMockIssue()

	mockIssuesService.On("Get", mock.Anything, "testowner", "testrepo", 456).
		Return(mockIssue, &github.Response{}, nil)

	// Mock comments fetch to return an error
	mockIssuesService.On("ListComments", mock.Anything, "testowner", "testrepo", 456, mock.AnythingOfType("*github.IssueListCommentsOptions")).
		Return(([]*github.IssueComment)(nil), (*github.Response)(nil), errors.New("comments fetch error"))

	processor := NewTestableIssueProcessor(&Client{}, mockIssuesService)

	ctx := context.Background()
	url := "https://github.com/testowner/testrepo/issues/456"

	issueData, err := processor.FetchAndProcess(ctx, url)

	// Should still succeed but without comments
	require.NoError(t, err)
	require.NotNil(t, issueData)
	assert.Equal(t, 2, issueData.Comments)   // Original comment count from issue
	assert.Empty(t, issueData.IssueComments) // But no actual comment data

	mockIssuesService.AssertExpectations(t)
}

func TestIssueProcessor_FetchAndProcess_Pagination(t *testing.T) {
	mockIssuesService := &MockIssuesService{}

	// Create a mock issue with comments
	mockIssue := createMockIssue()

	// Create comments for pagination test
	firstPageComments := []*github.IssueComment{createMockComments()[0]}
	secondPageComments := []*github.IssueComment{createMockComments()[1]}

	mockIssuesService.On("Get", mock.Anything, "testowner", "testrepo", 456).
		Return(mockIssue, &github.Response{}, nil)

	// First page of comments
	mockIssuesService.On("ListComments", mock.Anything, "testowner", "testrepo", 456, mock.MatchedBy(func(opts *github.IssueListCommentsOptions) bool {
		return opts.Page == 0 // First call
	})).Return(firstPageComments, &github.Response{NextPage: 2}, nil)

	// Second page of comments
	mockIssuesService.On("ListComments", mock.Anything, "testowner", "testrepo", 456, mock.MatchedBy(func(opts *github.IssueListCommentsOptions) bool {
		return opts.Page == 2 // Second call
	})).Return(secondPageComments, &github.Response{NextPage: 0}, nil)

	processor := NewTestableIssueProcessor(&Client{}, mockIssuesService)

	ctx := context.Background()
	url := "https://github.com/testowner/testrepo/issues/456"

	issueData, err := processor.FetchAndProcess(ctx, url)

	require.NoError(t, err)
	require.NotNil(t, issueData)
	require.Len(t, issueData.IssueComments, 2)

	// Verify both comments are present
	assert.Equal(t, int64(789), issueData.IssueComments[0].ID)
	assert.Equal(t, int64(790), issueData.IssueComments[1].ID)

	mockIssuesService.AssertExpectations(t)
}

func TestIssueProcessor_FetchAndProcess_IssueWithoutMilestone(t *testing.T) {
	mockIssuesService := &MockIssuesService{}

	// Create a mock issue without milestone
	mockIssue := createMockIssue()
	mockIssue.Milestone = nil // No milestone

	mockIssuesService.On("Get", mock.Anything, "testowner", "testrepo", 456).
		Return(mockIssue, &github.Response{}, nil)

	mockIssuesService.On("ListComments", mock.Anything, "testowner", "testrepo", 456, mock.AnythingOfType("*github.IssueListCommentsOptions")).
		Return(createMockComments(), &github.Response{NextPage: 0}, nil)

	processor := NewTestableIssueProcessor(&Client{}, mockIssuesService)

	ctx := context.Background()
	url := "https://github.com/testowner/testrepo/issues/456"

	issueData, err := processor.FetchAndProcess(ctx, url)

	require.NoError(t, err)
	require.NotNil(t, issueData)
	assert.Empty(t, issueData.Milestone)

	mockIssuesService.AssertExpectations(t)
}

func TestIssueProcessor_WriteIssueJSON_DirectoryCreationError(t *testing.T) {
	processor := &IssueProcessor{}

	// Create a temporary directory and change to it
	tempDir := t.TempDir()
	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd)
	os.Chdir(tempDir)

	// Create a file named .kodelet to prevent directory creation
	err := os.WriteFile(".kodelet", []byte("blocking file"), 0644)
	require.NoError(t, err)

	issueData := &IssueData{
		Owner:  "testowner",
		Repo:   "testrepo",
		Number: 123,
		Title:  "Test Issue",
	}

	err = processor.WriteIssueJSON(issueData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create .kodelet directory")
}

func TestIssueProcessor_FormatIssueAsMarkdown_MultipleCommentsSeparator(t *testing.T) {
	processor := &IssueProcessor{}

	issueData := &IssueData{
		Owner:     "testowner",
		Repo:      "testrepo",
		Number:    123,
		Title:     "Test Issue",
		Body:      "Test body",
		Author:    "testuser",
		State:     "open",
		HTMLURL:   "https://github.com/testowner/testrepo/issues/123",
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 2, 12, 0, 0, 0, time.UTC),
		IssueComments: []CommentData{
			{
				ID:        789,
				Author:    "commenter1",
				Body:      "First comment",
				CreatedAt: time.Date(2023, 1, 3, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, 1, 3, 12, 0, 0, 0, time.UTC),
			},
			{
				ID:        790,
				Author:    "commenter2",
				Body:      "Second comment",
				CreatedAt: time.Date(2023, 1, 4, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, 1, 4, 12, 0, 0, 0, time.UTC),
			},
			{
				ID:        791,
				Author:    "commenter3",
				Body:      "Third comment",
				CreatedAt: time.Date(2023, 1, 5, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, 1, 5, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	markdown := processor.formatIssueAsMarkdown(issueData)

	// Check that separators exist between comments (but not after the last one)
	assert.Contains(t, markdown, "### Comment #1 by @commenter1")
	assert.Contains(t, markdown, "### Comment #2 by @commenter2")
	assert.Contains(t, markdown, "### Comment #3 by @commenter3")

	// Count occurrences of separator
	separatorCount := 0
	for _, line := range []string{"---"} {
		for i := 0; i < len(markdown)-len(line); i++ {
			if markdown[i:i+len(line)] == line {
				separatorCount++
			}
		}
	}
	assert.Equal(t, 2, separatorCount) // Should have 2 separators for 3 comments
}
