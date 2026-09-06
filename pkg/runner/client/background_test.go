package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBackgroundTestService(t *testing.T, mode string) (*Service, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", t.TempDir())
	t.Setenv("KODELET_BACKGROUND_HELPER_MODE", mode)
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".kodelet", "extensions")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	executable, err := os.Executable()
	require.NoError(t, err)
	script := fmt.Sprintf("#!/bin/sh\nKODELET_BACKGROUND_HELPER=1 exec %q -test.run '^TestBackgroundExtensionHelper$'\n", executable)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kodelet-extension-lifetime"), []byte(script), 0o700))
	service, err := NewService(t.Context(), workspace, ServiceOptions{
		ConfigLoader: func(string) (llmtypes.Config, error) {
			return llmtypes.Config{Skills: &llmtypes.SkillsConfig{Enabled: false}}, nil
		},
	})
	require.NoError(t, err)
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner", Generation: 1}))
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	return service, workspace
}

type backgroundHelperState struct {
	PID          int    `json:"pid"`
	Conversation string `json:"conversation"`
	LeaseID      string `json:"leaseId"`
	DataDir      string `json:"dataDir"`
	Previous     string `json:"previous"`
	Error        string `json:"error"`
	ChildError   string `json:"childError"`
}

func backgroundState(t *testing.T, workspace, conversation string) backgroundHelperState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, conversation+".json"))
	require.NoError(t, err)
	var state backgroundHelperState
	require.NoError(t, json.Unmarshal(data, &state))
	require.Empty(t, state.Error)
	return state
}

func assertBackgroundProcessStopped(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH }, 3*time.Second, 10*time.Millisecond, "extension process must be reaped")
}

func TestBackgroundSessionStartLeaseIsProvisional(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	environment := &blockingOpenEnvironment{started: make(chan struct{})}
	service.environmentFactory = func(string, *extensions.Runtime) agentenv.Environment { return environment }
	opened := make(chan error, 1)
	go func() {
		_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "opening", ConversationID: "opening"})
		opened <- err
	}()
	select {
	case <-environment.started:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "environment did not start opening")
	}
	state := backgroundState(t, workspace, "opening")
	require.NotEmpty(t, state.LeaseID, "session.start must be able to acquire a provisional lease")
	assert.Contains(t, state.ChildError, "provisional until run.open succeeds")
	service.mu.Lock()
	resources := service.backgroundLeases[state.LeaseID]
	assert.NotNil(t, resources)
	assert.Empty(t, service.backgrounds, "opening leases must not publish a retained scope")
	assert.Empty(t, service.backgroundRunIDs)
	if resources != nil {
		assert.Equal(t, "opening", resources.leases[state.LeaseID].openingRunID)
	}
	service.mu.Unlock()
	source := &recordingUIExtensionSource{owner: extensions.UIExtensionOwner{ExtensionID: "second", Generation: 7}}
	ctx := service.decorateRunContext(t.Context(), "opening", "opening")
	for range maxRunnerBackgroundTasks - 1 {
		_, err := service.AcquireBackgroundTask(ctx, source, extensions.BackgroundTaskAcquireRequest{})
		require.NoError(t, err)
	}
	_, err := service.AcquireBackgroundTask(ctx, source, extensions.BackgroundTaskAcquireRequest{})
	require.ErrorContains(t, err, "at most 64", "provisional leases must reserve the same bounded capacity")
	_, err = service.ReleaseBackgroundTask(ctx, source, extensions.BackgroundTaskReleaseRequest{LeaseID: state.LeaseID})
	require.ErrorContains(t, err, "another extension process")
	require.NoError(t, service.cancelRun(t.Context(), "opening"))
	select {
	case err := <-opened:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "opening cancellation did not finish")
	}
	assertBackgroundProcessStopped(t, state.PID)
	assert.Empty(t, service.backgroundLeases)
	assert.Empty(t, service.backgrounds)
	assert.Equal(t, int32(1), environment.closeCalls.Load())
}

func TestBackgroundSessionStartLeaseActivatesAndReattaches(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "one", ConversationID: "conversation"})
	require.NoError(t, err)
	state := backgroundState(t, workspace, "conversation")
	require.NotEmpty(t, state.LeaseID)
	resources := service.backgrounds["conversation"]
	require.NotNil(t, resources)
	assert.Empty(t, resources.leases[state.LeaseID].openingRunID)
	require.NoError(t, service.closeRun(t.Context(), "one"))
	require.NoError(t, syscall.Kill(state.PID, 0), "activated lease retains the worker after run.close")
	_, err = service.openRun(t.Context(), protocol.RunOpenParams{RunID: "two", ConversationID: "conversation"})
	require.NoError(t, err)
	assert.Same(t, resources, service.runs["two"].resources)
	result, err := service.executeTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: "two", ToolCallID: "release", Name: "lifetime", Input: json.RawMessage(`{"operation":"release"}`)})
	require.NoError(t, err)
	assert.Empty(t, result.Result.Error)
	assert.Empty(t, service.backgroundLeases)
	require.NoError(t, service.closeRun(t.Context(), "two"))
	assertBackgroundProcessStopped(t, state.PID)
	data, err := os.ReadFile(filepath.Join(state.DataDir, "conversation.txt"))
	require.NoError(t, err)
	assert.Equal(t, "persisted", string(data), "process release must not remove extension-owned data")
	_, err = service.openRun(t.Context(), protocol.RunOpenParams{RunID: "three", ConversationID: "conversation"})
	require.NoError(t, err)
	fresh := backgroundState(t, workspace, "conversation")
	assert.Equal(t, "persisted", fresh.Previous)
	assert.NotEqual(t, state.PID, fresh.PID)
	require.NoError(t, service.Close())
	assertBackgroundProcessStopped(t, fresh.PID)
}

func TestBackgroundFailedOpenRevokesProvisionalLeases(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	environment := &failingOpenEnvironment{}
	service.environmentFactory = func(string, *extensions.Runtime) agentenv.Environment { return environment }
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "failed", ConversationID: "failed"})
	require.ErrorContains(t, err, "open failed")
	state := backgroundState(t, workspace, "failed")
	assert.NotEmpty(t, state.LeaseID)
	assertBackgroundProcessStopped(t, state.PID)
	assert.Empty(t, service.runs)
	assert.Empty(t, service.backgroundLeases)
	assert.Empty(t, service.backgrounds)
	assert.True(t, environment.closed)
}

func TestBackgroundInitializeLeaseActivatesAfterOpen(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "initialize-lease")
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "run", ConversationID: "conversation"})
	require.NoError(t, err)
	state := backgroundState(t, workspace, "conversation")
	require.NotEmpty(t, state.LeaseID)
	assert.Empty(t, service.backgrounds["conversation"].leases[state.LeaseID].openingRunID)
	require.NoError(t, service.closeRun(t.Context(), "run"))
	require.NoError(t, syscall.Kill(state.PID, 0))
	require.NoError(t, service.Close())
	assertBackgroundProcessStopped(t, state.PID)
}

func TestBackgroundCancelDuringSessionStart(t *testing.T) {
	for _, mode := range []string{"request", "run", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			service, workspace := newBackgroundTestService(t, "hang-start")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			opened := make(chan error, 1)
			go func() {
				_, err := service.openRun(ctx, protocol.RunOpenParams{RunID: "run", ConversationID: "conversation"})
				opened <- err
			}()
			require.Eventually(t, func() bool {
				_, err := os.Stat(filepath.Join(workspace, "conversation.json"))
				return err == nil
			}, 5*time.Second, 10*time.Millisecond)
			state := backgroundState(t, workspace, "conversation")
			require.NotEmpty(t, state.LeaseID)
			switch mode {
			case "request":
				cancel()
			case "run":
				require.NoError(t, service.cancelRun(t.Context(), "run"))
			case "shutdown":
				require.NoError(t, service.Close())
			}
			select {
			case err := <-opened:
				require.Error(t, err)
			case <-time.After(7 * time.Second):
				require.FailNow(t, "session.start cancellation did not finish")
			}
			assertBackgroundProcessStopped(t, state.PID)
			assert.Empty(t, service.runs)
			assert.Empty(t, service.backgroundLeases)
			assert.Empty(t, service.backgrounds)
		})
	}
}

func TestBackgroundExplicitCancellationReleasesRetainedWorkers(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "one", ConversationID: "conversation"})
	require.NoError(t, err)
	state := backgroundState(t, workspace, "conversation")
	require.NoError(t, service.closeRun(t.Context(), "one"))
	_, err = service.openRun(t.Context(), protocol.RunOpenParams{RunID: "two", ConversationID: "conversation"})
	require.NoError(t, err)
	require.NoError(t, service.cancelRun(t.Context(), "two"))
	assert.Empty(t, service.backgroundLeases)
	require.NoError(t, service.closeRun(t.Context(), "two"))
	assertBackgroundProcessStopped(t, state.PID)
	assert.Empty(t, service.backgrounds)
}

type backgroundLeasePeer struct {
	recordingPeer
	released chan delegation.Params
}

func (p *backgroundLeasePeer) Call(ctx context.Context, method string, params, result any) error {
	if method == delegation.ReleaseMethod {
		p.released <- params.(delegation.Params)
		if result != nil {
			*result.(*delegation.Result) = delegation.Result{Done: true}
		}
		return nil
	}
	return p.recordingPeer.Call(ctx, method, params, result)
}

func TestBackgroundLeaseRevocationReleasesChildAuthorityAcrossReattachment(t *testing.T) {
	for _, mode := range []string{"release", "cancel", "crash", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			service, workspace := newBackgroundTestService(t, "lease")
			_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "one", ConversationID: "conversation"})
			require.NoError(t, err)
			state := backgroundState(t, workspace, "conversation")
			owner := service.backgrounds["conversation"].leases[state.LeaseID].owner
			require.NoError(t, service.closeRun(t.Context(), "one"))
			_, err = service.openRun(t.Context(), protocol.RunOpenParams{RunID: "two", ConversationID: "conversation"})
			require.NoError(t, err)
			peer := &backgroundLeasePeer{released: make(chan delegation.Params, 4)}
			service.Attach(peer)
			switch mode {
			case "release":
				service.mu.Lock()
				lease := service.backgroundLeases[state.LeaseID].leases[state.LeaseID]
				lease.childAuthority = true
				service.backgroundLeases[state.LeaseID].leases[state.LeaseID] = lease
				service.mu.Unlock()
				_, err = service.ReleaseBackgroundTask(t.Context(), &recordingUIExtensionSource{owner: owner}, extensions.BackgroundTaskReleaseRequest{LeaseID: state.LeaseID})
				require.NoError(t, err)
			case "cancel":
				require.NoError(t, service.cancelRun(t.Context(), "two"))
			case "crash":
				require.NoError(t, syscall.Kill(state.PID, syscall.SIGKILL))
			case "shutdown":
				require.NoError(t, service.Close())
			}
			runs := make([]string, 0, 2)
			wantRuns := []string{"one", "two"}
			if mode == "release" {
				wantRuns = []string{""}
			} // synchronous owner-scoped revocation covers all reattachments
			for range len(wantRuns) {
				select {
				case release := <-peer.released:
					runs = append(runs, release.RunID)
					assert.Equal(t, state.LeaseID, release.LeaseID)
					assert.Equal(t, owner.ExtensionID, release.ExtensionID)
					assert.Equal(t, owner.Generation, release.Generation)
				case <-time.After(5 * time.Second):
					require.FailNow(t, "child authority was not revoked for each attached run")
				}
			}
			assert.ElementsMatch(t, wantRuns, runs)
			require.NoError(t, service.Close())
			assertBackgroundProcessStopped(t, state.PID)
		})
	}
}

func TestBackgroundConnectionLossRevokesRetainedWorkers(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "run", ConversationID: "conversation"})
	require.NoError(t, err)
	state := backgroundState(t, workspace, "conversation")
	require.NoError(t, service.closeRun(t.Context(), "run"))
	require.NoError(t, syscall.Kill(state.PID, 0))
	require.NoError(t, service.AbortActiveRun(t.Context()))
	assertBackgroundProcessStopped(t, state.PID)
	service.mu.Lock()
	assert.Empty(t, service.backgroundLeases)
	assert.Empty(t, service.backgrounds)
	assert.Empty(t, service.backgroundRunIDs)
	service.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(state.DataDir, "conversation.txt"))
	require.NoError(t, err)
	assert.Equal(t, "persisted", string(data), "lost process authority does not delete extension storage")
	// Reconnection starts a fresh generation, not stale worker/UI authority.
	service.Attach(&recordingPeer{})
	require.NoError(t, service.SetRegistration(protocol.RegisterResult{RunnerID: "runner", Generation: 2}))
	_, err = service.openRun(t.Context(), protocol.RunOpenParams{RunID: "next", ConversationID: "conversation"})
	require.NoError(t, err)
	restarted := backgroundState(t, workspace, "conversation")
	assert.NotEqual(t, state.PID, restarted.PID)
	assert.Equal(t, "persisted", restarted.Previous)
}

func TestBackgroundExtensionCrashReleasesLeases(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "one", ConversationID: "conversation"})
	require.NoError(t, err)
	state := backgroundState(t, workspace, "conversation")
	resources := service.backgrounds["conversation"]
	require.NoError(t, service.closeRun(t.Context(), "one"))
	require.NoError(t, syscall.Kill(state.PID, syscall.SIGKILL))
	select {
	case <-resources.cleanupDone:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "failed process generation did not release its background resources")
	}
	assertBackgroundProcessStopped(t, state.PID)
	starts, err := os.ReadFile(filepath.Join(workspace, "initializations.log"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(starts), "\n"), "session.end must not restart a crashed extension")
	service.mu.Lock()
	assert.Empty(t, service.backgroundLeases)
	assert.Empty(t, service.backgrounds)
	service.mu.Unlock()
}

func TestBackgroundRetainedSettingsRequireExplicitRelease(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "one", ConversationID: "conversation"})
	require.NoError(t, err)
	state := backgroundState(t, workspace, "conversation")
	resources := service.backgrounds["conversation"]
	require.NoError(t, service.closeRun(t.Context(), "one"))
	service.configLoader = func(string) (llmtypes.Config, error) {
		return llmtypes.Config{ExtensionSettings: map[string]any{"deny": []string{"lifetime"}}}, nil
	}
	_, err = service.openRun(t.Context(), protocol.RunOpenParams{RunID: "changed", ConversationID: "conversation"})
	require.ErrorContains(t, err, "release background leases first")
	assert.Same(t, resources, service.backgrounds["conversation"])
	assert.Contains(t, service.backgroundLeases, state.LeaseID, "rejected reattachment must not kill already-active workers")
	require.NoError(t, syscall.Kill(state.PID, 0))
	require.NoError(t, service.Close())
	assertBackgroundProcessStopped(t, state.PID)
}

func TestBackgroundReloadAndRemovalAfterRelease(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "plain")
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "one", ConversationID: "conversation"})
	require.NoError(t, err)
	state := backgroundState(t, workspace, "conversation")
	require.NoError(t, service.closeRun(t.Context(), "one"))
	assertBackgroundProcessStopped(t, state.PID)
	extension := filepath.Join(workspace, ".kodelet", "extensions", "kodelet-extension-lifetime")
	script, err := os.ReadFile(extension)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(extension, append(script, []byte("# replaced extension generation\n")...), 0o700))
	_, err = service.openRun(t.Context(), protocol.RunOpenParams{RunID: "two", ConversationID: "conversation"})
	require.NoError(t, err)
	reloaded := backgroundState(t, workspace, "conversation")
	assert.NotEqual(t, state.PID, reloaded.PID)
	assert.Equal(t, "persisted", reloaded.Previous)
	require.NoError(t, service.closeRun(t.Context(), "two"))
	assertBackgroundProcessStopped(t, reloaded.PID)
	require.NoError(t, os.Remove(extension))
	manifest, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "three", ConversationID: "conversation"})
	require.NoError(t, err)
	for _, tool := range manifest.Tools {
		assert.NotEqual(t, "lifetime", tool.Name)
	}
	assert.Empty(t, service.runs["three"].runtime.Tools())
	require.NoError(t, service.closeRun(t.Context(), "three"))
	data, err := os.ReadFile(filepath.Join(state.DataDir, "conversation.txt"))
	require.NoError(t, err)
	assert.Equal(t, "persisted", string(data), "removing an executable does not remove its persistent storage")
}

func TestBackgroundConcurrentConversationsUseIsolatedProcesses(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "plain")
	var wg sync.WaitGroup
	for _, conversation := range []string{"first", "second"} {
		wg.Go(func() {
			_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: conversation, ConversationID: conversation})
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	first, second := backgroundState(t, workspace, "first"), backgroundState(t, workspace, "second")
	assert.NotEqual(t, first.PID, second.PID)
	require.NoError(t, service.closeRun(t.Context(), "first"))
	assertBackgroundProcessStopped(t, first.PID)
	require.NoError(t, syscall.Kill(second.PID, 0), "closing one conversation must not kill another")
	require.NoError(t, service.closeRun(t.Context(), "second"))
	assertBackgroundProcessStopped(t, second.PID)
}

func TestBackgroundDiscoveryDoesNotRetainWorkers(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "probe")
	manifest, err := service.ProbeManifestForCWD(t.Context(), workspace, "")
	require.NoError(t, err)
	assert.NotEmpty(t, manifest.Tools)
	data, err := os.ReadFile(filepath.Join(workspace, "probe.json"))
	require.NoError(t, err)
	var state backgroundHelperState
	require.NoError(t, json.Unmarshal(data, &state))
	assert.Contains(t, state.Error, "not available")
	assert.Empty(t, state.LeaseID)
	assertBackgroundProcessStopped(t, state.PID)
	_, err = os.Stat(filepath.Join(workspace, "runner-manifest-probe.json"))
	assert.ErrorIs(t, err, os.ErrNotExist, "discovery must not dispatch session.start")
	assert.Empty(t, service.backgroundLeases)
}

func TestBackgroundShutdownBoundsSessionEnd(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "hang-end")
	_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: "run", ConversationID: "conversation"})
	require.NoError(t, err)
	state := backgroundState(t, workspace, "conversation")
	started := time.Now()
	require.NoError(t, service.Close())
	assert.Less(t, time.Since(started), 7*time.Second, "an extension opting out of timeouts cannot block daemon shutdown")
	assertBackgroundProcessStopped(t, state.PID)
}

type backgroundForkPeer struct {
	recordingPeer
	forks []runnerpayload.ConversationForkParams
}

func (p *backgroundForkPeer) Call(ctx context.Context, method string, params, result any) error {
	if method == protocol.MethodConversationFork {
		p.forks = append(p.forks, params.(runnerpayload.ConversationForkParams))
		*result.(*runnerpayload.ConversationForkResult) = runnerpayload.ConversationForkResult{ConversationID: "forked"}
		return nil
	}
	return p.recordingPeer.Call(ctx, method, params, result)
}

func TestBackgroundExtensionFollowUpsAndForkUseCurrentRunContext(t *testing.T) {
	service, workspace := newBackgroundTestService(t, "lease")
	peer := &backgroundForkPeer{}
	service.Attach(peer)
	for _, runID := range []string{"one", "two"} {
		_, err := service.openRun(t.Context(), protocol.RunOpenParams{RunID: runID, ConversationID: "conversation"})
		require.NoError(t, err)
		followup, err := service.dispatchLifecycle(t.Context(), runnerpayload.LifecycleDispatchParams{RunID: runID, Event: runnerpayload.LifecycleAgentEnd})
		require.NoError(t, err)
		assert.Equal(t, []string{"follow-up for conversation"}, followup.FollowUpMessages)
		result, err := service.executeTool(t.Context(), runnerpayload.ToolExecuteParams{RunID: runID, ToolCallID: "fork-call", Name: "lifetime", Input: json.RawMessage(`{"operation":"fork"}`)})
		require.NoError(t, err)
		assert.Empty(t, result.Result.Error)
		assert.Contains(t, result.Result.AssistantFacing, "forked")
		require.NoError(t, service.closeRun(t.Context(), runID))
	}
	assert.Equal(t, []runnerpayload.ConversationForkParams{
		{RunID: "one", ToolCallID: "fork-call", Name: "extension child"},
		{RunID: "two", ToolCallID: "fork-call", Name: "extension child"},
	}, peer.forks)
	state := backgroundState(t, workspace, "conversation")
	require.NoError(t, service.Close())
	assertBackgroundProcessStopped(t, state.PID)
}

// A real stdio extension exercises session.start reverse RPC, process lifetime,
// persistent data, and generation cleanup without a Node/Python dependency.
func TestBackgroundExtensionHelper(t *testing.T) {
	if os.Getenv("KODELET_BACKGROUND_HELPER") != "1" {
		return
	}
	runBackgroundExtensionHelper()
	os.Exit(0)
}

type backgroundHelperMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func readBackgroundHelperMessage(reader *bufio.Reader) (backgroundHelperMessage, error) {
	var message backgroundHelperMessage
	length := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return message, err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if value, found := strings.CutPrefix(line, "Content-Length:"); found {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return message, err
			}
		}
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return message, err
	}
	err := json.Unmarshal(data, &message)
	return message, err
}

func writeBackgroundHelperMessage(value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
}

func runBackgroundExtensionHelper() {
	reader := bufio.NewReader(os.Stdin)
	state := backgroundHelperState{PID: os.Getpid()}
	mode, cwd := os.Getenv("KODELET_BACKGROUND_HELPER_MODE"), ""
	nextID := 1000
	requestHost := func(parent json.RawMessage, method string, params any) backgroundHelperMessage {
		nextID++
		writeBackgroundHelperMessage(map[string]any{"jsonrpc": "2.0", "id": nextID, "parentId": parent, "method": method, "params": params})
		response, _ := readBackgroundHelperMessage(reader)
		return response
	}
	acquire := func(parent json.RawMessage) {
		response := requestHost(parent, extensions.BackgroundTaskAcquireMethod, extensions.BackgroundTaskAcquireRequest{Description: "initialization worker"})
		var lease extensions.BackgroundTaskAcquireResponse
		_ = json.Unmarshal(response.Result, &lease)
		state.LeaseID = lease.LeaseID
		if len(response.Error) > 0 && string(response.Error) != "null" {
			state.Error = string(response.Error)
		}
	}
	for {
		request, err := readBackgroundHelperMessage(reader)
		if err != nil {
			return
		}
		var result any = map[string]any{}
		switch request.Method {
		case "kodelet.ui.capabilities", "extension.ui.surface.closed", extensions.UISurfaceInputMethod, extensions.UISurfaceResizeMethod:
			data, _ := json.Marshal(map[string]any{"method": request.Method, "params": request.Params})
			if file, err := os.OpenFile(filepath.Join(cwd, "ui-notifications.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = fmt.Fprintln(file, string(data))
				_ = file.Close()
			}
			continue
		case "extension.initialize":
			var params struct {
				Extension struct {
					CWD     string `json:"cwd"`
					DataDir string `json:"dataDir"`
				} `json:"extension"`
			}
			_ = json.Unmarshal(request.Params, &params)
			cwd, state.DataDir = params.Extension.CWD, params.Extension.DataDir
			if file, err := os.OpenFile(filepath.Join(cwd, "initializations.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = fmt.Fprintln(file, state.PID)
				_ = file.Close()
			}
			if mode == "probe" {
				acquire(request.ID)
				data, _ := json.Marshal(state)
				_ = os.WriteFile(filepath.Join(cwd, "probe.json"), data, 0o600)
			}
			if mode == "initialize-lease" {
				acquire(request.ID)
			}
			noTimeout := float64(0)
			result = extensions.InitializeResult{
				Name: "lifetime", Version: "1",
				Tools:         []extensions.ToolRegistration{{Name: "lifetime", Description: "Inspect and release extension lifetime", InputSchema: map[string]any{"type": "object"}}},
				Subscriptions: []extensions.Subscription{{Event: extensions.EventSessionStart, TimeoutInSec: &noTimeout}, {Event: extensions.EventSessionEnd, TimeoutInSec: &noTimeout}, {Event: extensions.EventAgentEnd}},
			}
		case "extension.event.handle":
			var params struct {
				Event   string                          `json:"event"`
				Context extensions.ExtensionCallContext `json:"context"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Event == extensions.EventSessionEnd && mode == "hang-end" {
				for {
					time.Sleep(time.Hour)
				}
			}
			if params.Event == extensions.EventSessionStart {
				state.Conversation = params.Context.ConversationID
				if mode == "lease" || mode == "hang-start" {
					acquire(request.ID)
					response := requestHost(request.ID, "kodelet."+delegation.ReadMethod, map[string]any{"leaseId": state.LeaseID, "childId": "not-open-yet"})
					state.ChildError = string(response.Error)
				}
				path := filepath.Join(state.DataDir, state.Conversation+".txt")
				previous, _ := os.ReadFile(path)
				state.Previous = string(previous)
				_ = os.WriteFile(path, []byte("persisted"), 0o600)
				data, _ := json.Marshal(state)
				_ = os.WriteFile(filepath.Join(cwd, state.Conversation+".json"), data, 0o600)
				if mode == "hang-start" {
					// Leave the opening request pending but still handle session.end.
					continue
				}
			}
			if params.Event == extensions.EventAgentEnd {
				result = extensions.EventResult{FollowUpMessages: []string{"follow-up for " + state.Conversation}}
			}
		case "extension.tool.execute":
			var params struct {
				Input struct{ Operation string } `json:"input"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Input.Operation == "release" {
				requestHost(nil, extensions.BackgroundTaskReleaseMethod, extensions.BackgroundTaskReleaseRequest{LeaseID: state.LeaseID})
			}
			result = extensions.ToolExecutionResult{Content: "handled " + params.Input.Operation + " for " + state.Conversation}
			if params.Input.Operation == "surface" {
				response := requestHost(request.ID, "kodelet.ui.surface.open", extensions.UISurfaceOpenRequest{ScopeID: state.Conversation, ID: "canvas", Frame: extensions.UIFrame{Sequence: 1}})
				result = extensions.ToolExecutionResult{Content: string(response.Result), Error: string(response.Error)}
			}
			if params.Input.Operation == "fork" {
				response := requestHost(request.ID, "kodelet.conversation.fork", map[string]any{"name": "extension child"})
				result = extensions.ToolExecutionResult{Content: string(response.Result), Error: string(response.Error)}
			}
		}
		if len(request.ID) != 0 {
			writeBackgroundHelperMessage(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}
}
