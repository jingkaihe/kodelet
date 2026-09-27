package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

const maxServerStatusResponseSize = 64 << 10

// ServerStatus is the connected server's version, build metadata, and readiness.
type ServerStatus struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit,omitempty"`
	BuildTime string `json:"buildTime,omitempty"`
	APIReady  bool   `json:"apiReady"`
}

// ServerStatus reports the connected server's version and build metadata.
func (r *Client) ServerStatus(ctx context.Context) (ServerStatus, error) {
	var result ServerStatus
	if r == nil || r.client == nil {
		return result, errors.New("the chat connection is not initialized")
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "status")
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, errors.Wrap(err, "failed to create server status request")
	}
	r.authorize(request)
	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.Wrap(err, "could not contact the server")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, controlPlaneResponseError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxServerStatusResponseSize)).Decode(&result); err != nil {
		return result, errors.Wrap(err, "invalid server status response")
	}
	return result, nil
}

// RunnerStatus describes the selected runner and the current user's terminal permission.
type RunnerStatus struct {
	Runner             *runnerregistry.Runner
	TerminalAuthorized bool
}

// RunnerStatus inspects the selected runner without requiring workspace readiness.
// Saved conversation affinity takes precedence over all other runner selections.
// An authorization failure returns the runner with permissions disabled and an error.
func (r *Client) RunnerStatus(ctx context.Context, target WorkspaceTarget) (RunnerStatus, error) {
	var result RunnerStatus
	if r == nil || r.client == nil {
		return result, errors.New("the chat connection is not initialized")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	runnerID := strings.TrimSpace(target.RunnerID)
	if conversationID := strings.TrimSpace(target.ConversationID); conversationID != "" {
		var conversation struct {
			RunnerID string `json:"runnerId"`
		}
		if err := r.conversationAPIRequest(ctx, http.MethodGet,
			[]string{"api", "conversations", conversationID}, url.Values{"format": {"stream"}}, &conversation,
		); err != nil {
			return result, errors.Wrap(err, "could not load the conversation's runner")
		}
		runnerID = strings.TrimSpace(conversation.RunnerID)
		if runnerID == "" {
			return result, errors.New("this conversation has no saved runner")
		}
	} else {
		if runnerID == "" {
			runnerID = r.runnerID
		}
		if runnerID == "" {
			settings, err := r.ChatSettings(ctx, target.Profile)
			if err != nil {
				return result, errors.Wrap(err, "could not determine the default runner")
			}
			runnerID = strings.TrimSpace(settings.DefaultRunnerID)
		}
	}
	if runnerID == "" {
		return result, nil
	}

	var runner runnerregistry.Runner
	if err := r.conversationAPIRequest(ctx, http.MethodGet,
		[]string{"api", "runners", runnerID}, nil, &runner,
	); err != nil {
		return result, errors.Wrapf(err, "could not load runner %q", runnerID)
	}
	result.Runner = &runner
	var principal struct {
		Roles []string `json:"roles"`
	}
	if err := r.conversationAPIRequest(ctx, http.MethodGet,
		[]string{"api", "auth", "me"}, nil, &principal,
	); err != nil {
		return result, errors.Wrap(err, "could not load runner permissions")
	}
	result.TerminalAuthorized = slices.Contains(principal.Roles, "terminal") || slices.Contains(principal.Roles, "admin")
	return result, nil
}
