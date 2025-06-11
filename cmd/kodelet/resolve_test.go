package main

import (
	"strings"
	"testing"
)

func TestGenerateIssueResolutionPrompt(t *testing.T) {
	issueURL := "https://github.com/owner/repo/issues/123"
	bin := "kodelet"
	botMention := "@kodelet"
	prompt := generateIssueResolutionPrompt(bin, issueURL, botMention)

	// Test that the prompt contains the issue URL in the right places
	if !strings.Contains(prompt, issueURL) {
		t.Errorf("Expected prompt to contain issue URL %s", issueURL)
	}

	// Test that the prompt contains dual workflow detection
	workflowKeywords := []string{
		"IMPLEMENTATION ISSUE",
		"QUESTION ISSUE",
		"Determine the issue type",
		"Choose the Appropriate Workflow",
	}

	for _, keyword := range workflowKeywords {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("Expected prompt to contain workflow keyword: %s", keyword)
		}
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
		if !strings.Contains(prompt, step) {
			t.Errorf("Expected prompt to contain implementation step: %s", step)
		}
	}

	// Test that the prompt contains question workflow steps
	questionSteps := []string{
		"Understand the question by reading issue comments",
		"Research the codebase",
		"comment directly on the issue with your answer",
		"Do NOT create branches, make code changes, or create pull requests",
	}

	for _, step := range questionSteps {
		if !strings.Contains(prompt, step) {
			t.Errorf("Expected prompt to contain question step: %s", step)
		}
	}

	// Test that the critical warning is included
	if !strings.Contains(prompt, "!!!CRITICAL!!!") {
		t.Error("Expected prompt to contain critical warning about git config")
	}

	// Test that the bot mention is included
	if !strings.Contains(prompt, botMention) {
		t.Errorf("Expected prompt to contain bot mention %s", botMention)
	}

	// Test that examples are included
	exampleKeywords := []string{
		"<example>",
		"</example>",
		"Add user authentication middleware",
		"How does the logging system work",
		"Fix memory leak in worker pool",
	}

	for _, keyword := range exampleKeywords {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("Expected prompt to contain example keyword: %s", keyword)
		}
	}
}

func TestGenerateIssueResolutionPromptWithCustomBotMention(t *testing.T) {
	issueURL := "https://github.com/owner/repo/issues/456"
	bin := "kodelet"
	customBotMention := "@mybot"
	prompt := generateIssueResolutionPrompt(bin, issueURL, customBotMention)

	// Test that the custom bot mention is included
	if !strings.Contains(prompt, customBotMention) {
		t.Errorf("Expected prompt to contain custom bot mention %s", customBotMention)
	}

	// Test that the prompt still contains dual workflow functionality
	if !strings.Contains(prompt, "IMPLEMENTATION ISSUE") {
		t.Error("Expected prompt to contain implementation workflow even with custom bot mention")
	}

	if !strings.Contains(prompt, "QUESTION ISSUE") {
		t.Error("Expected prompt to contain question workflow even with custom bot mention")
	}
}

func TestIssueResolveConfigDefaults(t *testing.T) {
	config := NewIssueResolveConfig()

	if config.Provider != GitHubProvider {
		t.Errorf("Expected default provider to be %s, got %s", GitHubProvider, config.Provider)
	}

	if config.BotMention != DefaultBotMention {
		t.Errorf("Expected default bot mention to be %s, got %s", DefaultBotMention, config.BotMention)
	}

	if config.IssueURL != "" {
		t.Errorf("Expected default issue URL to be empty, got %s", config.IssueURL)
	}
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
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %s", err.Error())
				}
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
		if !strings.Contains(prompt, cmd) {
			t.Errorf("Expected prompt to contain command with custom binary path: %s", cmd)
		}
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

	if !strings.Contains(prompt, implementationSection) {
		t.Error("Expected prompt to contain implementation workflow section")
	}

	if !strings.Contains(prompt, questionSection) {
		t.Error("Expected prompt to contain question workflow section")
	}

	// Test that the sections appear in the correct order
	implIndex := strings.Index(prompt, implementationSection)
	questionIndex := strings.Index(prompt, questionSection)

	if implIndex == -1 || questionIndex == -1 {
		t.Error("Both workflow sections should be present")
	} else if implIndex >= questionIndex {
		t.Error("Implementation workflow section should appear before question workflow section")
	}
}
