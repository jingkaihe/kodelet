package main

import (
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/llmstxt"
	"github.com/stretchr/testify/assert"
)

func TestLlmstxtCommand(t *testing.T) {
	// Get the content
	content := llmstxt.GetContent()

	// Verify content is not empty
	assert.NotEmpty(t, content, "llms.txt content should not be empty")

	// Verify content starts with expected header
	assert.True(t, strings.HasPrefix(content, "# Kodelet - LLM-Friendly Guide"),
		"llms.txt should start with the correct header")

	// Verify content contains key sections
	assert.Contains(t, content, "## Quick Start", "llms.txt should contain Quick Start section")
	assert.Contains(t, content, "## Core Usage Modes", "llms.txt should contain Core Usage Modes section")
	assert.Contains(t, content, "## Key Features", "llms.txt should contain Key Features section")
	assert.Contains(t, content, "## Configuration", "llms.txt should contain Configuration section")
	assert.Contains(t, content, "## LLM Providers", "llms.txt should contain LLM Providers section")
	assert.Contains(t, content, "## Advanced Features", "llms.txt should contain Advanced Features section")
	assert.Contains(t, content, "## Security & Best Practices", "llms.txt should contain Security section")
	assert.Contains(t, content, "## Troubleshooting", "llms.txt should contain Troubleshooting section")

	// Verify specific important topics are covered
	assert.Contains(t, content, "kodelet run", "llms.txt should document run command")
	assert.Contains(t, content, "kodelet chat", "llms.txt should document chat command")
	assert.Contains(t, content, "AGENTS.md", "llms.txt should mention agent context files")
	assert.Contains(t, content, "Fragments/Recipes", "llms.txt should mention fragments")
	assert.Contains(t, content, "Custom Tools", "llms.txt should mention custom tools")
	assert.Contains(t, content, "MCP Integration", "llms.txt should mention MCP")
}
