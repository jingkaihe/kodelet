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

	// Test that the prompt contains the required steps
	expectedSteps := []string{
		"gh issue view",
		"git checkout -b kodelet/issue-",
		"kodelet commit --short",
		"kodelet pr",
		"comment on the issue",
	}

	for _, step := range expectedSteps {
		if !strings.Contains(prompt, step) {
			t.Errorf("Expected prompt to contain step: %s", step)
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

	// Test that the default @kodelet is NOT included when using custom mention
	if strings.Contains(prompt, "@kodelet") {
		t.Error("Expected prompt to not contain default @kodelet when using custom bot mention")
	}
}
