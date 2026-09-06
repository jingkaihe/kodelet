package tui

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type remoteShortcutRunner struct {
	recordingRunner
	discovery protocol.WorkspaceDiscoverResult
	target    chat.WorkspaceTarget
	request   *chat.WorkspaceShortcutRequest
}

func (r *remoteShortcutRunner) DiscoverWorkspace(_ context.Context, target chat.WorkspaceTarget) (protocol.WorkspaceDiscoverResult, error) {
	r.target = target
	return r.discovery, nil
}

func (r *remoteShortcutRunner) ExecuteWorkspaceShortcut(ctx context.Context, request chat.WorkspaceShortcutRequest) (runnerpayload.ShortcutExecuteResult, error) {
	r.request = &request
	if err := ctx.Err(); err != nil {
		return runnerpayload.ShortcutExecuteResult{}, err
	}
	return runnerpayload.ShortcutExecuteResult{Matched: true, Result: &extensions.ShortcutResult{Action: "submit", Message: "/review"}}, nil
}

func TestRemoteShortcutNewAndActiveUseRunnerThenExistingSubmit(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "active"}[active], func(t *testing.T) {
			runner := &remoteShortcutRunner{discovery: protocol.WorkspaceDiscoverResult{CWD: "/only/on/runner", EnvironmentProfile: "review", Digest: "sha256:discovery", Shortcuts: []protocol.ShortcutDescriptor{{Key: "ctrl+r", ExtensionID: "review", Generation: 4}}}}
			m := newModel(t.Context(), Config{Runner: runner, Remote: true, CWD: "../only-on-runner", EnvironmentProfile: "review"})
			t.Cleanup(m.cancel)
			t.Cleanup(func() { assert.NoError(t, m.extensionRuntimes.Close()) })
			message := m.loadRemoteSlashCommands(m.conversationState)().(slashCommandsMsg)
			updated, _ := m.Update(message)
			m = updated.(model)
			require.Len(t, m.extensionShortcuts, 1)
			if active {
				m.conversationID = "stored-conversation"
				m.running = true
				runner.discovery.RunID = "active-lease"
				runner.discovery.Shortcuts[0].Generation = 8
			}
			m.textarea.SetValue("keep draft")
			command := m.startExtensionShortcut(m.extensionShortcuts[0])
			require.NotNil(t, command)
			result := command().(extensionShortcutDoneMsg)
			require.NoError(t, result.err)
			require.NotNil(t, runner.request)
			assert.Empty(t, runner.req.Message, "shortcut RPC must not itself submit a model turn")
			if active {
				assert.Equal(t, chat.WorkspaceTarget{ConversationID: "stored-conversation"}, runner.target)
				assert.Equal(t, "active-lease", runner.request.RunID)
				assert.Equal(t, uint64(8), runner.request.Shortcut.Generation)
			} else {
				assert.Equal(t, chat.WorkspaceTarget{CWD: "../only-on-runner", EnvironmentProfile: "review"}, runner.target)
				assert.Equal(t, "/only/on/runner", runner.request.Target.CWD, "invoke canonical runner directory, not client-relative path")
				assert.Empty(t, runner.request.RunID)
			}
			updated, next := m.Update(result)
			require.NotNil(t, next, "shortcut completion refreshes remote discovery without a local runtime")
			m = updated.(model)
			assert.Equal(t, "keep draft", m.textarea.Value())
			assert.Empty(t, m.shortcutCalls)
			if active {
				assert.Equal(t, []string{"/review"}, m.queuedFollowUps)
			} else {
				assert.True(t, m.running)
				require.NotEmpty(t, m.entries)
				assert.Equal(t, "/review", m.entries[0].content)
			}
		})
	}
}

func TestRemoteShortcutRejectsChangedScopeOrExtensionWithoutLocalFallback(t *testing.T) {
	runner := &remoteShortcutRunner{discovery: protocol.WorkspaceDiscoverResult{Digest: "sha256:changed", Shortcuts: []protocol.ShortcutDescriptor{{Key: "ctrl+r", ExtensionID: "replacement", Generation: 2}}}}
	shortcut := extensions.Shortcut{Key: "ctrl+r", ExtensionID: "original", Generation: 1}
	_, err := executeRemoteShortcut(t.Context(), runner, chat.WorkspaceTarget{RunnerID: "runner"}, shortcut, "sha256:original")
	assert.ErrorContains(t, err, "environment changed")
	assert.Nil(t, runner.request)
	runner.discovery.Digest = "sha256:original"
	_, err = executeRemoteShortcut(t.Context(), runner, chat.WorkspaceTarget{RunnerID: "runner"}, shortcut, "sha256:original")
	assert.ErrorContains(t, err, "registration changed")
	assert.Nil(t, runner.request)
	_, err = executeRemoteShortcut(t.Context(), &recordingRunner{}, chat.WorkspaceTarget{}, shortcut, "sha256:original")
	assert.ErrorContains(t, err, "does not support")
}
