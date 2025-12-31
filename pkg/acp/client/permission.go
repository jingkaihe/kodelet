package client

import (
	"context"
	"encoding/json"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/pkg/errors"
)

// PermissionOutcome values
const (
	OutcomeSelected  = "selected"
	OutcomeDismissed = "dismissed"
	OutcomeTimeout   = "timeout"
)

// PermissionResult contains the result of a permission request
type PermissionResult struct {
	Outcome  string
	OptionID string
}

// RequestPermission sends a permission request to the client and waits for response.
// This is used to ask the user for confirmation before performing destructive operations.
func (c *Client) RequestPermission(ctx context.Context, sessionID acptypes.SessionID, toolCallID, toolName string, input json.RawMessage, message string, options []acptypes.PermissionOption) (*PermissionResult, error) {
	params := acptypes.RequestPermissionParams{
		SessionID: sessionID,
		ToolCall: acptypes.ToolCallForPermission{
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Input:      input,
		},
		Message: message,
		Options: options,
	}

	result, err := c.caller.CallClient(ctx, "session/request_permission", params)
	if err != nil {
		return nil, errors.Wrap(err, "permission request failed")
	}

	var resp acptypes.RequestPermissionResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, errors.Wrap(err, "failed to parse permission response")
	}

	return &PermissionResult{
		Outcome:  resp.Outcome.Outcome,
		OptionID: resp.Outcome.OptionID,
	}, nil
}

// RequestApproval is a convenience method for simple yes/no permission requests.
// Returns true if approved, false if denied or dismissed.
func (c *Client) RequestApproval(ctx context.Context, sessionID acptypes.SessionID, toolCallID, toolName string, input json.RawMessage, message string) (bool, error) {
	options := []acptypes.PermissionOption{
		{ID: "approve", Label: "Approve", Shortcut: "y", IsDefault: false},
		{ID: "deny", Label: "Deny", Shortcut: "n", IsDefault: true},
	}

	result, err := c.RequestPermission(ctx, sessionID, toolCallID, toolName, input, message, options)
	if err != nil {
		return false, err
	}

	return result.Outcome == OutcomeSelected && result.OptionID == "approve", nil
}
