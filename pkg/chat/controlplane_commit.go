package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

func (r *ControlPlaneChatRunner) commitTargetQuery(target WorkspaceTarget) (url.Values, error) {
	if target.RunnerID == "" && target.ConversationID == "" {
		target.RunnerID = r.runnerID
	}
	if target.RunnerID == "" && target.ConversationID == "" {
		return nil, errors.New("commit requires a runner or conversation target")
	}
	query := url.Values{}
	for key, value := range map[string]string{"runnerId": target.RunnerID, "conversationId": target.ConversationID, "cwd": target.CWD} {
		if value != "" {
			query.Set(key, value)
		}
	}
	return query, nil
}

// PrepareCommit reads a bounded, immutable staged diff on the selected runner.
func (r *ControlPlaneChatRunner) PrepareCommit(ctx context.Context, target WorkspaceTarget) (protocol.WorkspaceGitCommitSnapshot, error) {
	var result protocol.WorkspaceGitCommitSnapshot
	query, err := r.commitTargetQuery(target)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = r.conversationAPIRequest(ctx, http.MethodGet, []string{"api", "git", "commit"}, query, &result)
	if err != nil {
		return result, err
	}
	if result.RunnerID == "" || result.Diff == "" {
		return result, errors.New("daemon returned an incomplete commit snapshot")
	}
	return result, (protocol.WorkspaceGitCommitParams{CWD: result.CWD, Head: result.Head, HeadRef: result.HeadRef, Tree: result.Tree, Generation: result.Generation, Message: "validate snapshot"}).Validate()
}

// CreateCommit submits one explicit approval without retrying an uncertain mutation.
func (r *ControlPlaneChatRunner) CreateCommit(ctx context.Context, target WorkspaceTarget, approval protocol.WorkspaceGitCommitParams) (protocol.WorkspaceGitCommitResult, error) {
	var result protocol.WorkspaceGitCommitResult
	if err := approval.Validate(); err != nil {
		return result, err
	}
	query, err := r.commitTargetQuery(target)
	if err != nil {
		return result, err
	}
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "git", "commit")
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(approval)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?"+query.Encode(), bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	r.authorize(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return result, errors.Wrap(err, "runner commit was not acknowledged; inspect Git history before retrying (not retried)")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, errors.Wrap(controlPlaneResponseError(response), "runner commit was not acknowledged; inspect Git history before retrying (not retried)")
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return result, errors.Wrap(err, "cannot read commit acknowledgement; inspect Git history before retrying (not retried)")
	}
	if result.Commit == "" {
		return result, errors.New("daemon did not acknowledge a commit identity; inspect Git history before retrying")
	}
	return result, nil
}
