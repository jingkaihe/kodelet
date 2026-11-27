package chat

import (
	"context"
	"net/http"
	"strings"
	"time"

	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// ConversationStatus is a conversation's state without its messages, cheap
// enough to poll. A conversation whose first turn was accepted but not yet
// saved has an ID and LatestTurn only.
type ConversationStatus struct {
	ID           string            `json:"id"`
	CWD          string            `json:"cwd,omitempty"`
	Profile      string            `json:"profile,omitempty"`
	Model        string            `json:"model,omitempty"`
	RunnerID     string            `json:"runnerId,omitempty"`
	Summary      string            `json:"summary,omitempty"`
	Running      bool              `json:"running"`
	MessageCount int               `json:"messageCount"`
	CreatedAt    *time.Time        `json:"createdAt,omitempty"`
	UpdatedAt    *time.Time        `json:"updatedAt,omitempty"`
	Usage        ConversationUsage `json:"usage"`
	LatestTurn   *ConversationTurn `json:"latestTurn,omitempty"`
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

// ConversationTurn is the saved outcome of one submitted turn. Status is
// accepted, running, succeeded, failed, cancelled, or interrupted; Result is
// the turn's final reply.
type ConversationTurn struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// NewConversationUsage converts stored usage to its public form.
func NewConversationUsage(usage llmtypes.Usage) ConversationUsage {
	return ConversationUsage{
		InputTokens:          usage.InputTokens,
		OutputTokens:         usage.OutputTokens,
		CacheReadTokens:      usage.CacheReadInputTokens,
		CacheWriteTokens:     usage.CacheCreationInputTokens,
		InputCost:            usage.InputCost,
		OutputCost:           usage.OutputCost,
		CacheReadCost:        usage.CacheReadCost,
		CacheWriteCost:       usage.CacheCreationCost,
		TotalCost:            usage.InputCost + usage.OutputCost + usage.CacheReadCost + usage.CacheCreationCost,
		CurrentContextWindow: usage.CurrentContextWindow,
		MaxContextWindow:     usage.MaxContextWindow,
	}
}

// NewConversationTurn converts a turn receipt to its public form.
func NewConversationTurn(receipt TurnReceipt) ConversationTurn {
	turn := ConversationTurn{
		ID:        receipt.TurnID,
		Status:    receipt.Status,
		Error:     receipt.Error,
		CreatedAt: receipt.CreatedAt,
		UpdatedAt: receipt.UpdatedAt,
	}
	if receipt.Result != nil {
		turn.Result = *receipt.Result
	}
	return turn
}

// GetConversationStatus reads a conversation's status without its messages.
// It returns an error wrapping ErrConversationNotFound when the server has
// neither the conversation nor any turn for it.
func (r *Client) GetConversationStatus(ctx context.Context, conversationID string) (ConversationStatus, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" || conversationID == "." || conversationID == ".." || strings.ContainsAny(conversationID, "/\\") {
		return ConversationStatus{}, errors.New("conversation ID is required and must be a single path segment")
	}
	var status ConversationStatus
	err := r.conversationAPIRequest(ctx, http.MethodGet, []string{"api", "conversations", conversationID, "status"}, nil, &status)
	var httpErr *ControlPlaneHTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return ConversationStatus{}, errors.Wrapf(convtypes.ErrConversationNotFound, "%s", conversationID)
	}
	if err != nil {
		return ConversationStatus{}, err
	}
	if status.ID != conversationID {
		return ConversationStatus{}, &ControlPlaneStreamProtocolError{err: errors.New("conversation status does not match the requested conversation")}
	}
	return status, nil
}
