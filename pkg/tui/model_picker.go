package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func normalizeModelOptions(options []string, selected string) []string {
	selected = strings.TrimSpace(selected)
	normalized := make([]string, 0, len(options)+1)
	seen := make(map[string]bool, len(options)+1)
	for _, option := range append(append([]string(nil), options...), selected) {
		option = strings.TrimSpace(option)
		if option != "" && !seen[option] {
			normalized = append(normalized, option)
			seen[option] = true
		}
	}
	// Keep the selection visible, then show higher versions first within each
	// model family. Model IDs alone do not establish release dates across families.
	ordering := collate.New(language.English, collate.Numeric)
	slices.SortFunc(normalized, func(a, b string) int {
		if a == b {
			return 0
		}
		if a == selected {
			return -1
		}
		if b == selected {
			return 1
		}
		return ordering.CompareString(b, a)
	})
	return normalized
}

func (m *model) refreshModelSettingsForProfile() {
	settings, _ := profileSettingsFor(m.profileSettings, m.profile)
	m.selectedModel = strings.TrimSpace(settings.Model)
	m.modelOptions = normalizeModelOptions(settings.ModelOptions, m.selectedModel)
	m.modelPickerOpen = false
	m.modelPickerQuery = ""
	m.modelPickerIndex = 0
}

func (m model) canChangeModel() bool {
	return strings.TrimSpace(m.conversationID) == "" && !m.conversationWasResumed && !m.running
}

func (m *model) handleModelCommand(args string) tea.Cmd {
	defer func() {
		m.resize()
		m.refreshViewport(false)
	}()
	m.profilePickerOpen = false
	m.reasoningPickerOpen = false
	m.modelPickerOpen = false
	if !m.canChangeModel() {
		return m.addUINotification(uiNotification{
			level:   uiNotificationInfo,
			title:   "Model is locked",
			message: "The model cannot change after a conversation starts. Use /new, then /model to select a model.",
		})
	}
	if len(m.modelOptions) == 0 {
		return m.addUINotification(uiNotification{
			level:   uiNotificationInfo,
			title:   "No models available",
			message: "No models are available for this profile. Select another profile or check the daemon's model settings.",
		})
	}
	if requested := strings.TrimSpace(args); requested != "" {
		if !slices.Contains(m.modelOptions, requested) {
			return m.addUINotification(uiNotification{
				level:   uiNotificationError,
				title:   "Model unavailable",
				message: "That model is not available for this profile. Use /model to select an available model.",
			})
		}
		m.selectedModel = requested
		return nil
	}
	m.cancelHistorySearch()
	m.shortcutsOpen = false
	m.modelPickerOpen = true
	m.modelPickerQuery = ""
	m.modelOptions = normalizeModelOptions(m.modelOptions, m.selectedModel)
	m.modelPickerIndex = 0
	return nil
}

func (m model) filteredModelOptions() []string {
	query := strings.ToLower(strings.TrimSpace(m.modelPickerQuery))
	if query == "" {
		return m.modelOptions
	}
	var options []string
	for _, option := range m.modelOptions {
		if strings.Contains(strings.ToLower(option), query) {
			options = append(options, option)
		}
	}
	return options
}

func (m *model) updateModelPickerKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+d":
		m.modelPickerOpen = false
	case "enter":
		m.selectModelPickerOption(m.modelPickerIndex)
	case "up", "shift+tab":
		m.moveModelPicker(-1)
	case "down", "tab":
		m.moveModelPicker(1)
	case "pgup":
		m.moveModelPicker(-max(1, m.modelPickerHeight()-1))
	case "pgdown":
		m.moveModelPicker(max(1, m.modelPickerHeight()-1))
	case "backspace", "ctrl+h":
		_, size := utf8.DecodeLastRuneInString(m.modelPickerQuery)
		m.modelPickerQuery = m.modelPickerQuery[:len(m.modelPickerQuery)-size]
		m.modelPickerIndex = 0
	case "ctrl+u":
		m.modelPickerQuery = ""
		m.modelPickerIndex = 0
	default:
		if msg.Text != "" {
			m.modelPickerQuery += normalizeSingleLinePaste(msg.Text)
			m.modelPickerIndex = 0
		}
	}
}

func (m *model) moveModelPicker(delta int) {
	count := len(m.filteredModelOptions())
	if count > 0 {
		m.modelPickerIndex = ((m.modelPickerIndex+delta)%count + count) % count
	}
}

func (m *model) selectModelPickerOption(index int) {
	options := m.filteredModelOptions()
	if !m.modelPickerOpen || !m.canChangeModel() || index < 0 || index >= len(options) {
		return
	}
	m.selectedModel = options[index]
	m.modelPickerOpen = false
}

func (m model) modelPickerHeight() int {
	if !m.modelPickerOpen {
		return 0
	}
	available := m.height - inputHeight - 2 - 1 -
		m.extensionWidgetsHeight(extensions.UIWidgetPlacementAboveComposer) -
		m.extensionWidgetsHeight(extensions.UIWidgetPlacementBelowComposer)
	return max(0, min(9, available, 1+max(1, len(m.filteredModelOptions()))))
}

func (m model) modelPickerWindow() (start, end int) {
	rows := m.modelPickerHeight()
	if rows > 1 {
		rows-- // Search header; keep the selected option visible on short terminals.
	}
	count := len(m.filteredModelOptions())
	start = min(max(0, m.modelPickerIndex-rows/2), max(0, count-rows))
	return start, min(count, start+rows)
}

func (m model) renderModelPicker() string {
	height := m.modelPickerHeight()
	if height == 0 {
		return ""
	}
	width := m.inputOuterWidth()
	options := m.filteredModelOptions()
	lines := make([]string, 0, height)
	if height > 1 {
		position := 0
		if len(options) > 0 {
			position = m.modelPickerIndex + 1
		}
		header := fmt.Sprintf("Model: %s  (%d/%d) · type to search · ↑/↓ Enter Esc", m.modelPickerQuery, position, len(options))
		lines = append(lines, inputLabelStyle.Render(fitVisible(header, width)))
	}
	if len(options) == 0 {
		lines = append(lines, inputLabelStyle.Render(fitVisible("No matching models", width)))
	}
	start, end := m.modelPickerWindow()
	for index := start; index < end; index++ {
		label := "  " + normalizeSingleLinePaste(options[index])
		style := inputLabelStyle
		if index == m.modelPickerIndex {
			label = "> " + normalizeSingleLinePaste(options[index])
			style = style.Background(themeColor(m.theme.ProfileSelected))
		}
		if options[index] == m.selectedModel {
			label += " (current)"
		}
		lines = append(lines, renderPersistentStyle(style, padVisible(fitVisible(label, width), width)))
	}
	return strings.Join(lines, "\n")
}

func (m model) modelPickerOptionAt(screenX, screenY int) (int, bool) {
	height := m.modelPickerHeight()
	if height == 0 || screenX < tuiLeftMargin || screenX >= tuiLeftMargin+m.inputOuterWidth() {
		return 0, false
	}
	row := screenY - m.viewport.Height()
	if height > 1 {
		row--
	}
	start, end := m.modelPickerWindow()
	return start + row, row >= 0 && start+row < end
}
