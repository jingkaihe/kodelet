package controlplane

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/pkg/errors"
)

const localExtensionUINamespace = "local"

const (
	maxWebUIWidgetLines     = 64
	maxWebUIWidgetSpans     = 128
	maxWebUIWidgetTextBytes = 256 * 1024
	maxWebUIWidgetsPerScope = 32
	maxWebUIWidgetsTotal    = 1024
)

type webExtensionUIKey struct {
	namespace   string
	extensionID string
	scopeID     string
	id          string
}

type webExtensionWidget struct {
	key             webExtensionUIKey
	owner           extensions.UIExtensionOwner
	ownerGeneration string
	placement       string
	frame           extensions.UIFrame
}

type webExtensionUIHost struct {
	mu      sync.Mutex
	widgets map[webExtensionUIKey]webExtensionWidget
	emit    func(string, chat.ChatEvent)
}

type webExtensionUINamespacedSource interface {
	webExtensionUINamespace() string
}

type webExtensionUIRunnerGenerationSource interface {
	webExtensionUIRunnerGeneration() int64
}

func newWebExtensionUIHost(emit func(string, chat.ChatEvent)) *webExtensionUIHost {
	return &webExtensionUIHost{
		widgets: make(map[webExtensionUIKey]webExtensionWidget),
		emit:    emit,
	}
}

func (*webExtensionUIHost) ExtensionUIHostCapabilities(context.Context) extensions.ExtensionUIHostCapabilities {
	return extensions.ExtensionUIHostCapabilities{Widgets: true}
}

func (h *webExtensionUIHost) SetWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetSetRequest) (extensions.UIFrameResponse, error) {
	key, owner, ownerGeneration, err := webExtensionWidgetRequestKey(ctx, source, request.ScopeID, request.ID)
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
	if err := normalizeAndValidateWebUIFrame(&request.Frame); err != nil {
		return extensions.UIFrameResponse{}, err
	}

	h.mu.Lock()
	current, exists := h.widgets[key]
	latest := uint64(0)
	if exists && current.ownerGeneration == ownerGeneration {
		latest = current.frame.Sequence
	}
	if request.Frame.Sequence <= latest {
		h.mu.Unlock()
		return webUIStaleFrameResponse(latest), nil
	}
	if !exists {
		if len(h.widgets) >= maxWebUIWidgetsTotal {
			h.mu.Unlock()
			return extensions.UIFrameResponse{Reason: fmt.Sprintf("web ui supports at most %d extension widgets", maxWebUIWidgetsTotal)}, nil
		}
		widgetsInScope := 0
		for existingKey := range h.widgets {
			if existingKey.scopeID == key.scopeID {
				widgetsInScope++
			}
		}
		if widgetsInScope >= maxWebUIWidgetsPerScope {
			h.mu.Unlock()
			return extensions.UIFrameResponse{Reason: fmt.Sprintf("web ui supports at most %d extension widgets per conversation", maxWebUIWidgetsPerScope)}, nil
		}
	}
	widget := webExtensionWidget{key: key, owner: owner, ownerGeneration: ownerGeneration, placement: placement, frame: request.Frame}
	h.widgets[key] = widget
	h.mu.Unlock()

	h.emitWidget(widget, false)
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (h *webExtensionUIHost) UpdateWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetFrameRequest) (extensions.UIFrameResponse, error) {
	key, _, ownerGeneration, err := webExtensionWidgetRequestKey(ctx, source, request.ScopeID, request.ID)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := extensions.ValidateUISequence(request.Frame.Sequence); err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := normalizeAndValidateWebUIFrame(&request.Frame); err != nil {
		return extensions.UIFrameResponse{}, err
	}

	h.mu.Lock()
	widget, exists := h.widgets[key]
	if !exists || widget.ownerGeneration != ownerGeneration {
		h.mu.Unlock()
		return extensions.UIFrameResponse{Reason: "widget is not open"}, nil
	}
	latest := widget.frame.Sequence
	if request.Frame.Sequence <= latest {
		h.mu.Unlock()
		return webUIStaleFrameResponse(latest), nil
	}
	widget.frame = request.Frame
	h.widgets[key] = widget
	h.mu.Unlock()

	h.emitWidget(widget, false)
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Frame.Sequence}, nil
}

func (h *webExtensionUIHost) RemoveWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetRemoveRequest) (extensions.UIFrameResponse, error) {
	key, _, ownerGeneration, err := webExtensionWidgetRequestKey(ctx, source, request.ScopeID, request.ID)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if err := extensions.ValidateUISequence(request.Sequence); err != nil {
		return extensions.UIFrameResponse{}, err
	}

	h.mu.Lock()
	widget, exists := h.widgets[key]
	if !exists || widget.ownerGeneration != ownerGeneration {
		h.mu.Unlock()
		return extensions.UIFrameResponse{Reason: "widget is not open"}, nil
	}
	latest := widget.frame.Sequence
	if request.Sequence <= latest {
		h.mu.Unlock()
		return webUIStaleFrameResponse(latest), nil
	}
	delete(h.widgets, key)
	widget.frame = extensions.UIFrame{Sequence: request.Sequence}
	h.mu.Unlock()

	h.emitWidget(widget, true)
	return extensions.UIFrameResponse{Accepted: true, LatestSequence: request.Sequence}, nil
}

func (*webExtensionUIHost) OpenSurface(context.Context, extensions.UIExtensionSource, extensions.UISurfaceOpenRequest) (extensions.UIFrameResponse, error) {
	return extensions.UIFrameResponse{Reason: "web ui interactive extension surfaces are not available"}, nil
}

func (*webExtensionUIHost) UpdateSurface(context.Context, extensions.UIExtensionSource, extensions.UISurfaceFrameRequest) (extensions.UIFrameResponse, error) {
	return extensions.UIFrameResponse{Reason: "web ui interactive extension surfaces are not available"}, nil
}

func (*webExtensionUIHost) CloseSurface(context.Context, extensions.UIExtensionSource, extensions.UISurfaceCloseRequest) (extensions.UIFrameResponse, error) {
	return extensions.UIFrameResponse{Reason: "web ui interactive extension surfaces are not available"}, nil
}

func (h *webExtensionUIHost) CleanupExtensionUI(owner extensions.UIExtensionOwner) {
	if h == nil || strings.TrimSpace(owner.ExtensionID) == "" || owner.Generation == 0 {
		return
	}

	h.mu.Lock()
	removed := make([]webExtensionWidget, 0)
	for key, widget := range h.widgets {
		if key.namespace != localExtensionUINamespace || widget.owner != owner {
			continue
		}
		delete(h.widgets, key)
		widget.frame = extensions.UIFrame{Sequence: widget.frame.Sequence + 1}
		removed = append(removed, widget)
	}
	h.mu.Unlock()

	for _, widget := range removed {
		h.emitWidget(widget, true)
	}
}

func (h *webExtensionUIHost) RemoveConversation(scopeID string) {
	if h == nil {
		return
	}
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		return
	}
	h.mu.Lock()
	for key := range h.widgets {
		if key.scopeID == scopeID {
			delete(h.widgets, key)
		}
	}
	h.mu.Unlock()
}

func (h *webExtensionUIHost) Snapshot(scopeID string) []chat.UIWidgetEvent {
	if h == nil {
		return nil
	}
	scopeID = strings.TrimSpace(scopeID)
	h.mu.Lock()
	widgets := make([]webExtensionWidget, 0)
	for _, widget := range h.widgets {
		if widget.key.scopeID == scopeID {
			widgets = append(widgets, widget)
		}
	}
	h.mu.Unlock()
	sort.Slice(widgets, func(i, j int) bool {
		if widgets[i].key.namespace != widgets[j].key.namespace {
			return widgets[i].key.namespace < widgets[j].key.namespace
		}
		if widgets[i].key.extensionID != widgets[j].key.extensionID {
			return widgets[i].key.extensionID < widgets[j].key.extensionID
		}
		return widgets[i].key.id < widgets[j].key.id
	})
	events := make([]chat.UIWidgetEvent, 0, len(widgets))
	for _, widget := range widgets {
		events = append(events, webExtensionWidgetEvent(widget, false))
	}
	return events
}

func (h *webExtensionUIHost) emitWidget(widget webExtensionWidget, removed bool) {
	if h == nil || h.emit == nil {
		return
	}
	h.emit(widget.key.scopeID, chat.ChatEvent{
		Kind:           "ui-widget",
		ConversationID: widget.key.scopeID,
		UIWidget:       pointerTo(webExtensionWidgetEvent(widget, removed)),
	})
}

func webExtensionWidgetRequestKey(ctx context.Context, source extensions.UIExtensionSource, scopeID, id string) (webExtensionUIKey, extensions.UIExtensionOwner, string, error) {
	if err := ctx.Err(); err != nil {
		return webExtensionUIKey{}, extensions.UIExtensionOwner{}, "", err
	}
	if source == nil {
		return webExtensionUIKey{}, extensions.UIExtensionOwner{}, "", errors.New("extension UI source is required")
	}
	owner := source.ExtensionUIOwner()
	if strings.TrimSpace(owner.ExtensionID) == "" || owner.Generation == 0 {
		return webExtensionUIKey{}, extensions.UIExtensionOwner{}, "", errors.New("extension UI owner is invalid")
	}
	if err := extensions.ValidateUIObjectID(id); err != nil {
		return webExtensionUIKey{}, extensions.UIExtensionOwner{}, "", err
	}
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		scopeID = extensions.ExtensionUIScopeFromContext(ctx)
	}
	if scopeID == "" {
		return webExtensionUIKey{}, extensions.UIExtensionOwner{}, "", errors.New("extension widget requires a conversation scope")
	}
	namespace := localExtensionUINamespace
	if namespaced, ok := source.(webExtensionUINamespacedSource); ok {
		if candidate := strings.TrimSpace(namespaced.webExtensionUINamespace()); candidate != "" {
			namespace = candidate
		}
	}
	return webExtensionUIKey{
		namespace:   namespace,
		extensionID: strings.TrimSpace(owner.ExtensionID),
		scopeID:     scopeID,
		id:          id,
	}, owner, webExtensionUIOwnerGeneration(source, owner), nil
}

func webExtensionUIOwnerGeneration(source extensions.UIExtensionSource, owner extensions.UIExtensionOwner) string {
	runnerGeneration := int64(0)
	if runnerSource, ok := source.(webExtensionUIRunnerGenerationSource); ok {
		runnerGeneration = runnerSource.webExtensionUIRunnerGeneration()
		if runnerGeneration < 0 {
			runnerGeneration = 0
		}
	}
	return strconv.FormatInt(runnerGeneration, 10) + ":" + strconv.FormatUint(owner.Generation, 10)
}

func webExtensionWidgetEvent(widget webExtensionWidget, removed bool) chat.UIWidgetEvent {
	return chat.UIWidgetEvent{
		Key:         webExtensionWidgetEventKey(widget.key),
		ExtensionID: widget.key.extensionID,
		Generation:  widget.ownerGeneration,
		ID:          widget.key.id,
		Placement:   widget.placement,
		Frame:       widget.frame,
		Removed:     removed,
	}
}

func normalizeAndValidateWebUIFrame(frame *extensions.UIFrame) error {
	if frame.Lines == nil {
		frame.Lines = []extensions.UIFrameLine{}
	}
	if len(frame.Lines) > maxWebUIWidgetLines {
		return errors.Errorf("extension widget exceeds %d lines", maxWebUIWidgetLines)
	}
	totalTextBytes := 0
	for _, line := range frame.Lines {
		if len(line.Spans) > maxWebUIWidgetSpans {
			return errors.Errorf("extension widget line exceeds %d spans", maxWebUIWidgetSpans)
		}
		for _, span := range line.Spans {
			totalTextBytes += len(span.Text)
			if totalTextBytes > maxWebUIWidgetTextBytes {
				return errors.Errorf("extension widget text exceeds %d bytes", maxWebUIWidgetTextBytes)
			}
		}
	}
	return nil
}

func webExtensionWidgetEventKey(key webExtensionUIKey) string {
	raw := strings.Join([]string{key.namespace, key.extensionID, key.scopeID, key.id}, "\x00")
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func webUIStaleFrameResponse(latest uint64) extensions.UIFrameResponse {
	return extensions.UIFrameResponse{LatestSequence: latest, Reason: "stale extension UI frame"}
}

func pointerTo[T any](value T) *T {
	return &value
}
