package tools

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/fragments"
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

	// Invalid inputs
	err = tool.ValidateInput(state, `{"question": ""}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "question is required")
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

	t.Run("valid workflow", func(t *testing.T) {
		err := tool.ValidateInput(state, `{"question": "test", "workflow": "valid-workflow"}`)
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

func TestSubAgentTool_TracingKVs(t *testing.T) {
	tool := NewSubAgentTool(nil, false)

	t.Run("without workflow", func(t *testing.T) {
		kvs, err := tool.TracingKVs(`{"question": "test question"}`)
		assert.NoError(t, err)
		expected := []attribute.KeyValue{
			attribute.String("question", "test question"),
		}
		assert.Equal(t, expected, kvs)
	})

	t.Run("with workflow", func(t *testing.T) {
		kvs, err := tool.TracingKVs(`{"question": "test question", "workflow": "test-workflow"}`)
		assert.NoError(t, err)
		expected := []attribute.KeyValue{
			attribute.String("question", "test question"),
			attribute.String("workflow", "test-workflow"),
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
	// Test successful result structured data
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

	// Test error result structured data
	errorResult := &SubAgentToolResult{
		err:      "some error",
		question: "test question",
	}

	errorStructuredData := errorResult.StructuredData()
	assert.Equal(t, "subagent", errorStructuredData.ToolName)
	assert.False(t, errorStructuredData.Success)
	assert.Equal(t, "some error", errorStructuredData.Error)

	var errorMetadata tooltypes.SubAgentMetadata
	assert.True(t, tooltypes.ExtractMetadata(errorStructuredData.Metadata, &errorMetadata))
	assert.Equal(t, "test question", errorMetadata.Question)
	assert.Empty(t, errorMetadata.Response)
}

func TestBuildSubagentArgs(t *testing.T) {
	ctx := context.Background()

	t.Run("basic args without subagent_args", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, "", input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"What is foo?",
		}, args)
	})

	t.Run("with --use-weak-model", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, "--use-weak-model", input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"What is foo?",
		}, args)
	})

	t.Run("with --profile flag", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, "--profile cheap", input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "cheap",
			"What is foo?",
		}, args)
	})

	t.Run("with multiple flags", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, "--profile openai-subagent --use-weak-model", input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "openai-subagent",
			"--use-weak-model",
			"What is foo?",
		}, args)
	})

	t.Run("with quoted argument in subagent_args", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, `--profile "my profile"`, input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--profile", "my profile",
			"What is foo?",
		}, args)
	})

	t.Run("preserves question with special characters", func(t *testing.T) {
		question := `Where is the "foo()" function defined?`
		input := &SubAgentInput{Question: question}
		args := BuildSubagentArgs(ctx, "", input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			question,
		}, args)
	})

	t.Run("invalid shlex syntax falls back gracefully", func(t *testing.T) {
		input := &SubAgentInput{Question: "What is foo?"}
		args := BuildSubagentArgs(ctx, `--profile "unclosed`, input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"What is foo?",
		}, args)
	})

	t.Run("empty question", func(t *testing.T) {
		input := &SubAgentInput{Question: ""}
		args := BuildSubagentArgs(ctx, "--use-weak-model", input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"",
		}, args)
	})

	t.Run("with workflow", func(t *testing.T) {
		input := &SubAgentInput{
			Question: "Create a PR",
			Workflow: "github/pr",
		}
		args := BuildSubagentArgs(ctx, "", input)

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
		args := BuildSubagentArgs(ctx, "", input)

		// Check base args
		assert.Contains(t, args, "run")
		assert.Contains(t, args, "--result-only")
		assert.Contains(t, args, "--as-subagent")
		assert.Contains(t, args, "-r")
		assert.Contains(t, args, "github/pr")
		assert.Contains(t, args, "Create a PR")

		// Check that --arg flags are present (order may vary due to map iteration)
		argCount := 0
		for i, arg := range args {
			if arg == "--arg" {
				argCount++
				nextArg := args[i+1]
				assert.True(t, nextArg == "target=develop" || nextArg == "draft=true",
					"unexpected --arg value: %s", nextArg)
			}
		}
		assert.Equal(t, 2, argCount, "expected 2 --arg flags")
	})

	t.Run("with workflow and subagent_args", func(t *testing.T) {
		input := &SubAgentInput{
			Question: "Create a PR",
			Workflow: "github/pr",
		}
		args := BuildSubagentArgs(ctx, "--use-weak-model", input)

		assert.Equal(t, []string{
			"run", "--result-only", "--as-subagent",
			"--use-weak-model",
			"-r", "github/pr",
			"Create a PR",
		}, args)
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

// Execute tests require integration testing (shell-out via exec.CommandContext)
