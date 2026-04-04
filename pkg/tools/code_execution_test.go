package tools

import (
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
)

func TestCodeExecutionTool_Description(t *testing.T) {
	t.Run("full mode mentions file tools", func(t *testing.T) {
		tool := NewCodeExecutionToolWithOptions(nil, llmtypes.ToolModeFull, false)
		desc := tool.Description()

		assert.Contains(t, desc, "Read generated files (`index.ts` + tool files) to get exact schemas.")
		assert.Contains(t, desc, "using `file_write` / `file_edit` / `apply_patch`.")
		assert.Contains(t, desc, "file_read /absolute/path/to/mcp-workspace/servers/lsp/index.ts")
		assert.Contains(t, desc, "file_write /absolute/path/to/mcp-workspace/check_diagnostics.ts")
	})

	t.Run("patch uses bash inspection and apply_patch only", func(t *testing.T) {
		tool := NewCodeExecutionToolWithOptions(nil, llmtypes.ToolModePatch, false)
		desc := tool.Description()

		assert.Contains(t, desc, "Inspect generated files using shell commands such as `sed`, `cat`, or `rg` via the `bash` tool")
		assert.Contains(t, desc, "Create or update scripts in the MCP code workspace using `apply_patch` only.")
		assert.Contains(t, desc, "Use `apply_patch` for all script edits in this mode.")
		assert.Contains(t, desc, "bash: (cd /absolute/path/to/mcp-workspace && sed -n '1,120p' servers/lsp/index.ts && sed -n '1,160p' servers/lsp/diagnostics.ts)")
		assert.Contains(t, desc, "apply_patch to create /absolute/path/to/mcp-workspace/check_diagnostics.ts")
		assert.NotContains(t, desc, "file_write /absolute/path/to/mcp-workspace/check_diagnostics.ts")
	})
}
