package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/binaries"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBashTool_GenerateSchema(t *testing.T) {
	tool := NewBashTool(false)
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/bash-input", string(schema.ID))
	timeoutSchema, exists := schema.Properties.Get("timeout")
	assert.True(t, exists)
	assert.Equal(t, "Timeout in seconds (10-120)", timeoutSchema.Description)
	assert.Equal(t, json.Number("10"), timeoutSchema.Minimum)
	assert.Equal(t, json.Number("120"), timeoutSchema.Maximum)
}

func TestBashTool_GenerateSchema_CustomTimeout(t *testing.T) {
	tool := NewBashToolWithTimeout(false, 5*time.Minute)
	schema := tool.GenerateSchema()
	assert.NotNil(t, schema)

	timeoutSchema, exists := schema.Properties.Get("timeout")
	assert.True(t, exists)
	assert.Equal(t, "Timeout in seconds (10-300)", timeoutSchema.Description)
	assert.Equal(t, json.Number("10"), timeoutSchema.Minimum)
	assert.Equal(t, json.Number("300"), timeoutSchema.Maximum)
}

func TestBashTool_ShortStatesTimeoutUnit(t *testing.T) {
	assert.Equal(t,
		"Run a non-interactive bash command; shell state such as cd and export does not persist; timeout is in seconds (10-120).",
		(&BashTool{}).Short())
	assert.Contains(t, NewBashToolWithTimeout(false, 5*time.Minute).Short(), "in seconds (10-300).")
}

func TestBashTool_Name(t *testing.T) {
	tool := &BashTool{}
	assert.Equal(t, "bash", tool.Name())
}

func TestBashTool_Description(t *testing.T) {
	tool := NewBashTool(false)
	desc := tool.Description()
	assert.Contains(t, desc, "Run a bash command non-interactively in the working directory.")
	assert.Contains(t, desc, "does not persist between bash tool calls")
	assert.Contains(t, desc, "# Input")
	assert.Contains(t, desc, "timeout: required, 10-120")
}

func TestBashTool_Description_CustomTimeout(t *testing.T) {
	tool := NewBashToolWithTimeout(false, 5*time.Minute)
	desc := tool.Description()

	assert.Contains(t, desc, "timeout: required, 10-300")
}

func TestBashTool_Description_FSSearchToolsDisabled(t *testing.T) {
	tool := NewBashTool(false)
	desc := tool.Description()

	assert.Contains(t, desc, "use fd and rg via this tool only")
	assert.NotContains(t, desc, "Prefer grep_tool/glob_tool over grep/find in bash.")
}

func TestBashTool_Description_ConsistentOutput(t *testing.T) {
	// Test that the description is consistent and contains all expected sections
	tool := NewBashTool(false)
	desc := tool.Description()

	// Basic structure should always be present
	assert.Contains(t, desc, "Run a bash command non-interactively in the working directory.")
	assert.Contains(t, desc, "does not persist between bash tool calls")
	assert.NotContains(t, desc, "# Restrictions")
	assert.Contains(t, desc, "# Input")
	assert.Contains(t, desc, "# Rules")
	assert.Contains(t, desc, "Examples:")

	// Should be well-formed
	assert.NotEmpty(t, desc)
	assert.Greater(t, len(desc), 300) // Should still be substantial

	// Test that multiple calls return the same result
	desc2 := tool.Description()
	assert.Equal(t, desc, desc2)
}

func TestBashTool_Execute_Success(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Echo test",
		Command:     "echo 'hello world'",
		Timeout:     10,
	}
	params, _ := json.Marshal(input)

	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))
	assert.False(t, result.IsError())
	assert.Equal(t, "hello world\n", result.GetResult())
	assert.Equal(t, map[string]any{"exitCode": 0, "timedOut": false}, result.StructuredData().Data)
}

func TestBashTool_Execute_Timeout(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Sleep test",
		Command:     "sleep 0.2",
		Timeout:     1,
	}
	params, _ := json.Marshal(input)

	// Use a shorter context timeout to simulate timeout faster
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := tool.Execute(ctx, NewBasicState(context.TODO()), string(params))
	assert.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "Command timed out")
	assert.Empty(t, result.GetResult())
	assert.Nil(t, result.StructuredData().Data.(map[string]any)["exitCode"])
	assert.Equal(t, true, result.StructuredData().Data.(map[string]any)["timedOut"])
}

func TestBashTool_Execute_Error(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Invalid command",
		Command:     "nonexistentcommand",
		Timeout:     10,
	}
	params, _ := json.Marshal(input)

	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), string(params))
	assert.Contains(t, result.GetError(), "Command exited with status 127")
	assert.Contains(t, result.GetResult(), "nonexistentcommand: command not found")
	assert.Equal(t, 127, result.StructuredData().Data.(map[string]any)["exitCode"])
}

func TestBashTool_Execute_DirectoryChangeDoesNotPersist(t *testing.T) {
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	other, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	state := NewBasicState(t.Context(), WithWorkingDirectory(cwd))
	tool := NewBashTool(false)

	run := func(command string) string {
		params, err := json.Marshal(BashInput{
			Description: "Print working directory",
			Command:     command,
			Timeout:     10,
		})
		require.NoError(t, err)
		require.NoError(t, tool.ValidateInput(state, string(params)))
		result := tool.Execute(t.Context(), state, string(params))
		require.False(t, result.IsError(), result.GetError())
		return strings.TrimSpace(result.GetResult())
	}

	assert.Equal(t, other, run("cd '"+other+"' && pwd -P"))
	assert.Equal(t, cwd, run("pwd -P"))
}

func TestBashTool_Execute_InvalidJSON(t *testing.T) {
	tool := NewBashTool(false)
	result := tool.Execute(context.Background(), NewBasicState(context.TODO()), "invalid json")
	assert.True(t, result.IsError())
	assert.Empty(t, result.GetResult())
}

func TestBashTool_Execute_CodeMode(t *testing.T) {
	for _, test := range []struct {
		name     string
		command  string
		exitCode any
		timedOut bool
		wantErr  bool
	}{
		{name: "success", command: "exit 0", exitCode: 0},
		{name: "nonzero exit", command: "exit 42", exitCode: 42},
		{name: "command not found", command: "nonexistentcommand", exitCode: 127},
		{name: "signal", command: "kill -TERM $$", wantErr: true},
		{name: "start failure", command: "exit 0", wantErr: true},
		{name: "command timeout", command: "sleep 5", timedOut: true},
		{name: "parent timeout", command: "sleep 5", timedOut: true, wantErr: true},
		{name: "cancelled", command: "exit 0", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{})
			cwd := t.TempDir()
			switch test.name {
			case "start failure":
				cwd = filepath.Join(cwd, "missing")
			case "parent timeout":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			params, err := json.Marshal(BashInput{
				Command:     test.command,
				Description: "Test code-mode bash exit handling",
				Timeout:     1,
			})
			require.NoError(t, err)
			state := NewBasicState(t.Context(), WithWorkingDirectory(cwd))
			result := NewBashTool(false).Execute(ctx, state, string(params))
			assert.Equal(t, test.wantErr, result.IsError(), result.GetError())
			assert.Equal(t, !test.wantErr, result.StructuredData().Success)
			assert.Equal(t, test.exitCode, result.StructuredData().Data.(map[string]any)["exitCode"])
			assert.Equal(t, test.timedOut, result.StructuredData().Data.(map[string]any)["timedOut"])
			if !test.wantErr {
				assert.Empty(t, result.GetError())
			}
			if test.name == "command timeout" {
				assert.Equal(t, "[Command timed out after 1 seconds. Output above may be partial.]\n", result.GetResult())
			} else {
				assert.NotContains(t, result.GetResult(), "[Command timed out")
			}
		})
	}
}

func TestBashTool_CodeModeTimeoutNoticeSurvivesTruncation(t *testing.T) {
	ctx := ContextWithCodeExecution(t.Context(), CodeExecutionContext{})
	params, err := json.Marshal(BashInput{
		Command:     "printf '%080000d' 0; sleep 5",
		Description: "Preserve timeout notice after large output",
		Timeout:     1,
	})
	require.NoError(t, err)
	result := NewBashTool(false).Execute(ctx, NewBasicState(t.Context()), string(params))
	require.False(t, result.IsError(), result.GetError())
	metadata := result.StructuredData().Metadata.(*tooltypes.BashMetadata)
	require.NotNil(t, metadata.Truncation)
	assert.True(t, metadata.Truncation.Truncated)
	wantSuffix := "\n[Command timed out after 1 seconds. Output above may be partial.]\n"
	assert.True(t, strings.HasSuffix(metadata.Output, wantSuffix))
	require.NotEmpty(t, metadata.FullOutputPath)
	t.Cleanup(func() { _ = os.Remove(metadata.FullOutputPath) })
	fullOutput, err := os.ReadFile(metadata.FullOutputPath)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("0", 80000)+wantSuffix, string(fullOutput))
}

func TestBashTool_Execute_ContextCancellation(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Long running command",
		Command:     "sleep 5",
		Timeout:     20,
	}
	params, _ := json.Marshal(input)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := tool.Execute(ctx, NewBasicState(context.TODO()), string(params))
	assert.Contains(t, result.GetError(), "Command timed out")
	assert.Empty(t, result.GetResult())
}

func TestBashTool_ExecuteStreamingEmitsAccumulatedSnapshots(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Stream command output",
		Command:     "printf 'first\\n'; sleep 0.25; printf 'second\\n'",
		Timeout:     10,
	}
	params, err := json.Marshal(input)
	require.NoError(t, err)

	updates := []string{}
	result := tool.ExecuteStreaming(context.Background(), NewBasicState(context.TODO()), string(params), func(update tooltypes.ToolResult) {
		metadata := update.StructuredData().Metadata.(*tooltypes.BashMetadata)
		updates = append(updates, metadata.Output)
	})

	require.False(t, result.IsError())
	assert.Equal(t, "first\nsecond\n", result.GetResult())
	require.GreaterOrEqual(t, len(updates), 3)
	assert.Empty(t, updates[0])
	assert.Contains(t, updates[len(updates)-1], "first\nsecond\n")
	assert.True(t, slices.ContainsFunc(updates, func(update string) bool {
		return strings.Contains(update, "first\n") && !strings.Contains(update, "second\n")
	}))
}

func TestBashTool_ExecuteStreamingSlowUpdateDoesNotConsumeCommandTimeout(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Run despite slow callback",
		Command:     "printf 'ran\\n'",
		Timeout:     10,
	}
	params, err := json.Marshal(input)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	callbackStarted := make(chan struct{}, 1)
	releaseCallback := make(chan struct{})
	resultCh := make(chan tooltypes.ToolResult, 1)
	go func() {
		resultCh <- tool.ExecuteStreaming(ctx, NewBasicState(context.TODO()), string(params), func(tooltypes.ToolResult) {
			select {
			case callbackStarted <- struct{}{}:
			default:
			}
			<-releaseCallback
		})
	}()

	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("streaming callback did not start")
	}
	time.Sleep(75 * time.Millisecond)
	close(releaseCallback)

	select {
	case result := <-resultCh:
		require.False(t, result.IsError())
		assert.Equal(t, "ran\n", result.GetResult())
	case <-time.After(time.Second):
		t.Fatal("bash execution did not finish")
	}
}

func TestBashTool_ExecuteStreamingSlowUpdateDoesNotRelabelFailureAsTimeout(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Preserve command failure status",
		Command:     "printf 'failed\\n'; exit 7",
		Timeout:     10,
	}
	params, err := json.Marshal(input)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	callbackStarted := make(chan struct{}, 1)
	releaseCallback := make(chan struct{})
	resultCh := make(chan tooltypes.ToolResult, 1)
	go func() {
		resultCh <- tool.ExecuteStreaming(ctx, NewBasicState(context.TODO()), string(params), func(tooltypes.ToolResult) {
			select {
			case callbackStarted <- struct{}{}:
			default:
			}
			<-releaseCallback
		})
	}()

	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("streaming callback did not start")
	}
	time.Sleep(75 * time.Millisecond)
	close(releaseCallback)

	select {
	case result := <-resultCh:
		require.True(t, result.IsError())
		assert.Equal(t, "Command exited with status 7", result.GetError())
		assert.Equal(t, "failed\n", result.GetResult())
	case <-time.After(time.Second):
		t.Fatal("bash execution did not finish")
	}
}

func TestBashTool_ExecuteStreamingPreservesOutputOnTimeout(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Capture output before timeout",
		Command:     "printf 'ready\\n'; sleep 5",
		Timeout:     20,
	}
	params, err := json.Marshal(input)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	result := tool.ExecuteStreaming(ctx, NewBasicState(context.TODO()), string(params), func(tooltypes.ToolResult) {})

	require.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "Command timed out")
	assert.Contains(t, result.GetResult(), "ready")
}

func TestBashTool_ExecuteStreamingPreservesMergedOutputOnFailure(t *testing.T) {
	tool := NewBashTool(false)
	input := BashInput{
		Description: "Stream mixed failing output",
		Command:     "printf 'stdout-1\\n'; sleep 0.05; printf 'stderr-1\\n' >&2; sleep 0.05; printf 'stdout-2\\n'; exit 7",
		Timeout:     10,
	}
	params, err := json.Marshal(input)
	require.NoError(t, err)
	updates := []string{}

	result := tool.ExecuteStreaming(context.Background(), NewBasicState(context.TODO()), string(params), func(update tooltypes.ToolResult) {
		metadata := update.StructuredData().Metadata.(*tooltypes.BashMetadata)
		updates = append(updates, metadata.Output)
	})

	require.True(t, result.IsError())
	assert.Equal(t, "Command exited with status 7", result.GetError())
	assert.Equal(t, "stdout-1\nstderr-1\nstdout-2\n", result.GetResult())
	require.NotEmpty(t, updates)
	assert.Equal(t, "stdout-1\nstderr-1\nstdout-2\n", updates[len(updates)-1])
}

func TestBashOutputAccumulatorSpillsFullOutputWhenSnapshotIsTruncated(t *testing.T) {
	accumulator := newBashOutputAccumulator(8)
	_, err := accumulator.Write([]byte("abcdef"))
	require.NoError(t, err)
	_, err = accumulator.Write([]byte("ghijkl\n"))
	require.NoError(t, err)

	snapshot := accumulator.finish()
	require.True(t, snapshot.truncated)
	assert.Equal(t, int64(13), snapshot.totalBytes)
	assert.Equal(t, 1, snapshot.totalLines)
	assert.Contains(t, snapshot.output, "tokens truncated")
	assert.NotContains(t, snapshot.output, "Total output lines:")
	require.NotEmpty(t, snapshot.fullOutputPath)
	t.Cleanup(func() { _ = os.Remove(snapshot.fullOutputPath) })

	fullOutput, err := os.ReadFile(snapshot.fullOutputPath)
	require.NoError(t, err)
	assert.Equal(t, "abcdefghijkl\n", string(fullOutput))

	result := newBashToolResult("printf output", t.TempDir(), time.Second, snapshot, true)
	assert.Equal(t, "abcdefghijkl\n", result.GetResult())
	metadata := result.StructuredData().Metadata.(*tooltypes.BashMetadata)
	assert.Contains(t, metadata.Output, "tokens truncated")
	assert.NotContains(t, metadata.Output, "Total output lines:")
	require.NotNil(t, metadata.Truncation)
	assert.True(t, metadata.Truncation.Truncated)
	assert.Equal(t, 1, metadata.Truncation.TotalLines)
	assert.Equal(t, snapshot.fullOutputPath, metadata.FullOutputPath)
}

func TestBashOutputAccumulatorKeepsUTF8BoundariesValid(t *testing.T) {
	accumulator := newBashOutputAccumulator(8)
	_, err := accumulator.Write([]byte("ab🙂cd🙂ef\n"))
	require.NoError(t, err)

	snapshot := accumulator.finish()
	require.True(t, snapshot.truncated)
	assert.True(t, utf8.ValidString(snapshot.output))
	assert.NotContains(t, snapshot.output, "\uFFFD")
	if snapshot.fullOutputPath != "" {
		t.Cleanup(func() { _ = os.Remove(snapshot.fullOutputPath) })
	}
}

func TestBashOutputAccumulatorOmitsIncompleteUTF8FromPartialSnapshot(t *testing.T) {
	accumulator := newBashOutputAccumulator(64)
	content := []byte("prefix🙂")
	_, err := accumulator.Write(content[:len(content)-1])
	require.NoError(t, err)

	partial := accumulator.snapshot()
	assert.True(t, utf8.ValidString(partial.output))
	assert.Equal(t, "prefix", partial.output)

	_, err = accumulator.Write(content[len(content)-1:])
	require.NoError(t, err)
	final := accumulator.finish()
	assert.True(t, utf8.ValidString(final.output))
	assert.Equal(t, "prefix🙂", final.output)
}

func TestBashOutputAccumulatorOmitsIncompleteUTF8FromTruncatedTail(t *testing.T) {
	accumulator := newBashOutputAccumulator(8)
	_, err := accumulator.Write([]byte("abcdefghij"))
	require.NoError(t, err)
	emoji := []byte("🙂")
	_, err = accumulator.Write(emoji[:len(emoji)-1])
	require.NoError(t, err)

	partial := accumulator.snapshot()
	require.True(t, partial.truncated)
	assert.True(t, utf8.ValidString(partial.output))
	assert.NotContains(t, partial.output, "\uFFFD")

	_, err = accumulator.Write(emoji[len(emoji)-1:])
	require.NoError(t, err)
	final := accumulator.finish()
	assert.True(t, utf8.ValidString(final.output))
	assert.Contains(t, final.output, "🙂")
	if final.fullOutputPath != "" {
		t.Cleanup(func() { _ = os.Remove(final.fullOutputPath) })
	}
}

func TestBashToolResult_AssistantFacing_TruncatesLargeOutput(t *testing.T) {
	longOutput := strings.Repeat("token token token token\n", 15000)
	result := &BashToolResult{
		command:        "seq 1 15000",
		combinedOutput: longOutput,
	}

	assistantFacing := result.AssistantFacing()

	assert.Contains(t, assistantFacing, "<result>")
	assert.NotContains(t, assistantFacing, "Total output lines:")
	assert.Contains(t, assistantFacing, "tokens truncated")
	assert.NotContains(t, assistantFacing, longOutput)

	structured := result.StructuredData()
	metadata := structured.Metadata.(*tooltypes.BashMetadata)
	assert.NotContains(t, metadata.Output, "Total output lines:")
	assert.Contains(t, metadata.Output, "tokens truncated")
	assert.NotContains(t, metadata.Output, longOutput)
}

func TestBashToolResult_AssistantFacing_PreservesSmallOutput(t *testing.T) {
	result := &BashToolResult{
		command:        "echo hello",
		combinedOutput: "hello\n",
	}

	assistantFacing := result.AssistantFacing()

	assert.Contains(t, assistantFacing, "hello\n")
	assert.NotContains(t, assistantFacing, "tokens truncated")
	assert.NotContains(t, assistantFacing, "Total output lines:")
}

func TestBashTool_ValidateInput(t *testing.T) {
	tool := NewBashTool(false)
	tests := []struct {
		name        string
		input       BashInput
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid single command",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid multiple commands with &&",
			input: BashInput{
				Description: "test",
				Command:     "echo hello && echo world",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid multiple commands with ;",
			input: BashInput{
				Description: "test",
				Command:     "echo hello; echo world",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "valid complex command",
			input: BashInput{
				Description: "test",
				Command:     "echo hello && echo world; echo test || echo fail",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "commands are not filtered",
			input: BashInput{
				Description: "test",
				Command:     "cd /tmp && less file.txt; vim file.txt",
				Timeout:     10,
			},
			expectError: false,
		},
		{
			name: "empty command",
			input: BashInput{
				Description: "test",
				Command:     "",
				Timeout:     10,
			},
			expectError: true,
			errorMsg:    "command is required",
		},
		{
			name: "missing description",
			input: BashInput{
				Command: "echo hello",
				Timeout: 10,
			},
			expectError: true,
			errorMsg:    "description is required",
		},
		{
			name: "invalid timeout too low",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     0,
			},
			expectError: true,
			errorMsg:    "timeout must be between 10 and 120 seconds",
		},
		{
			name: "invalid timeout too high",
			input: BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     150,
			},
			expectError: true,
			errorMsg:    "timeout must be between 10 and 120 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			err := tool.ValidateInput(NewBasicState(context.TODO()), string(input))
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBashTool_ValidateInput_CustomTimeout(t *testing.T) {
	tool := NewBashToolWithTimeout(false, 5*time.Minute)

	for _, timeout := range []int{120, 300} {
		t.Run(strconv.Itoa(timeout), func(t *testing.T) {
			input, _ := json.Marshal(BashInput{
				Description: "test",
				Command:     "echo hello",
				Timeout:     timeout,
			})

			err := tool.ValidateInput(NewBasicState(context.TODO()), string(input))
			assert.NoError(t, err)
		})
	}

	input, _ := json.Marshal(BashInput{
		Description: "test",
		Command:     "echo hello",
		Timeout:     301,
	})
	err := tool.ValidateInput(NewBasicState(context.TODO()), string(input))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout must be between 10 and 300 seconds")
}

func TestBashToolResult_StructuredDataFields(t *testing.T) {
	tool := NewBashTool(false)
	state := NewBasicState(context.TODO())

	t.Run("successful command has all metadata fields", func(t *testing.T) {
		input := BashInput{
			Description: "Test command",
			Command:     "echo 'hello world'",
			Timeout:     10,
		}

		inputJSON, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), state, string(inputJSON))

		assert.False(t, result.IsError())

		structuredResult := result.StructuredData()
		assert.True(t, structuredResult.Success)
		assert.Equal(t, "bash", structuredResult.ToolName)

		// Extract metadata and verify all fields are populated
		bashResult, ok := result.(*BashToolResult)
		assert.True(t, ok)

		// Verify execution time is tracked
		assert.Greater(t, bashResult.executionTime, time.Duration(0))

		// Verify exit code is set correctly
		assert.Equal(t, 0, bashResult.exitCode)

		// Verify working directory is captured
		assert.NotEmpty(t, bashResult.workingDir)

		// Verify command and output
		assert.Equal(t, "echo 'hello world'", bashResult.command)
		assert.Contains(t, bashResult.combinedOutput, "hello world")

		// Check structured metadata
		metadata := structuredResult.Metadata.(*tooltypes.BashMetadata)
		assert.Equal(t, "echo 'hello world'", metadata.Command)
		assert.Equal(t, 0, metadata.ExitCode)
		assert.Greater(t, metadata.ExecutionTime, time.Duration(0))
		assert.NotEmpty(t, metadata.WorkingDir)
		assert.Contains(t, metadata.Output, "hello world")
	})

	t.Run("failed command has correct exit code", func(t *testing.T) {
		input := BashInput{
			Description: "Test failing command",
			Command:     "exit 42",
			Timeout:     10,
		}

		inputJSON, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), state, string(inputJSON))

		assert.True(t, result.IsError())

		bashResult, ok := result.(*BashToolResult)
		assert.True(t, ok)

		// Verify execution time is tracked even for failures
		assert.Greater(t, bashResult.executionTime, time.Duration(0))

		// Verify correct exit code is captured
		assert.Equal(t, 42, bashResult.exitCode)

		// Verify working directory is captured
		assert.NotEmpty(t, bashResult.workingDir)

		// Check structured metadata for error case
		structuredResult := result.StructuredData()
		assert.False(t, structuredResult.Success)
		assert.Contains(t, structuredResult.Error, "Command exited with status 42")
	})

	t.Run("timeout has execution time and working dir", func(t *testing.T) {
		input := BashInput{
			Description: "Test timeout command",
			Command:     "sleep 5", // Will timeout with 1s limit
			Timeout:     1,
		}

		inputJSON, _ := json.Marshal(input)
		result := tool.Execute(context.Background(), state, string(inputJSON))

		assert.True(t, result.IsError())

		bashResult, ok := result.(*BashToolResult)
		assert.True(t, ok)

		// Verify execution time is tracked even for timeouts
		assert.Greater(t, bashResult.executionTime, time.Duration(0))

		// Verify working directory is captured
		assert.NotEmpty(t, bashResult.workingDir)

		// Verify timeout error message
		assert.Contains(t, bashResult.error, "Command timed out after 1 seconds")
	})
}

func TestBashToolCanonicalData(t *testing.T) {
	result := &BashToolResult{
		command: "echo hello", combinedOutput: "hello\n", exitCode: 0, exitCodeKnown: true, workingDir: "/workspace",
	}
	assert.Equal(t, map[string]any{
		"exitCode": 0,
		"timedOut": false,
	}, result.StructuredData().Data)

	result.error = "Command exited with status 42"
	result.exitCode = 42
	assert.Equal(t, 42, result.StructuredData().Data.(map[string]any)["exitCode"])

	result.outputTruncated = true
	result.combinedOutput = "bounded snapshot"
	result.fullOutputComplete = true
	result.fullOutputPath = "/workspace/full-output.txt"
	data := result.StructuredData().Data.(map[string]any)
	assert.NotContains(t, data, "output", "command output belongs only in reply.text")
	assert.NotContains(t, data, "truncated", "truncation belongs to the reply envelope")
	assert.Equal(t, result.fullOutputPath, data["fullOutputPath"])
	data["exitCode"] = 100
	assert.Equal(t, 42, result.StructuredData().Data.(map[string]any)["exitCode"])
	metadata := result.StructuredData().Metadata.(*tooltypes.BashMetadata)
	assert.Equal(t, "bounded snapshot", metadata.Output)
	assert.Equal(t, "echo hello", metadata.Command, "UI metadata keeps the command")
	assert.True(t, metadata.Truncation.Truncated)

	result.fullOutputComplete = false
	assert.NotContains(t, result.StructuredData().Data, "fullOutputPath")
	result.exitCodeKnown = false
	assert.Nil(t, result.StructuredData().Data.(map[string]any)["exitCode"])
	result.timedOut = true
	assert.Equal(t, true, result.StructuredData().Data.(map[string]any)["timedOut"])

	tool := NewBashTool(false)
	schema := tooltypes.OutputSchemaForTool(tool)
	assert.Equal(t, []string{"exitCode", "timedOut"}, schema["required"])
	schema["properties"].(map[string]any)["exitCode"] = false
	assert.IsType(t, map[string]any{}, tool.RawOutputSchema()["properties"].(map[string]any)["exitCode"])
}

func TestBashEnvWithPreferredBinDirs(t *testing.T) {
	env, err := bashEnvWithPreferredBinDirs()
	assert.NoError(t, err)

	var pathValue string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathValue = strings.TrimPrefix(kv, "PATH=")
			break
		}
	}

	assert.NotEmpty(t, pathValue)
	parts := strings.Split(pathValue, string(os.PathListSeparator))
	homeDir, err := os.UserHomeDir()
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(parts), 2)
	assert.Equal(t, binaries.GetLibexecBinDir(), parts[0])
	assert.Equal(t, filepath.Join(homeDir, ".kodelet", "bin"), parts[1])
}
