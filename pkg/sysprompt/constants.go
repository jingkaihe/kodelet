package sysprompt

import "embed"

//go:embed templates/*
var TemplateFS embed.FS

const (
	ProductName = "kodelet"

	TodoWriteTool = "todo_write"
	TodoReadTool  = "todo_read"
	BashTool      = "bash"
	SubagentTool  = "subagent"
	GrepTool      = "grep_tool"
	GlobTool      = "glob_tool"
	Backtick      = "`"

	AgentsMd  = "AGENTS.md"
	KodeletMd = "KODELET.md"
	ReadmeMd  = "README.md"

	// Template paths for Anthropic provider
	SystemTemplate   = "templates/system.tmpl"
	SubagentTemplate = "templates/subagent.tmpl"

	// Template path for OpenAI provider (embedded)
	OpenAITemplate = "templates/openai_prompt.md"

	// Provider constants
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)
