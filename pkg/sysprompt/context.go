package sysprompt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
)

// PromptContext holds all variables for template rendering
type PromptContext struct {
	WorkingDirectory string
	IsGitRepo        bool
	Platform         string
	OSVersion        string
	Date             string

	// Content contexts (README, AGENTS.md)
	ContextFiles map[string]string

	// Active context file name (resolved from configured patterns)
	ActiveContextFile string
	Args              map[string]string

	// MCP tools information
	MCPExecutionMode string
	MCPServers       []string // List of available MCP server names
}

type contextEntry struct {
	Filename string
	Dir      string
	Content  string
}

// newPromptContext creates a new PromptContext with default values.
func newPromptContext(contexts map[string]string) *PromptContext {
	pwd, _ := os.Getwd()
	isGitRepo := checkIsGitRepo(pwd)
	platform := runtime.GOOS
	osVersion := getOSVersion()
	date := time.Now().Format("2006-01-02")

	// Use provided contexts or initialize empty map
	contextFiles := contexts
	if contextFiles == nil {
		contextFiles = make(map[string]string)
	}

	return &PromptContext{
		WorkingDirectory:  pwd,
		IsGitRepo:         isGitRepo,
		Platform:          platform,
		OSVersion:         osVersion,
		Date:              date,
		ContextFiles:      contextFiles,
		ActiveContextFile: AgentsMd,
		Args:              map[string]string{},
		MCPExecutionMode:  "",
		MCPServers:        []string{},
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

func (ctx *PromptContext) contextEntries() []contextEntry {
	if len(ctx.ContextFiles) == 0 {
		return nil
	}

	entries := make([]contextEntry, 0, len(ctx.ContextFiles))
	ctxFiles := make([]string, 0, len(ctx.ContextFiles))
	sortedFilenames := make([]string, 0, len(ctx.ContextFiles))
	for filename := range ctx.ContextFiles {
		sortedFilenames = append(sortedFilenames, filename)
	}
	sort.Strings(sortedFilenames)

	for _, filename := range sortedFilenames {
		ctxFiles = append(ctxFiles, filename)
		entries = append(entries, contextEntry{
			Filename: filename,
			Dir:      filepath.Dir(filename),
			Content:  ctx.ContextFiles[filename],
		})
	}

	logger.G(context.Background()).WithField("context_files", ctxFiles).Debug("loaded context files")

	return entries
}

func (ctx *PromptContext) hasContextEntries() bool {
	return len(ctx.ContextFiles) > 0
}

func (ctx *PromptContext) hasMCPServers() bool {
	return ctx.MCPExecutionMode == "code" && len(ctx.MCPServers) > 0
}

func (ctx *PromptContext) mcpServersCSV() string {
	return strings.Join(ctx.MCPServers, ", ")
}

func (ctx *PromptContext) formatContexts() string {
	return ctx.formatContextsWithRenderer(defaultRenderer)
}

func resolveActiveContextFile(workingDir string, contexts map[string]string, patterns []string) string {
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

func (ctx *PromptContext) formatMCPServers() string {
	return ctx.formatMCPServersWithRenderer(defaultRenderer)
}

func (ctx *PromptContext) renderSectionWithRenderer(renderer *Renderer, templateName string) string {
	if renderer == nil {
		logger.G(context.Background()).WithField("template", templateName).Warn("failed to render sysprompt context section")
		return ""
	}

	rendered, err := renderer.RenderPrompt(templateName, ctx)
	if err != nil {
		logger.G(context.Background()).WithError(err).WithField("template", templateName).Warn("failed to render sysprompt context section")
		return ""
	}
	if strings.TrimSpace(rendered) == "" {
		return ""
	}
	return rendered
}

func (ctx *PromptContext) formatSystemInfoWithRenderer(renderer *Renderer) string {
	return ctx.renderSectionWithRenderer(renderer, "templates/sections/runtime_system_info.tmpl")
}

func (ctx *PromptContext) formatContextsWithRenderer(renderer *Renderer) string {
	return ctx.renderSectionWithRenderer(renderer, "templates/sections/runtime_loaded_contexts.tmpl")
}

func (ctx *PromptContext) formatMCPServersWithRenderer(renderer *Renderer) string {
	return ctx.renderSectionWithRenderer(renderer, "templates/sections/runtime_mcp_servers.tmpl")
}
