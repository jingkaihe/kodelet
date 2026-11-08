package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type toolInventoryRunner struct {
	aboutRunner
	inventory protocol.WorkspaceInventoryResult
	err       error
	requests  []chat.WorkspaceTarget
}

func (r *toolInventoryRunner) ToolInventory(_ context.Context, target chat.WorkspaceTarget) (protocol.WorkspaceInventoryResult, error) {
	r.requests = append(r.requests, target)
	return r.inventory, r.err
}

func sampleToolInventory() protocol.WorkspaceInventoryResult {
	return protocol.WorkspaceInventoryResult{
		CWD: "/workspace",
		BuiltinTools: []protocol.InventoryTool{
			{Name: "bash", Description: "Run a bash command.", Enabled: true},
			{Name: "file_read", Description: "Read a file."},
		},
		Plugins: []protocol.InventoryPlugin{{
			Name:  "acme@kit",
			Scope: protocol.InventoryScopeGlobal,
			Path:  "/home/user/.kodelet/plugins/acme@kit",
			Extensions: []protocol.InventoryExtension{{
				ID: "acme@kit/finder", Name: "finder", Version: "1.2.0", Scope: protocol.InventoryScopeGlobal,
				Path: "/home/user/.kodelet/plugins/acme@kit/extensions/finder", Status: protocol.InventoryStatusReady,
				Tools: []protocol.InventoryTool{{Name: "search_code", Description: "Search code by concept.", Enabled: true}},
				Hooks: []string{"turn.end"},
			}},
			Skills: []protocol.InventorySkill{{Name: "helper", Description: "Help with kits.", Scope: protocol.InventoryScopeGlobal, Path: "/home/user/.kodelet/plugins/acme@kit/skills/helper", Enabled: true}},
		}},
		Extensions: []protocol.InventoryExtension{{
			ID: "broken", Name: "broken", Scope: protocol.InventoryScopeRepo, Path: "/workspace/.kodelet/extensions/broken",
			Status: protocol.InventoryStatusFailed, Error: "exit status 1", Tools: []protocol.InventoryTool{},
		}},
		Skills: []protocol.InventorySkill{{Name: "release", Description: "Cut a release.", Scope: protocol.InventoryScopeRepo, Path: "/workspace/.kodelet/skills/release", Enabled: true}},
	}
}

func loadedAboutToolsState(inventory protocol.WorkspaceInventoryResult) *aboutToolsState {
	state := &aboutToolsState{}
	state.setInventory(inventory)
	return state
}

func rowLabels(rows []inventoryRow) []string {
	labels := make([]string, len(rows))
	for i, row := range rows {
		labels[i] = strings.Repeat("  ", row.level-1) + row.node.label
	}
	return labels
}

func TestBuildInventoryTreeGroupsBySource(t *testing.T) {
	state := loadedAboutToolsState(sampleToolInventory())
	require.Len(t, state.tree, 3)
	builtin, plugins, standalone := state.tree[0], state.tree[1], state.tree[2]
	assert.Equal(t, "1 tool · 1 off", builtin.meta)
	assert.True(t, builtin.children[1].off)
	assert.Equal(t, "1 plugin", plugins.meta)
	plugin := plugins.children[0]
	assert.Equal(t, "1 extension · 1 skill", plugin.meta)
	assert.Equal(t, "1 tool · 1 hook", plugin.children[0].meta)
	assert.Equal(t, "/home/user/.kodelet/plugins/acme@kit/extensions/finder", plugin.children[0].children[1].path, "tools show their extension's path")
	assert.Equal(t, "1 extension · 1 skill", standalone.meta)
	broken := standalone.children[0]
	assert.True(t, broken.failed)
	assert.Equal(t, "Not loaded", broken.meta)
	assert.Equal(t, "Error", broken.children[0].label)
	assert.Equal(t, 2, state.tools, "failed extensions and off tools are not counted")
	assert.Equal(t, 2, state.extensions)
	assert.Equal(t, 2, state.skills)
	assert.Equal(t, 1, state.failed)

	assert.Equal(t, []string{
		"Built-in tools", "  bash", "  file_read",
		"Plugins", "  acme@kit",
		"Standalone", "  broken", "  release",
	}, rowLabels(state.rows()))
}

func TestFilterInventoryExpandsMatchesAndTheirAncestors(t *testing.T) {
	state := loadedAboutToolsState(sampleToolInventory())
	state.setQuery("search")
	assert.Equal(t, []string{"Plugins", "  acme@kit", "    finder", "      search_code"}, rowLabels(state.rows()))
	assert.Equal(t, "plugin:global:acme@kit/extension:finder/tool:search_code", state.cursorID, "the cursor starts on the first match")

	state.setQuery("finder")
	assert.Equal(t, []string{"Plugins", "  acme@kit", "    finder"}, rowLabels(state.rows()), "a matching container keeps its subtree folded")

	state.setQuery("nothing")
	assert.Empty(t, state.rows())
}

func TestAboutToolsTabLoadsOnFirstVisit(t *testing.T) {
	runner := &toolInventoryRunner{inventory: sampleToolInventory()}
	m := newThemeTestModel(t, Config{Runner: runner})
	m.width, m.height = 90, 40
	m.resize()
	require.NotNil(t, m.conversationState)
	m.conversationID = "conversation-1"
	m.conversationState.conversationID = "conversation-1"
	m.openAboutDialog()
	assert.Nil(t, m.updateAboutDialogKey("tab"), "the context tab loads nothing")
	cmd := m.updateAboutDialogKey("tab")
	require.Equal(t, aboutTabTools, m.aboutDialog.tab)
	require.NotNil(t, cmd)
	assert.Contains(t, xansi.Strip(m.renderAboutDialog()), "Loading tools…")
	for _, msg := range aboutMessagesFromCmd(t, cmd) {
		updated, _ := m.Update(msg)
		*m = updated.(model)
	}
	require.Equal(t, []chat.WorkspaceTarget{{ConversationID: "conversation-1"}}, runner.requests)

	view := xansi.Strip(m.renderAboutDialog())
	for _, want := range []string{"[Tools] · Tab", "2 tools · 2 extensions · 2 skills", "! 1 extension failed to load", "Built-in tools", "1 tool · 1 off", "acme@kit global", "broken repo failed", "Not loaded", "↑/↓ move · ←/→ fold · / filter · Esc close"} {
		assert.Contains(t, view, want)
	}
	assert.NotContains(t, view, "Run a bash command.", "descriptions stay in the details footer")
	assert.Contains(t, view, "Built-in tools · Section", "the details footer describes the selected row")

	m.updateAboutDialogKey("shift+tab")
	assert.Nil(t, m.updateAboutDialogKey("tab"), "returning to the tab keeps the loaded list")
	assert.Len(t, runner.requests, 1)
}

func TestAboutToolsListsTheDraftWorkspace(t *testing.T) {
	runner := &toolInventoryRunner{inventory: sampleToolInventory()}
	m := newThemeTestModel(t, Config{Runner: runner})
	m.width, m.height = 90, 40
	m.resize()
	require.NotNil(t, m.conversationState)
	m.conversationState.conversationID = ""
	m.conversationState.requestedCWD = "/repo/other"
	m.conversationState.profile = "review"
	m.environmentProfile = "sandbox"
	m.openAboutDialog()
	cmd := m.setAboutTab(aboutTabTools)
	aboutMessagesFromCmd(t, cmd)

	require.Len(t, runner.requests, 1)
	assert.Equal(t, chat.WorkspaceTarget{CWD: "/repo/other", Profile: "review", EnvironmentProfile: "sandbox"}, runner.requests[0], "drafts list the workspace their slash commands come from")
	assert.Equal(t, m.workspaceTargetForState(m.conversationState), runner.requests[0])
}

func TestAboutToolsKeysNavigateFoldAndFilter(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.width, m.height = 90, 40
	m.resize()
	m.openAboutDialog()
	m.aboutDialog.tab = aboutTabTools
	m.aboutDialog.tools = loadedAboutToolsState(sampleToolInventory())
	state := m.aboutDialog.tools
	press := func(keys ...string) {
		for _, key := range keys {
			m.updateAboutDialogKey(key)
		}
	}

	press("down")
	assert.Equal(t, "section:builtin/tool:bash", state.cursorID)
	press("left")
	assert.Equal(t, "section:builtin", state.cursorID, "← moves to the parent")
	press("left")
	assert.False(t, state.expanded["section:builtin"], "← folds an open row")
	press("right", "right")
	assert.Equal(t, "section:builtin/tool:bash", state.cursorID, "→ unfolds, then moves to the first child")
	press("end", "up", "up")
	assert.Equal(t, "section:standalone", state.cursorID)
	press("enter")
	assert.False(t, state.expanded["section:standalone"], "Enter toggles instead of closing")
	require.NotNil(t, m.aboutDialog)

	press("/", "s", "e", "a", "r", "c", "h")
	assert.True(t, state.filtering)
	assert.Equal(t, "search", state.query)
	view := xansi.Strip(m.renderAboutDialog())
	assert.Contains(t, view, "/ search")
	assert.Contains(t, view, "search_code")
	assert.Contains(t, view, "Type to filter · Enter done · Esc clear")
	press("enter")
	assert.False(t, state.filtering)
	assert.Equal(t, "search", state.query, "Enter keeps the filter")
	assert.Contains(t, xansi.Strip(m.renderAboutDialog()), "/ filter · Esc clear", "Esc clears an applied filter before closing")
	press("q")
	assert.Nil(t, m.aboutDialog, "q still closes outside the filter input")

	m.openAboutDialog()
	m.aboutDialog.tab = aboutTabTools
	m.aboutDialog.tools = loadedAboutToolsState(sampleToolInventory())
	state = m.aboutDialog.tools
	press("/", "z", "z")
	assert.Contains(t, xansi.Strip(m.renderAboutDialog()), "Nothing matches “zz”.")
	press("esc")
	assert.Empty(t, state.query, "Esc clears the filter first")
	require.NotNil(t, m.aboutDialog)
	press("esc")
	assert.Nil(t, m.aboutDialog, "a second Esc closes the dialog")
}

func TestAboutToolsRetriesFailedLoads(t *testing.T) {
	runner := &toolInventoryRunner{err: errors.New("runner is offline")}
	m := newThemeTestModel(t, Config{Runner: runner})
	m.width, m.height = 90, 30
	m.resize()
	m.openAboutDialog()
	cmd := m.setAboutTab(aboutTabTools)
	for _, msg := range aboutMessagesFromCmd(t, cmd) {
		updated, _ := m.Update(msg)
		*m = updated.(model)
	}
	view := xansi.Strip(m.renderAboutDialog())
	assert.Contains(t, view, "Could not load tools: runner is offline")
	assert.Contains(t, view, "Press r to try again")

	runner.err = nil
	runner.inventory = sampleToolInventory()
	cmd = m.updateAboutDialogKey("r")
	require.NotNil(t, cmd)
	stale := toolInventoryMsg{requestID: m.aboutDialog.tools.requestID - 1, err: errors.New("stale")}
	m.applyToolInventory(stale)
	assert.Nil(t, m.aboutDialog.tools.err, "responses to earlier requests are discarded")
	for _, msg := range aboutMessagesFromCmd(t, cmd) {
		updated, _ := m.Update(msg)
		*m = updated.(model)
	}
	assert.Contains(t, xansi.Strip(m.renderAboutDialog()), "Built-in tools")
	assert.Len(t, runner.requests, 2)
}

func TestAboutToolsWithoutAProvider(t *testing.T) {
	m := newThemeTestModel(t, Config{})
	m.width, m.height = 90, 30
	m.resize()
	m.openAboutDialog()
	assert.Nil(t, m.setAboutTab(aboutTabTools))
	assert.Contains(t, xansi.Strip(m.renderAboutDialog()), "This connection does not list tools.")
	m.updateAboutDialogKey("enter")
	assert.Nil(t, m.aboutDialog, "Enter closes when there is no list to fold")
}

func TestAboutToolsSelectedRowKeepsItsWidth(t *testing.T) {
	newThemeTestModel(t, Config{}) // Initializes the theme styles rows render with.
	state := loadedAboutToolsState(sampleToolInventory())
	state.setExpanded("plugin:global:acme@kit", true)
	for _, row := range state.rows() {
		for _, width := range []int{12, 30, 68} {
			for _, selected := range []bool{false, true} {
				line := renderInventoryRow(row, "", width, selected)
				assert.Equal(t, width, lipgloss.Width(line), "%s at %d", row.node.label, width)
			}
		}
	}
}
