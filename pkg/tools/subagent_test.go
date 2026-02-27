package tools

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestSubAgentTool_BasicMethods(t *testing.T) {
	tool := NewSubAgentTool(nil, false)

	assert.Equal(t, "subagent", tool.Name())
	assert.NotNil(t, tool.GenerateSchema())
	assert.Contains(t, tool.Description(), "delegate tasks to a sub-agent")
}

func TestSubAgentTool_DescriptionWithWorkflows(t *testing.T) {
	workflows := map[string]*fragments.Fragment{
		"test-workflow": {
			ID: "test-workflow",
			Metadata: fragments.Metadata{
				Name:        "Test Workflow",
				Description: "A test workflow for testing",
				Arguments: map[string]fragments.ArgumentMeta{
					"arg1": {
						Description: "First argument",
						Default:     "default1",
					},
					"arg2": {
						Description: "Second argument",
					},
				},
			},
		},
	}

	t.Run("workflows disabled", func(t *testing.T) {
		tool := NewSubAgentTool(workflows, false)
		desc := tool.Description()
		assert.Contains(t, desc, "<no_workflows_available />")
		assert.NotContains(t, desc, "test-workflow")
	})

	t.Run("workflows enabled", func(t *testing.T) {
		tool := NewSubAgentTool(workflows, true)
		desc := tool.Description()
		assert.Contains(t, desc, "<workflows>")
		assert.Contains(t, desc, `<workflow name="test-workflow">`)
		assert.Contains(t, desc, "<description>A test workflow for testing</description>")
		assert.Contains(t, desc, `<argument name="arg1" default="default1">First argument</argument>`)
		assert.Contains(t, desc, `<argument name="arg2">Second argument</argument>`)
		assert.Contains(t, desc, "</workflow>")
		assert.Contains(t, desc, "</workflows>")
	})

	t.Run("no workflows", func(t *testing.T) {
		tool := NewSubAgentTool(nil, true)
		desc := tool.Description()
		assert.Contains(t, desc, "<no_workflows_available />")
	})
}

func TestSubAgentTool_ValidateInput(t *testing.T) {
	tool := NewSubAgentTool(nil, false)
	state := NewBasicState(context.TODO())

	// Valid inputs
	err := tool.ValidateInput(state, `{"question": "test"}`)
	assert.NoError(t, err)

	// Invalid inputs - empty question without workflow
	err = tool.ValidateInput(state, `{"question": ""}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "question is required when workflow is not specified")

	// Invalid inputs - args without workflow
	err = tool.ValidateInput(state, `{"question": "test", "args": {"key": "value"}}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "args can only be used with a workflow")
}

func TestSubAgentTool_ValidateInputWithWorkflows(t *testing.T) {
	workflows := map[string]*fragments.Fragment{
		"valid-workflow": {
			ID: "valid-workflow",
			Metadata: fragments.Metadata{
				Name:        "Valid Workflow",
				Description: "A valid workflow",
			},
		},
	}
	tool := NewSubAgentTool(workflows, true)
	state := NewBasicState(context.TODO())

	t.Run("valid workflow with question", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test", "workflow": "valid-workflow"}`)
		assert.NoError(t, err)
	})

	t.Run("valid workflow without question", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"workflow": "valid-workflow"}`)
		assert.NoError(t, err)
	})

	t.Run("invalid workflow", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test", "workflow": "invalid-workflow"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown workflow 'invalid-workflow'")
	})

	t.Run("no workflow specified", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test"}`)
		assert.NoError(t, err)
	})
}

func TestSubAgentTool_ValidateInputCwd(t *testing.T) {
	tool := NewSubAgentTool(nil, false)
	state := NewBasicState(context.TODO())

	t.Run("valid absolute cwd", func(t *testing.T) {
		// Use /tmp which exists on all Unix systems
		err := tool.ValidateInput(state, `{"question": "test", "cwd": "/tmp"}`)
		assert.NoError(t, err)
	})

	t.Run("invalid relative cwd", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test", "cwd": "relative/path"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cwd must be an absolute path")
	})

	t.Run("invalid cwd with dot prefix", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test", "cwd": "./relative/path"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cwd must be an absolute path")
	})

	t.Run("empty cwd is allowed", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test", "cwd": ""}`)
		assert.NoError(t, err)
	})

	t.Run("no cwd is allowed", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test"}`)
		assert.NoError(t, err)
	})

	t.Run("non-existent cwd directory", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test", "cwd": "/nonexistent/path/that/does/not/exist"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cwd directory does not exist")
	})

	t.Run("cwd is a file not a directory", func(t *testing.T) {
		// /etc/hosts is a file that exists on all Unix systems
		err := tool.ValidateInput(state, `{"question": "test", "cwd": "/etc/hosts"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cwd is not a directory")
	})
}

func TestSubAgentTool_TracingKVs(t *testing.T) {
	tool := NewSubAgentTool(nil, false)

	t.Run("with question only", func(t *testing.T) {
		kvs, err := tool.TracingKVs(`{"question": "test question"}`)
		assert.NoError(t, err)
		expected := []attribute.KeyValue{
			attribute.String("question", "test question"),
		}
		assert.Equal(t, expected, kvs)
	})

	t.Run("with question and workflow", func(t *testing.T) {
		kvs, err := tool.TracingKVs(`{"question": "test question", "workflow": "test-workflow"}`)
		assert.NoError(t, err)
		expected := []attribute.KeyValue{
			attribute.String("question", "test question"),
			attribute.String("workflow", "test-workflow"),
		}
		assert.Equal(t, expected, kvs)
	})

	t.Run("with workflow only", func(t *testing.T) {
		kvs, err := tool.TracingKVs(`{"workflow": "test-workflow"}`)
		assert.NoError(t, err)
		expected := []attribute.KeyValue{
			attribute.String("workflow", "test-workflow"),
		}
		assert.Equal(t, expected, kvs)
	})

	t.Run("with cwd", func(t *testing.T) {
		kvs, err := tool.TracingKVs(`{"question": "test", "cwd": "/tmp/project"}`)
		assert.NoError(t, err)
		expected := []attribute.KeyValue{
			attribute.String("question", "test"),
			attribute.String("cwd", "/tmp/project"),
		}
		assert.Equal(t, expected, kvs)
	})
}

func TestSubAgentToolResult_Methods(t *testing.T) {
	// Test successful result
	result := &SubAgentToolResult{
		result:   "success",
		question: "test q",
	}

	assert.Equal(t, "success", result.GetResult())
	assert.Empty(t, result.GetError())
	assert.False(t, result.IsError())
	assert.Contains(t, result.AssistantFacing(), "success")

	// Test error result
	errorResult := &SubAgentToolResult{
		err:      "error",
		question: "test q",
	}

	assert.Empty(t, errorResult.GetResult())
	assert.Equal(t, "error", errorResult.GetError())
	assert.True(t, errorResult.IsError())
	assert.Contains(t, errorResult.AssistantFacing(), "error")
}

func TestSubAgentToolResult_StructuredData(t *testing.T) {
	t.Run("successful result with all fields", func(t *testing.T) {
		result := &SubAgentToolResult{
			result:   "test response",
			question: "test question",
			workflow: "github/pr",
			cwd:      "/home/user/project",
		}

		structuredData := result.StructuredData()
		assert.Equal(t, "subagent", structuredData.ToolName)
		assert.True(t, structuredData.Success)

		var metadata tooltypes.SubAgentMetadata
		assert.True(t, tooltypes.ExtractMetadata(structuredData.Metadata, &metadata))
		assert.Equal(t, "test question", metadata.Question)
		assert.Equal(t, "test response", metadata.Response)
		assert.Equal(t, "github/pr", metadata.Workflow)
		assert.Equal(t, "/home/user/project", metadata.Cwd)
	})

	t.Run("successful result without workflow and cwd", func(t *testing.T) {
		result := &SubAgentToolResult{
			result:   "test response",
			question: "test question",
		}

		structuredData := result.StructuredData()
		assert.Equal(t, "subagent", structuredData.ToolName)
		assert.True(t, structuredData.Success)

		var metadata tooltypes.SubAgentMetadata
		assert.True(t, tooltypes.ExtractMetadata(structuredData.Metadata, &metadata))
		assert.Equal(t, "test question", metadata.Question)
		assert.Equal(t, "test response", metadata.Response)
		assert.Empty(t, metadata.Workflow)
		assert.Empty(t, metadata.Cwd)
	})

	t.Run("error result includes workflow and cwd", func(t *testing.T) {
		errorResult := &SubAgentToolResult{
			err:      "some error",
			question: "test question",
			workflow: "commit",
			cwd:      "/tmp/project",
		}

		errorStructuredData := errorResult.StructuredData()
		assert.Equal(t, "subagent", errorStructuredData.ToolName)
		assert.False(t, errorStructuredData.Success)
		assert.Equal(t, "some error", errorStructuredData.Error)

		var errorMetadata tooltypes.SubAgentMetadata
		assert.True(t, tooltypes.ExtractMetadata(errorStructuredData.Metadata, &errorMetadata))
		assert.Equal(t, "test question", errorMetadata.Question)
		assert.Empty(t, errorMetadata.Response)
		assert.Equal(t, "commit", errorMetadata.Workflow)
		assert.Equal(t, "/tmp/project", errorMetadata.Cwd)
	})
}

func TestBuildSubagentArgs(t *testing.T) {
	ctx := context.Background()

	t.Run("basic args without subagent_args", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, "", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"What is foo?",
		}, args)
	})

	t.Run("with --use-weak-model", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, "--use-weak-model", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"What is foo?",
		}, args)
	})

	t.Run("with --profile flag", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, "--profile cheap", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "cheap",
			"What is foo?",
		}, args)
	})

	t.Run("with multiple flags", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, "--profile openai-subagent --use-weak-model", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "openai-subagent",
			"--use-weak-model",
			"What is foo?",
		}, args)
	})

	t.Run("inherits sysprompt from llm config", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		ctxWithConfig := context.WithValue(ctx, subagentConfigContextKey{}, llmtypes.Config{Sysprompt: "./custom.tmpl"})
		args := BuildSubagentArgs(ctxWithConfig, "--use-weak-model", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"--sysprompt", "./custom.tmpl",
			"What is foo?",
		}, args)
	})

	t.Run("does not duplicate sysprompt when already in subagent_args", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		ctxWithConfig := context.WithValue(ctx, subagentConfigContextKey{}, llmtypes.Config{Sysprompt: "./custom.tmpl"})
		args := BuildSubagentArgs(ctxWithConfig, "--sysprompt ./already.tmpl", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--sysprompt", "./already.tmpl",
			"What is foo?",
		}, args)
	})

	t.Run("with quoted argument in subagent_args", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, `--profile "my profile"`, input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "my profile",
			"What is foo?",
		}, args)
	})

	t.Run("preserves question with special characters", func(t *testing.T) {
		question := `Where is the "foo()" function defined?`
		input := &SubAgentInput{Question: question}
		args := BuildSubagentArgs(ctx, "", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			question,
		}, args)
	})

	t.Run("invalid shlex syntax falls back gracefully", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, `--profile "unclosed`, input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"What is foo?",
		}, args)
	})

	t.Run("empty question not appended", func(t *testing.T) {
		input := &SubAgentInput{Question: ""}
		args := BuildSubagentArgs(ctx, "--use-weak-model", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
		}, args)
	})

	t.Run("workflow only without question", func(t *testing.T) {
		input := &SubAgentInput{
			Workflow: "github/pr",
		}
		args := BuildSubagentArgs(ctx, "", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"-r", "github/pr",
		}, args)
	})

	t.Run("with workflow", func(t *testing.T) {
		input := &SubAgentInput{
			Question: "Create a PR",
			Workflow: "github/pr",
		}
		args := BuildSubagentArgs(ctx, "", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"-r", "github/pr",
			"Create a PR",
		}, args)
	})

	t.Run("with workflow and args", func(t *testing.T) {
		input := &SubAgentInput{
			Question: "Create a PR",
			Workflow: "github/pr",
			Args: map[string]string{
				"target": "develop",
				"draft":  "true",
			},
		}
		args := BuildSubagentArgs(ctx, "", input, nil)

		// Args are now sorted alphabetically for deterministic output
		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"-r", "github/pr",
			"--arg", "draft=true",
			"--arg", "target=develop",
			"Create a PR",
		}, args)
	})

	t.Run("with workflow and subagent_args", func(t *testing.T) {
		input := &SubAgentInput{
			Question: "Create a PR",
			Workflow: "github/pr",
		}
		args := BuildSubagentArgs(ctx, "--use-weak-model", input, nil)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"-r", "github/pr",
			"Create a PR",
		}, args)
	})

	t.Run("with workflow metadata profile", func(t *testing.T) {
		input := &SubAgentInput{
			Question: "Create a PR",
			Workflow: "github/pr",
		}
		workflow := &fragments.Fragment{
			ID: "github/pr",
			Metadata: fragments.Metadata{
				Profile: "cheap",
			},
		}
		args := BuildSubagentArgs(ctx, "", input, workflow)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "cheap",
			"-r", "github/pr",
			"Create a PR",
		}, args)
	})

	t.Run("workflow profile takes precedence over subagent_args", func(t *testing.T) {
		input := &SubAgentInput{
			Question: "Create a PR",
			Workflow: "github/pr",
		}
		workflow := &fragments.Fragment{
			ID: "github/pr",
			Metadata: fragments.Metadata{
				Profile: "expensive",
			},
		}
		// subagent_args has --profile cheap, but workflow metadata has profile: expensive
		// The --profile from subagent_args is stripped, workflow profile is used
		args := BuildSubagentArgs(ctx, "--profile cheap --use-weak-model", input, workflow)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"--profile", "expensive",
			"-r", "github/pr",
			"Create a PR",
		}, args)
	})
}

func TestStripProfileFlag(t *testing.T) {
	t.Run("removes --profile and its value", func(t *testing.T) {
		args := []string{"--use-weak-model", "--profile", "cheap", "--timeout", "30"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{"--use-weak-model", "--timeout", "30"}, result)
	})

	t.Run("no --profile flag", func(t *testing.T) {
		args := []string{"--use-weak-model", "--timeout", "30"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{"--use-weak-model", "--timeout", "30"}, result)
	})

	t.Run("--profile at end without value", func(t *testing.T) {
		args := []string{"--use-weak-model", "--profile"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{"--use-weak-model", "--profile"}, result) // unchanged, no value to strip
	})

	t.Run("--profile only", func(t *testing.T) {
		args := []string{"--profile", "cheap"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{}, result)
	})

	t.Run("empty args", func(t *testing.T) {
		result := stripProfileFlag([]string{})
		assert.Equal(t, []string{}, result)
	})

	t.Run("--profile=value format", func(t *testing.T) {
		args := []string{"--use-weak-model", "--profile=cheap", "--timeout", "30"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{"--use-weak-model", "--timeout", "30"}, result)
	})

	t.Run("--profile=value only", func(t *testing.T) {
		args := []string{"--profile=cheap"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{}, result)
	})

	t.Run("multiple --profile flags", func(t *testing.T) {
		args := []string{"--profile", "cheap", "--use-weak-model", "--profile", "expensive"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{"--use-weak-model"}, result)
	})

	t.Run("multiple --profile=value flags", func(t *testing.T) {
		args := []string{"--profile=cheap", "--use-weak-model", "--profile=expensive"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{"--use-weak-model"}, result)
	})

	t.Run("mixed --profile formats", func(t *testing.T) {
		args := []string{"--profile", "cheap", "--profile=expensive", "--use-weak-model"}
		result := stripProfileFlag(args)
		assert.Equal(t, []string{"--use-weak-model"}, result)
	})
}

func TestSubAgentTool_GetWorkflowsAndIsWorkflowEnabled(t *testing.T) {
	workflows := map[string]*fragments.Fragment{
		"test": {ID: "test"},
	}

	t.Run("enabled with workflows", func(t *testing.T) {
		tool := NewSubAgentTool(workflows, true)
		assert.True(t, tool.IsWorkflowEnabled())
		assert.Equal(t, workflows, tool.GetWorkflows())
	})

	t.Run("disabled", func(t *testing.T) {
		tool := NewSubAgentTool(workflows, false)
		assert.False(t, tool.IsWorkflowEnabled())
	})

	t.Run("nil workflows", func(t *testing.T) {
		tool := NewSubAgentTool(nil, true)
		assert.True(t, tool.IsWorkflowEnabled())
		assert.Nil(t, tool.GetWorkflows())
	})
}

func TestSubAgentTool_WorkflowFiltering(t *testing.T) {
	// Create a mix of workflow and non-workflow fragments
	allFragments := map[string]*fragments.Fragment{
		"github/pr": {
			ID: "github/pr",
			Metadata: fragments.Metadata{
				Name:        "PR Generator",
				Description: "Creates PRs",
				Workflow:    true,
			},
		},
		"init": {
			ID: "init",
			Metadata: fragments.Metadata{
				Name:        "Init",
				Description: "Bootstrap AGENTS.md",
				Workflow:    true,
			},
		},
		"commit": {
			ID: "commit",
			Metadata: fragments.Metadata{
				Name:        "Commit Generator",
				Description: "Creates commit messages",
				Workflow:    false, // Not a workflow
			},
		},
		"compact": {
			ID: "compact",
			Metadata: fragments.Metadata{
				Name:        "Compact",
				Description: "Compacts context",
				// Workflow not set (defaults to false)
			},
		},
	}

	// Simulate filtering logic that should be applied by discoverWorkflows
	filteredWorkflows := make(map[string]*fragments.Fragment)
	for id, frag := range allFragments {
		if frag.Metadata.Workflow {
			filteredWorkflows[id] = frag
		}
	}

	t.Run("only workflow fragments are included", func(t *testing.T) {
		assert.Len(t, filteredWorkflows, 2)
		assert.Contains(t, filteredWorkflows, "github/pr")
		assert.Contains(t, filteredWorkflows, "init")
		assert.NotContains(t, filteredWorkflows, "commit")
		assert.NotContains(t, filteredWorkflows, "compact")
	})

	t.Run("subagent tool shows only workflows in description", func(t *testing.T) {
		tool := NewSubAgentTool(filteredWorkflows, true)
		desc := tool.Description()

		// Should contain workflow fragments
		assert.Contains(t, desc, `<workflow name="github/pr">`)
		assert.Contains(t, desc, `<workflow name="init">`)

		// Should NOT contain non-workflow fragments
		assert.NotContains(t, desc, `<workflow name="commit">`)
		assert.NotContains(t, desc, `<workflow name="compact">`)
	})

	t.Run("subagent validates only known workflows", func(t *testing.T) {
		tool := NewSubAgentTool(filteredWorkflows, true)
		state := NewBasicState(context.TODO())

		// Valid workflow should pass
		err := tool.ValidateInput(state, `{"workflow": "github/pr"}`)
		assert.NoError(t, err)

		// Invalid workflow should fail
		err = tool.ValidateInput(state, `{"workflow": "commit"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown workflow 'commit'")

		// Non-existent workflow should fail
		err = tool.ValidateInput(state, `{"workflow": "nonexistent"}`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown workflow 'nonexistent'")
	})
}

func TestSubAgentTool_DescriptionWithWorkflowField(t *testing.T) {
	// Test that only fragments with Workflow: true appear in description
	workflows := map[string]*fragments.Fragment{
		"custom-tool": {
			ID: "custom-tool",
			Metadata: fragments.Metadata{
				Name:        "Custom Tool Generator",
				Description: "Creates custom tools",
				Workflow:    true,
				Arguments: map[string]fragments.ArgumentMeta{
					"task": {
						Description: "Description of what the tool should do",
					},
					"global": {
						Description: "Whether to save globally",
						Default:     "false",
					},
				},
			},
		},
	}

	tool := NewSubAgentTool(workflows, true)
	desc := tool.Description()

	// Verify workflows section structure
	assert.Contains(t, desc, "<workflows>")
	assert.Contains(t, desc, "</workflows>")

	// Verify custom-tool workflow
	assert.Contains(t, desc, `<workflow name="custom-tool">`)
	assert.Contains(t, desc, "<description>Creates custom tools</description>")
	assert.Contains(t, desc, `<argument name="global" default="false">Whether to save globally</argument>`)
	assert.Contains(t, desc, `<argument name="task">Description of what the tool should do</argument>`)
}

// Execute tests require integration testing (shell-out via exec.CommandContext)
