package tui

import (
	"context"
	"image/color"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
)

// toolInventoryProvider is implemented by daemon-backed runners that can list
// the tools, plugins, extensions and skills a run would load.
type toolInventoryProvider interface {
	ToolInventory(context.Context, chat.WorkspaceTarget) (protocol.WorkspaceInventoryResult, error)
}

type toolInventoryMsg struct {
	requestID int
	inventory protocol.WorkspaceInventoryResult
	err       error
}

const (
	inventoryBuiltinSection    = "section:builtin"
	inventoryPluginsSection    = "section:plugins"
	inventoryStandaloneSection = "section:standalone"
	// inventoryDetailLines is the selected item's description footer height.
	inventoryDetailLines = 4
)

type inventoryKind int

const (
	inventorySection inventoryKind = iota
	inventoryPlugin
	inventoryExtension
	inventoryTool
	inventorySkill
	inventoryDetail
)

func (k inventoryKind) label() string {
	return [...]string{"Section", "Plugin", "Extension", "Tool", "Skill", ""}[k]
}

type inventoryNode struct {
	id          string
	kind        inventoryKind
	label       string
	description string
	meta        string
	scope       string
	path        string
	version     string
	off         bool
	failed      bool
	children    []*inventoryNode
}

type inventoryRow struct {
	node       *inventoryNode
	level      int
	parentID   string
	expandable bool
	expanded   bool
}

// aboutToolsState is the About dialog's tools tab: the loaded inventory tree,
// what is expanded, the filter, and the cursor.
type aboutToolsState struct {
	requestID      int
	loading        bool
	unsupported    bool
	err            error
	tree           []*inventoryNode
	tools          int
	extensions     int
	skills         int
	failed         int
	expanded       map[string]bool
	filterExpanded map[string]bool
	query          string
	filtering      bool
	cursorID       string
	offset         int
}

func inventoryPlural(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + singular + "s"
}

func inventoryCounts(parts ...string) string {
	var kept []string
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " · ")
}

func inventoryToolNode(parentID, path string, tool protocol.InventoryTool) *inventoryNode {
	return &inventoryNode{
		id:          parentID + "/tool:" + tool.Name,
		kind:        inventoryTool,
		label:       tool.Name,
		description: tool.Description,
		path:        path,
		off:         !tool.Enabled,
	}
}

func inventorySkillNode(parentID string, skill protocol.InventorySkill, showScope bool) *inventoryNode {
	node := &inventoryNode{
		id:          parentID + "/skill:" + skill.Name,
		kind:        inventorySkill,
		label:       skill.Name,
		description: skill.Description,
		path:        skill.Path,
		off:         !skill.Enabled,
	}
	if showScope {
		node.scope = skill.Scope
	}
	return node
}

func inventoryExtensionNode(parentID string, ext protocol.InventoryExtension, showScope bool) *inventoryNode {
	id := parentID + "/extension:" + ext.Name
	failed := ext.Status == protocol.InventoryStatusFailed
	node := &inventoryNode{
		id:      id,
		kind:    inventoryExtension,
		label:   ext.Name,
		path:    ext.Path,
		version: ext.Version,
		failed:  failed,
	}
	if showScope {
		node.scope = ext.Scope
	}
	switch {
	case failed:
		node.meta = "Not loaded"
	default:
		node.meta = inventoryCounts(
			countIf(len(ext.Tools), "tool"),
			countIf(len(ext.Commands), "command"),
			countIf(len(ext.Hooks), "hook"),
		)
		if node.meta == "" {
			node.meta = "No tools"
		}
	}
	detail := func(key, label, value string, failed bool) {
		if value != "" {
			node.children = append(node.children, &inventoryNode{id: id + "/detail:" + key, kind: inventoryDetail, label: label, description: value, failed: failed})
		}
	}
	detail("error", "Error", ext.Error, true)
	detail("commands", "Commands", strings.Join(ext.Commands, ", "), false)
	detail("shortcuts", "Shortcuts", strings.Join(ext.Shortcuts, ", "), false)
	detail("hooks", "Hooks", strings.Join(ext.Hooks, ", "), false)
	for _, tool := range ext.Tools {
		node.children = append(node.children, inventoryToolNode(id, ext.Path, tool))
	}
	return node
}

func countIf(count int, singular string) string {
	if count == 0 {
		return ""
	}
	return inventoryPlural(count, singular)
}

// buildInventoryTree mirrors the web UI: built-in tools, then plugins with
// their extensions and skills, then items installed outside a plugin.
func buildInventoryTree(inventory protocol.WorkspaceInventoryResult) []*inventoryNode {
	builtin := &inventoryNode{
		id:          inventoryBuiltinSection,
		kind:        inventorySection,
		label:       "Built-in tools",
		description: "Tools compiled into Kodelet. Off tools are excluded by the active profile's allowed tools.",
	}
	var enabled, off int
	for _, pass := range []bool{true, false} {
		for _, tool := range inventory.BuiltinTools {
			if tool.Enabled == pass {
				builtin.children = append(builtin.children, inventoryToolNode(builtin.id, "", tool))
				if pass {
					enabled++
				} else {
					off++
				}
			}
		}
	}
	builtin.meta = inventoryPlural(enabled, "tool")
	if off > 0 {
		builtin.meta += " · " + strconv.Itoa(off) + " off"
	}

	plugins := &inventoryNode{
		id:          inventoryPluginsSection,
		kind:        inventorySection,
		label:       "Plugins",
		description: "Installed plugin packages and the extensions and skills they provide.",
		meta:        "None",
	}
	for _, plugin := range inventory.Plugins {
		id := "plugin:" + plugin.Scope + ":" + plugin.Name
		node := &inventoryNode{
			id:    id,
			kind:  inventoryPlugin,
			label: plugin.Name,
			scope: plugin.Scope,
			path:  plugin.Path,
			meta: inventoryCounts(
				countIf(len(plugin.Extensions), "extension"),
				countIf(len(plugin.Skills), "skill"),
			),
		}
		if node.meta == "" {
			node.meta = "Empty"
		}
		for _, ext := range plugin.Extensions {
			node.children = append(node.children, inventoryExtensionNode(id, ext, false))
		}
		for _, skill := range plugin.Skills {
			node.children = append(node.children, inventorySkillNode(id, skill, false))
		}
		plugins.children = append(plugins.children, node)
	}
	if len(inventory.Plugins) > 0 {
		plugins.meta = inventoryPlural(len(inventory.Plugins), "plugin")
	} else {
		plugins.children = []*inventoryNode{{id: plugins.id + "/detail:empty", kind: inventoryDetail, label: "No plugins", description: "kodelet plugin add owner/repo"}}
	}

	standalone := &inventoryNode{
		id:          inventoryStandaloneSection,
		kind:        inventorySection,
		label:       "Standalone",
		description: "Extensions and skills installed outside a plugin.",
		meta: inventoryCounts(
			countIf(len(inventory.Extensions), "extension"),
			countIf(len(inventory.Skills), "skill"),
		),
	}
	for _, ext := range inventory.Extensions {
		standalone.children = append(standalone.children, inventoryExtensionNode(standalone.id, ext, true))
	}
	for _, skill := range inventory.Skills {
		standalone.children = append(standalone.children, inventorySkillNode(standalone.id, skill, true))
	}
	if standalone.meta == "" {
		standalone.meta = "None"
		standalone.children = []*inventoryNode{{id: standalone.id + "/detail:empty", kind: inventoryDetail, label: "Nothing here", description: "Add extensions or skills under .kodelet/ or ~/.kodelet/"}}
	}
	return []*inventoryNode{builtin, plugins, standalone}
}

func (s *aboutToolsState) setInventory(inventory protocol.WorkspaceInventoryResult) {
	s.tree = buildInventoryTree(inventory)
	s.tools, s.extensions, s.skills, s.failed = 0, 0, 0, 0
	for _, tool := range inventory.BuiltinTools {
		if tool.Enabled {
			s.tools++
		}
	}
	countExtensions := func(items []protocol.InventoryExtension) {
		for _, ext := range items {
			s.extensions++
			if ext.Status == protocol.InventoryStatusFailed {
				s.failed++
				continue
			}
			for _, tool := range ext.Tools {
				if tool.Enabled {
					s.tools++
				}
			}
		}
	}
	countSkills := func(items []protocol.InventorySkill) {
		for _, skill := range items {
			if skill.Enabled {
				s.skills++
			}
		}
	}
	countExtensions(inventory.Extensions)
	countSkills(inventory.Skills)
	for _, plugin := range inventory.Plugins {
		countExtensions(plugin.Extensions)
		countSkills(plugin.Skills)
	}
	s.expanded = map[string]bool{inventoryBuiltinSection: true, inventoryPluginsSection: true, inventoryStandaloneSection: true}
	s.filterExpanded = map[string]bool{}
	s.query, s.filtering, s.offset = "", false, 0
	s.cursorID = s.tree[0].id
}

func inventoryMatches(node *inventoryNode, query string) bool {
	return query != "" && node.kind != inventorySection &&
		(strings.Contains(strings.ToLower(node.label), query) || strings.Contains(strings.ToLower(node.description), query))
}

// filterInventory keeps matches with their whole subtree and the ancestors of
// matches with only their matching branches. Every ancestor of a match is
// expanded, including matching containers, so nested matches stay visible.
func filterInventory(nodes []*inventoryNode, query string) ([]*inventoryNode, map[string]bool) {
	expanded := map[string]bool{}
	var visit func([]*inventoryNode) []*inventoryNode
	visit = func(items []*inventoryNode) []*inventoryNode {
		var kept []*inventoryNode
		for _, node := range items {
			children := visit(node.children)
			matched := inventoryMatches(node, query)
			if !matched && len(children) == 0 {
				continue
			}
			if len(children) > 0 {
				expanded[node.id] = true
			}
			copied := *node
			if !matched {
				copied.children = children
			}
			kept = append(kept, &copied)
		}
		return kept
	}
	return visit(nodes), expanded
}

func flattenInventory(nodes []*inventoryNode, expanded map[string]bool) []inventoryRow {
	var rows []inventoryRow
	var visit func([]*inventoryNode, int, string)
	visit = func(items []*inventoryNode, level int, parentID string) {
		for _, node := range items {
			expandable := len(node.children) > 0
			isExpanded := expandable && expanded[node.id]
			rows = append(rows, inventoryRow{node: node, level: level, parentID: parentID, expandable: expandable, expanded: isExpanded})
			if isExpanded {
				visit(node.children, level+1, node.id)
			}
		}
	}
	visit(nodes, 1, "")
	return rows
}

func (s *aboutToolsState) normalizedQuery() string {
	return strings.ToLower(strings.TrimSpace(s.query))
}

func (s *aboutToolsState) rows() []inventoryRow {
	query := s.normalizedQuery()
	if query == "" {
		return flattenInventory(s.tree, s.expanded)
	}
	nodes, _ := filterInventory(s.tree, query)
	return flattenInventory(nodes, s.filterExpanded)
}

func (s *aboutToolsState) cursorIndex(rows []inventoryRow) int {
	for i, row := range rows {
		if row.node.id == s.cursorID {
			return i
		}
	}
	return 0
}

func (s *aboutToolsState) setQuery(query string) {
	s.query, s.offset = query, 0
	normalized := s.normalizedQuery()
	_, s.filterExpanded = filterInventory(s.tree, normalized)
	rows := s.rows()
	s.cursorID = ""
	for _, row := range rows {
		if inventoryMatches(row.node, normalized) {
			s.cursorID = row.node.id
			break
		}
	}
	if s.cursorID == "" && len(rows) > 0 {
		s.cursorID = rows[0].node.id
	}
}

// clearFilter drops the query but keeps the selected row, opening its
// ancestors because the filter may have been what expanded them.
func (s *aboutToolsState) clearFilter() {
	cursor := s.cursorID
	s.filtering = false
	s.setQuery("")
	s.cursorID = cursor
	var reveal func([]*inventoryNode, []string) bool
	reveal = func(nodes []*inventoryNode, ancestors []string) bool {
		for _, node := range nodes {
			if node.id == cursor {
				for _, id := range ancestors {
					s.expanded[id] = true
				}
				return true
			}
			if reveal(node.children, append(ancestors[:len(ancestors):len(ancestors)], node.id)) {
				return true
			}
		}
		return false
	}
	reveal(s.tree, nil)
}

func (s *aboutToolsState) setExpanded(id string, open bool) {
	target := s.expanded
	if s.normalizedQuery() != "" {
		target = s.filterExpanded
	}
	if open {
		target[id] = true
	} else {
		delete(target, id)
	}
}

// keepCursorVisible clamps the cursor to the visible rows and scrolls them.
func (s *aboutToolsState) keepCursorVisible(height int) {
	rows := s.rows()
	if len(rows) == 0 {
		s.offset = 0
		return
	}
	index := s.cursorIndex(rows)
	s.cursorID = rows[index].node.id
	height = max(1, height)
	s.offset = max(0, min(s.offset, len(rows)-height))
	if index < s.offset {
		s.offset = index
	}
	if index >= s.offset+height {
		s.offset = index - height + 1
	}
}

func (m *model) loadToolInventory() tea.Cmd {
	if m.aboutDialog == nil {
		return nil
	}
	m.nextAboutRequestID++
	state := &aboutToolsState{requestID: m.nextAboutRequestID}
	m.aboutDialog.tools = state
	provider, ok := m.runner.(toolInventoryProvider)
	if !ok {
		state.unsupported = true
		return nil
	}
	state.loading = true
	// List the same workspace slash commands come from, including a draft's directory.
	target := m.workspaceTargetForState(m.conversationState)
	parent, requestID := m.ctx, state.requestID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		inventory, err := provider.ToolInventory(ctx, target)
		return toolInventoryMsg{requestID: requestID, inventory: inventory, err: err}
	}
}

func (m *model) applyToolInventory(msg toolInventoryMsg) {
	if m.aboutDialog == nil || m.aboutDialog.tools == nil || m.aboutDialog.tools.requestID != msg.requestID {
		return
	}
	state := m.aboutDialog.tools
	state.loading, state.err = false, msg.err
	if msg.err == nil {
		state.setInventory(msg.inventory)
	}
}

// updateAboutToolsKey handles keys on the tools tab. Unhandled keys fall back
// to the dialog's tab switching and dismissal.
func (m *model) updateAboutToolsKey(key string) (tea.Cmd, bool) {
	state := m.aboutDialog.tools
	if state == nil {
		return nil, false
	}
	if state.err != nil && (key == "r" || key == "R") {
		return m.loadToolInventory(), true
	}
	if state.tree == nil {
		return nil, false
	}
	height := m.aboutToolsLayout().list
	defer state.keepCursorVisible(height)

	if state.filtering {
		switch key {
		case "esc":
			state.clearFilter()
		case "enter", "down":
			state.filtering = false
		case "backspace":
			if state.query != "" {
				_, size := utf8.DecodeLastRuneInString(state.query)
				state.setQuery(state.query[:len(state.query)-size])
			}
		case "space":
			state.setQuery(state.query + " ")
		case "ctrl+c", "ctrl+d", "tab", "shift+tab":
			state.filtering = false
			return nil, false
		default:
			if utf8.RuneCountInString(key) == 1 {
				state.setQuery(state.query + key)
			}
		}
		return nil, true
	}

	rows := state.rows()
	if len(rows) == 0 {
		switch key {
		case "/":
			state.filtering = true
			return nil, true
		case "esc":
			state.setQuery("")
			return nil, true
		}
		return nil, false
	}
	index := state.cursorIndex(rows)
	row := rows[index]
	moveTo := func(target int) {
		state.cursorID = rows[max(0, min(len(rows)-1, target))].node.id
	}
	switch key {
	case "up", "k":
		moveTo(index - 1)
	case "down", "j":
		moveTo(index + 1)
	case "pgup":
		moveTo(index - height)
	case "pgdown":
		moveTo(index + height)
	case "home", "g":
		moveTo(0)
	case "end", "G":
		moveTo(len(rows) - 1)
	case "right", "l":
		if row.expandable && !row.expanded {
			state.setExpanded(row.node.id, true)
		} else if row.expanded {
			moveTo(index + 1)
		}
	case "left", "h":
		if row.expanded {
			state.setExpanded(row.node.id, false)
		} else if row.parentID != "" {
			for i, candidate := range rows {
				if candidate.node.id == row.parentID {
					moveTo(i)
				}
			}
		}
	case "enter", "space":
		if row.expandable {
			state.setExpanded(row.node.id, !row.expanded)
		}
	case "/":
		state.filtering = true
	case "esc":
		if state.query == "" {
			return nil, false
		}
		state.clearFilter()
	default:
		return nil, false
	}
	return nil, true
}

type aboutToolsLayout struct {
	list    int
	top     bool
	summary bool
	filter  bool
	blank   bool
	details bool
}

// aboutToolsLayout fits the tab into the dialog body, dropping the details
// footer first, then the summary, so the list keeps at least one row.
func (m model) aboutToolsLayout() aboutToolsLayout {
	height := max(1, m.height-5)
	alert := 0
	if m.aboutDialog != nil && m.aboutDialog.tools != nil && m.aboutDialog.tools.failed > 0 {
		alert = 1
	}
	full := 1 + alert + 1 + 1 + 1 + inventoryDetailLines
	if height-full-1 >= 8 {
		// Separate the summary from the tab line when the list keeps room.
		return aboutToolsLayout{list: height - full - 1, top: true, summary: true, filter: true, blank: true, details: true}
	}
	if height-full >= 4 {
		return aboutToolsLayout{list: height - full, summary: true, filter: true, blank: true, details: true}
	}
	compact := 1 + alert + 1 + 1
	if height-compact >= 2 {
		return aboutToolsLayout{list: height - compact, summary: true, filter: true, blank: true}
	}
	if height >= 2 {
		return aboutToolsLayout{list: height - 1, filter: true}
	}
	return aboutToolsLayout{list: 1}
}

type styledText struct {
	text  string
	style lipgloss.Style
}

func styledWidth(segments []styledText) int {
	width := 0
	for _, segment := range segments {
		width += lipgloss.Width(segment.text)
	}
	return width
}

// renderStyledLine fits segments to exactly width cells, truncating the last
// visible segment with an ellipsis and painting background behind every cell.
func renderStyledLine(segments []styledText, width int, background color.Color) string {
	var b strings.Builder
	used := 0
	for _, segment := range segments {
		remaining := width - used
		if remaining <= 0 {
			break
		}
		text := segment.text
		if lipgloss.Width(text) > remaining {
			text = fitVisiblePrefix(text, remaining)
		}
		style := segment.style
		if background != nil {
			style = style.Background(background)
		}
		b.WriteString(renderPersistentStyle(style, text))
		used += lipgloss.Width(text)
	}
	if used < width {
		pad := lipgloss.NewStyle()
		if background != nil {
			pad = pad.Background(background)
		}
		b.WriteString(renderPersistentStyle(pad, strings.Repeat(" ", width-used)))
	}
	return b.String()
}

func inventoryHighlight(text, query string, style lipgloss.Style) []styledText {
	index := -1
	if query != "" {
		index = strings.Index(strings.ToLower(text), query)
	}
	if index < 0 || len(strings.ToLower(text)) != len(text) {
		return []styledText{{text: text, style: style}}
	}
	return []styledText{
		{text: text[:index], style: style},
		{text: text[index : index+len(query)], style: historySearchQueryStyle.Bold(true)},
		{text: text[index+len(query):], style: style},
	}
}

func inventoryLabelStyle(node *inventoryNode) lipgloss.Style {
	switch {
	case node.off:
		return uiDialogMutedStyle
	case node.failed && node.kind != inventoryDetail:
		return slashCommandErrorStyle
	}
	switch node.kind {
	case inventorySection:
		return uiDialogButtonStyle.Bold(true)
	case inventoryPlugin:
		return uiDialogBodyStyle.Bold(true)
	case inventoryExtension:
		return toolHeaderStyle
	case inventorySkill:
		return thoughtHeaderStyle
	case inventoryDetail:
		return uiDialogMutedStyle
	default:
		return uiDialogBodyStyle
	}
}

func renderInventoryRow(row inventoryRow, query string, width int, selected bool) string {
	node := row.node
	chevron := "  "
	if row.expandable {
		chevron = "▸ "
		if row.expanded {
			chevron = "▾ "
		}
	}
	prefix := strings.Repeat("  ", row.level-1) + chevron
	left := append([]styledText{{text: prefix, style: uiDialogMutedStyle}}, inventoryHighlight(node.label, query, inventoryLabelStyle(node))...)
	if node.version != "" {
		left = append(left, styledText{text: " v" + node.version, style: uiDialogMutedStyle})
	}
	if node.scope != "" {
		left = append(left, styledText{text: " " + inventoryScopeLabel(node.scope), style: uiDialogMutedStyle})
	}
	if node.off {
		left = append(left, styledText{text: " off", style: uiDialogMutedStyle})
	}
	if node.failed && node.kind != inventoryDetail {
		left = append(left, styledText{text: " failed", style: slashCommandErrorStyle})
	}
	var background color.Color
	if selected {
		background = uiDialogSelectedStyle.GetBackground()
	}

	leaf := node.kind == inventoryTool || node.kind == inventorySkill
	// Descriptions live in the details footer unless they explain a filter match.
	if node.kind == inventoryDetail || (leaf && query != "" && strings.Contains(strings.ToLower(node.description), query)) {
		column := max(styledWidth(left)+2, lipgloss.Width(prefix)+21)
		style := uiDialogMutedStyle
		if node.kind == inventoryDetail {
			style = uiDialogBodyStyle
			if node.failed {
				style = slashCommandErrorStyle
			}
		}
		segments := append(left, styledText{text: strings.Repeat(" ", column-styledWidth(left))})
		segments = append(segments, inventoryHighlight(node.description, query, style)...)
		return renderStyledLine(segments, width, background)
	}
	if leaf || node.meta == "" {
		return renderStyledLine(left, width, background)
	}
	meta := styledText{text: node.meta, style: uiDialogMutedStyle}
	if node.failed {
		meta.style = slashCommandErrorStyle
	}
	metaWidth := lipgloss.Width(node.meta)
	leftWidth := min(styledWidth(left), width-metaWidth-2)
	if leftWidth < 8 {
		return renderStyledLine(left, width, background)
	}
	right := []styledText{{text: strings.Repeat(" ", width-leftWidth-metaWidth)}, meta}
	return renderStyledLine(left, leftWidth, background) + renderStyledLine(right, width-leftWidth, background)
}

func inventoryScopeLabel(scope string) string {
	if scope == protocol.InventoryScopePath {
		return "PATH"
	}
	return scope
}

func inventoryDetailFooter(node *inventoryNode, width int) []string {
	lines := make([]string, inventoryDetailLines)
	if node == nil {
		return lines
	}
	var facts []string
	if node.kind != inventoryDetail {
		facts = append(facts, node.kind.label())
	}
	if node.scope != "" {
		facts = append(facts, inventoryScopeLabel(node.scope))
	}
	if node.version != "" {
		facts = append(facts, "v"+node.version)
	}
	header := []styledText{{text: node.label, style: uiDialogBodyStyle.Bold(true)}}
	if len(facts) > 0 {
		header = append(header, styledText{text: " · " + strings.Join(facts, " · "), style: uiDialogMutedStyle})
	}
	lines[0] = renderStyledLine(header, width, nil)

	body, bodyStyle := node.description, uiDialogBodyStyle
	if body == "" {
		body = node.meta
	}
	for _, child := range node.children {
		if child.kind == inventoryDetail && child.failed {
			// A failed extension's error is the most useful thing to show.
			body = child.description
		}
	}
	if node.failed {
		bodyStyle = slashCommandErrorStyle
	}
	wrapped := strings.Split(wrapText(body, width), "\n")
	if len(wrapped) > 2 {
		wrapped[1] = strings.Join(wrapped[1:], " ")
	}
	for i := 0; i < 2 && i < len(wrapped); i++ {
		lines[1+i] = renderStyledLine([]styledText{{text: wrapped[i], style: bodyStyle}}, width, nil)
	}
	lines[3] = renderPersistentStyle(uiDialogMutedStyle, fitVisible(node.path, width))
	return lines
}

// aboutToolsContent renders the tools tab body and footer for the dialog.
func (m model) aboutToolsContent() ([]string, string) {
	state := m.aboutDialog.tools
	width := max(1, m.uiDialogWidth()-4)
	muted := func(text string) []string {
		var lines []string
		for _, line := range strings.Split(wrapText(text, width), "\n") {
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible(line, width)))
		}
		return lines
	}
	closeFooter := "Press Esc, Enter, or q to close."
	switch {
	case state == nil || state.unsupported:
		return muted("This connection does not list tools."), closeFooter
	case state.loading:
		return muted("Loading tools…"), closeFooter
	case state.err != nil:
		return muted("Could not load tools: " + state.err.Error()), "Press r to try again, or Esc to close."
	case state.tree == nil:
		return muted("Loading tools…"), closeFooter
	}

	layout := m.aboutToolsLayout()
	var lines []string
	if layout.top {
		lines = append(lines, "")
	}
	if layout.summary {
		count := func(value int, singular string, last bool) []styledText {
			segments := []styledText{
				{text: strconv.Itoa(value), style: uiDialogBodyStyle},
				{text: strings.TrimPrefix(inventoryPlural(value, singular), strconv.Itoa(value)), style: uiDialogMutedStyle},
			}
			if !last {
				segments = append(segments, styledText{text: " · ", style: uiDialogMutedStyle})
			}
			return segments
		}
		summary := append(count(state.tools, "tool", false), count(state.extensions, "extension", false)...)
		summary = append(summary, count(state.skills, "skill", true)...)
		lines = append(lines, renderStyledLine(summary, width, nil))
		if state.failed > 0 {
			lines = append(lines, renderStyledLine([]styledText{{text: "! " + inventoryPlural(state.failed, "extension") + " failed to load", style: slashCommandErrorStyle}}, width, nil))
		}
	}
	if layout.filter {
		var filter []styledText
		switch {
		case state.filtering:
			filter = []styledText{{text: "/ ", style: uiDialogButtonStyle}, {text: state.query, style: uiDialogBodyStyle}, {text: "█", style: uiDialogBodyStyle}}
		case state.query != "":
			filter = []styledText{{text: "/ ", style: uiDialogMutedStyle}, {text: state.query, style: historySearchQueryStyle}, {text: "  Esc clear", style: uiDialogMutedStyle}}
		default:
			filter = []styledText{{text: "/ Filter tools, plugins, and skills", style: uiDialogMutedStyle}}
		}
		lines = append(lines, renderStyledLine(filter, width, nil))
	}
	if layout.blank {
		lines = append(lines, "")
	}

	rows := state.rows()
	query := state.normalizedQuery()
	offset := max(0, min(state.offset, len(rows)-layout.list))
	for i := 0; i < layout.list; i++ {
		index := offset + i
		switch {
		case index < len(rows):
			row := rows[index]
			lines = append(lines, renderInventoryRow(row, query, width, row.node.id == state.cursorID && !state.filtering))
		case i == 0 && len(rows) == 0:
			lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisiblePrefix("Nothing matches “"+strings.TrimSpace(state.query)+"”.", width)))
		default:
			lines = append(lines, "")
		}
	}

	if layout.details {
		label := "no matches"
		if len(rows) > 0 {
			label = strconv.Itoa(offset+1) + "–" + strconv.Itoa(min(len(rows), offset+layout.list)) + " of " + strconv.Itoa(len(rows))
		}
		rule := strings.Repeat("─", max(0, width-lipgloss.Width(label)-1)) + " " + label
		lines = append(lines, renderPersistentStyle(uiDialogMutedStyle, fitVisible(rule, width)))
		var cursor *inventoryNode
		for _, row := range rows {
			if row.node.id == state.cursorID {
				cursor = row.node
			}
		}
		lines = append(lines, inventoryDetailFooter(cursor, width)...)
	}

	footer := "↑/↓ move · ←/→ fold · / filter · Esc close"
	switch {
	case state.filtering:
		footer = "Type to filter · Enter done · Esc clear"
	case state.query != "":
		footer = "↑/↓ move · ←/→ fold · / filter · Esc clear"
	}
	return lines, footer
}
