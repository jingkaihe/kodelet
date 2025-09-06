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

	// Content contexts (README, AGENTS.md/KODELET.md)
	ContextFiles map[string]string

	// Active context file name (AGENTS.md, KODELET.md, or empty)
	ActiveContextFile string

	Features map[string]bool

	BashBannedCommands  []string
	BashAllowedCommands []string
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
		ActiveContextFile:   getContextFileName(),
		Features:            features,
		BashBannedCommands:  tools.BannedCommands,
		BashAllowedCommands: []string{},
	}
}

// getContextFileName returns the name of the context file to use
func getContextFileName() string {
	ctx := context.Background()
	log := logger.G(ctx)

	if _, err := os.Stat(AgentsMd); err == nil {
		log.WithField("context_file", AgentsMd).Debug("Using AGENTS.md as context file")
		return AgentsMd
	}

	if _, err := os.Stat(KodeletMd); err == nil {
		log.WithField("context_file", KodeletMd).Debug("Using KODELET.md as context file (fallback)")
		return KodeletMd
	}

	log.WithField("context_file", AgentsMd).Debug("No context file found, defaulting to AGENTS.md")
	return AgentsMd
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
