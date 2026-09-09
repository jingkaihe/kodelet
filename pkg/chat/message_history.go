package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

// LoadMessageHistory recalls raw composer messages from the selected runner's workspace.
func (r *Client) LoadMessageHistory(ctx context.Context, target WorkspaceTarget) (protocol.WorkspaceMessageHistoryResult, error) {
	return r.messageHistoryRequest(ctx, target, nil)
}

// AppendMessageHistory saves a raw composer message without starting a conversation or model turn.
func (r *Client) AppendMessageHistory(ctx context.Context, target WorkspaceTarget, entry messagehistory.Entry) error {
	if strings.TrimSpace(entry.Text) == "" {
		return errors.New("message history requires nonempty text")
	}
	_, err := r.messageHistoryRequest(ctx, target, &entry)
	return err
}

func (r *Client) messageHistoryRequest(ctx context.Context, target WorkspaceTarget, entry *messagehistory.Entry) (protocol.WorkspaceMessageHistoryResult, error) {
	var result protocol.WorkspaceMessageHistoryResult
	if r == nil || r.client == nil {
		return result, errors.New("the chat connection is not initialized")
	}
	if target.Options != nil {
		return result, errors.New("message history does not accept execution options")
	}
	if target.RunnerID == "" && target.ConversationID == "" {
		target.RunnerID = r.runnerID
	}
	if target.RunnerID == "" && target.ConversationID == "" {
		return result, errors.New("message history requires a runner or conversation target")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "chat", "message-history")
	if err != nil {
		return result, err
	}
	query := target.queryValues()
	method := http.MethodGet
	var body io.Reader
	if entry != nil {
		data, err := json.Marshal(entry)
		if err != nil {
			return result, err
		}
		if len(data) > 1<<20 {
			return result, errors.New("message history entry exceeds 1 MiB")
		}
		method, body = http.MethodPost, bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint+"?"+query.Encode(), body)
	if err != nil {
		return result, err
	}
	r.authorize(request)
	if entry != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.Wrap(err, "could not access message history on the runner")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, controlPlaneResponseError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxControlPlaneConversationHistorySize)).Decode(&result); err != nil {
		return result, errors.Wrap(err, "could not decode message history")
	}
	return result, nil
}
