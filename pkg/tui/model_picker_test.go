package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func modelOptionLabels(options []modelOption) []string {
	labels := make([]string, len(options))
	for index, option := range options {
		labels[index] = option.label()
	}
	return labels
}

func TestModelOptionsKeepSelectionFirstThenSortVersionsDescending(t *testing.T) {
	options := []string{" model-2.9 ", "model-2.10", "model-10", "model-2", "model-2.10", ""}
	assert.Equal(t, []string{"model-2", "model-10", "model-2.10", "model-2.9"}, normalizeModelOptions(options, " model-2 "))
	assert.Equal(t, []string{"model-10", "model-2.10", "model-2.9", "model-2"}, normalizeModelOptions(options, ""))
	assert.Equal(t, " model-2.9 ", options[0], "sorting must not mutate the source catalog")

	m := newThemeTestModel(t, Config{Remote: true, Model: "model-2", ModelOptions: options})
	m.handleModelCommand("model-2.9")
	m.handleModelCommand("")
	assert.Zero(t, m.modelPickerIndex)
	assert.Equal(t, []string{"model-2.9", "model-10", "model-2.10", "model-2"}, modelOptionLabels(m.filteredModelOptions()))
	updated, _ := m.Update(keyPress(tea.KeyDown))
	*m = updated.(model)
	updated, _ = m.Update(keyPress(tea.KeyEnter))
	*m = updated.(model)
	assert.Equal(t, "model-10", m.selectedModel)
	assert.False(t, m.modelPickerOpen)
}

func TestModelSlashCommandIsLocalBeforeDiscoveryReadiness(t *testing.T) {
	runner := &recordingRunner{}
	m := newThemeTestModel(t, Config{Remote: true, Runner: runner, Model: "first", ModelOptions: []string{"first", "second"}})
	assert.Contains(t, slashCommandNames(m.slashCommands), "model")
	m.extensionLifecyclePending = true
	m.initialHistoryPending = true
	m.deferSubmitUntilHistory = true
	m.reasoningPickerOpen = true
	m.textarea.SetValue("/model")

	updated, cmd := m.Update(keyPress(tea.KeyEnter))
	*m = updated.(model)
	assert.Nil(t, cmd)
	assert.True(t, m.modelPickerOpen)
	assert.False(t, m.reasoningPickerOpen)
	assert.False(t, m.slashCommandSuggestionsOpen())
	assert.Empty(t, m.textarea.Value())
	assert.Empty(t, m.submitAfterHistoryLoad)
	assert.Empty(t, m.submitAfterExtensionLifecycle)
	assert.Empty(t, m.conversationID)
	assert.False(t, m.running)
	assert.Empty(t, runner.req.Message)
	assert.Contains(t, xansi.Strip(m.View().Content), "Profile/model:")
}

func TestModelSlashCommandDirectSelectionAndErrors(t *testing.T) {
	m := newThemeTestModel(t, Config{Remote: true, Model: "first", ModelOptions: []string{"first", "second"}})
	m.textarea.SetValue("/model second")
	assert.Nil(t, m.submit())
	assert.Equal(t, "second", m.selectedModel)
	assert.False(t, m.modelPickerOpen)
	assert.Empty(t, m.textarea.Value())

	m.textarea.SetValue("/model other-profile-model")
	assert.NotNil(t, m.submit())
	assert.Equal(t, "second", m.selectedModel)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, "Model unavailable", m.uiNotifications[0].title)

	m.selectedModel = ""
	m.modelOptions = nil
	m.textarea.SetValue("/model")
	assert.NotNil(t, m.submit())
	assert.False(t, m.modelPickerOpen)
	assert.Equal(t, "No models available", m.uiNotifications[len(m.uiNotifications)-1].title)
}

func TestModelPickerUsesVisibleProfilesAndExactPairs(t *testing.T) {
	m := newThemeTestModel(t, Config{
		Remote: true, Profile: "private", Model: "cli-model",
		ProfileOptions: []string{"deep", "flair"},
		ProfileSettings: map[string]ProfileSettings{
			"private": {Model: "model-2"},
			"deep":    {Model: "model-2", ModelOptions: []string{"model-10", "model-2.9", "model-2.10"}},
			"flair":   {Model: "model-2"},
			"hidden":  {Model: "model-99"},
		},
	})
	m.handleModelCommand("")
	assert.Equal(t, []string{
		"private/cli-model", "deep/model-10", "deep/model-2.10", "deep/model-2.9",
		"deep/model-2", "flair/model-2", "private/model-2",
	}, modelOptionLabels(m.filteredModelOptions()))
	m.modelPickerQuery = "FLAIR"
	assert.Equal(t, []modelOption{{profile: "flair", model: "model-2"}}, m.filteredModelOptions())
	m.selectModelPickerOption(0)
	assert.Equal(t, "flair", m.profile)
	assert.Equal(t, "model-2", m.selectedModel)
	m.handleModelCommand("")
	assert.Equal(t, modelOption{profile: "flair", model: "model-2"}, m.filteredModelOptions()[0])
	assert.NotContains(t, xansi.Strip(m.renderModelPicker()), "hidden/model-99")
}

func TestModelPickerOpensWhenOnlyAnotherProfileHasModels(t *testing.T) {
	for _, profile := range []string{"empty", ""} {
		t.Run("profile="+profile, func(t *testing.T) {
			m := newThemeTestModel(t, Config{
				Remote: true, Profile: profile, ProfileOptions: []string{"deep"},
				ProfileSettings: map[string]ProfileSettings{"deep": {Model: "vendor/model"}},
			})
			assert.Nil(t, m.handleModelCommand(""))
			assert.True(t, m.modelPickerOpen)
			assert.Equal(t, []modelOption{{profile: "deep", model: "vendor/model"}}, m.filteredModelOptions())
			m.selectModelPickerOption(0)
			assert.Equal(t, "deep/vendor/model", m.modelLabel())
		})
	}
}

func TestModelCommandPreservesSlashIDsAndRejectsAmbiguousQualifiedLabels(t *testing.T) {
	m := newThemeTestModel(t, Config{
		Remote: true, Profile: "current", Model: "current-model",
		ProfileOptions: []string{"current", "team", "team/vendor"},
		ProfileSettings: map[string]ProfileSettings{
			"current":     {Model: "current-model", ModelOptions: []string{"vendor/model"}},
			"team":        {Model: "vendor/model"},
			"team/vendor": {Model: "model"},
		},
	})
	assert.NotNil(t, m.handleModelCommand("team/vendor/model"))
	assert.Equal(t, "current", m.profile)
	assert.Equal(t, "current-model", m.selectedModel)
	require.Len(t, m.uiNotifications, 1)
	assert.Equal(t, "Ambiguous profile/model", m.uiNotifications[0].title)
	m.handleModelCommand("")
	picker := xansi.Strip(m.renderModelPicker())
	assert.Contains(t, picker, "team/vendor/model (profile: team)")
	assert.Contains(t, picker, "team/vendor/model (profile: team/vendor)")
	assert.NotContains(t, picker, "current/current-model (profile:")
	for index, option := range m.filteredModelOptions() {
		if option.profile == "team/vendor" {
			m.selectModelPickerOption(index)
			break
		}
	}
	assert.Equal(t, "team/vendor", m.profile)
	assert.Equal(t, "model", m.selectedModel)
	m.handleModelCommand("current/vendor/model")
	assert.Equal(t, "current", m.profile)
	assert.Equal(t, "vendor/model", m.selectedModel)
	m.handleModelCommand("current-model")
	m.handleModelCommand("vendor/model")
	assert.Equal(t, "current", m.profile, "slash-containing raw IDs retain current-profile shorthand semantics")
	assert.Equal(t, "vendor/model", m.selectedModel)
	m.modelOptions = append(m.modelOptions, "team/vendor/model")
	assert.Nil(t, m.handleModelCommand("team/vendor/model"))
	assert.Equal(t, "current", m.profile, "exact raw current-profile IDs take precedence over qualified labels")
	assert.Equal(t, "team/vendor/model", m.selectedModel)
}

func TestModelPickerProfileAndExplicitEffortReachRequest(t *testing.T) {
	for _, effort := range []struct {
		name     string
		initial  string
		want     string
		explicit bool
	}{
		{"supported", "high", "high", true},
		{"unsupported", "minimal", "medium", false},
	} {
		t.Run(effort.name, func(t *testing.T) {
			runner := &recordingRunner{}
			m := newThemeTestModel(t, Config{
				Remote: true, Runner: runner, Profile: "flair", Model: "shared-model",
				ProfileOptions:  []string{"flair", "deep"},
				ReasoningEffort: effort.initial, ReasoningEffortExplicit: true,
				ProfileSettings: map[string]ProfileSettings{
					"flair": {Model: "shared-model", ReasoningEffort: "low", ReasoningEffortOptions: []string{"minimal", "low", "high"}},
					"deep":  {Model: "deep-default", ModelOptions: []string{"shared-model"}, ReasoningEffort: "medium", ReasoningEffortOptions: []string{"medium", "high"}},
				},
			})
			m.textarea.SetValue("/model deep/shared-model")
			updated, cmd := m.Update(keyPress(tea.KeyEnter))
			*m = updated.(model)
			assert.Nil(t, cmd)
			assert.Empty(t, runner.req.Message, "model selection must never invoke the runner")
			assert.Equal(t, "deep/shared-model", m.modelLabel())
			assert.Equal(t, []string{"medium", "high"}, m.reasoningEffortOptions)
			assert.Equal(t, effort.want, m.reasoningEffort)
			assert.Equal(t, effort.explicit, m.reasoningEffortExplicit)
			m.textarea.SetValue("hello")
			cmd = m.submit()
			require.NotNil(t, cmd)
			assert.Nil(t, cmd())
			receiveRunMsg(t, m.runCh)
			receiveRunMsg(t, m.runCh)
			assert.Equal(t, "deep", runner.req.Profile)
			require.NotNil(t, runner.req.Options)
			require.NotNil(t, runner.req.Options.Model)
			assert.Equal(t, "shared-model", *runner.req.Options.Model)
			assert.Equal(t, effort.want, runner.req.ReasoningEffort)
		})
	}
}

func TestModelSelectionRefreshesLiveEffortsForNewDraftAfterResume(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%t", explicit), func(t *testing.T) {
			m := newThemeTestModel(t, Config{
				Remote: true, ConversationID: "saved", Profile: "deep", Model: "saved-model",
				ReasoningEffort: "high", ReasoningEffortOptions: []string{"high"}, ReasoningEffortExplicit: explicit,
				ProfileSettings: map[string]ProfileSettings{
					"deep": {Model: "live-model", ReasoningEffort: "medium", ReasoningEffortOptions: []string{"low", "medium", "high"}},
				},
			})
			assert.False(t, m.canChangeModel())
			m.createNewConversation()
			assert.True(t, m.canChangeModel())
			assert.Equal(t, []string{"high"}, m.reasoningEffortOptions)
			m.handleModelCommand("live-model")
			assert.Equal(t, "deep/live-model", m.modelLabel())
			assert.Equal(t, []string{"low", "medium", "high"}, m.reasoningEffortOptions)
			if explicit {
				assert.Equal(t, "high", m.reasoningEffort)
			} else {
				assert.Equal(t, "medium", m.reasoningEffort)
			}
			assert.Equal(t, explicit, m.reasoningEffortExplicit)
		})
	}
}

func TestModelPickerSearchKeyboardAndEscape(t *testing.T) {
	m := newThemeTestModel(t, Config{Remote: true, Model: "alpha", ModelOptions: []string{"alpha", "beta", "gamma"}})
	m.handleModelCommand("")
	m.textarea.SetValue("keep this draft")
	for _, step := range []struct {
		msg   tea.KeyPressMsg
		index int
	}{
		{keyPress(tea.KeyDown), 1},
		{keyPress(tea.KeyUp), 0},
		{keyPress(tea.KeyUp), 2},
		{keyPress(tea.KeyTab), 0},
		{keyPressWithMod(tea.KeyTab, tea.ModShift), 2},
	} {
		updated, cmd := m.Update(step.msg)
		*m = updated.(model)
		assert.Nil(t, cmd)
		assert.Equal(t, step.index, m.modelPickerIndex)
	}
	updated, _ := m.Update(keyPress(tea.KeyEscape))
	*m = updated.(model)
	assert.False(t, m.modelPickerOpen)
	assert.Equal(t, "alpha", m.selectedModel)
	assert.Equal(t, "keep this draft", m.textarea.Value())

	m.handleModelCommand("")
	updated, _ = m.Update(textKeyPress("unknowné"))
	*m = updated.(model)
	assert.Empty(t, m.filteredModelOptions())
	assert.Contains(t, xansi.Strip(m.renderModelPicker()), "No matching models")
	updated, _ = m.Update(keyPress(tea.KeyEnter))
	*m = updated.(model)
	assert.True(t, m.modelPickerOpen, "Enter on an empty result must not submit to the LLM")
	updated, _ = m.Update(keyPress(tea.KeyBackspace))
	*m = updated.(model)
	assert.Equal(t, "unknown", m.modelPickerQuery)
	updated, _ = m.Update(keyPressWithMod('u', tea.ModCtrl))
	*m = updated.(model)
	assert.Empty(t, m.modelPickerQuery)
	updated, _ = m.Update(keyPress(tea.KeyBackspace))
	*m = updated.(model)
	assert.Empty(t, m.modelPickerQuery)
	updated, _ = m.Update(tea.PasteMsg{Content: "\x1b[31mBETA\n\x1b[0m"})
	*m = updated.(model)
	assert.Equal(t, "BETA", m.modelPickerQuery)
	assert.Equal(t, []string{"beta"}, modelOptionLabels(m.filteredModelOptions()))
	updated, _ = m.Update(keyPressWithMod(tea.KeyEnter, tea.ModShift))
	*m = updated.(model)
	assert.Equal(t, "keep this draft", m.textarea.Value())
	updated, _ = m.Update(keyPress(tea.KeyEnter))
	*m = updated.(model)
	assert.Equal(t, "beta", m.selectedModel)
	assert.False(t, m.modelPickerOpen)
	assert.Equal(t, "keep this draft", m.textarea.Value())
}

func TestModelPickerScrollsAndFitsSmallTerminals(t *testing.T) {
	options := make([]string, 50)
	for index := range options {
		options[index] = fmt.Sprintf("provider/model-%02d", index)
	}
	m := newThemeTestModel(t, Config{Remote: true, Model: options[0], ModelOptions: options})
	m.handleModelCommand("")
	assert.Contains(t, xansi.Strip(m.renderModelPicker()), "provider/model-49")
	updated, _ := m.Update(keyPress(tea.KeyPgDown))
	*m = updated.(model)
	assert.Equal(t, 8, m.modelPickerIndex)
	updated, _ = m.Update(keyPress(tea.KeyPgUp))
	*m = updated.(model)
	assert.Zero(t, m.modelPickerIndex)
	m.moveModelPicker(-1)
	assert.Equal(t, 49, m.modelPickerIndex)
	assert.Contains(t, xansi.Strip(m.renderModelPicker()), "> provider/model-01")
	assert.NotContains(t, xansi.Strip(m.renderModelPicker()), "provider/model-00")

	for _, size := range []tea.WindowSizeMsg{
		{Width: 80, Height: 24}, {Width: 24, Height: 10}, {Width: 8, Height: 7}, {Width: 4, Height: 3},
	} {
		updated, _ := m.Update(size)
		*m = updated.(model)
		picker := m.renderModelPicker()
		assert.LessOrEqual(t, m.modelPickerHeight(), max(0, size.Height-inputHeight-3))
		if picker != "" {
			assert.Equal(t, m.modelPickerHeight(), lipgloss.Height(picker))
			assert.LessOrEqual(t, lipgloss.Width(picker), m.inputOuterWidth())
			start, end := m.modelPickerWindow()
			assert.LessOrEqual(t, start, m.modelPickerIndex)
			assert.Greater(t, end, m.modelPickerIndex)
		}
		assert.NotPanics(t, func() { m.View() })
		if size.Height >= inputHeight+3 {
			assert.LessOrEqual(t, lipgloss.Height(m.View().Content), size.Height)
		}
	}
}

func TestModelPickerMouseSelectionAndOtherPickers(t *testing.T) {
	m := newThemeTestModel(t, Config{
		Remote: true, Model: "first", ModelOptions: []string{"first", "second"},
		ProfileOptions: []string{"default", "work"}, ReasoningEffortOptions: []string{"low", "high"},
	})
	m.handleModelCommand("")
	_, ok := m.modelPickerOptionAt(tuiLeftMargin, m.viewport.Height())
	assert.False(t, ok, "search header is not a selectable option")
	updated, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: tuiLeftMargin, Y: m.viewport.Height() + 2})
	*m = updated.(model)
	assert.Equal(t, "second", m.selectedModel)
	assert.False(t, m.modelPickerOpen)

	m.handleModelCommand("")
	updated, _ = m.Update(keyPressWithMod('t', tea.ModCtrl))
	*m = updated.(model)
	assert.False(t, m.modelPickerOpen)
	assert.Equal(t, "second", m.selectedModel, "Ctrl+T confirms the highlighted pair")
	m.handleModelCommand("")
	updated, _ = m.Update(keyPressWithMod('y', tea.ModCtrl))
	*m = updated.(model)
	assert.True(t, m.reasoningPickerOpen)
	assert.False(t, m.modelPickerOpen)
	m.handleModelCommand("")
	updated, _ = m.Update(keyPressWithMod('l', tea.ModCtrl))
	*m = updated.(model)
	assert.NotNil(t, m.conversationPicker)
	assert.False(t, m.modelPickerOpen)
}

func TestModelPickerProfileResetAndDraftIsolation(t *testing.T) {
	settings := map[string]ProfileSettings{
		"default": {Model: "default-model", ModelOptions: []string{"default-model", "other-model"}},
		"WORK":    {Model: "work-model", ModelOptions: []string{"work-model", "work-other"}},
	}
	m := newThemeTestModel(t, Config{
		Remote:          true,
		Model:           "cli-model",
		Profile:         "default",
		ProfileOptions:  []string{"default", "work"},
		ProfileSettings: settings,
	})
	assert.Equal(t, "cli-model", m.selectedModel)
	assert.Equal(t, []string{"cli-model", "other-model", "default-model"}, m.modelOptions)
	settings["WORK"].ModelOptions[0] = "mutated"
	m.handleModelCommand("other-model")
	m.handleModelCommand("")
	m.selectModelPickerOption(0)
	assert.Equal(t, "other-model", m.selectedModel, "confirming the selected pair should preserve its draft choice")
	m.handleModelCommand("work/work-other")
	assert.Equal(t, "work-other", m.selectedModel)
	assert.Equal(t, []string{"work-other", "work-model"}, m.modelOptions)
	m.handleModelCommand("default/default-model")
	assert.Equal(t, "default-model", m.selectedModel, "switching profiles must select the chosen model, not the CLI override")
	m.handleModelCommand("other-model")
	firstDraft := m.conversationState
	m.createNewConversation()
	assert.Equal(t, "cli-model", m.selectedModel, "new drafts use startup defaults")
	m.modelOptions[0] = "new-draft-only"
	assert.Equal(t, "other-model", firstDraft.modelOptions[0])
	assert.Equal(t, "cli-model", m.conversationDefaults.modelOptions[0])
	m.activateConversation(firstDraft.key)
	assert.Equal(t, "other-model", m.selectedModel)
}

func TestModelSelectionSurvivesAllocatedIDRetriesAndFollowUps(t *testing.T) {
	runner := &recordingRunner{}
	m := newThemeTestModel(t, Config{Remote: true, Runner: runner, Model: "cli-model", ModelOptions: []string{"cli-model", "picked-model"}})
	m.handleModelCommand("picked-model")
	var conversationID string
	for index, runErr := range []error{assert.AnError, nil, nil} {
		runner.err = runErr
		m.textarea.SetValue(fmt.Sprintf("turn %d", index))
		cmd := m.submit()
		require.NotNil(t, cmd)
		require.NotEmpty(t, m.conversationID, "submission allocates the ID before request execution")
		if index == 0 {
			conversationID = m.conversationID
		}
		assert.Equal(t, conversationID, m.conversationID)
		assert.False(t, m.modelPickerOpen)
		assert.Nil(t, cmd())
		for range 2 {
			updated, _ := m.Update(receiveRunMsg(t, m.runCh))
			*m = updated.(model)
		}
		require.NotNil(t, runner.req.Options)
		require.NotNil(t, runner.req.Options.Model)
		assert.Equal(t, "picked-model", *runner.req.Options.Model)
		assert.Equal(t, conversationID, runner.req.ConversationID)
		assert.False(t, m.running)
		m.textarea.SetValue("/model cli-model")
		assert.NotNil(t, m.submit())
		assert.Equal(t, "picked-model", m.selectedModel)
		assert.Equal(t, "Model is locked", m.uiNotifications[len(m.uiNotifications)-1].title)
	}
}

func TestModelPickerResumedHistoryLocksSelectionAndOmitsOverride(t *testing.T) {
	runner := &conversationSourceRunner{history: chat.ConversationHistory{
		ID: "saved-id", Profile: "saved-profile", Model: " saved-model ",
	}}
	m := newThemeTestModel(t, Config{Remote: true, Runner: runner, ConversationID: "saved-id", Model: "cli-model"})
	m.textarea.SetValue("/model")
	assert.NotNil(t, m.submit(), "resume is locked even before history loads")
	assert.False(t, m.modelPickerOpen)
	updated, _ := m.Update(loadConversationHistoryFromSource(t.Context(), m.key, m.conversationID, runner)())
	*m = updated.(model)
	assert.Equal(t, "saved-model", m.selectedModel)
	assert.Equal(t, []string{"saved-model"}, m.modelOptions)
	assert.False(t, m.canChangeModel())
	m.textarea.SetValue("/model cli-model")
	assert.NotNil(t, m.submit())
	assert.Equal(t, "saved-model", m.selectedModel)
	assert.Contains(t, m.uiNotifications[len(m.uiNotifications)-1].message, "/new")
	m.textarea.SetValue("continue")
	cmd := m.submit()
	require.NotNil(t, cmd)
	cmd()
	receiveRunMsg(t, m.runCh)
	receiveRunMsg(t, m.runCh)
	assert.Nil(t, runner.req.Options)
}

func TestModelPickerDeferredInitializationAndComposerLabel(t *testing.T) {
	options := []string{"configured", "picked"}
	m := newThemeTestModel(t, Config{Initialize: func(context.Context) (Config, error) {
		return Config{Runner: &recordingRunner{}, Model: "cli-model", ModelOptions: options}, nil
	}})
	m.textarea.SetValue("draft during startup")
	updated, _ := m.Update(m.initializeCommand()())
	*m = updated.(model)
	assert.Equal(t, "cli-model", m.selectedModel)
	assert.Equal(t, []string{"cli-model", "picked", "configured"}, m.modelOptions)
	assert.Equal(t, "draft during startup", m.textarea.Value())
	options[0] = "mutated"
	assert.Equal(t, "configured", m.modelOptions[2])
	m.width = 140
	m.resize()
	assert.Equal(t, "$0.00 · cli-model · medium", m.inputTopRightLabel())
	assert.Equal(t, m.inputTopRightLabel(), xansi.Strip(m.renderInputTopLabel(m.inputTopRightLabel())))
	start, end, ok := m.reasoningEffortLabelBoundsInBlock()
	require.True(t, ok)
	assert.Equal(t, m.reasoningEffortLabel(), xansi.Cut(xansi.Strip(m.renderInputTopBorder()), start, end))
	m.selectedModel = strings.Repeat("long-model-id", 20)
	assert.Equal(t, "$0.00 · medium", m.inputTopRightLabel())
}
