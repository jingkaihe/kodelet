package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

func (r *Client) InspectWorkspace(ctx context.Context, target WorkspaceTarget, params protocol.WorkspaceInspectParams) (protocol.WorkspaceInspectResult, error) {
	var result protocol.WorkspaceInspectResult
	if err := params.Validate(); err != nil {
		return result, err
	}
	if target.ConversationID != "" || target.Options != nil {
		return result, errors.New("inspection requires a workspace target, not a conversation or execution options")
	}
	if params.CWD != "" || params.Profile != "" || params.EnvironmentProfile != "" {
		return result, errors.New("specify the directory and profiles in the workspace target")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "chat", "workspace-inspection")
	if err != nil {
		return result, err
	}
	query := target.queryValues()
	data, err := json.Marshal(params)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?"+query.Encode(), bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	r.authorize(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.Wrap(err, "could not complete workspace inspection; check the server connection before trying again")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, controlPlaneResponseError(response)
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result)
	return result, err
}

// WorkspaceRunners lists the available targets using this client's server and credential.
func (r *Client) WorkspaceRunners(ctx context.Context) ([]runnerregistry.Runner, error) {
	var result struct {
		Runners []runnerregistry.Runner `json:"runners"`
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := r.conversationAPIRequest(ctx, http.MethodGet, []string{"api", "runners"}, nil, &result)
	return result.Runners, err
}
