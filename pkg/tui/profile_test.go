package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteProfilePickerRefreshesDiscoveryAndClearsStaleResources(t *testing.T) {
	for _, confirm := range []string{"enter", "ctrl+t", "click"} {
		t.Run(confirm, func(t *testing.T) {
			runner := &remoteShortcutRunner{discovery: protocol.WorkspaceDiscoverResult{
				Commands:  []slashcommands.Command{{Name: "old-profile-command"}},
				Shortcuts: []protocol.ShortcutDescriptor{{Key: "ctrl+r", ExtensionID: "old", Generation: 1}}, Digest: "old-digest",
			}}
			m := newModel(t.Context(), Config{Remote: true, Runner: runner, CWD: "/runner/project", Profile: "default", ProfileOptions: []string{"default", "work"}})
			t.Cleanup(m.cancel)
			m.width, m.height = 80, 24
			m.resize()
			updated, _ := m.Update(m.loadRemoteSlashCommands(m.conversationState)())
			m = updated.(model)
			m.textarea.SetValue("/old-profile-command")
			m.slashCommandIndex = 0
			m.slashCommandErr = errors.New("old discovery failure")
			assert.True(t, m.slashCommandSuggestionsOpen())
			require.NotEmpty(t, m.extensionShortcuts)
			staleDiscovery := m.loadRemoteSlashCommands(m.conversationState)

			updated, cmd := m.Update(keyPressWithMod('t', tea.ModCtrl))
			m = updated.(model)
			require.Nil(t, cmd)
			require.True(t, m.profilePickerOpen)
			m.profilePickerIndex = 1
			var input tea.Msg
			switch confirm {
			case "enter":
				input = keyPress(tea.KeyEnter)
			case "ctrl+t":
				input = keyPressWithMod('t', tea.ModCtrl)
			case "click":
				start, _, ok := m.profilePickerBoundsInBlock()
				require.True(t, ok)
				input = tea.MouseClickMsg{Button: tea.MouseLeft, X: tuiLeftMargin + start, Y: m.viewport.Height() + 1}
			}
			updated, cmd = m.Update(input)
			m = updated.(model)
			require.NotNil(t, cmd, "every profile confirmation path must schedule discovery")
			assert.Equal(t, "work", m.profile)
			assert.False(t, m.profilePickerOpen)
			assert.Equal(t, withTUIBuiltInSlashCommands(nil), m.slashCommands)
			assert.Empty(t, m.extensionShortcuts)
			assert.Empty(t, m.shortcutDigest)
			assert.NoError(t, m.slashCommandErr)
			assert.Equal(t, -1, m.slashCommandIndex)
			assert.False(t, m.slashCommandSuggestionsOpen())
			assert.Equal(t, "/old-profile-command", m.textarea.Value(), "profile selection must preserve the draft")

			staleMessage := staleDiscovery().(slashCommandsMsg)
			assert.Equal(t, "default", staleMessage.profile, "discovery must capture its target before the profile changes")
			updated, next := m.Update(staleMessage)
			m = updated.(model)
			assert.Nil(t, next)
			assert.Equal(t, withTUIBuiltInSlashCommands(nil), m.slashCommands)
			assert.Empty(t, m.extensionShortcuts)
			assert.Empty(t, m.shortcutDigest)
			staleMessage.err = errors.New("late failure from old profile")
			updated, _ = m.Update(staleMessage)
			m = updated.(model)
			assert.NoError(t, m.slashCommandErr)

			runner.discovery.Commands = []slashcommands.Command{{Name: "work-command"}}
			runner.discovery.Shortcuts = []protocol.ShortcutDescriptor{{Key: "ctrl+r", ExtensionID: "work", Generation: 2}}
			runner.discovery.Digest = "work-digest"
			updated, next = m.Update(cmd())
			m = updated.(model)
			assert.Nil(t, next, "remote discovery must not start local extension discovery")
			assert.Equal(t, "work", runner.target.Profile)
			assert.Contains(t, slashCommandNames(m.slashCommands), "work-command")
			require.Len(t, m.extensionShortcuts, 1)
			assert.Equal(t, "work", m.extensionShortcuts[0].ExtensionID)
			assert.Equal(t, "work-digest", m.shortcutDigest)
			updated, _ = m.Update(staleMessage)
			m = updated.(model)
			assert.Contains(t, slashCommandNames(m.slashCommands), "work-command")
			assert.Equal(t, "work-digest", m.shortcutDigest)
			assert.NoError(t, m.slashCommandErr, "late failures must not overwrite fresh discovery")

			m.openProfilePicker()
			cmd = m.selectProfilePickerOption(0)
			require.NotNil(t, cmd)
			assert.Empty(t, m.extensionShortcuts)
			assert.Empty(t, m.shortcutDigest)
			message := cmd().(slashCommandsMsg)
			assert.Equal(t, "default", message.profile)
			assert.Equal(t, "default", runner.target.Profile, "explicit default must not be dropped")
			assert.Empty(t, runner.req.Message, "profile switching must not submit a model turn")
		})
	}
}

func TestProfilePickerPreservesResourcesWithoutRemoteProfileChange(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local change", true: "remote unchanged"}[remote], func(t *testing.T) {
			m := newModel(t.Context(), Config{Remote: remote, Runner: &remoteDiscoveryRunner{}, Profile: "default", ProfileOptions: []string{"default", "work"}})
			t.Cleanup(m.cancel)
			m.slashCommands = []slashcommands.Command{{Name: "keep-command"}}
			m.extensionShortcuts = []extensions.Shortcut{{Key: "ctrl+r", ExtensionID: "keep"}}
			m.shortcutDigest = "keep-digest"
			m.openProfilePicker()
			index := 1
			if remote {
				index = 0
			}
			assert.Nil(t, m.selectProfilePickerOption(index))
			assert.Equal(t, []string{"keep-command"}, slashCommandNames(m.slashCommands))
			assert.Len(t, m.extensionShortcuts, 1)
			assert.Equal(t, "keep-digest", m.shortcutDigest)
		})
	}
}

func TestLoadProfileOptionsDefaultFirstThenSortedConfiguredProfiles(t *testing.T) {
	oldCWD, err := os.Getwd()
	require.NoError(t, err)
	repoDir := t.TempDir()
	require.NoError(t, os.Chdir(repoDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldCWD))
	})

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv(llm.ConfigFileEnv, "")
	t.Setenv(llm.ConfigFileModeEnv, "")
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".kodelet", "config.yaml"), []byte(`profiles:
  zeta:
    model: zeta-model
  shared:
    model: global-shared
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "kodelet-config.yaml"), []byte(`profiles:
  alpha:
    model: alpha-model
  shared:
    model: repo-shared
  default:
    model: ignored
`), 0o644))

	assert.Equal(t, []string{"default", "alpha", "shared", "zeta"}, loadProfileOptions())
}

func TestLoadProfileOptionsIncludesOverrideLastAndHonoursIsolatedMode(t *testing.T) {
	repoDir := t.TempDir()
	t.Chdir(repoDir)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".kodelet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".kodelet", "config.yaml"), []byte("profiles:\n  global-only:\n    provider: anthropic\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, llm.RepoConfigFile), []byte("profiles:\n  repo-only:\n    provider: openai\n"), 0o644))
	overridePath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(overridePath, []byte("profiles:\n  override-only:\n    provider: openai\n"), 0o644))
	t.Setenv(llm.ConfigFileEnv, overridePath)

	t.Run("merge", func(t *testing.T) {
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeMerge)
		assert.Equal(t, []string{"default", "global-only", "override-only", "repo-only"}, loadProfileOptions())
	})

	t.Run("isolated", func(t *testing.T) {
		t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeIsolated)
		assert.Equal(t, []string{"default", "override-only"}, loadProfileOptions())
	})
}

func TestLoadProfileOptionsHidesProfilesWithoutLosingExplicitOrSavedSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`profiles:
  hidden-search:
    hidden: true
    model: search-model
  visible-search:
    hidden: false
    model: search-model
  work:
    model: work-model
  default:
    hidden: true
`), 0o644))
	t.Setenv(llm.ConfigFileEnv, configPath)
	t.Setenv(llm.ConfigFileModeEnv, llm.ConfigFileModeIsolated)
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.SetConfigFile(configPath)
	require.NoError(t, viper.ReadInConfig())

	options := loadProfileOptions()
	assert.Equal(t, []string{"default", "visible-search", "work"}, options)
	for _, conversationID := range []string{"", "saved-conversation"} {
		t.Run("conversation="+conversationID, func(t *testing.T) {
			m := newModel(t.Context(), Config{
				Remote: true, ConversationID: conversationID,
				Profile: "hidden-search", ProfileOptions: options,
			})
			t.Cleanup(m.cancel)
			assert.Equal(t, "hidden-search", m.profile)
			assert.Equal(t, "hidden-search", m.profileOptions[m.profileIndex])
			assert.Equal(t, "hidden-search", profileForRequest(m.profile))
			assert.Equal(t, conversationID == "", m.canChangeProfile())
		})
	}
}
