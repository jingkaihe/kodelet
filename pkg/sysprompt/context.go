package sysprompt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/tools"
)

// PromptContext holds all variables for template rendering
type PromptContext struct {
	WorkingDirectory string
	IsGitRepo        bool
	Platform         string
	OSVersion        string
	Date             string

	ToolNames map[string]string

	// Content contexts (README, AGENTS.md)
	ContextFiles map[string]string

	// Active context file name (resolved from configured patterns)
	ActiveContextFile string

	Features map[string]bool

	BashBannedCommands  []string
	BashAllowedCommands []string

	// MCP tools information
	MCPExecutionMode string
	MCPServers       []string // List of available MCP server names
}

// NewPromptContext creates a new PromptContext with default values
func NewPromptContext(contexts map[string]string) *PromptContext {
	pwd, _ := os.Getwd()
	isGitRepo := checkIsGitRepo(pwd)
	platform := runtime.GOOS
	osVersion := getOSVersion()
	date := time.Now().Format("2006-01-02")

	toolNames := map[string]string{
		"todo_write": "todo_write",
		"todo_read":  "todo_read",
		"bash":       "bash",
		"subagent":   "subagent",
		"grep":       "grep_tool",
		"glob":       "glob_tool",
	}

	features := map[string]bool{
		"subagentEnabled":  true,
		"todoToolsEnabled": true,
	}

	// Use provided contexts or initialize empty map
	contextFiles := contexts
	if contextFiles == nil {
		contextFiles = make(map[string]string)
	}

	return &PromptContext{
		WorkingDirectory:    pwd,
		IsGitRepo:           isGitRepo,
		Platform:            platform,
		OSVersion:           osVersion,
		Date:                date,
		ToolNames:           toolNames,
		ContextFiles:        contextFiles,
		ActiveContextFile:   AgentsMd,
		Features:            features,
		BashBannedCommands:  tools.BannedCommands,
		BashAllowedCommands: []string{},
		MCPExecutionMode:    "",
		MCPServers:          []string{},
	}
}

// WithMCPConfig adds MCP configuration to the prompt context
func (ctx *PromptContext) WithMCPConfig(executionMode, workspaceDir string) *PromptContext {
	ctx.MCPExecutionMode = executionMode
	if executionMode == "code" && workspaceDir != "" {
		ctx.MCPServers = loadMCPServers(workspaceDir)
	}
	return ctx
}

// FormatContexts formats the loaded contexts into a string
func (ctx *PromptContext) FormatContexts() string {
	if len(ctx.ContextFiles) == 0 {
		return ""
	}

	ctxFiles := []string{}

	prompt := `Here are some useful context to help you solve the user's problem.
When you are working in these directories, make sure that you are following the guidelines provided in the context.
Note that the contexts in $HOME/.kodelet/ are universally applicable.
`
	for filename, content := range ctx.ContextFiles {
		ctxFiles = append(ctxFiles, filename)
		dir := filepath.Dir(filename)
		prompt += fmt.Sprintf(`
<context filename="%s", dir="%s">
%s
</context>
`, filename, dir, content)
	}

	logger.G(context.Background()).WithField("context_files", ctxFiles).Debug("loaded context files")
	return prompt
}

// ResolveActiveContextFile selects the best context file name based on configured patterns
// and the contexts that were actually loaded.
func ResolveActiveContextFile(workingDir string, contexts map[string]string, patterns []string) string {
	if len(patterns) == 0 {
		return AgentsMd
	}

	if workingDir != "" && len(contexts) > 0 {
		for _, pattern := range patterns {
			if _, ok := contexts[filepath.Join(workingDir, pattern)]; ok {
				return pattern
			}
		}
	}

	if len(contexts) > 0 {
		loaded := make(map[string]struct{}, len(contexts))
		for path := range contexts {
			loaded[filepath.Base(path)] = struct{}{}
		}
		for _, pattern := range patterns {
			if _, ok := loaded[pattern]; ok {
				return pattern
			}
		}
	}

	return patterns[0]
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

// loadMCPServers reads the MCP servers directory and returns a list of server names
func loadMCPServers(workspaceDir string) []string {
	ctx := context.Background()
	log := logger.G(ctx)

	var servers []string

	if workspaceDir == "" {
		return servers
	}

	serversDir := filepath.Join(workspaceDir, "servers")

	// Check if servers directory exists
	if _, err := os.Stat(serversDir); os.IsNotExist(err) {
		log.WithField("servers_dir", serversDir).Debug("MCP servers directory does not exist")
		return servers
	}

	// Read server directories
	entries, err := os.ReadDir(serversDir)
	if err != nil {
		log.WithError(err).WithField("servers_dir", serversDir).Warn("Failed to read MCP servers directory")
		return servers
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		serverName := entry.Name()
		indexFile := filepath.Join(serversDir, serverName, "index.ts")

		// Check if index.ts exists
		if _, err := os.Stat(indexFile); os.IsNotExist(err) {
			continue
		}

		servers = append(servers, serverName)
		log.WithField("server", serverName).Debug("Found MCP server")
	}

	return servers
}

// FormatMCPServers formats the MCP servers information into a string
func (ctx *PromptContext) FormatMCPServers() string {
	if ctx.MCPExecutionMode != "code" || len(ctx.MCPServers) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n# MCP Servers Available\n")
	sb.WriteString("When using MCP tools in 'code' execution mode, the following MCP servers are available:\n\n")

	sb.WriteString(strings.Join(ctx.MCPServers, ", "))
	sb.WriteString("\n\nYou can import and use tools from these servers in your TypeScript code within the bash tool. Check the generated TypeScript API in .kodelet/mcp/servers/ for available functions.\n")

	return sb.String()
}
