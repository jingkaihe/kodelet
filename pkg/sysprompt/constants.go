// Package sysprompt renders system prompts from embedded templates.
package sysprompt

import "embed"

// TemplateFS contains the embedded template files for system prompts.
//
//go:embed templates/*.tmpl templates/sections/*.tmpl
var TemplateFS embed.FS

const (
	// ProductName is the name of the product used in prompts and documentation.
	ProductName = "kodelet"

	// TodoWriteTool is the identifier for the todo write tool.
	TodoWriteTool = "todo_write"
	// TodoReadTool is the identifier for the todo read tool.
	TodoReadTool = "todo_read"
	// BashTool is the identifier for the bash command execution tool.
	BashTool = "bash"
	// SubagentTool is the identifier for the subagent tool.
	SubagentTool = "subagent"
	// GrepTool is the identifier for the grep search tool.
	GrepTool = "grep_tool"
	// GlobTool is the identifier for the glob file matching tool.
	GlobTool = "glob_tool"
	// Backtick is the backtick character used in markdown formatting.
	Backtick = "`"

	// AgentsMd is the filename for agent-specific documentation.
	AgentsMd = "AGENTS.md"
	// ReadmeMd is the filename for README documentation.
	ReadmeMd = "README.md"

	// SystemTemplate is the path to the main system prompt template.
	SystemTemplate = "templates/system.tmpl"
	// CodexTemplate is the path to the Codex-specific system prompt template.
	CodexTemplate = "templates/codex.tmpl"
)
