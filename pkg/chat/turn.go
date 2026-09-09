package chat

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// TurnReceipt reconciles an ordinary persisted conversation submission. It is
// not an instruction to retry execution or replay transient tool/UI events.
type TurnReceipt struct {
	ConversationID  string    `json:"conversationId" db:"conversation_id"`
	TurnID          string    `json:"turnId" db:"turn_id"`
	RunID           string    `json:"runId,omitempty" db:"run_id"`
	Status          string    `json:"status" db:"status"`
	CancelRequested bool      `json:"cancelRequested,omitempty" db:"cancel_requested"`
	Result          *string   `json:"result,omitempty" db:"result"`
	Error           string    `json:"error,omitempty" db:"error"`
	CreatedAt       time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt       time.Time `json:"updatedAt" db:"updated_at"`
}

// Terminal reports an authoritative outcome, including interrupted execution.
func (r TurnReceipt) Terminal() bool {
	switch r.Status {
	case "succeeded", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

// GetTurnReceipt queries durable status without submitting or restarting work.
func (r *Client) GetTurnReceipt(ctx context.Context, conversationID, turnID string) (TurnReceipt, error) {
	conversationID, turnID = strings.TrimSpace(conversationID), strings.TrimSpace(turnID)
	for _, id := range []string{conversationID, turnID} {
		if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
			return TurnReceipt{}, errors.New("conversation and turn IDs are required and must be single path segments")
		}
	}
	var receipt TurnReceipt
	err := r.conversationAPIRequest(ctx, http.MethodGet, []string{"api", "conversations", conversationID, "turns", turnID}, nil, &receipt)
	if err == nil {
		err = validateTurnReceipt(receipt, conversationID, turnID)
	}
	return receipt, err
}

func validateTurnReceipt(receipt TurnReceipt, conversationID, turnID string) error {
	if receipt.ConversationID != conversationID || receipt.TurnID != turnID {
		return &ControlPlaneStreamProtocolError{err: errors.New("turn receipt does not match the requested conversation and turn")}
	}
	if !receipt.Terminal() && receipt.Status != "accepted" && receipt.Status != "running" {
		return &ControlPlaneStreamProtocolError{err: errors.New("turn receipt has an unknown status")}
	}
	return nil
}

// UncertainSubmissionError retains the identities needed for safe reconciliation.
// Unwrap preserves transport/cancellation errors for existing callers.
type UncertainSubmissionError struct {
	ConversationID, TurnID string
	Err                    error
}

func (e *UncertainSubmissionError) Error() string {
	return fmt.Sprintf("could not confirm the result; check 'kodelet conversation turn %s %s' before sending the request again: %v", e.ConversationID, e.TurnID, e.Err)
}
func (e *UncertainSubmissionError) Unwrap() error { return e.Err }

// Retryable forbids execution retry; callers can make a read-only status query.
func (e *UncertainSubmissionError) Retryable() bool { return false }

// TurnPendingError means the daemon already owns this exact submission.
type TurnPendingError struct{ Receipt TurnReceipt }

func (e *TurnPendingError) Error() string {
	return fmt.Sprintf("turn %s in conversation %s is already %s; check its saved status before sending the request again", e.Receipt.TurnID, e.Receipt.ConversationID, e.Receipt.Status)
}

// Retryable is false because the daemon already owns this submission.
func (e *TurnPendingError) Retryable() bool { return false }
