package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/mcp/runtime"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCodeExecutionTool_MetadataAndValidation(t *testing.T) {
	tool := NewCodeExecutionTool(nil)
	assert.Equal(t, "code_execution", tool.Name())
	require.NotNil(t, tool.GenerateSchema())
	assert.NoError(t, tool.ValidateInput(nil, `{}`))
	kvs, err := tool.TracingKVs(`{"code_path":"script.ts"}`)
	require.NoError(t, err)
	assert.Empty(t, kvs)

	result := &CodeExecutionResult{code: "console.log('hi')", output: "hi\n", runtime: "node-tsx"}
	assert.False(t, result.IsError())
	assert.Equal(t, "hi\n", result.GetResult())
	assert.Empty(t, result.GetError())
	assert.Contains(t, result.AssistantFacing(), "hi")

	structured := result.StructuredData()
	assert.Equal(t, "code_execution", structured.ToolName)
	assert.True(t, structured.Success)
	var meta tooltypes.CodeExecutionMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &meta))
	assert.Equal(t, "node-tsx", meta.Runtime)

	errResult := &CodeExecutionResult{err: "failed", runtime: "node-tsx"}
	assert.True(t, errResult.IsError())
	assert.Equal(t, "failed", errResult.GetError())
	assert.Contains(t, errResult.AssistantFacing(), "failed")
}

func TestCodeExecutionTool_ExecuteInputErrors(t *testing.T) {
	workspace := t.TempDir()
	tool := NewCodeExecutionTool(runtime.NewNodeRuntime(workspace, ""))
	state := NewBasicState(context.Background())

	invalidJSON := tool.Execute(context.Background(), state, `{`)
	require.True(t, invalidJSON.IsError())
	assert.Contains(t, invalidJSON.GetError(), "invalid parameters")

	for _, params := range []string{
		`{"code_path":"/tmp/script.ts"}`,
		`{"code_path":"../script.ts"}`,
		`{"code_path":"."}`,
	} {
		result := tool.Execute(context.Background(), state, params)
		require.True(t, result.IsError())
		assert.Contains(t, result.GetError(), "invalid code_path")
	}

	missingFile := tool.Execute(context.Background(), state, `{"code_path":"missing.ts"}`)
	require.True(t, missingFile.IsError())
	assert.Contains(t, missingFile.GetError(), "failed to read file")
}

func TestCodeExecutionTool_ExecuteSuccessIfNodeAvailable(t *testing.T) {
	workspace := t.TempDir()
	scriptPath := filepath.Join(workspace, "script.js")
	require.NoError(t, os.WriteFile(scriptPath, []byte("console.log('hello from code execution')\n"), 0o644))

	tool := NewCodeExecutionTool(runtime.NewNodeRuntime(workspace, ""))
	result := tool.Execute(context.Background(), NewBasicState(context.Background()), `{"code_path":"script.js"}`)
	if result.IsError() && (strings.Contains(result.GetError(), "npx") || strings.Contains(result.GetError(), "executable file not found")) {
		t.Skip("npx/tsx is not available in this environment")
	}

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "hello from code execution")
}
