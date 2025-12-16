package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

// Execute runs all hooks of a given type with the provided payload.
// Returns the result bytes from the last successful hook execution.
func (m HookManager) Execute(ctx context.Context, hookType HookType, payload interface{}) ([]byte, error) {
	hooks := m.hooks[hookType]
	if len(hooks) == 0 {
		return nil, nil
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal payload")
	}

	var lastResult []byte
	for _, hook := range hooks {
		result, err := m.executeHook(ctx, hook, payloadBytes)
		if err != nil {
			// Log error but continue with other hooks
			logger.G(ctx).WithError(err).WithField("hook", hook.Name).Warn("hook execution failed")
			continue
		}
		lastResult = result
	}

	return lastResult, nil
}

// executeHook runs a single hook with timeout enforcement
func (m HookManager) executeHook(ctx context.Context, hook *Hook, payload []byte) ([]byte, error) {
	timeout := m.timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, hook.Path, "run")
	cmd.Stdin = bytes.NewReader(payload)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.Errorf("hook %s timed out after %s", hook.Name, timeout)
		}
		return nil, errors.Wrapf(err, "hook %s failed: %s", hook.Name, stderr.String())
	}

	return stdout.Bytes(), nil
}

// ExecuteUserMessageSend runs user_message_send hooks and returns typed result.
// Empty or nil output with exit code 0 is treated as "no action" (not blocked).
func (m HookManager) ExecuteUserMessageSend(ctx context.Context, payload UserMessageSendPayload) (*UserMessageSendResult, error) {
	resultBytes, err := m.Execute(ctx, HookTypeUserMessageSend, payload)
	if err != nil {
		return nil, err
	}
	if len(resultBytes) == 0 {
		return &UserMessageSendResult{}, nil // No output = not blocked
	}

	var result UserMessageSendResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal result")
	}
	return &result, nil
}

// ExecuteBeforeToolCall runs before_tool_call hooks and returns typed result.
// Empty or nil output with exit code 0 is treated as "no action" (not blocked, no modification).
func (m HookManager) ExecuteBeforeToolCall(ctx context.Context, payload BeforeToolCallPayload) (*BeforeToolCallResult, error) {
	resultBytes, err := m.Execute(ctx, HookTypeBeforeToolCall, payload)
	if err != nil {
		return nil, err
	}
	if len(resultBytes) == 0 {
		return &BeforeToolCallResult{}, nil // No output = not blocked, use original input
	}

	var result BeforeToolCallResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal result")
	}
	return &result, nil
}

// ExecuteAfterToolCall runs after_tool_call hooks and returns typed result.
// Empty or nil output with exit code 0 is treated as "no modification".
func (m HookManager) ExecuteAfterToolCall(ctx context.Context, payload AfterToolCallPayload) (*AfterToolCallResult, error) {
	resultBytes, err := m.Execute(ctx, HookTypeAfterToolCall, payload)
	if err != nil {
		return nil, err
	}
	if len(resultBytes) == 0 {
		return &AfterToolCallResult{}, nil // No output = use original output
	}

	var result AfterToolCallResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal result")
	}
	return &result, nil
}

// ExecuteAgentStop runs agent_stop hooks
func (m HookManager) ExecuteAgentStop(ctx context.Context, payload AgentStopPayload) (*AgentStopResult, error) {
	_, err := m.Execute(ctx, HookTypeAgentStop, payload)
	if err != nil {
		return nil, err
	}
	return &AgentStopResult{}, nil
}
