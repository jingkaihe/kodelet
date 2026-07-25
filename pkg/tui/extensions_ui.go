package tui

import (
	"context"
	"sort"
	"strings"
	"sync"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/pkg/errors"
)

const maxExtensionWidgetLines = 10

type extensionUIKey struct {
	owner extensions.UIExtensionOwner
	id    string
}

type tuiExtensionWidget struct {
	key       extensionUIKey
	placement string
	frame     extensions.UIFrame
}

type extensionSurfaceLayout struct {
	x      int
	y      int
	width  int
	height int
}

type tuiExtensionSurface struct {
	key           extensionUIKey
	source        extensions.UIExtensionSource
	options       extensions.UISurfaceOptions
	frame         extensions.UIFrame
	layout        extensionSurfaceLayout
	openOrdinal   uint64
	eventSequence uint64
}

type pendingExtensionWidget struct {
	widget tuiExtensionWidget
	remove bool
}

type pendingExtensionSurface struct {
	surface tuiExtensionSurface
	opened  bool
	remove  bool
}

type extensionUIBatch struct {
	cleanups []extensions.UIExtensionOwner
	widgets  []pendingExtensionWidget
	surfaces []pendingExtensionSurface
}

type extensionUIFlushMsg struct{}

type extensionUITransportErrorMsg struct {
	owner extensions.UIExtensionOwner
}

type extensionUITranscriptMsg struct {
	owner   extensions.UIExtensionOwner
	title   string
	message string
}

type tuiExtensionUIHost struct {
	mu                 sync.Mutex
	surfaceLifecycleMu sync.Mutex

	ch   chan<- tea.Msg
	done <-chan struct{}

	flushQueued bool
	closed      map[extensions.UIExtensionOwner]struct{}
	cleanups    map[extensions.UIExtensionOwner]struct{}
	widgets     map[extensionUIKey]tuiExtensionWidget
	surfaces    map[extensionUIKey]tuiExtensionSurface
	pendingW    map[extensionUIKey]pendingExtensionWidget
	pendingS    map[extensionUIKey]pendingExtensionSurface
	widgetSeq   map[extensionUIKey]uint64
	surfaceSeq  map[extensionUIKey]uint64
	openOrdinal uint64
}

func newTUIExtensionUIHost(ch chan<- tea.Msg, done <-chan struct{}) *tuiExtensionUIHost {
	return &tuiExtensionUIHost{
		ch:         ch,
		done:       done,
		closed:     map[extensions.UIExtensionOwner]struct{}{},
		cleanups:   map[extensions.UIExtensionOwner]struct{}{},
		widgets:    map[extensionUIKey]tuiExtensionWidget{},
		surfaces:   map[extensionUIKey]tuiExtensionSurface{},
		pendingW:   map[extensionUIKey]pendingExtensionWidget{},
		pendingS:   map[extensionUIKey]pendingExtensionSurface{},
		widgetSeq:  map[extensionUIKey]uint64{},
		surfaceSeq: map[extensionUIKey]uint64{},
	}
}

func (h *tuiExtensionUIHost) AppendTranscript(ctx context.Context, source extensions.UIExtensionSource, request extensions.UITranscriptAppendRequest) (extensions.UITranscriptAppendResponse, error) {
	if source == nil {
		return extensions.UITranscriptAppendResponse{}, errors.New("extension UI source is required")
	}
	owner := source.ExtensionUIOwner()
	if strings.TrimSpace(owner.ExtensionID) == "" || owner.Generation == 0 {
		return extensions.UITranscriptAppendResponse{}, errors.New("extension UI owner is invalid")
	}
	title := sanitizeExtensionTranscriptText(request.Title)
	message := sanitizeExtensionTranscriptText(request.Message)
	if strings.TrimSpace(title) == "" && strings.TrimSpace(message) == "" {
		return extensions.UITranscriptAppendResponse{}, errors.New("extension transcript entry is empty")
	}

	h.mu.Lock()
	if _, closed := h.closed[owner]; closed {
		h.mu.Unlock()
		return extensions.UITranscriptAppendResponse{Reason: "extension process generation is closed"}, nil
	}
	h.mu.Unlock()

	select {
	case <-ctx.Done():
		return extensions.UITranscriptAppendResponse{}, ctx.Err()
	case <-h.done:
		return extensions.UITranscriptAppendResponse{Reason: "extension UI host is closed"}, nil
	case h.ch <- extensionUITranscriptMsg{owner: owner, title: title, message: message}:
		return extensions.UITranscriptAppendResponse{Accepted: true}, nil
	}
}

func (h *tuiExtensionUIHost) SetWidget(_ context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetSetRequest) (extensions.UIFrameResponse, error) {
	key, err := extensionUIRequestKey(source, request.ID)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	placement, err := extensions.NormalizeWidgetPlacement(request.Placement)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := extensions.ValidateUISequence(request.Frame.Sequence); err != nil {
		return extensions.UIFrameResponse{}, err
	}

	h.mu.Lock()
	latest := h.widgetSeq[key]
	if _, closed := h.closed[key.owner]; closed {
		h.mu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "extension process is closed"}, nil
	}
	if request.Frame.Sequence <= latest {
		h.mu.Unlock()
		return staleUIFrameResponse(latest), nil
	}
	widget := tuiExtensionWidget{key: key, placement: placement, frame: request.Frame}
	h.widgetSeq[key] = request.Frame.Sequence
	h.widgets[key] = widget
	h.pendingW[key] = pendingExtensionWidget{widget: widget}
	queue := h.queueFlushLocked()
	h.mu.Unlock()
	if queue {
		if err := h.enqueueFlush(); err != nil {
			return extensions.UIFrameResponse{}, err
		}
	}
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (h *tuiExtensionUIHost) UpdateWidget(_ context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetFrameRequest) (extensions.UIFrameResponse, error) {
	key, err := extensionUIRequestKey(source, request.ID)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := extensions.ValidateUISequence(request.Frame.Sequence); err != nil {
		return extensions.UIFrameResponse{}, err
	}

	h.mu.Lock()
	latest := h.widgetSeq[key]
	if _, closed := h.closed[key.owner]; closed {
		h.mu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "extension process is closed"}, nil
	}
	widget, exists := h.widgets[key]
	if !exists {
		h.mu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "widget is not open"}, nil
	}
	if request.Frame.Sequence <= latest {
		h.mu.Unlock()
		return staleUIFrameResponse(latest), nil
	}
	widget.frame = request.Frame
	h.widgetSeq[key] = request.Frame.Sequence
	h.widgets[key] = widget
	h.pendingW[key] = pendingExtensionWidget{widget: widget}
	queue := h.queueFlushLocked()
	h.mu.Unlock()
	if queue {
		if err := h.enqueueFlush(); err != nil {
			return extensions.UIFrameResponse{}, err
		}
	}
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (h *tuiExtensionUIHost) RemoveWidget(_ context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetRemoveRequest) (extensions.UIFrameResponse, error) {
	key, err := extensionUIRequestKey(source, request.ID)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := extensions.ValidateUISequence(request.Sequence); err != nil {
		return extensions.UIFrameResponse{}, err
	}

	h.mu.Lock()
	latest := h.widgetSeq[key]
	if request.Sequence <= latest {
		h.mu.Unlock()
		return staleUIFrameResponse(latest), nil
	}
	h.widgetSeq[key] = request.Sequence
	delete(h.widgets, key)
	h.pendingW[key] = pendingExtensionWidget{widget: tuiExtensionWidget{key: key}, remove: true}
	queue := h.queueFlushLocked()
	h.mu.Unlock()
	if queue {
		if err := h.enqueueFlush(); err != nil {
			return extensions.UIFrameResponse{}, err
		}
	}
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Sequence}, nil
}

func (h *tuiExtensionUIHost) OpenSurface(_ context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceOpenRequest) (extensions.UIFrameResponse, error) {
	key, err := extensionUIRequestKey(source, request.ID)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := extensions.ValidateUISequence(request.Frame.Sequence); err != nil {
		return extensions.UIFrameResponse{}, err
	}
	anchor, err := extensions.NormalizeSurfaceAnchor(request.Options.Anchor)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	request.Options.Anchor = anchor

	h.surfaceLifecycleMu.Lock()
	h.mu.Lock()
	latest := h.surfaceSeq[key]
	if _, closed := h.closed[key.owner]; closed {
		h.mu.Unlock()
		h.surfaceLifecycleMu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "extension process is closed"}, nil
	}
	if request.Frame.Sequence <= latest {
		h.mu.Unlock()
		h.surfaceLifecycleMu.Unlock()
		return staleUIFrameResponse(latest), nil
	}
	h.openOrdinal++
	surface := tuiExtensionSurface{key: key, source: source, options: request.Options, frame: request.Frame, openOrdinal: h.openOrdinal}
	h.surfaces[key] = surface
	h.mu.Unlock()

	extensions.PrepareUISurfaceEventLifecycle(source, request.ID, surface.openOrdinal)

	h.mu.Lock()
	if _, closed := h.closed[key.owner]; closed {
		latest = h.surfaceSeq[key]
		h.mu.Unlock()
		h.surfaceLifecycleMu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "extension process is closed"}, nil
	}
	h.surfaceSeq[key] = request.Frame.Sequence
	h.pendingS[key] = pendingExtensionSurface{surface: surface, opened: true}
	queue := h.queueFlushLocked()
	h.mu.Unlock()
	h.surfaceLifecycleMu.Unlock()
	if queue {
		if err := h.enqueueFlush(); err != nil {
			return extensions.UIFrameResponse{}, err
		}
	}
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (h *tuiExtensionUIHost) UpdateSurface(_ context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceFrameRequest) (extensions.UIFrameResponse, error) {
	key, err := extensionUIRequestKey(source, request.ID)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := extensions.ValidateUISequence(request.Frame.Sequence); err != nil {
		return extensions.UIFrameResponse{}, err
	}

	h.surfaceLifecycleMu.Lock()
	h.mu.Lock()
	latest := h.surfaceSeq[key]
	if _, closed := h.closed[key.owner]; closed {
		h.mu.Unlock()
		h.surfaceLifecycleMu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "extension process is closed"}, nil
	}
	surface, exists := h.surfaces[key]
	if !exists {
		h.mu.Unlock()
		h.surfaceLifecycleMu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "surface is not open"}, nil
	}
	if request.Frame.Sequence <= latest {
		h.mu.Unlock()
		h.surfaceLifecycleMu.Unlock()
		return staleUIFrameResponse(latest), nil
	}
	surface.frame = request.Frame
	h.surfaceSeq[key] = request.Frame.Sequence
	h.surfaces[key] = surface
	pending := h.pendingS[key]
	pending.surface = surface
	h.pendingS[key] = pending
	queue := h.queueFlushLocked()
	h.mu.Unlock()
	h.surfaceLifecycleMu.Unlock()
	if queue {
		if err := h.enqueueFlush(); err != nil {
			return extensions.UIFrameResponse{}, err
		}
	}
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (h *tuiExtensionUIHost) CloseSurface(_ context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceCloseRequest) (extensions.UIFrameResponse, error) {
	key, err := extensionUIRequestKey(source, request.ID)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := extensions.ValidateUISequence(request.Sequence); err != nil {
		return extensions.UIFrameResponse{}, err
	}

	h.surfaceLifecycleMu.Lock()
	h.mu.Lock()
	latest := h.surfaceSeq[key]
	if request.Sequence <= latest {
		h.mu.Unlock()
		h.surfaceLifecycleMu.Unlock()
		return staleUIFrameResponse(latest), nil
	}
	h.openOrdinal++
	delete(h.surfaces, key)
	lifecycle := h.openOrdinal
	h.mu.Unlock()

	extensions.PrepareUISurfaceEventLifecycle(source, request.ID, lifecycle)

	h.mu.Lock()
	if _, closed := h.closed[key.owner]; closed {
		latest = h.surfaceSeq[key]
		h.mu.Unlock()
		h.surfaceLifecycleMu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "extension process is closed"}, nil
	}
	h.surfaceSeq[key] = request.Sequence
	h.pendingS[key] = pendingExtensionSurface{surface: tuiExtensionSurface{key: key}, remove: true}
	queue := h.queueFlushLocked()
	h.mu.Unlock()
	h.surfaceLifecycleMu.Unlock()
	if queue {
		if err := h.enqueueFlush(); err != nil {
			return extensions.UIFrameResponse{}, err
		}
	}
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Sequence}, nil
}

func (h *tuiExtensionUIHost) CleanupExtensionUI(owner extensions.UIExtensionOwner) {
	if h == nil || owner.Generation == 0 {
		return
	}
	h.mu.Lock()
	if _, exists := h.closed[owner]; exists {
		h.mu.Unlock()
		return
	}
	h.closed[owner] = struct{}{}
	h.cleanups[owner] = struct{}{}
	for key := range h.widgets {
		if key.owner == owner {
			delete(h.widgets, key)
			delete(h.pendingW, key)
		}
	}
	for key := range h.surfaces {
		if key.owner == owner {
			delete(h.surfaces, key)
			delete(h.pendingS, key)
		}
	}
	queue := h.queueFlushLocked()
	h.mu.Unlock()
	if queue {
		_ = h.enqueueFlush()
	}
}

func (h *tuiExtensionUIHost) queueFlushLocked() bool {
	if h.flushQueued {
		return false
	}
	h.flushQueued = true
	return true
}

func (h *tuiExtensionUIHost) enqueueFlush() error {
	if h == nil || h.ch == nil {
		return errors.New("TUI extension UI is not available")
	}
	msg := extensionUIFlushMsg{}
	select {
	case h.ch <- msg:
		return nil
	case <-h.done:
		return context.Canceled
	}
}

func (h *tuiExtensionUIHost) drain() extensionUIBatch {
	h.mu.Lock()
	defer h.mu.Unlock()

	batch := extensionUIBatch{
		cleanups: make([]extensions.UIExtensionOwner, 0, len(h.cleanups)),
		widgets:  make([]pendingExtensionWidget, 0, len(h.pendingW)),
		surfaces: make([]pendingExtensionSurface, 0, len(h.pendingS)),
	}
	for owner := range h.cleanups {
		batch.cleanups = append(batch.cleanups, owner)
	}
	for _, widget := range h.pendingW {
		batch.widgets = append(batch.widgets, widget)
	}
	for _, surface := range h.pendingS {
		batch.surfaces = append(batch.surfaces, surface)
	}
	clear(h.cleanups)
	clear(h.pendingW)
	clear(h.pendingS)
	h.flushQueued = false

	sort.Slice(batch.cleanups, func(i, j int) bool {
		return extensionUIOwnerLess(batch.cleanups[i], batch.cleanups[j])
	})
	sort.Slice(batch.widgets, func(i, j int) bool {
		return extensionUIKeyLess(batch.widgets[i].widget.key, batch.widgets[j].widget.key)
	})
	sort.Slice(batch.surfaces, func(i, j int) bool {
		if batch.surfaces[i].opened != batch.surfaces[j].opened {
			return !batch.surfaces[i].opened
		}
		if batch.surfaces[i].opened && batch.surfaces[i].surface.openOrdinal != batch.surfaces[j].surface.openOrdinal {
			return batch.surfaces[i].surface.openOrdinal < batch.surfaces[j].surface.openOrdinal
		}
		return extensionUIKeyLess(batch.surfaces[i].surface.key, batch.surfaces[j].surface.key)
	})
	return batch
}

func extensionUIRequestKey(source extensions.UIExtensionSource, id string) (extensionUIKey, error) {
	if source == nil {
		return extensionUIKey{}, errors.New("extension UI source is required")
	}
	if err := extensions.ValidateUIObjectID(id); err != nil {
		return extensionUIKey{}, err
	}
	owner := source.ExtensionUIOwner()
	if strings.TrimSpace(owner.ExtensionID) == "" || owner.Generation == 0 {
		return extensionUIKey{}, errors.New("extension UI source is not running")
	}
	return extensionUIKey{owner: owner, id: id}, nil
}

func staleUIFrameResponse(latest uint64) extensions.UIFrameResponse {
	return extensions.UIFrameResponse{LatestSequence: latest, Reason: "stale sequence"}
}

func extensionUIOwnerLess(left, right extensions.UIExtensionOwner) bool {
	if left.ExtensionID != right.ExtensionID {
		return left.ExtensionID < right.ExtensionID
	}
	return left.Generation < right.Generation
}

func extensionUIKeyLess(left, right extensionUIKey) bool {
	if left.owner != right.owner {
		return extensionUIOwnerLess(left.owner, right.owner)
	}
	return left.id < right.id
}

func (m *model) applyExtensionUIBatch(batch extensionUIBatch) tea.Cmd {
	if m.extensionWidgets == nil {
		m.extensionWidgets = map[extensionUIKey]tuiExtensionWidget{}
	}
	if m.extensionSurfaces == nil {
		m.extensionSurfaces = map[extensionUIKey]tuiExtensionSurface{}
	}
	oldFocusKey, oldFocused := m.focusedExtensionSurfaceKey()
	var oldFocus tuiExtensionSurface
	if oldFocused {
		oldFocus = m.extensionSurfaces[oldFocusKey]
	}

	for _, owner := range batch.cleanups {
		for key := range m.extensionWidgets {
			if key.owner == owner {
				delete(m.extensionWidgets, key)
			}
		}
		for key := range m.extensionSurfaces {
			if key.owner == owner {
				delete(m.extensionSurfaces, key)
				m.removeExtensionSurfaceOrder(key)
			}
		}
	}
	for _, mutation := range batch.widgets {
		if mutation.remove {
			delete(m.extensionWidgets, mutation.widget.key)
			continue
		}
		m.extensionWidgets[mutation.widget.key] = mutation.widget
	}
	for _, mutation := range batch.surfaces {
		key := mutation.surface.key
		if mutation.remove {
			delete(m.extensionSurfaces, key)
			m.removeExtensionSurfaceOrder(key)
			continue
		}
		current, exists := m.extensionSurfaces[key]
		if exists && !mutation.opened {
			mutation.surface.eventSequence = current.eventSequence
			mutation.surface.layout = current.layout
		}
		m.extensionSurfaces[key] = mutation.surface
		if mutation.opened || !exists {
			m.removeExtensionSurfaceOrder(key)
			m.extensionSurfaceOrder = append(m.extensionSurfaceOrder, key)
		}
	}

	m.resize()
	cmds := m.updateExtensionSurfaceLayouts()
	newFocusKey, newFocused := m.focusedExtensionSurfaceKey()
	newFocus := tuiExtensionSurface{}
	if newFocused {
		newFocus = m.extensionSurfaces[newFocusKey]
	}
	focusChanged := oldFocused != newFocused || oldFocusKey != newFocusKey || (oldFocused && newFocused && oldFocus.openOrdinal != newFocus.openOrdinal)
	if focusChanged {
		if oldFocused {
			if current, stillOpen := m.extensionSurfaces[oldFocusKey]; stillOpen && current.openOrdinal == oldFocus.openOrdinal {
				cmds = append(cmds, m.nextExtensionSurfaceInputCmd(oldFocusKey, current, extensions.UISurfaceInputBlur, "", "", false, false, false, nil))
			}
		}
		if newFocused {
			surface := m.extensionSurfaces[newFocusKey]
			cmds = append(cmds, m.nextExtensionSurfaceInputCmd(newFocusKey, surface, extensions.UISurfaceInputFocus, "", "", false, false, false, nil))
		}
	}
	m.refreshViewport(false)
	return tea.Sequence(cmds...)
}

func (m *model) removeExtensionSurfaceOrder(key extensionUIKey) {
	for index, candidate := range m.extensionSurfaceOrder {
		if candidate != key {
			continue
		}
		m.extensionSurfaceOrder = append(m.extensionSurfaceOrder[:index], m.extensionSurfaceOrder[index+1:]...)
		return
	}
}

func (m model) focusedExtensionSurfaceKey() (extensionUIKey, bool) {
	for index := len(m.extensionSurfaceOrder) - 1; index >= 0; index-- {
		key := m.extensionSurfaceOrder[index]
		surface, ok := m.extensionSurfaces[key]
		if ok && !surface.options.NonCapturing {
			return key, true
		}
	}
	return extensionUIKey{}, false
}

func (m *model) routeExtensionSurfaceKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	key, ok := m.focusedExtensionSurfaceKey()
	if !ok {
		return nil, false
	}
	surface := m.extensionSurfaces[key]
	text := ""
	if msg.Type == tea.KeyRunes {
		text = string(msg.Runes)
	}
	keyName := msg.String()
	shift := strings.Contains(keyName, "shift+")
	ctrl := strings.Contains(keyName, "ctrl+")
	return m.nextExtensionSurfaceInputCmd(key, surface, extensions.UISurfaceInputKey, keyName, text, msg.Alt, shift, ctrl, nil), true
}

func (m *model) routeExtensionSurfaceMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	x := msg.X - tuiLeftMargin
	y := msg.Y
	for index := len(m.extensionSurfaceOrder) - 1; index >= 0; index-- {
		key := m.extensionSurfaceOrder[index]
		surface, ok := m.extensionSurfaces[key]
		if !ok || x < surface.layout.x || x >= surface.layout.x+surface.layout.width || y < surface.layout.y || y >= surface.layout.y+surface.layout.height {
			continue
		}
		mouse := &extensions.UISurfaceMouseEvent{
			X:      x - surface.layout.x,
			Y:      y - surface.layout.y,
			Button: extensionMouseButton(msg.Button),
			Action: extensionMouseAction(msg.Action),
			Shift:  msg.Shift,
			Alt:    msg.Alt,
			Ctrl:   msg.Ctrl,
		}
		return m.nextExtensionSurfaceInputCmd(key, surface, extensions.UISurfaceInputMouse, "", "", msg.Alt, msg.Shift, msg.Ctrl, mouse), true
	}
	return nil, false
}

func (m *model) nextExtensionSurfaceInputCmd(key extensionUIKey, surface tuiExtensionSurface, kind, keyName, text string, alt, shift, ctrl bool, mouse *extensions.UISurfaceMouseEvent) tea.Cmd {
	surface.eventSequence++
	m.extensionSurfaces[key] = surface
	request := extensions.UISurfaceInputNotification{
		ID:       surface.key.id,
		Sequence: surface.eventSequence,
		Kind:     kind,
		Key:      keyName,
		Text:     text,
		Alt:      alt,
		Shift:    shift,
		Ctrl:     ctrl,
		Mouse:    mouse,
	}
	return notifyExtensionSurfaceCmd(m.extensionUI, surface, extensions.UISurfaceInputMethod, request)
}

func (m *model) updateExtensionSurfaceLayouts() []tea.Cmd {
	cmds := []tea.Cmd{}
	for _, key := range m.extensionSurfaceOrder {
		surface, ok := m.extensionSurfaces[key]
		if !ok {
			continue
		}
		layout := m.resolveExtensionSurfaceLayout(surface)
		if layout == surface.layout {
			continue
		}
		surface.layout = layout
		surface.eventSequence++
		m.extensionSurfaces[key] = surface
		request := extensions.UISurfaceResizeNotification{
			ID:       key.id,
			Sequence: surface.eventSequence,
			Width:    layout.width,
			Height:   layout.height,
		}
		cmds = append(cmds, notifyExtensionSurfaceCmd(m.extensionUI, surface, extensions.UISurfaceResizeMethod, request))
	}
	return cmds
}

func notifyExtensionSurfaceCmd(host *tuiExtensionUIHost, surface tuiExtensionSurface, method string, request any) tea.Cmd {
	return func() tea.Msg {
		if surface.source == nil {
			return extensionUITransportErrorMsg{owner: surface.key.owner}
		}
		if host != nil && !host.isCurrentSurfaceLifecycle(surface.key, surface.openOrdinal) {
			return nil
		}
		if surface.source.ExtensionUIOwner() != surface.key.owner {
			return extensionUITransportErrorMsg{owner: surface.key.owner}
		}
		if err := extensions.NotifyUISurfaceEvent(context.Background(), surface.source, surface.openOrdinal, method, request); err != nil {
			return extensionUITransportErrorMsg{owner: surface.key.owner}
		}
		return nil
	}
}

func (h *tuiExtensionUIHost) isCurrentSurfaceLifecycle(key extensionUIKey, openOrdinal uint64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	surface, ok := h.surfaces[key]
	return ok && surface.openOrdinal == openOrdinal
}

func (m model) resolveExtensionSurfaceLayout(surface tuiExtensionSurface) extensionSurfaceLayout {
	margin := surface.options.Margin
	totalWidth := max(1, m.contentWidth())
	totalHeight := max(1, m.height)
	margin.Left = min(max(0, margin.Left), totalWidth-1)
	margin.Right = min(max(0, margin.Right), totalWidth-margin.Left-1)
	margin.Top = min(max(0, margin.Top), totalHeight-1)
	margin.Bottom = min(max(0, margin.Bottom), totalHeight-margin.Top-1)
	availableWidth := totalWidth - margin.Left - margin.Right
	availableHeight := totalHeight - margin.Top - margin.Bottom
	contentHeight := max(1, min(len(surface.frame.Lines), availableHeight))
	width := resolveExtensionSize(surface.options.Width, availableWidth, min(80, availableWidth))
	height := resolveExtensionSize(surface.options.Height, availableHeight, contentHeight)
	if surface.options.MaxWidth.Set {
		width = min(width, resolveExtensionSize(surface.options.MaxWidth, availableWidth, availableWidth))
	}
	if surface.options.MaxHeight.Set {
		height = min(height, resolveExtensionSize(surface.options.MaxHeight, availableHeight, availableHeight))
	}
	width = max(1, min(width, availableWidth))
	height = max(1, min(height, availableHeight))

	x := margin.Left
	y := margin.Top
	switch surface.options.Anchor {
	case extensions.UISurfaceAnchorTop:
		x += (availableWidth - width) / 2
	case extensions.UISurfaceAnchorTopRight:
		x += availableWidth - width
	case extensions.UISurfaceAnchorLeft:
		y += (availableHeight - height) / 2
	case extensions.UISurfaceAnchorRight:
		x += availableWidth - width
		y += (availableHeight - height) / 2
	case extensions.UISurfaceAnchorBottomLeft:
		y += availableHeight - height
	case extensions.UISurfaceAnchorBottom:
		x += (availableWidth - width) / 2
		y += availableHeight - height
	case extensions.UISurfaceAnchorBottomRight:
		x += availableWidth - width
		y += availableHeight - height
	case extensions.UISurfaceAnchorCenter:
		x += (availableWidth - width) / 2
		y += (availableHeight - height) / 2
	}
	x = max(margin.Left, min(x+surface.options.OffsetX, margin.Left+availableWidth-width))
	y = max(margin.Top, min(y+surface.options.OffsetY, margin.Top+availableHeight-height))
	return extensionSurfaceLayout{x: x, y: y, width: width, height: height}
}

func resolveExtensionSize(value extensions.UISizeValue, available, fallback int) int {
	if !value.Set {
		return fallback
	}
	if value.Cells > 0 {
		return value.Cells
	}
	return max(1, int(float64(available)*value.Percent/100))
}

func (m model) extensionWidgetsHeight(placement string) int {
	height := 0
	for _, widget := range m.sortedExtensionWidgets(placement) {
		height += min(len(widget.frame.Lines), maxExtensionWidgetLines-height)
		if height == maxExtensionWidgetLines {
			break
		}
	}
	return height
}

func (m model) renderExtensionWidgets(placement string) string {
	width := m.contentWidth()
	lines := []string{}
	for _, widget := range m.sortedExtensionWidgets(placement) {
		limit := min(len(widget.frame.Lines), maxExtensionWidgetLines-len(lines))
		for _, line := range widget.frame.Lines[:limit] {
			lines = append(lines, renderExtensionFrameLine(line, width))
		}
		if len(lines) == maxExtensionWidgetLines {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) sortedExtensionWidgets(placement string) []tuiExtensionWidget {
	widgets := []tuiExtensionWidget{}
	for _, widget := range m.extensionWidgets {
		if widget.placement == placement {
			widgets = append(widgets, widget)
		}
	}
	sort.Slice(widgets, func(i, j int) bool { return extensionUIKeyLess(widgets[i].key, widgets[j].key) })
	return widgets
}

func (m model) overlayExtensionSurfaces(lines []string) []string {
	width := m.contentWidth()
	for _, key := range m.extensionSurfaceOrder {
		surface, ok := m.extensionSurfaces[key]
		if !ok || surface.layout.width <= 0 || surface.layout.height <= 0 {
			continue
		}
		for row := 0; row < surface.layout.height; row++ {
			targetY := surface.layout.y + row
			if targetY < 0 || targetY >= len(lines) {
				continue
			}
			overlay := strings.Repeat(" ", surface.layout.width)
			if row < len(surface.frame.Lines) {
				overlay = renderExtensionFrameLine(surface.frame.Lines[row], surface.layout.width)
			}
			left := xansi.Cut(lines[targetY], 0, surface.layout.x)
			right := xansi.Cut(lines[targetY], surface.layout.x+surface.layout.width, width)
			lines[targetY] = padVisible(left+overlay+right, width)
		}
	}
	return lines
}

func renderExtensionFrameLine(line extensions.UIFrameLine, width int) string {
	var rendered strings.Builder
	for _, span := range line.Spans {
		text := sanitizeExtensionUIText(span.Text)
		style := lipgloss.NewStyle().
			Bold(span.Style.Bold).
			Faint(span.Style.Dim).
			Italic(span.Style.Italic).
			Underline(span.Style.Underline).
			Strikethrough(span.Style.Strikethrough).
			Reverse(span.Style.Reverse)
		if color := normalizeExtensionUIColor(span.Style.Foreground); color != "" {
			style = style.Foreground(lipgloss.Color(color))
		}
		if color := normalizeExtensionUIColor(span.Style.Background); color != "" {
			style = style.Background(lipgloss.Color(color))
		}
		rendered.WriteString(style.Render(text))
	}
	return padVisible(xansi.Cut(rendered.String(), 0, width), width)
}

func sanitizeExtensionUIText(text string) string {
	text = xansi.Strip(text)
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r':
			return ' '
		case '\t':
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}

func sanitizeExtensionTranscriptText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for index := range lines {
		lines[index] = sanitizeExtensionUIText(lines[index])
	}
	return strings.Join(lines, "\n")
}

func normalizeExtensionUIColor(color string) string {
	color = strings.TrimSpace(color)
	if len(color) != 7 || color[0] != '#' {
		return ""
	}
	for _, digit := range color[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", digit) {
			return ""
		}
	}
	return color
}

func extensionMouseButton(button tea.MouseButton) string {
	switch button {
	case tea.MouseButtonLeft:
		return "left"
	case tea.MouseButtonMiddle:
		return "middle"
	case tea.MouseButtonRight:
		return "right"
	case tea.MouseButtonWheelUp:
		return "wheelUp"
	case tea.MouseButtonWheelDown:
		return "wheelDown"
	case tea.MouseButtonWheelLeft:
		return "wheelLeft"
	case tea.MouseButtonWheelRight:
		return "wheelRight"
	default:
		return "none"
	}
}

func extensionMouseAction(action tea.MouseAction) string {
	switch action {
	case tea.MouseActionPress:
		return "press"
	case tea.MouseActionRelease:
		return "release"
	case tea.MouseActionMotion:
		return "motion"
	default:
		return "unknown"
	}
}
