package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/pkg/errors"
)

// ConversationMoveRequest selects a saved destination without contacting a runner.
// An omitted CWD preserves the saved directory. Confirmation is the digest of a
// previously returned move plan, not permission to replace conversation history.
type ConversationMoveRequest struct {
	RunnerID     string `json:"runnerId"`
	CWD          string `json:"cwd,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
}

// ConversationMoveResult describes the source and destination, not execution readiness.
type ConversationMoveResult struct {
	ConversationID     string `json:"conversationId"`
	SourceRunnerID     string `json:"sourceRunnerId,omitempty"`
	SourceCWD          string `json:"sourceCwd"`
	RunnerID           string `json:"runnerId"`
	RunnerName         string `json:"runnerName,omitempty"`
	CWD                string `json:"cwd"`
	EnvironmentProfile string `json:"environmentProfile,omitempty"`
	Confirmation       string `json:"confirmation"`
	Moved              bool   `json:"moved"`
}

// MoveConversation plans or confirms a daemon-owned conversation move.
// It never retries a mutation after an uncertain transport outcome.
func (r *Client) MoveConversation(ctx context.Context, id string, params ConversationMoveRequest) (ConversationMoveResult, error) {
	var result ConversationMoveResult
	if strings.TrimSpace(id) == "" || strings.TrimSpace(params.RunnerID) == "" {
		return result, errors.New("conversation ID and explicit runner ID are required")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", id, "move")
	if err != nil {
		return result, err
	}
	body, err := json.Marshal(params)
	if err != nil {
		return result, errors.Wrap(err, "failed to encode move request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, errors.Wrap(err, "failed to create move request")
	}
	request.Header.Set("Content-Type", "application/json")
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.Wrap(err, "could not confirm the runner assignment; check 'kodelet conversation show' before trying again")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, controlPlaneResponseError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return result, errors.Wrap(err, "received an invalid response; check the conversation's saved runner before trying the move again")
	}
	return result, nil
}
