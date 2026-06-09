package sysprompt

import (
	"os"
	"path/filepath"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderRuntimeSections(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		assert.Nil(t, RenderRuntimeSections(nil, NewRenderer(TemplateFS)))
	})

	t.Run("renders all runtime sections", func(t *testing.T) {
		ctx := &PromptContext{
			WorkingDirectory: "/workspace/project",
			IsGitRepo:        true,
			Platform:         "linux",
			OSVersion:        "Linux 6.18.16",
			Date:             "2026-05-23",
			ContextFiles: map[string]string{
				"/workspace/project/AGENTS.md": "# Project instructions",
			},
			MCPExecutionMode: "code",
			MCPWorkspaceDir:  "/tmp/mcp-workspace",
			MCPServers:       []string{"grafana", "playwright"},
		}

		sections := RenderRuntimeSections(ctx, NewRenderer(TemplateFS))

		require.Len(t, sections, 3)
		assert.Contains(t, sections[0], "# System Information")
		assert.Contains(t, sections[0], "Current working directory: /workspace/project")
		assert.Contains(t, sections[0], "Is this a git repository? true")
		assert.Contains(t, sections[0], "Date: 2026-05-23")
		assert.Contains(t, sections[1], `<context filename="/workspace/project/AGENTS.md", dir="/workspace/project">`)
		assert.Contains(t, sections[1], "# Project instructions")
		assert.Contains(t, sections[2], "# MCP Servers Available")
		assert.Contains(t, sections[2], "grafana, playwright")
		assert.Contains(t, sections[2], "/tmp/mcp-workspace/servers/")
	})

	t.Run("nil renderer falls back to default renderer", func(t *testing.T) {
		ctx := &PromptContext{
			WorkingDirectory: "/workspace/project",
			Platform:         "linux",
			OSVersion:        "Linux test",
			Date:             "2026-05-23",
			ContextFiles:     map[string]string{},
		}

		sections := RenderRuntimeSections(ctx, nil)

		require.Len(t, sections, 3)
		assert.Contains(t, sections[0], "Current working directory: /workspace/project")
		assert.Empty(t, sections[1])
		assert.Empty(t, sections[2])
	})
}

func TestBuildRuntimeContext(t *testing.T) {
	workingDir := t.TempDir()
	mcpWorkspace := t.TempDir()
	serverDir := filepath.Join(mcpWorkspace, "servers", "local")
	require.NoError(t, os.MkdirAll(serverDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "index.ts"), []byte("export {};"), 0o644))

	contexts := map[string]string{
		filepath.Join(workingDir, "README.md"): "# README",
	}
	config := llmtypes.Config{
		WorkingDirectory: workingDir,
		Context: &llmtypes.ContextConfig{
			Patterns: []string{"README.md", "AGENTS.md"},
		},
		MCPExecutionMode:    "code",
		MCPWorkspaceDir:     mcpWorkspace,
		SyspromptArgs:       map[string]string{"project": "kodelet"},
		EnableFSSearchTools: true,
	}

	ctx := BuildRuntimeContext(config, contexts)

	assert.Equal(t, workingDir, ctx.WorkingDirectory)
	assert.Equal(t, "README.md", ctx.ActiveContextFile)
	assert.Equal(t, config.SyspromptArgs, ctx.Args)
	assert.True(t, ctx.EnableFSSearchTools)
	assert.Equal(t, "code", ctx.MCPExecutionMode)
	assert.Equal(t, mcpWorkspace, ctx.MCPWorkspaceDir)
	assert.ElementsMatch(t, []string{"local"}, ctx.MCPServers)
}

func TestResolveRendererForConfig(t *testing.T) {
	t.Run("default renderer", func(t *testing.T) {
		renderer, err := ResolveRendererForConfig(llmtypes.Config{})
		require.NoError(t, err)
		require.NotNil(t, renderer)

		prompt, err := renderer.RenderSystemPrompt(&PromptContext{})
		require.NoError(t, err)
		assert.Contains(t, prompt, "You are an interactive CLI tool")
	})

	t.Run("custom renderer", func(t *testing.T) {
		tmplPath := filepath.Join(t.TempDir(), "custom.tmpl")
		require.NoError(t, os.WriteFile(tmplPath, []byte("custom {{.WorkingDirectory}}"), 0o644))

		renderer, err := ResolveRendererForConfig(llmtypes.Config{Sysprompt: tmplPath})
		require.NoError(t, err)

		rendered, err := renderer.RenderSystemPrompt(&PromptContext{WorkingDirectory: "/work"})
		require.NoError(t, err)
		assert.Equal(t, "custom /work", rendered)
	})
}

func TestRenderSectionWithRendererFailures(t *testing.T) {
	ctx := &PromptContext{}

	assert.Empty(t, ctx.renderSectionWithRenderer(nil, "templates/sections/runtime_system_info.tmpl"))
	assert.Empty(t, ctx.renderSectionWithRenderer(NewRenderer(TemplateFS), "templates/sections/missing.tmpl"))
}
