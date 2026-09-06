package client

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildHostFencesLeasesAndNeverFallsBack(t *testing.T) {
	for _, scenario := range []string{"no peer", "provisional", "wrong owner", "wrong run", "absent", "no active tool", "delegate", "unknown options"} {
		t.Run(scenario, func(t *testing.T) {
			owner := extensions.UIExtensionOwner{ExtensionID: "search", Generation: 7}
			cwd := t.TempDir()
			resources := &runnerBackgroundResources{workingDirectory: cwd, runIDs: map[string]struct{}{"parent": {}}, leases: map[string]runnerBackgroundLease{"lease": {owner: owner}}}
			s := &Service{runs: map[string]*activeRun{"parent": {manifest: runnerpayload.Manifest{WorkingDirectory: cwd}}}, backgroundLeases: map[string]*runnerBackgroundResources{"lease": resources}}
			ctx := s.decorateRunContext(t.Context(), "parent", "conversation")
			ctx = context.WithValue(ctx, childToolKey{}, "tool")
			raw := json.RawMessage(`{"requestId":"one","profile":"search","message":"search","leaseId":"lease"}`)
			switch scenario {
			case "provisional":
				resources.leases["lease"] = runnerBackgroundLease{owner: owner, openingRunID: "parent"}
			case "wrong owner":
				owner.Generation++
			case "wrong run":
				delete(resources.runIDs, "parent")
			case "absent":
				delete(s.backgroundLeases, "lease")
			case "no active tool":
				raw = []byte(`{"requestId":"one","profile":"search","message":"search"}`)
				ctx = s.decorateRunContext(t.Context(), "parent", "conversation")
			case "unknown options":
				raw = []byte(`{"requestId":"one","profile":"search","message":"search","options":{"apiKey":"no"}}`)
			case "delegate":
				s.peer = &modelHelperPeer{call: func(_ context.Context, method string, params, result any) error {
					assert.Equal(t, delegation.StartMethod, method)
					p := params.(delegation.Params)
					assert.Equal(t, "parent", p.RunID)
					assert.Equal(t, "tool", p.ToolCallID)
					assert.Equal(t, uint64(7), p.Generation)
					assert.Equal(t, cwd, p.Request.CWD)
					*result.(*delegation.Result) = delegation.Result{Identity: delegation.Identity{ConversationID: "child"}}
					return nil
				}}
			}
			_, err := s.ChildRequest(ctx, &recordingUIExtensionSource{owner: owner}, delegation.StartMethod, raw)
			if scenario == "delegate" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if scenario == "provisional" {
				assert.ErrorContains(t, err, "provisional")
			}
			if scenario == "no peer" {
				assert.ErrorContains(t, err, "central child execution is unavailable")
			}
		})
	}
}

func TestChildExplicitReleaseWaitsAndRetriesWithoutNewAdmission(t *testing.T) {
	owner := extensions.UIExtensionOwner{ExtensionID: "search", Generation: 7}
	resources := &runnerBackgroundResources{attachedRunID: "parent", workingDirectory: t.TempDir(), runIDs: map[string]struct{}{"parent": {}}, leases: map[string]runnerBackgroundLease{"lease": {owner: owner}}}
	s := &Service{backgroundLeases: map[string]*runnerBackgroundResources{"lease": resources}}
	ctx := s.decorateRunContext(t.Context(), "parent", "conversation")
	source := &recordingUIExtensionSource{owner: owner}
	s.peer = &modelHelperPeer{call: func(_ context.Context, method string, _ any, _ any) error {
		require.Equal(t, delegation.StartMethod, method)
		return errors.New("start reply lost")
	}}
	_, err := s.ChildRequest(ctx, source, delegation.StartMethod, json.RawMessage(`{"requestId":"uncertain","profile":"search","message":"hello","leaseId":"lease"}`))
	require.ErrorContains(t, err, "reply lost")
	entered, complete := make(chan struct{}), make(chan struct{})
	s.peer = &modelHelperPeer{call: func(ctx context.Context, method string, params, result any) error {
		assert.Equal(t, delegation.ReleaseMethod, method)
		assert.Empty(t, params.(delegation.Params).RunID)
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-complete:
			*result.(*delegation.Result) = delegation.Result{Done: true}
			return nil
		}
	}}
	releaseCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := s.ReleaseBackgroundTask(releaseCtx, source, extensions.BackgroundTaskReleaseRequest{LeaseID: "lease"})
		finished <- err
	}()
	<-entered
	_, err = s.ChildRequest(ctx, source, delegation.StartMethod, json.RawMessage(`{"requestId":"late","profile":"search","message":"hello","leaseId":"lease"}`))
	require.ErrorContains(t, err, "being released")
	require.ErrorContains(t, <-finished, "unconfirmed")
	require.Contains(t, s.backgroundLeases, "lease", "uncertain cleanup remains reconcilable")
	s.peer = &modelHelperPeer{call: func(_ context.Context, method string, _ any, result any) error {
		assert.Equal(t, delegation.ReleaseMethod, method)
		*result.(*delegation.Result) = delegation.Result{Done: true}
		return nil
	}}
	response, err := s.ReleaseBackgroundTask(t.Context(), source, extensions.BackgroundTaskReleaseRequest{LeaseID: "lease"})
	require.NoError(t, err)
	assert.True(t, response.Released)
	assert.NotContains(t, s.backgroundLeases, "lease")
}

func TestChildWireForwardsExactRunSteerAndPreservesResumeCWD(t *testing.T) {
	owner := extensions.UIExtensionOwner{ExtensionID: "search", Generation: 7}
	resources := &runnerBackgroundResources{workingDirectory: t.TempDir(), runIDs: map[string]struct{}{"parent": {}}, leases: map[string]runnerBackgroundLease{"lease": {owner: owner}}}
	s := &Service{backgroundLeases: map[string]*runnerBackgroundResources{"lease": resources}}
	ctx := s.decorateRunContext(t.Context(), "parent", "conversation")
	for _, method := range []string{delegation.ReadMethod, delegation.CancelMethod, delegation.SteerMethod, delegation.StartMethod} {
		s.peer = &modelHelperPeer{call: func(_ context.Context, actual string, params, result any) error {
			assert.Equal(t, method, actual)
			p := params.(delegation.Params)
			if method == delegation.StartMethod {
				assert.Equal(t, "child", p.Request.Resume)
				assert.Empty(t, p.Request.CWD)
			} else {
				assert.Equal(t, "exact-run", p.ChildRunID)
			}
			if method == delegation.SteerMethod {
				assert.Equal(t, "guidance", p.Message)
				assert.Equal(t, "idempotency", p.RequestID)
				*result.(*delegation.SteerResult) = delegation.SteerResult{Outcome: "injected"}
			}
			return nil
		}}
		raw := json.RawMessage(`{"childId":"child","childRunId":"exact-run","message":"guidance","requestId":"idempotency","leaseId":"lease"}`)
		if method == delegation.StartMethod {
			raw = json.RawMessage(`{"resume":"child","message":"followup","profile":"search","requestId":"new","leaseId":"lease"}`)
		}
		_, err := s.ChildRequest(ctx, &recordingUIExtensionSource{owner: owner}, method, raw)
		require.NoError(t, err)
	}
}

func TestChildRetainedHandleKeepsOriginRunAcrossReattachment(t *testing.T) {
	owner := extensions.UIExtensionOwner{ExtensionID: "search", Generation: 7}
	resources := &runnerBackgroundResources{workingDirectory: t.TempDir(), runIDs: map[string]struct{}{"original": {}, "reattached": {}}, leases: map[string]runnerBackgroundLease{"lease": {owner: owner, childAuthority: true, childAuthorityRunID: "original"}}}
	s := &Service{backgroundLeases: map[string]*runnerBackgroundResources{"lease": resources}}
	ctx := s.decorateRunContext(t.Context(), "reattached", "conversation")
	ctx = context.WithValue(ctx, childToolKey{}, "unrelated-collect-tool")
	for _, method := range []string{delegation.ReadMethod, delegation.CancelMethod, delegation.SteerMethod, delegation.StartMethod} {
		s.peer = &modelHelperPeer{call: func(_ context.Context, actual string, params, _ any) error {
			assert.Equal(t, method, actual)
			p := params.(delegation.Params)
			assert.Equal(t, "original", p.RunID)
			assert.Empty(t, p.ToolCallID)
			if method != delegation.StartMethod {
				assert.Equal(t, "exact-child-run", p.ChildRunID)
			}
			return nil
		}}
		raw := json.RawMessage(`{"childId":"child","childRunId":"exact-child-run","leaseId":"lease","message":"guidance","requestId":"one"}`)
		if method == delegation.StartMethod {
			raw = json.RawMessage(`{"resume":"child","leaseId":"lease","message":"followup","requestId":"next","profile":"search"}`)
		}
		_, err := s.ChildRequest(ctx, &recordingUIExtensionSource{owner: owner}, method, raw)
		require.NoError(t, err)
	}
}
