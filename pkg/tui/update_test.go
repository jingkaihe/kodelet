package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRunner struct {
	req            chat.ChatRequest
	conversationID string
	err            error
}

func (r *recordingRunner) Run(ctx context.Context, req chat.ChatRequest, sink chat.ChatEventSink) (string, error) {
	r.req = req
	if err := sink.Send(chat.ChatEvent{Kind: "text", Delta: "streamed"}); err != nil {
		return "", err
	}
	return r.conversationID, r.err
}

func receiveRunMsg(t *testing.T, ch <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run message")
		return nil
	}
}

func TestCancelActiveRunFinishesActiveBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()

	cancelled := false
	m.running = true
	m.activeRunID = 1
	m.cancelRun = func() { cancelled = true }
	m.entries = []chatEntry{
		{kind: entryUser, content: "go on"},
		{
			kind: entryAssistant,
			blocks: []assistantBlock{
				{
					kind: blockThoughts,
					thoughts: []thoughtBlock{{
						text: "still thinking",
						done: false,
					}},
				},
				{
					kind: blockTools,
					tools: []toolCall{{
						name: "bash",
						done: false,
					}},
				},
			},
		},
	}

	m.cancelActiveRun()
	content, _ := m.renderTranscript()

	assert.True(t, cancelled)
	assert.False(t, m.running)
	assert.Equal(t, 0, m.activeRunID)
	assert.Equal(t, "cancelled", m.status)
	assert.False(t, hasActiveThought(m.entries[1].blocks[0]))
	assert.False(t, hasActiveTool(m.entries[1].blocks[1]))
	assert.Contains(t, content, "Had 1 Thought")
	assert.Contains(t, content, "Ran 1 command")
	assert.NotContains(t, content, "Thinking")
}

func TestWaitForMsgAndInitCommands(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: t.TempDir()})
	t.Cleanup(m.cancel)

	cmd := waitForMsg(m.runCh)
	m.runCh <- chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text", Delta: "hello"}}
	_, ok := cmd().(chatEventMsg)
	assert.True(t, ok)

	close(m.runCh)
	assert.Nil(t, waitForMsg(m.runCh)())

	initMsg := m.Init()()
	batch, ok := initMsg.(tea.BatchMsg)
	require.True(t, ok)
	assert.Len(t, batch, 5)
}

func TestUpdateIgnoresStaleRunEvents(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.entries = []chatEntry{{kind: entryUser, content: "first"}}
	m.activeRunID = 2
	m.running = true

	updated, _ := m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text", Delta: "stale"}})
	m = updated.(model)
	content, _ := m.renderTranscript()
	assert.NotContains(t, content, "stale")

	updated, _ = m.Update(chatEventMsg{runID: 2, event: chat.ChatEvent{Kind: "text", Delta: "fresh"}})
	m = updated.(model)
	content, _ = m.renderTranscript()
	assert.Contains(t, content, "fresh")
}

func TestDoneFinishesActiveBlocks(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.activeRunID = 1
	m.running = true
	m.entries = []chatEntry{
		{kind: entryUser, content: "go on"},
		{
			kind: entryAssistant,
			blocks: []assistantBlock{{
				kind:     blockThoughts,
				thoughts: []thoughtBlock{{text: "still thinking"}},
			}},
		},
	}

	updated, _ := m.Update(chatDoneMsg{runID: 1, conversationID: "conv-1"})
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.False(t, m.running)
	assert.Equal(t, 0, m.activeRunID)
	assert.Equal(t, "conv-1", m.conversationID)
	assert.Equal(t, "ready", m.status)
	assert.False(t, hasActiveThought(m.entries[1].blocks[0]))
	assert.Contains(t, content, "Had 1 Thought")
	assert.NotContains(t, content, "Thinking")
}

func TestTextareaNewlineKeysInsertNewline(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "named shift enter", msg: stringMsg("shift+enter")},
		{name: "alt enter", msg: tea.KeyMsg{Type: tea.KeyEnter, Alt: true}},
		{name: "ctrl j", msg: tea.KeyMsg{Type: tea.KeyCtrlJ}},
		{name: "kitty csi u shift enter", msg: stringMsg("?CSI[49 51 59 50 117]?")},
		{name: "xterm modify other keys shift enter", msg: stringMsg("?CSI[50 55 59 50 59 49 51 126]?")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(context.Background(), Config{})
			t.Cleanup(m.cancel)
			m.textarea.SetValue("first line")

			updated, cmd := m.Update(tt.msg)
			m = updated.(model)

			assert.Nil(t, cmd)
			assert.Equal(t, "first line\n", m.textarea.Value())
			assert.Empty(t, m.entries)
		})
	}
}

func TestRunningShiftEnterInsertsSteeringNewline(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123456789"})
	t.Cleanup(m.cancel)
	m.running = true
	m.activeRunID = 1
	m.textarea.SetValue("first line")

	updated, cmd := m.Update(stringMsg("?CSI[49 51 59 50 117]?"))
	m = updated.(model)

	assert.Nil(t, cmd)
	assert.True(t, m.running)
	assert.Equal(t, "first line\n", m.textarea.Value())
	assert.Empty(t, m.queuedSteering)
}

func TestCtrlOTogglesDetails(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.entries = []chatEntry{{
		kind: entryAssistant,
		blocks: []assistantBlock{{
			kind:     blockThoughts,
			thoughts: []thoughtBlock{{text: "toggle me", done: true}},
		}},
	}}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = updated.(model)
	content, _ := m.renderTranscript()

	assert.Nil(t, cmd)
	assert.True(t, m.entries[0].blocks[0].expanded)
	assert.Contains(t, content, "toggle me")
}

func TestQuestionMarkOpensShortcutsDialogWhenComposerEmpty(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.True(t, m.shortcutsOpen)
	assert.Contains(t, xansi.Strip(m.View()), "Shortcuts")

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.shortcutsOpen)

	m.textarea.SetValue("what")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = updated.(model)
	assert.False(t, m.shortcutsOpen)
	assert.Equal(t, "what?", m.textarea.Value())
}

func TestApplyEditorResultUpdatesComposerAndCleansUpFile(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	path := filepath.Join(t.TempDir(), "draft.md")
	require.NoError(t, os.WriteFile(path, []byte("edited draft\n"), 0o644))

	cmd := m.applyEditorResult(editorFinishedMsg{path: path})

	assert.NotNil(t, cmd)
	assert.Equal(t, "edited draft", m.textarea.Value())
	assert.Equal(t, "ready", m.status)
	assert.Empty(t, m.steerError)
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestOpenComposerInEditorRequiresEditorAndIgnoresRunning(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")

	cmd := m.openComposerInEditor()

	assert.NotNil(t, cmd)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
	assert.Equal(t, "Editor unavailable", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "Set $EDITOR or $VISUAL")

	m.steerError = ""
	m.uiNotifications = nil
	m.running = true
	cmd = m.openComposerInEditor()

	assert.NotNil(t, cmd)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
	assert.Equal(t, "Editor unavailable", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "while Kodelet is running")
}

func TestOpenComposerInEditorCreatesDraftAndClearsTransientUI(t *testing.T) {
	m := newModel(context.Background(), Config{ProfileOptions: []string{"default", "work"}})
	t.Cleanup(m.cancel)
	beforeDrafts, err := filepath.Glob(filepath.Join(os.TempDir(), "kodelet-composer-*.md"))
	require.NoError(t, err)
	beforeDraftSet := map[string]struct{}{}
	for _, path := range beforeDrafts {
		beforeDraftSet[path] = struct{}{}
	}
	t.Cleanup(func() {
		afterDrafts, err := filepath.Glob(filepath.Join(os.TempDir(), "kodelet-composer-*.md"))
		if err != nil {
			return
		}
		for _, path := range afterDrafts {
			if _, ok := beforeDraftSet[path]; !ok {
				_ = os.Remove(path)
			}
		}
	})
	m.width = 80
	m.height = 24
	m.resize()
	m.textarea.SetValue("draft body")
	m.profilePickerOpen = true
	m.shortcutsOpen = true
	m.slashDismissedDraft = "dismissed"
	t.Setenv("EDITOR", "true")
	t.Setenv("VISUAL", "")

	cmd := m.openComposerInEditor()

	require.NotNil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.False(t, m.shortcutsOpen)
	assert.Empty(t, m.steerError)
	assert.Equal(t, "editing", m.status)
	msg := cmd()
	assert.NotNil(t, msg)
}

func TestWriteComposerEditorFileRoundTripsDraft(t *testing.T) {
	path, err := writeComposerEditorFile("draft body")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(path) })

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "draft body", string(content))
}

func TestEditorShortcutUsesCtrlGAndCtrlEPreservesLineEnd(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	m.textarea.SetValue("hello")
	m.textarea.SetCursor(0)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	m = updated.(model)

	assert.Equal(t, "hello!", m.textarea.Value())
	assert.Empty(t, m.steerError)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = updated.(model)

	assert.NotNil(t, cmd)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
	assert.Contains(t, m.uiNotifications[0].message, "Ctrl+G")
}

func TestEditorExecCommandParsesEditorArgs(t *testing.T) {
	cmd, err := editorExecCommand("vim -n", "/tmp/kodelet-draft.md")

	require.NoError(t, err)
	assert.Equal(t, "vim", filepath.Base(cmd.Path))
	assert.Equal(t, []string{"vim", "-n", "/tmp/kodelet-draft.md"}, cmd.Args)

	_, err = editorExecCommand("  ", "/tmp/kodelet-draft.md")
	assert.ErrorContains(t, err, "empty editor command")
	_, err = editorExecCommand("'unterminated", "/tmp/kodelet-draft.md")
	assert.Error(t, err)
}

func TestApplyEditorResultHandlesFailureAndReadError(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	failedPath := filepath.Join(t.TempDir(), "failed.md")
	require.NoError(t, os.WriteFile(failedPath, []byte("ignored"), 0o644))
	cmd := m.applyEditorResult(editorFinishedMsg{path: failedPath, err: errors.New("boom")})
	assert.NotNil(t, cmd)
	assert.Equal(t, "ready", m.status)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationError, m.uiNotifications[0].level)
	assert.Equal(t, "Editor failed", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "boom")
	_, err := os.Stat(failedPath)
	assert.True(t, os.IsNotExist(err))
	m.uiNotifications = nil

	notFoundPath := filepath.Join(t.TempDir(), "not-found.md")
	require.NoError(t, os.WriteFile(notFoundPath, []byte("ignored"), 0o644))
	cmd = m.applyEditorResult(editorFinishedMsg{path: notFoundPath, err: &exec.Error{Name: "missing-editor", Err: exec.ErrNotFound}})
	assert.NotNil(t, cmd)
	assert.Equal(t, "ready", m.status)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationWarning, m.uiNotifications[0].level)
	assert.Equal(t, "Editor unavailable", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "not found")
	_, err = os.Stat(notFoundPath)
	assert.True(t, os.IsNotExist(err))
	m.uiNotifications = nil

	missingPath := filepath.Join(t.TempDir(), "missing.md")
	cmd = m.applyEditorResult(editorFinishedMsg{path: missingPath})
	assert.NotNil(t, cmd)
	assert.Equal(t, "ready", m.status)
	assert.Empty(t, m.steerError)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, uiNotificationError, m.uiNotifications[0].level)
	assert.Equal(t, "Editor failed", m.uiNotifications[0].title)
	assert.Contains(t, m.uiNotifications[0].message, "Failed to read edited draft")
}

func TestCtrlTProfilePickerSelectsProfileForNewConversation(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{Profile: "default", ProfileOptions: []string{"default", "work", "prod"}, Runner: runner})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.True(t, m.profilePickerOpen)
	assert.Equal(t, 0, m.profilePickerIndex)

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, 1, m.profilePickerIndex)

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "work", m.profile)

	m.textarea.SetValue("hello")
	runCmd := m.submit()
	require.NotNil(t, runCmd)
	assert.Nil(t, runCmd())
	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)
	assert.Equal(t, "work", runner.req.Profile)
}

func TestSlashCommandKeyboardCompletion(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.slashCommands = []slashcommands.Command{
		{Name: "goal", Description: "Set the active goal"},
		{Name: "review", Description: "Review changes", Hint: "target"},
	}
	m.textarea.SetValue("/")
	m.resize()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, 0, m.slashCommandIndex)

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, 1, m.slashCommandIndex)

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.Equal(t, "/review ", m.textarea.Value())
	assert.Equal(t, -1, m.slashCommandIndex)
	assert.False(t, m.slashCommandSuggestionsOpen())
}

func TestSlashCommandTabSelectsFirstMatchAndPreservesIndent(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.slashCommands = []slashcommands.Command{
		{Name: "goal", Description: "Set the active goal"},
		{Name: "review", Description: "Review changes"},
	}
	m.textarea.SetValue("  /rev")
	m.resize()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(model)

	require.Nil(t, cmd)
	assert.Equal(t, "  /review ", m.textarea.Value())
}

func TestSlashCommandEscapeDismissesUntilDraftChanges(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.slashCommands = []slashcommands.Command{{Name: "goal", Description: "Set the active goal"}}
	m.textarea.SetValue("/")
	m.resize()
	require.True(t, m.slashCommandSuggestionsOpen())

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.slashCommandSuggestionsOpen())

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = updated.(model)
	require.NotNil(t, cmd)
	assert.Equal(t, "/g", m.textarea.Value())
	assert.True(t, m.slashCommandSuggestionsOpen())
}

func TestSlashCommandLoaderUsesRequestedCWDRecipes(t *testing.T) {
	workspace := t.TempDir()
	writeTUIRecipe(t, workspace, "workspace-only", `---
description: Workspace recipe
---
Body
`)
	withTUIViper(t, map[string]any{
		"extensions.enabled":         false,
		"extensions.local_dir":       filepath.Join(workspace, ".kodelet", "extensions"),
		"extensions.global_dir":      filepath.Join(t.TempDir(), "global-extensions"),
		"extensions.max_output_size": 102400,
	})

	commands, err := listSlashCommands(context.Background(), workspace)

	require.NoError(t, err)
	assert.Contains(t, slashCommandNames(commands), "goal")
	assert.Contains(t, slashCommandNames(commands), "workspace-only")
}

func TestSlashCommandLoadCommandsAndCWDHelpers(t *testing.T) {
	workspace := t.TempDir()
	withTUIViper(t, map[string]any{
		"extensions.enabled":         false,
		"extensions.local_dir":       filepath.Join(workspace, ".kodelet", "extensions"),
		"extensions.global_dir":      filepath.Join(t.TempDir(), "global-extensions"),
		"extensions.max_output_size": 102400,
	})

	baseMsg, ok := loadSlashCommands(context.Background(), "  "+workspace+"  ")().(slashCommandsMsg)
	require.True(t, ok)
	assert.Equal(t, workspace, baseMsg.cwd)
	assert.NoError(t, baseMsg.err)
	assert.Contains(t, slashCommandNames(baseMsg.commands), "goal")
	assert.False(t, baseMsg.extensionsOnly)

	extensionMsg, ok := loadExtensionSlashCommands(context.Background(), workspace)().(slashCommandsMsg)
	require.True(t, ok)
	assert.Equal(t, workspace, extensionMsg.cwd)
	assert.True(t, extensionMsg.extensionsOnly)
	assert.NoError(t, extensionMsg.err)
	assert.Empty(t, extensionMsg.commands)

	m := newModel(context.Background(), Config{CWD: workspace})
	t.Cleanup(m.cancel)
	assert.Equal(t, workspace, m.slashCommandCWD())
	m.requestedCWD = "./requested"
	assert.Equal(t, "./requested", m.slashCommandCWD())
}

func TestSlashCommandLoaderErrorsForInvalidCWD(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")

	baseCommands, err := listBaseSlashCommands(context.Background(), missing)
	assert.ErrorContains(t, err, "cwd directory does not exist")
	assert.Contains(t, slashCommandNames(baseCommands), "goal")

	extensionCommands, err := listExtensionSlashCommands(context.Background(), missing)
	assert.ErrorContains(t, err, "cwd directory does not exist")
	assert.Nil(t, extensionCommands)

	combined, err := listSlashCommands(context.Background(), missing)
	assert.ErrorContains(t, err, "cwd directory does not exist")
	assert.Contains(t, slashCommandNames(combined), "goal")

	_, err = resolveSlashCommandCWD(missing)
	assert.ErrorContains(t, err, "cwd directory does not exist")
}

func slashCommandNames(commands []slashcommands.Command) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	return names
}

func writeTUIRecipe(t *testing.T, workspace, name, content string) {
	t.Helper()
	recipeDir := filepath.Join(workspace, ".kodelet", "recipes")
	require.NoError(t, os.MkdirAll(recipeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(recipeDir, name+".md"), []byte(content), 0o644))
}

func withTUIViper(t *testing.T, values map[string]any) {
	t.Helper()
	snapshot := viper.AllSettings()
	viper.Reset()
	for key, value := range values {
		viper.Set(key, value)
	}
	t.Cleanup(func() {
		viper.Reset()
		for key, value := range snapshot {
			viper.Set(key, value)
		}
	})
}

func TestClickProfilePickerSelectsProfileForNewConversation(t *testing.T) {
	m := newModel(context.Background(), Config{Profile: "default", ProfileOptions: []string{"default", "work", "prod"}})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	profileStart, _, ok := m.profileLabelBoundsInBlock()
	require.True(t, ok)
	updated, cmd := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      tuiLeftMargin + profileStart,
		Y:      m.viewport.Height,
	})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.True(t, m.profilePickerOpen)

	pickerStart, _, ok := m.profilePickerBoundsInBlock()
	require.True(t, ok)
	updated, cmd = m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      tuiLeftMargin + pickerStart,
		Y:      m.viewport.Height + 2,
	})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "prod", m.profile)
}

func TestProfilePickerLockedForExistingConversation(t *testing.T) {
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Profile: "work", ProfileOptions: []string{"default", "work", "prod"}})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "work", m.profile)

	profileStart, _, ok := m.profileLabelBoundsInBlock()
	require.True(t, ok)
	updated, cmd = m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      tuiLeftMargin + profileStart,
		Y:      m.viewport.Height,
	})
	m = updated.(model)
	require.Nil(t, cmd)
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "work", m.profile)
}

func TestProfilePickerToggleCloseAndWrap(t *testing.T) {
	m := newModel(context.Background(), Config{Profile: "work", ProfileOptions: []string{"default", "work", "prod"}})
	t.Cleanup(m.cancel)

	m.toggleProfilePickerFromKeyboard()
	require.True(t, m.profilePickerOpen)
	m.moveProfilePicker(-1)
	assert.Equal(t, 0, m.profilePickerIndex)
	m.moveProfilePicker(-1)
	assert.Equal(t, 2, m.profilePickerIndex)
	m.toggleProfilePickerFromKeyboard()
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, "prod", m.profile)

	m.toggleProfilePickerFromClick()
	require.True(t, m.profilePickerOpen)
	m.closeProfilePicker()
	assert.False(t, m.profilePickerOpen)
	assert.Equal(t, m.profileIndex, m.profilePickerIndex)
}

func TestTypingInComposerDoesNotMoveViewport(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	m.entries = []chatEntry{{
		kind:   entryAssistant,
		blocks: []assistantBlock{{kind: blockText, text: numberedLines(30)}},
	}}
	m.refreshViewport(true)
	bottomOffset := m.viewport.YOffset
	require.Greater(t, bottomOffset, 0)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(model)
	scrolledOffset := m.viewport.YOffset
	require.Less(t, scrolledOffset, bottomOffset)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = updated.(model)
	assert.Equal(t, scrolledOffset, m.viewport.YOffset)
	assert.Equal(t, "x", m.textarea.Value())

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	assert.Equal(t, scrolledOffset, m.viewport.YOffset)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = updated.(model)
	assert.Equal(t, scrolledOffset, m.viewport.YOffset)
	assert.Empty(t, m.textarea.Value())

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(model)
	assert.Greater(t, m.viewport.YOffset, scrolledOffset)
}

func TestHorizontalViewportMouseNavigation(t *testing.T) {
	assert.True(t, isHorizontalViewportMouseNavigation(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelLeft}))
	assert.True(t, isHorizontalViewportMouseNavigation(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelRight}))
	assert.True(t, isHorizontalViewportMouseNavigation(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, Shift: true}))
	assert.True(t, shouldUpdateViewport(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, Shift: true}))
	assert.False(t, isHorizontalViewportMouseNavigation(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonWheelLeft}))
	assert.False(t, isHorizontalViewportMouseNavigation(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}))
}

func TestSubmitStartsRunAndStreamsRunnerMessages(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Profile: "work", CWD: "/tmp", Runner: runner})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.textarea.SetValue(" hello ")

	cmd := m.submit()
	require.NotNil(t, cmd)

	assert.True(t, m.running)
	assert.Equal(t, 1, m.activeRunID)
	assert.Equal(t, "working", m.status)
	assert.Empty(t, m.textarea.Value())
	require.Len(t, m.entries, 1)
	assert.Equal(t, chatEntry{kind: entryUser, content: "hello"}, m.entries[0])

	assert.Nil(t, cmd())

	event, ok := receiveRunMsg(t, m.runCh).(chatEventMsg)
	require.True(t, ok)
	assert.Equal(t, 1, event.runID)
	assert.Equal(t, "text", event.event.Kind)
	assert.Equal(t, "streamed", event.event.Delta)

	done, ok := receiveRunMsg(t, m.runCh).(chatDoneMsg)
	require.True(t, ok)
	assert.Equal(t, 1, done.runID)
	assert.Equal(t, "conversation-done", done.conversationID)
	assert.NoError(t, done.err)

	assert.Equal(t, chat.ChatRequest{
		Message:        "hello",
		ConversationID: "conversation-123",
		Profile:        "work",
		CWD:            "/tmp",
	}, runner.req)
}

func TestSubmitGoalSlashCommandDisplaysObjectiveImmediately(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Runner: runner})
	t.Cleanup(m.cancel)
	m.width = 100
	m.height = 30
	m.resize()
	m.textarea.SetValue("/goal run ls -la")

	cmd := m.submit()
	require.NotNil(t, cmd)
	require.Len(t, m.entries, 1)
	assert.Equal(t, chatEntry{kind: entryUser, content: "Objective: run ls -la"}, m.entries[0])

	assert.Nil(t, cmd())
	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)
	assert.Equal(t, "/goal run ls -la", runner.req.Message)
}

func TestSubmitWithDefaultRunnerKeepsRelativeCWDAsRequestOnly(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	capturedDefaultCWD := "unset"
	previous := newDefaultChatRunner
	newDefaultChatRunner = func(defaultCWD string) chat.ChatRunner {
		capturedDefaultCWD = defaultCWD
		return runner
	}
	t.Cleanup(func() {
		newDefaultChatRunner = previous
	})

	m := newModel(context.Background(), Config{ConversationID: "conversation-123", CWD: "./backend"})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("hello")

	cmd := m.submit()
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())

	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)

	assert.Empty(t, capturedDefaultCWD)
	assert.Equal(t, "./backend", runner.req.CWD)
}

func TestSubmitResumedChatWithoutExplicitCWDDoesNotSendCurrentDirectory(t *testing.T) {
	runner := &recordingRunner{conversationID: "conversation-done"}
	m := newModel(context.Background(), Config{ConversationID: "conversation-123", Runner: runner})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("hello")

	cmd := m.submit()
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())

	_ = receiveRunMsg(t, m.runCh)
	_ = receiveRunMsg(t, m.runCh)

	assert.Equal(t, "conversation-123", runner.req.ConversationID)
	assert.Empty(t, runner.req.CWD)
	assert.NotEmpty(t, m.cwd)
}

func TestSubmitIgnoresEmptyComposer(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.textarea.SetValue("   ")

	cmd := m.submit()

	assert.Nil(t, cmd)
	assert.False(t, m.running)
	assert.Empty(t, m.entries)
	assert.Empty(t, m.conversationID)
}

func TestSlashCommandIndexMovementAndMergeHelpers(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.slashCommands = []slashcommands.Command{{Name: "goal", Description: "Set goal"}, {Name: "review", Description: "Review"}}
	m.textarea.SetValue("/")

	m.resetSlashCommandIndex()
	assert.Equal(t, -1, m.slashCommandIndex)
	m.slashCommandIndex = 4
	m.resetSlashCommandIndex()
	assert.Equal(t, -1, m.slashCommandIndex)
	m.moveSlashCommandSelection(-1)
	assert.Equal(t, 1, m.slashCommandIndex)
	m.moveSlashCommandSelection(-1)
	assert.Equal(t, 0, m.slashCommandIndex)
	m.moveSlashCommandSelection(-1)
	assert.Equal(t, -1, m.slashCommandIndex)

	m.textarea.SetValue("no slash")
	m.moveSlashCommandSelection(1)
	assert.Equal(t, -1, m.slashCommandIndex)

	merged := mergeSlashCommands(
		[]slashcommands.Command{{Name: "goal"}, {Name: " "}, {Name: "review"}},
		[]slashcommands.Command{{Name: "review"}, {Name: "custom"}, {Name: ""}},
	)
	assert.Equal(t, []string{"goal", "review", "custom"}, slashCommandNames(merged))
}

func TestUnknownCSIAndModifierBranches(t *testing.T) {
	assert.False(t, isShiftEnterCSISequence("not-csi"))
	assert.False(t, isShiftEnterCSISequence("?CSI[49 51 117]?"))
	assert.False(t, isShiftEnterCSISequence("?CSI[49 52 59 50 117]?"))
	assert.False(t, isShiftEnterCSISequence("?CSI[49 51 59 49 117]?"))
	assert.False(t, isShiftEnterCSISequence("?CSI[50 55 59 50 59 49 52 126]?"))
	assert.False(t, hasShiftModifier("not-number"))
	assert.False(t, hasShiftModifier("1"))
	assert.True(t, hasShiftModifier("2:3"))
}

func TestUserDisplayMessageFallsBackForInvalidGoalCommand(t *testing.T) {
	invalidGoal := "  /goal   "
	assert.Equal(t, strings.TrimSpace(invalidGoal), userDisplayMessage(invalidGoal))
}

func TestStreamingDeltasAreDebouncedBeforeViewportRefresh(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 24
	m.resize()
	m.activeRunID = 1
	m.running = true
	m.refreshViewport(true)
	initialContent := m.viewport.View()

	updated, cmd := m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text-delta", Delta: "**hello**"}})
	m = updated.(model)

	require.NotNil(t, cmd)
	require.True(t, m.pendingRefresh)
	require.Len(t, m.entries, 1)
	assert.Equal(t, "**hello**", m.entries[0].blocks[0].text)
	assert.Equal(t, initialContent, m.viewport.View())

	updated, _ = m.Update(transcriptRefreshMsg{})
	m = updated.(model)

	assert.False(t, m.pendingRefresh)
	assert.Contains(t, xansi.Strip(m.viewport.View()), "hello")
}

func TestStreamingPreservesViewportAfterUserScrollsUp(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	m.entries = []chatEntry{{
		kind:   entryAssistant,
		blocks: []assistantBlock{{kind: blockText, text: numberedLines(30)}},
	}}
	m.refreshViewport(true)
	bottomOffset := m.viewport.YOffset
	require.Greater(t, bottomOffset, 0)
	require.True(t, m.autoFollow)

	updated, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	m = updated.(model)
	scrolledOffset := m.viewport.YOffset
	require.Less(t, scrolledOffset, bottomOffset)
	assert.False(t, m.autoFollow)

	m.running = true
	m.activeRunID = 1
	updated, _ = m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text-delta", Delta: "\nstill streaming"}})
	m = updated.(model)

	assert.Equal(t, scrolledOffset, m.viewport.YOffset)
	assert.False(t, m.autoFollow)
}

func TestScrollingBackToBottomResumesStreamingAutoFollow(t *testing.T) {
	m := newModel(context.Background(), Config{})
	t.Cleanup(m.cancel)
	m.width = 80
	m.height = 14
	m.resize()
	m.entries = []chatEntry{{
		kind:   entryAssistant,
		blocks: []assistantBlock{{kind: blockText, text: numberedLines(30)}},
	}}
	m.refreshViewport(true)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(model)
	require.False(t, m.autoFollow)
	require.False(t, m.viewport.AtBottom())

	for range 10 {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		m = updated.(model)
		if m.viewport.AtBottom() {
			break
		}
	}
	require.True(t, m.viewport.AtBottom())
	require.True(t, m.autoFollow)

	m.running = true
	m.activeRunID = 1
	updated, _ = m.Update(chatEventMsg{runID: 1, event: chat.ChatEvent{Kind: "text-delta", Delta: "\nnew bottom line"}})
	m = updated.(model)

	assert.True(t, m.viewport.AtBottom())
	assert.True(t, m.autoFollow)
}
