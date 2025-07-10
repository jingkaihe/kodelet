package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateIssueResolutionPrompt(t *testing.T) {
	issueURL := "https://github.com/owner/repo/issues/123"
	bin := "kodelet"
	botMention := "@kodelet"
	prompt := generateIssueResolutionPrompt(bin, issueURL, botMention)

	// Test that the prompt contains the issue URL in the right places
	assert.Contains(t, prompt, issueURL, "Expected prompt to contain issue URL")

	// Test that the prompt contains dual workflow detection
	workflowKeywords := []string{
		"IMPLEMENTATION ISSUE",
		"QUESTION ISSUE",
		"Determine the issue type",
		"Choose the Appropriate Workflow",
	}

	for _, keyword := range workflowKeywords {
		assert.Contains(t, prompt, keyword, "Expected prompt to contain workflow keyword: %s", keyword)
	}

	// Test that the prompt contains the required implementation steps
	implementationSteps := []string{
		"gh issue view",
		"git checkout -b kodelet/issue-",
		"commit --short --no-confirm",
		"pr",
		"Comment on the issue with the PR link",
	}

	for _, step := range implementationSteps {
		assert.Contains(t, prompt, step, "Expected prompt to contain implementation step: %s", step)
	}

	// Test that the prompt contains question workflow steps
	questionSteps := []string{
		"Understand the question by reading issue comments",
		"Research the codebase",
		"comment directly on the issue with your answer",
		"Do NOT create branches, make code changes, or create pull requests",
	}

	for _, step := range questionSteps {
		assert.Contains(t, prompt, step, "Expected prompt to contain question step: %s", step)
	}

	// Test that the critical warning is included
	assert.Contains(t, prompt, "!!!CRITICAL!!!", "Expected prompt to contain critical warning about git config")

	// Test that the bot mention is included
	assert.Contains(t, prompt, botMention, "Expected prompt to contain bot mention")

	// Test that examples are included
	exampleKeywords := []string{
		"<example>",
		"</example>",
		"Add user authentication middleware",
		"How does the logging system work",
		"Fix memory leak in worker pool",
	}

	for _, keyword := range exampleKeywords {
		assert.Contains(t, prompt, keyword, "Expected prompt to contain example keyword: %s", keyword)
	}
}

func TestGenerateIssueResolutionPromptWithCustomBotMention(t *testing.T) {
	issueURL := "https://github.com/owner/repo/issues/456"
	bin := "kodelet"
	customBotMention := "@mybot"
	prompt := generateIssueResolutionPrompt(bin, issueURL, customBotMention)

	// Test that the custom bot mention is included
	assert.Contains(t, prompt, customBotMention, "Expected prompt to contain custom bot mention")

	// Test that the prompt still contains dual workflow functionality
	assert.Contains(t, prompt, "IMPLEMENTATION ISSUE", "Expected prompt to contain implementation workflow even with custom bot mention")

	assert.Contains(t, prompt, "QUESTION ISSUE", "Expected prompt to contain question workflow even with custom bot mention")
}

func TestIssueResolveConfigDefaults(t *testing.T) {
	config := NewIssueResolveConfig()

	assert.Equal(t, GitHubProvider, config.Provider, "Expected default provider to be %s", GitHubProvider)

	assert.Equal(t, DefaultBotMention, config.BotMention, "Expected default bot mention to be %s", DefaultBotMention)

	assert.Empty(t, config.IssueURL, "Expected default issue URL to be empty")
}

func TestIssueResolveConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      *IssueResolveConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: &IssueResolveConfig{
				Provider:   GitHubProvider,
				IssueURL:   "https://github.com/owner/repo/issues/123",
				BotMention: "@kodelet",
			},
			expectError: false,
		},
		{
			name: "invalid provider",
			config: &IssueResolveConfig{
				Provider:   "gitlab",
				IssueURL:   "https://github.com/owner/repo/issues/123",
				BotMention: "@kodelet",
			},
			expectError: true,
			errorMsg:    "unsupported provider",
		},
		{
			name: "empty issue URL",
			config: &IssueResolveConfig{
				Provider:   GitHubProvider,
				IssueURL:   "",
				BotMention: "@kodelet",
			},
			expectError: true,
			errorMsg:    "issue URL cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError {
				require.Error(t, err, "Expected error but got none")
				assert.Contains(t, err.Error(), tt.errorMsg, "Expected error to contain '%s'", tt.errorMsg)
			} else {
				assert.NoError(t, err, "Expected no error but got: %v", err)
			}
		})
	}
}

func TestPromptContainsBinaryPath(t *testing.T) {
	issueURL := "https://github.com/owner/repo/issues/123"
	customBin := "/custom/path/to/kodelet"
	botMention := "@kodelet"
	prompt := generateIssueResolutionPrompt(customBin, issueURL, botMention)

	// Test that the custom binary path is used in subagent commands
	expectedCommands := []string{
		customBin + " commit --short --no-confirm",
		customBin + " pr",
	}

	for _, cmd := range expectedCommands {
		assert.Contains(t, prompt, cmd, "Expected prompt to contain command with custom binary path: %s", cmd)
	}
}

func TestPromptWorkflowSeparation(t *testing.T) {
	issueURL := "https://github.com/owner/repo/issues/123"
	bin := "kodelet"
	botMention := "@kodelet"
	prompt := generateIssueResolutionPrompt(bin, issueURL, botMention)

	// Test that implementation and question workflows are clearly separated
	implementationSection := "### For IMPLEMENTATION ISSUES (Feature/Fix/Code Changes):"
	questionSection := "### For QUESTION ISSUES (Information/Clarification):"

	assert.Contains(t, prompt, implementationSection, "Expected prompt to contain implementation workflow section")

	assert.Contains(t, prompt, questionSection, "Expected prompt to contain question workflow section")

	// Test that the sections appear in the correct order
	implIndex := strings.Index(prompt, implementationSection)
	questionIndex := strings.Index(prompt, questionSection)

	require.NotEqual(t, -1, implIndex, "Implementation workflow section should be present")
	require.NotEqual(t, -1, questionIndex, "Question workflow section should be present")
	assert.Less(t, implIndex, questionIndex, "Implementation workflow section should appear before question workflow section")
}
