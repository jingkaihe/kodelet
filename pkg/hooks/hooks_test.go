package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHookType_Constants(t *testing.T) {
	assert.Equal(t, HookType("before_tool_call"), HookTypeBeforeToolCall)
	assert.Equal(t, HookType("after_tool_call"), HookTypeAfterToolCall)
	assert.Equal(t, HookType("user_message_send"), HookTypeUserMessageSend)
	assert.Equal(t, HookType("agent_stop"), HookTypeAgentStop)
}

func TestInvokedBy_Constants(t *testing.T) {
	assert.Equal(t, InvokedBy("main"), InvokedByMain)
	assert.Equal(t, InvokedBy("subagent"), InvokedBySubagent)
}

func TestNewHookManager_NoHooksDir(t *testing.T) {
	// Create an empty temp dir with no hooks
	tempDir := t.TempDir()

	manager, err := NewHookManager(WithHookDirs(tempDir))
	require.NoError(t, err)
	assert.False(t, manager.HasHooks(HookTypeBeforeToolCall))
	assert.False(t, manager.HasHooks(HookTypeAfterToolCall))
	assert.False(t, manager.HasHooks(HookTypeUserMessageSend))
	assert.False(t, manager.HasHooks(HookTypeAgentStop))
}

func TestNewHookManager_NonExistentDir(t *testing.T) {
	// Use a non-existent directory
	manager, err := NewHookManager(WithHookDirs("/non-existent-dir-12345"))
	require.NoError(t, err)
	assert.False(t, manager.HasHooks(HookTypeBeforeToolCall))
}

func TestDiscovery_WithDefaultDirs(t *testing.T) {
	discovery, err := NewDiscovery(WithDefaultDirs())
	require.NoError(t, err)
	assert.NotNil(t, discovery)
	assert.Len(t, discovery.hookDirs, 2)
	assert.Equal(t, "./.kodelet/hooks", discovery.hookDirs[0])
}

func TestDiscovery_WithHookDirs(t *testing.T) {
	dirs := []string{"/tmp/dir1", "/tmp/dir2"}
	discovery, err := NewDiscovery(WithHookDirs(dirs...))
	require.NoError(t, err)
	assert.Equal(t, dirs, discovery.hookDirs)
}

func TestHookManager_SetTimeout(t *testing.T) {
	manager := HookManager{
		hooks:   make(map[HookType][]*Hook),
		timeout: DefaultTimeout,
	}

	newTimeout := 5 * time.Second
	manager.SetTimeout(newTimeout)
	assert.Equal(t, newTimeout, manager.timeout)
}

func TestHookManager_HasHooks(t *testing.T) {
	manager := HookManager{
		hooks: map[HookType][]*Hook{
			HookTypeBeforeToolCall: {{Name: "test_hook", Path: "/test/path"}},
		},
	}

	assert.True(t, manager.HasHooks(HookTypeBeforeToolCall))
	assert.False(t, manager.HasHooks(HookTypeAfterToolCall))
}

func TestHookManager_GetHooks(t *testing.T) {
	expectedHooks := []*Hook{
		{Name: "hook1", Path: "/path/1"},
		{Name: "hook2", Path: "/path/2"},
	}

	manager := HookManager{
		hooks: map[HookType][]*Hook{
			HookTypeUserMessageSend: expectedHooks,
		},
	}

	hooks := manager.GetHooks(HookTypeUserMessageSend)
	assert.Equal(t, expectedHooks, hooks)

	emptyHooks := manager.GetHooks(HookTypeAgentStop)
	assert.Nil(t, emptyHooks)
}

func TestExecute_NoHooks(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	ctx := context.Background()
	result, err := manager.Execute(ctx, HookTypeBeforeToolCall, nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestDiscoverHooks_SkipsDirectories(t *testing.T) {
	tempDir := t.TempDir()
	hooksDir := filepath.Join(tempDir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o755))

	// Create a subdirectory that should be skipped
	subDir := filepath.Join(hooksDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0o755))

	discovery, err := NewDiscovery(WithHookDirs(hooksDir))
	require.NoError(t, err)

	hooks, err := discovery.DiscoverHooks()
	require.NoError(t, err)

	// No hooks should be discovered (only directories)
	totalHooks := 0
	for _, h := range hooks {
		totalHooks += len(h)
	}
	assert.Equal(t, 0, totalHooks)
}

func TestDiscoverHooks_SkipsNonExecutable(t *testing.T) {
	tempDir := t.TempDir()
	hooksDir := filepath.Join(tempDir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o755))

	// Create a non-executable file
	nonExecFile := filepath.Join(hooksDir, "non_exec_hook")
	require.NoError(t, os.WriteFile(nonExecFile, []byte("#!/bin/bash\necho 'before_tool_call'"), 0o644))

	discovery, err := NewDiscovery(WithHookDirs(hooksDir))
	require.NoError(t, err)

	hooks, err := discovery.DiscoverHooks()
	require.NoError(t, err)

	// No hooks should be discovered (not executable)
	totalHooks := 0
	for _, h := range hooks {
		totalHooks += len(h)
	}
	assert.Equal(t, 0, totalHooks)
}

func TestHookPrecedence(t *testing.T) {
	// Create two temp directories
	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	// Create a hook with the same name in both directories
	hookContent := `#!/bin/bash
if [ "$1" == "hook" ]; then
    echo "before_tool_call"
    exit 0
fi
exit 0
`

	hookPath1 := filepath.Join(tempDir1, "test_hook")
	hookPath2 := filepath.Join(tempDir2, "test_hook")
	require.NoError(t, os.WriteFile(hookPath1, []byte(hookContent), 0o755))
	require.NoError(t, os.WriteFile(hookPath2, []byte(hookContent), 0o755))

	// tempDir1 should take precedence
	discovery, err := NewDiscovery(WithHookDirs(tempDir1, tempDir2))
	require.NoError(t, err)

	hooks, err := discovery.DiscoverHooks()
	require.NoError(t, err)

	// Only one hook should be discovered (from tempDir1)
	beforeToolCallHooks := hooks[HookTypeBeforeToolCall]
	assert.Len(t, beforeToolCallHooks, 1)
	assert.Equal(t, hookPath1, beforeToolCallHooks[0].Path)
}

func TestExecuteUserMessageSend_EmptyResult(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	ctx := context.Background()
	result, err := manager.ExecuteUserMessageSend(ctx, UserMessageSendPayload{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Blocked)
}

func TestExecuteBeforeToolCall_EmptyResult(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	ctx := context.Background()
	result, err := manager.ExecuteBeforeToolCall(ctx, BeforeToolCallPayload{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Blocked)
}

func TestExecuteAfterToolCall_EmptyResult(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	ctx := context.Background()
	result, err := manager.ExecuteAfterToolCall(ctx, AfterToolCallPayload{})
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Nil(t, result.Output)
}

func TestExecuteAgentStop_EmptyResult(t *testing.T) {
	manager := HookManager{
		hooks: make(map[HookType][]*Hook),
	}

	ctx := context.Background()
	result, err := manager.ExecuteAgentStop(ctx, AgentStopPayload{})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestPayloadSerialization_UserMessageSend(t *testing.T) {
	payload := UserMessageSendPayload{
		BasePayload: BasePayload{
			Event:     HookTypeUserMessageSend,
			ConvID:    "test-conv-123",
			CWD:       "/test/path",
			InvokedBy: InvokedByMain,
		},
		Message: "Hello, world!",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded UserMessageSendPayload
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, payload.Event, decoded.Event)
	assert.Equal(t, payload.ConvID, decoded.ConvID)
	assert.Equal(t, payload.CWD, decoded.CWD)
	assert.Equal(t, payload.InvokedBy, decoded.InvokedBy)
	assert.Equal(t, payload.Message, decoded.Message)
}

func TestPayloadSerialization_BeforeToolCall(t *testing.T) {
	payload := BeforeToolCallPayload{
		BasePayload: BasePayload{
			Event:     HookTypeBeforeToolCall,
			ConvID:    "test-conv-456",
			CWD:       "/test/cwd",
			InvokedBy: InvokedBySubagent,
		},
		ToolName:   "bash",
		ToolInput:  json.RawMessage(`{"command":"ls -la"}`),
		ToolUserID: "tool-123",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded BeforeToolCallPayload
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, payload.ToolName, decoded.ToolName)
	assert.Equal(t, string(payload.ToolInput), string(decoded.ToolInput))
	assert.Equal(t, payload.ToolUserID, decoded.ToolUserID)
}

func TestResultSerialization_BeforeToolCallResult(t *testing.T) {
	result := BeforeToolCallResult{
		Blocked: true,
		Reason:  "Security policy violation",
		Input:   json.RawMessage(`{"modified":true}`),
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded BeforeToolCallResult
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, result.Blocked, decoded.Blocked)
	assert.Equal(t, result.Reason, decoded.Reason)
	assert.Equal(t, string(result.Input), string(decoded.Input))
}

func TestResultSerialization_UserMessageSendResult(t *testing.T) {
	result := UserMessageSendResult{
		Blocked: true,
		Reason:  "Message contains blocked content",
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded UserMessageSendResult
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, result.Blocked, decoded.Blocked)
	assert.Equal(t, result.Reason, decoded.Reason)
}

func TestResultSerialization_AgentStopResult(t *testing.T) {
	result := AgentStopResult{
		FollowUpMessages: []string{
			"Please review the changes and confirm they look correct.",
			"Should I run the tests now?",
		},
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded AgentStopResult
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, result.FollowUpMessages, decoded.FollowUpMessages)
	assert.Len(t, decoded.FollowUpMessages, 2)
}

func TestResultSerialization_AgentStopResult_Empty(t *testing.T) {
	result := AgentStopResult{}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded AgentStopResult
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Nil(t, decoded.FollowUpMessages)
}

func TestDenyFast_BeforeToolCall_FirstHookBlocks(t *testing.T) {
	tempDir := t.TempDir()

	// Create two hooks: first blocks, second allows
	blockingHook := filepath.Join(tempDir, "01-blocking-hook")
	allowingHook := filepath.Join(tempDir, "02-allowing-hook")

	// Hook that reports as before_tool_call and blocks
	blockingScript := `#!/bin/bash
if [ "$1" == "hook" ]; then echo "before_tool_call"; exit 0; fi
if [ "$1" == "run" ]; then echo '{"blocked":true,"reason":"blocked by first hook"}'; exit 0; fi
`
	// Hook that reports as before_tool_call and allows
	allowingScript := `#!/bin/bash
if [ "$1" == "hook" ]; then echo "before_tool_call"; exit 0; fi
if [ "$1" == "run" ]; then echo '{"blocked":false}'; exit 0; fi
`

	require.NoError(t, os.WriteFile(blockingHook, []byte(blockingScript), 0o755))
	require.NoError(t, os.WriteFile(allowingHook, []byte(allowingScript), 0o755))

	manager, err := NewHookManager(WithHookDirs(tempDir))
	require.NoError(t, err)

	hooks := manager.GetHooks(HookTypeBeforeToolCall)
	require.Len(t, hooks, 2, "should have 2 hooks")

	payload := BeforeToolCallPayload{
		BasePayload: BasePayload{
			Event:  HookTypeBeforeToolCall,
			ConvID: "test-conv",
		},
		ToolName: "bash",
	}

	result, err := manager.ExecuteBeforeToolCall(context.Background(), payload)
	require.NoError(t, err)

	// Deny-fast: first hook blocks, should return immediately without executing second
	assert.True(t, result.Blocked)
	assert.Equal(t, "blocked by first hook", result.Reason)
}

func TestDenyFast_UserMessageSend_FirstHookBlocks(t *testing.T) {
	tempDir := t.TempDir()

	// Create two hooks: first blocks, second allows
	blockingHook := filepath.Join(tempDir, "01-blocking-hook")
	allowingHook := filepath.Join(tempDir, "02-allowing-hook")

	blockingScript := `#!/bin/bash
if [ "$1" == "hook" ]; then echo "user_message_send"; exit 0; fi
if [ "$1" == "run" ]; then echo '{"blocked":true,"reason":"blocked by first hook"}'; exit 0; fi
`
	allowingScript := `#!/bin/bash
if [ "$1" == "hook" ]; then echo "user_message_send"; exit 0; fi
if [ "$1" == "run" ]; then echo '{"blocked":false}'; exit 0; fi
`

	require.NoError(t, os.WriteFile(blockingHook, []byte(blockingScript), 0o755))
	require.NoError(t, os.WriteFile(allowingHook, []byte(allowingScript), 0o755))

	manager, err := NewHookManager(WithHookDirs(tempDir))
	require.NoError(t, err)

	hooks := manager.GetHooks(HookTypeUserMessageSend)
	require.Len(t, hooks, 2, "should have 2 hooks")

	payload := UserMessageSendPayload{
		BasePayload: BasePayload{
			Event:  HookTypeUserMessageSend,
			ConvID: "test-conv",
		},
		Message: "test message",
	}

	result, err := manager.ExecuteUserMessageSend(context.Background(), payload)
	require.NoError(t, err)

	// Deny-fast: first hook blocks, should return immediately
	assert.True(t, result.Blocked)
	assert.Equal(t, "blocked by first hook", result.Reason)
}

func TestDenyFast_AllHooksAllow(t *testing.T) {
	tempDir := t.TempDir()

	// Create two hooks that both allow
	hook1 := filepath.Join(tempDir, "01-allowing-hook")
	hook2 := filepath.Join(tempDir, "02-allowing-hook")

	allowingScript := `#!/bin/bash
if [ "$1" == "hook" ]; then echo "before_tool_call"; exit 0; fi
if [ "$1" == "run" ]; then echo '{"blocked":false}'; exit 0; fi
`

	require.NoError(t, os.WriteFile(hook1, []byte(allowingScript), 0o755))
	require.NoError(t, os.WriteFile(hook2, []byte(allowingScript), 0o755))

	manager, err := NewHookManager(WithHookDirs(tempDir))
	require.NoError(t, err)

	payload := BeforeToolCallPayload{
		BasePayload: BasePayload{
			Event:  HookTypeBeforeToolCall,
			ConvID: "test-conv",
		},
		ToolName: "bash",
	}

	result, err := manager.ExecuteBeforeToolCall(context.Background(), payload)
	require.NoError(t, err)

	// All hooks allow, should not be blocked
	assert.False(t, result.Blocked)
}

func TestAgentStop_AccumulatesFollowUpMessages(t *testing.T) {
	tempDir := t.TempDir()

	// Create two hooks that each return follow-up messages
	hook1 := filepath.Join(tempDir, "01-hook")
	hook2 := filepath.Join(tempDir, "02-hook")

	hook1Script := `#!/bin/bash
if [ "$1" == "hook" ]; then echo "agent_stop"; exit 0; fi
if [ "$1" == "run" ]; then echo '{"follow_up_messages":["message from hook 1"]}'; exit 0; fi
`
	hook2Script := `#!/bin/bash
if [ "$1" == "hook" ]; then echo "agent_stop"; exit 0; fi
if [ "$1" == "run" ]; then echo '{"follow_up_messages":["message from hook 2","another message"]}'; exit 0; fi
`

	require.NoError(t, os.WriteFile(hook1, []byte(hook1Script), 0o755))
	require.NoError(t, os.WriteFile(hook2, []byte(hook2Script), 0o755))

	manager, err := NewHookManager(WithHookDirs(tempDir))
	require.NoError(t, err)

	hooks := manager.GetHooks(HookTypeAgentStop)
	require.Len(t, hooks, 2, "should have 2 hooks")

	payload := AgentStopPayload{
		BasePayload: BasePayload{
			Event:  HookTypeAgentStop,
			ConvID: "test-conv",
		},
	}

	result, err := manager.ExecuteAgentStop(context.Background(), payload)
	require.NoError(t, err)

	// Should accumulate messages from all hooks
	require.Len(t, result.FollowUpMessages, 3)
	assert.Equal(t, "message from hook 1", result.FollowUpMessages[0])
	assert.Equal(t, "message from hook 2", result.FollowUpMessages[1])
	assert.Equal(t, "another message", result.FollowUpMessages[2])
}
