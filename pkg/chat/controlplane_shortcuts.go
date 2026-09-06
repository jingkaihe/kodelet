package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

// WorkspaceShortcutRequest binds an advertised shortcut to its discovery scope.
// RunID is present only for discovery of an active, pinned environment.
type WorkspaceShortcutRequest struct {
	Target   WorkspaceTarget             `json:"target"`
	RunID    string                      `json:"runId,omitempty"`
	Digest   string                      `json:"digest"`
	Shortcut protocol.ShortcutDescriptor `json:"shortcut"`
}

type shortcutEventSink func(ChatEvent) error

func (f shortcutEventSink) Send(event ChatEvent) error { return f(event) }

func (r WorkspaceShortcutRequest) Validate() error {
	if r.Target.RunnerID == "" && r.Target.ConversationID == "" {
		return errors.New("shortcut requires a runner or conversation target")
	}
	if err := (protocol.WorkspaceDiscoverParams{Options: r.Target.Options}).Validate(); err != nil {
		return err
	}
	if r.Target.Options != nil && r.Target.Options.NoExtensions != nil && *r.Target.Options.NoExtensions {
		return errors.New("extension shortcuts are disabled")
	}
	key, err := extensions.NormalizeShortcutKey(r.Shortcut.Key)
	if err != nil || key != r.Shortcut.Key || strings.TrimSpace(r.Shortcut.ExtensionID) == "" || r.Shortcut.Generation == 0 || r.Digest == "" {
		return errors.New("shortcut requires a canonical key, extension identity, generation and discovery digest")
	}
	if r.RunID != "" && r.Target.ConversationID == "" {
		return errors.New("active shortcut requires its conversation")
	}
	return nil
}

func (r *WorkspaceShortcutRequest) UnmarshalJSON(data []byte) error {
	type wire WorkspaceShortcutRequest
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var envelope struct {
		Target map[string]json.RawMessage `json:"target"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	for key, raw := range envelope.Target {
		if strings.EqualFold(key, "options") && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("shortcut options must not be null")
		}
	}
	request := WorkspaceShortcutRequest(value)
	if err := request.Validate(); err != nil {
		return err
	}
	*r = request
	return nil
}

// ExecuteWorkspaceShortcut performs one bounded runner-side invocation. Stream
// loss cancels this invocation; it never cancels an active conversation turn.
func (r *ControlPlaneChatRunner) ExecuteWorkspaceShortcut(ctx context.Context, request WorkspaceShortcutRequest) (runnerpayload.ShortcutExecuteResult, error) {
	var result runnerpayload.ShortcutExecuteResult
	if err := request.Validate(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "chat", "shortcuts")
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	r.authorize(httpRequest)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set(ClientIDHeader, r.clientID)
	httpRequest.Header.Set(UICapabilitiesHeader, controlPlaneUICapabilitiesHeader(ctx))
	response, err := r.client.Do(httpRequest)
	if err != nil {
		return result, errors.Wrap(err, "shortcut submission failed; not retried")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, controlPlaneResponseError(response)
	}
	received := false
	_, err = r.consumeChatStream(ctx, response.Body, "", shortcutEventSink(func(event ChatEvent) error {
		if event.Kind != "shortcut-result" {
			return nil
		}
		if received {
			return errors.New("duplicate shortcut result")
		}
		received = true
		data, err := json.Marshal(event.Content)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &result)
	}), true, true, "")
	if err == nil && !received {
		err = errors.New("shortcut stream ended without a result; not retried")
	}
	return result, err
}
