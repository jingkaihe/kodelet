package sysprompt

import "embed"

// Template files
//
//go:embed templates/*
var TemplateFS embed.FS

const (
	// Product name
	ProductName = "kodelet"

	// Tool names
	TodoWriteTool = "todo_write"
	TodoReadTool  = "todo_read"
	BashTool      = "bash"
	SubagentTool  = "subagent"
	GrepTool      = "grep_tool"
	GlobTool      = "glob_tool"
	Backtick      = "`"

	// Context file names
	AgentsMd  = "AGENTS.md"
	KodeletMd = "KODELET.md"
	ReadmeMd  = "README.md"

	// Template file paths
	SystemTemplate   = "templates/system.tmpl"
	SubagentTemplate = "templates/subagent.tmpl"
)
