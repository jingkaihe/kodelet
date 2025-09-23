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

	SystemTemplate   = "templates/system.tmpl"
	SubagentTemplate = "templates/subagent.tmpl"

	OpenAITemplate = "templates/openai_system.tmpl"

	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)
