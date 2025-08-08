package sysprompt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
)

// PromptContext holds all variables for template rendering
type PromptContext struct {
	// System info
	WorkingDirectory string
	IsGitRepo        bool
	Platform         string
	OSVersion        string
	Date             string

	// Tool names
	ToolNames map[string]string

	// Content contexts (README, AGENT.md/KODELET.md)
	ContextFiles map[string]string
	
	// Active context file name (AGENT.md, KODELET.md, or empty)
	ActiveContextFile string

	// Feature flags
	Features map[string]bool

	// Bash tool configuration
	BashBannedCommands  []string
	BashAllowedCommands []string
}

// NewPromptContext creates a new PromptContext with default values
func NewPromptContext() *PromptContext {
	pwd, _ := os.Getwd()
	isGitRepo := checkIsGitRepo(pwd)
	platform := runtime.GOOS
	osVersion := getOSVersion()
	date := time.Now().Format("2006-01-02")

	// Initialize tool names
	toolNames := map[string]string{
		"todo_write": "todo_write",
		"todo_read":  "todo_read",
		"bash":       "bash",
		"subagent":   "subagent",
		"grep":       "grep_tool",
		"glob":       "glob_tool",
	}

	// Initialize feature flags
	features := map[string]bool{
		"subagentEnabled":  true,
		"todoToolsEnabled": true,
	}

	// Get active context file name
	activeContextFile := getContextFileName()
	if activeContextFile == "" {
		// Default to AGENT.md for new projects
		activeContextFile = AgentMd
	}

	return &PromptContext{
		WorkingDirectory:    pwd,
		IsGitRepo:           isGitRepo,
		Platform:            platform,
		OSVersion:           osVersion,
		Date:                date,
		ToolNames:           toolNames,
		ContextFiles:        loadContexts(),
		ActiveContextFile:   activeContextFile,
		Features:            features,
		BashBannedCommands:  tools.BannedCommands,
		BashAllowedCommands: []string{}, // Empty by default, can be set via configuration
	}
}

// getContextFileName returns the name of the context file to use
// It checks for AGENT.md first, then falls back to KODELET.md
func getContextFileName() string {
	ctx := context.Background()
	log := logger.G(ctx)
	
	// Check for AGENT.md first
	if _, err := os.Stat(AgentMd); err == nil {
		log.WithField("context_file", AgentMd).Debug("Using AGENT.md as context file")
		return AgentMd
	}
	
	// Fall back to KODELET.md
	if _, err := os.Stat(KodeletMd); err == nil {
		log.WithField("context_file", KodeletMd).Debug("Using KODELET.md as context file (fallback)")
		return KodeletMd
	}
	
	log.Debug("No context file found (neither AGENT.md nor KODELET.md)")
	return ""
}

// loadContexts loads context files (AGENT.md/KODELET.md, README.md) from disk
func loadContexts() map[string]string {
	results := make(map[string]string)
	ctx := context.Background()
	log := logger.G(ctx)
	
	// Load the primary context file (AGENT.md or KODELET.md)
	contextFile := getContextFileName()
	if contextFile != "" {
		content, err := os.ReadFile(contextFile)
		if err != nil {
			log.WithError(err).WithField("filename", contextFile).Debug("failed to read context file")
		} else {
			results[contextFile] = string(content)
			log.WithField("filename", contextFile).WithField("size", len(content)).Debug("Loaded context file")
		}
	}
	
	// Load README.md
	if content, err := os.ReadFile(ReadmeMd); err == nil {
		results[ReadmeMd] = string(content)
		log.WithField("filename", ReadmeMd).WithField("size", len(content)).Debug("Loaded README.md")
	} else {
		log.WithError(err).WithField("filename", ReadmeMd).Debug("failed to read README.md")
	}
	
	return results
}

// FormatContexts formats the loaded contexts into a string
func (ctx *PromptContext) FormatContexts() string {
	if len(ctx.ContextFiles) == 0 {
		return ""
	}

	prompt := "\nHere are some useful context to help you solve the user's problem:\n"
	for filename, content := range ctx.ContextFiles {
		prompt += fmt.Sprintf(`
<context filename="%s">
%s
</context>
`, filename, content)
	}
	return prompt
}

// FormatSystemInfo formats the system information into a string
func (ctx *PromptContext) FormatSystemInfo() string {
	return fmt.Sprintf(`
# System Information
Here is the system information:
<system-information>
Current working directory: %s
Is this a git repository? %v
Operating system: %s %s
Date: %s
</system-information>
`, ctx.WorkingDirectory, ctx.IsGitRepo, ctx.Platform, ctx.OSVersion, ctx.Date)
}

// checkIsGitRepo checks if the given directory is a git repository
func checkIsGitRepo(dir string) bool {
	_, err := os.Stat(dir + "/.git")
	return err == nil
}

// getOSVersion returns the OS version string
func getOSVersion() string {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("sw_vers", "-productVersion")
		out, err := cmd.Output()
		if err == nil {
			return "macOS " + strings.TrimSpace(string(out))
		}
	case "linux":
		cmd := exec.Command("uname", "-r")
		out, err := cmd.Output()
		if err == nil {
			return "Linux " + strings.TrimSpace(string(out))
		}
	case "windows":
		cmd := exec.Command("cmd", "/c", "ver")
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return runtime.GOOS
}
