package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	conversationStatusMethod = "_conversation/status"
	conversationStopMethod   = "_conversation/stop"
	// codeResourceNotFound is ACP's error code for a missing resource.
	codeResourceNotFound = -32002
)

var (
	// ErrConversationNotFound reports that the daemon has neither the
	// conversation nor any turn submitted to it.
	ErrConversationNotFound = errors.New("conversation not found")
	// ErrConversationsUnsupported reports that kodelet acp predates
	// conversation status and stop (conversations version 1).
	ErrConversationsUnsupported = errors.New("kodelet acp does not support conversation status and stop (conversations version 1); update Kodelet and the daemon")
)

// Turn statuses reported by ConversationTurn.Status.
const (
	TurnAccepted    = "accepted"
	TurnRunning     = "running"
	TurnSucceeded   = "succeeded"
	TurnFailed      = "failed"
	TurnCancelled   = "cancelled"
	TurnInterrupted = "interrupted"
)

// ConversationStatus is a conversation's state without its messages, cheap
// enough to poll. A conversation whose first turn was accepted but not yet
// saved has only ID, Running, and LatestTurn.
type ConversationStatus struct {
	ID       string `json:"id"`
	CWD      string `json:"cwd,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Model    string `json:"model,omitempty"`
	RunnerID string `json:"runnerId,omitempty"`
	Summary  string `json:"summary,omitempty"`
	// Running is true while the daemon is running or about to run a turn.
	Running      bool              `json:"running"`
	MessageCount int               `json:"messageCount"`
	CreatedAt    *time.Time        `json:"createdAt,omitempty"`
	UpdatedAt    *time.Time        `json:"updatedAt,omitempty"`
	Usage        ConversationUsage `json:"usage"`
	// LatestTurn is the most recently submitted turn, or nil if the
	// conversation has none on record.
	LatestTurn *ConversationTurn `json:"latestTurn,omitempty"`
}

// ConversationUsage is a conversation's token usage and estimated cost.
type ConversationUsage struct {
	InputTokens          int     `json:"inputTokens"`
	OutputTokens         int     `json:"outputTokens"`
	CacheReadTokens      int     `json:"cacheReadTokens"`
	CacheWriteTokens     int     `json:"cacheWriteTokens"`
	InputCost            float64 `json:"inputCost"`
	OutputCost           float64 `json:"outputCost"`
	CacheReadCost        float64 `json:"cacheReadCost"`
	CacheWriteCost       float64 `json:"cacheWriteCost"`
	TotalCost            float64 `json:"totalCost"`
	CurrentContextWindow int     `json:"currentContextWindow"`
	MaxContextWindow     int     `json:"maxContextWindow"`
}

// ConversationTurn is the daemon's record of one submitted turn.
type ConversationTurn struct {
	ID string `json:"id"`
	// Status is one of the Turn* constants.
	Status string `json:"status"`
	// Result is the final reply of a succeeded turn.
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Terminal reports whether the turn has finished.
func (t ConversationTurn) Terminal() bool {
	return t.Status != TurnAccepted && t.Status != TurnRunning
}

// StopConversationOptions configures StopConversation.
type StopConversationOptions struct {
	// TurnID stops only that turn, including one the daemon has accepted
	// but not started. Empty stops whatever turn is running.
	TurnID string
}

// GetConversationStatus reads a conversation's status from the daemon
// without loading its messages. It starts a short-lived kodelet acp process
// and returns an error wrapping ErrConversationNotFound for an unknown
// conversation.
func (c *Client) GetConversationStatus(ctx context.Context, conversationID string) (ConversationStatus, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ConversationStatus{}, errors.New("conversation ID is required")
	}
	raw, err := c.conversationRequest(ctx, conversationStatusMethod, map[string]any{"conversationId": conversationID})
	if err != nil {
		return ConversationStatus{}, conversationError(conversationID, err)
	}
	var status ConversationStatus
	if err := json.Unmarshal(raw, &status); err != nil || status.ID != conversationID {
		return ConversationStatus{}, errors.New("invalid _conversation/status response from kodelet acp")
	}
	return status, nil
}

// StopConversation asks the daemon to stop a conversation's running turn. It
// returns once the stop is accepted; poll GetConversationStatus for the
// turn's final status.
func (c *Client) StopConversation(ctx context.Context, conversationID string, opts StopConversationOptions) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return errors.New("conversation ID is required")
	}
	params := map[string]any{"conversationId": conversationID}
	if turnID := strings.TrimSpace(opts.TurnID); turnID != "" {
		params["turnId"] = turnID
	}
	_, err := c.conversationRequest(ctx, conversationStopMethod, params)
	return conversationError(conversationID, err)
}

// conversationRequest sends one conversation request through a kodelet acp
// process that lives only for the call.
func (c *Client) conversationRequest(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	args := []string{"acp"}
	if c.opts.Server != "" {
		args = append(args, "--server", c.opts.Server)
	}
	proc, err := c.start(c.opts.Command, args, c.env())
	if err != nil {
		return nil, err
	}
	conn := newACPConn(proc, nil, nil)
	raw, err := conversationCall(ctx, conn, method, params)
	if closeErr := conn.close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return raw, err
}

func conversationCall(ctx context.Context, conn *acpConn, method string, params map[string]any) (json.RawMessage, error) {
	if err := conn.initialize(ctx); err != nil {
		return nil, err
	}
	conn.mu.Lock()
	supported := conn.conversations
	conn.mu.Unlock()
	if !supported {
		return nil, ErrConversationsUnsupported
	}
	return conn.request(ctx, method, params)
}

func conversationError(conversationID string, err error) error {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == codeResourceNotFound {
		return fmt.Errorf("%w: %s", ErrConversationNotFound, conversationID)
	}
	return err
}
