package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

// UIPersistentEvent is an owner-targeted, acknowledged native UI operation.
// RouteID is opaque input authority; clients never select a runner or run ID.
type UIPersistentEvent struct {
	RequestID        string                       `json:"requestId,omitempty"`
	RouteID          string                       `json:"routeId,omitempty"`
	Epoch            string                       `json:"epoch,omitempty"`
	Method           string                       `json:"method"`
	RunnerID         string                       `json:"runnerId,omitempty"`
	RunnerGeneration int64                        `json:"runnerGeneration,omitempty"`
	Owner            runnerpayload.ExtensionOwner `json:"owner"`
	Request          json.RawMessage              `json:"request,omitempty"`
}

type UIPersistentAck struct {
	RequestID string                     `json:"requestId"`
	Response  extensions.UIFrameResponse `json:"response"`
}

type UIPersistentInput struct {
	RouteID string          `json:"routeId"`
	Method  string          `json:"method"`
	Request json.RawMessage `json:"request"`
}

func controlPlaneClientCapabilities(ctx context.Context) ChatClientCapabilities {
	caps := ChatClientCapabilities{InteractiveUI: controlPlaneSupportsInteractiveUI(ctx)}
	if host, ok := extensions.ExtensionUIHostFromContext(ctx); ok {
		ui := extensions.ExtensionUIHostCapabilities{Widgets: true, Surfaces: true}
		if provider, ok := host.(extensions.ExtensionUIHostCapabilityProvider); ok {
			ui = provider.ExtensionUIHostCapabilities(ctx)
		}
		caps.PersistentWidgets, caps.PersistentSurfaces = ui.Widgets, ui.Surfaces
	}
	return caps
}

func controlPlaneUICapabilitiesHeader(ctx context.Context) string {
	caps := controlPlaneClientCapabilities(ctx)
	var names []string
	if caps.InteractiveUI {
		names = append(names, "interactive")
	}
	if caps.PersistentWidgets {
		names = append(names, "widgets")
	}
	if caps.PersistentSurfaces {
		names = append(names, "surfaces")
	}
	return strings.Join(names, ",")
}

type remoteUISource struct {
	owner          extensions.UIExtensionOwner
	runner         *ControlPlaneChatRunner
	conversationID string
	mu             sync.Mutex
	routes         map[string]string
	lifecycles     map[string]uint64
}

func (s *remoteUISource) ExtensionUIOwner() extensions.UIExtensionOwner { return s.owner }
func (s *remoteUISource) PrepareUISurfaceEventLifecycle(scopeID, id string, lifecycle uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lifecycles[scopeID+"\x00"+id] = lifecycle
}

func (s *remoteUISource) NotifyExtensionUI(ctx context.Context, method string, params any) error {
	return s.NotifyExtensionUISurfaceEvent(ctx, 0, method, params)
}

func (s *remoteUISource) NotifyExtensionUISurfaceEvent(ctx context.Context, lifecycle uint64, method string, params any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var request struct {
		ScopeID string `json:"scopeId"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	s.mu.Lock()
	key := request.ScopeID + "\x00" + request.ID
	route := s.routes[key]
	current := s.lifecycles[key]
	s.mu.Unlock()
	if route == "" || (lifecycle != 0 && lifecycle != current) {
		return errors.New("this interactive view is closed")
	}
	switch method {
	case extensions.UISurfaceInputMethod:
		method = protocol.MethodUISurfaceInput
	case extensions.UISurfaceResizeMethod:
		method = protocol.MethodUISurfaceResize
	default:
		return errors.New("unsupported remote surface event")
	}
	return s.runner.postPersistentUI(ctx, s.conversationID, "input", UIPersistentInput{RouteID: route, Method: method, Request: data})
}

type remoteUIStream struct {
	runner  *ControlPlaneChatRunner
	host    extensions.ExtensionUIHost
	id      string
	sources map[string]*remoteUISource
	events  map[string]UIPersistentEvent
	closed  map[string]bool
}

type remoteWidget struct {
	owner      extensions.UIExtensionOwner
	host       extensions.ExtensionUIHost
	generation string
}

func (s *remoteUIStream) handleWidgets(ctx context.Context, conversationID string, event ChatEvent) error {
	if s.host == nil {
		return nil
	}
	r := s.runner
	r.persistentMu.Lock()
	defer r.persistentMu.Unlock()
	if r.widgets == nil {
		r.widgets = make(map[string]remoteWidget)
		r.widgetRevisions = make(map[string]string)
	}
	previous := r.widgetRevisions[conversationID]
	if previous != "" && event.UIWidgetRevision != "" {
		oldEpoch, oldRev, _ := strings.Cut(previous, ":")
		epoch, rev, _ := strings.Cut(event.UIWidgetRevision, ":")
		oldSequence, _ := strconv.ParseUint(oldRev, 10, 64)
		sequence, _ := strconv.ParseUint(rev, 10, 64)
		if epoch == oldEpoch && sequence <= oldSequence {
			return nil
		}
	}
	items := event.UIWidgets
	if event.UIWidget != nil {
		items = []UIWidgetEvent{*event.UIWidget}
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		key := conversationID + "\x00" + item.Key
		seen[key] = true
		previous, exists := r.widgets[key]
		if exists && (previous.generation != item.Generation || item.Removed) {
			previous.host.CleanupExtensionUI(previous.owner)
			delete(r.widgets, key)
			exists = false
		}
		if item.Removed {
			continue
		}
		owner := previous.owner
		if !exists {
			owner = extensions.UIExtensionOwner{ExtensionID: "remote-widget/" + extensions.NewUIInputRequestID(), Generation: 1}
		}
		source := &remoteUISource{owner: owner}
		if _, err := s.host.SetWidget(ctx, source, extensions.UIWidgetSetRequest{ScopeID: conversationID, ID: item.ID, Placement: item.Placement, Frame: item.Frame}); err != nil {
			return err
		}
		r.widgets[key] = remoteWidget{owner: owner, host: s.host, generation: item.Generation}
	}
	if event.Kind == "ui-widgets" {
		for key, widget := range r.widgets {
			if strings.HasPrefix(key, conversationID+"\x00") && !seen[key] {
				widget.host.CleanupExtensionUI(widget.owner)
				delete(r.widgets, key)
			}
		}
	}
	r.widgetRevisions[conversationID] = event.UIWidgetRevision
	return nil
}

func newRemoteUIStream(ctx context.Context, runner *ControlPlaneChatRunner) *remoteUIStream {
	host, _ := extensions.ExtensionUIHostFromContext(ctx)
	return &remoteUIStream{runner: runner, host: host, id: extensions.NewUIInputRequestID(), sources: make(map[string]*remoteUISource), events: make(map[string]UIPersistentEvent), closed: make(map[string]bool)}
}

func (s *remoteUIStream) close() {
	if s.host == nil {
		return
	}
	for key := range s.sources {
		s.cleanupSource(key)
	}
}

func (s *remoteUIStream) cleanupSource(key string) {
	if source := s.sources[key]; source != nil {
		source.mu.Lock()
		clear(source.routes)
		source.mu.Unlock()
		s.host.CleanupExtensionUI(source.owner)
		delete(s.sources, key)
		delete(s.events, key)
	}
}

func nativeRemoteKey(event UIPersistentEvent) string {
	return event.Epoch + "/" + event.RunnerID + "/" + strconv.FormatInt(event.RunnerGeneration, 10) + "/" + event.Owner.ExtensionID + "/" + strconv.FormatUint(event.Owner.Generation, 10)
}

func (s *remoteUIStream) handle(ctx context.Context, conversationID string, event ChatEvent) (bool, error) {
	if event.Kind == "ui-widget" || event.Kind == "ui-widgets" {
		return true, s.handleWidgets(ctx, conversationID, event)
	}
	if event.UIPersistent == nil {
		return false, nil
	}
	operation := *event.UIPersistent
	processKey := nativeRemoteKey(operation)
	key := processKey + "/" + operation.RouteID
	if event.Kind == "ui-persistent-reset" || operation.Method == protocol.MethodUIExtensionCleanup || operation.Method == "ui.surface.invalidate" {
		for candidate, prior := range s.events {
			if (event.Kind == "ui-persistent-reset" && prior.Epoch == operation.Epoch) || (operation.Method == protocol.MethodUIExtensionCleanup && nativeRemoteKey(prior) == processKey) || (operation.Method == "ui.surface.invalidate" && candidate == key) {
				s.cleanupSource(candidate)
			}
		}
		switch {
		case event.Kind == "ui-persistent-reset":
			s.closed[operation.Epoch] = true
		case operation.Method == protocol.MethodUIExtensionCleanup:
			s.closed[processKey] = true
		default:
			s.closed[key] = true
		}
		return true, nil
	}
	response := extensions.UIFrameResponse{Reason: "native persistent UI is unavailable"}
	if s.host != nil && !s.closed[operation.Epoch] && !s.closed[processKey] && !s.closed[key] {
		source := s.sources[key]
		if source == nil {
			owner := extensions.UIExtensionOwner{ExtensionID: "remote/" + s.id + "/" + key, Generation: operation.Owner.Generation}
			source = &remoteUISource{owner: owner, runner: s.runner, conversationID: conversationID, routes: make(map[string]string), lifecycles: make(map[string]uint64)}
			s.sources[key], s.events[key] = source, operation
		}
		var err error
		switch operation.Method {
		case protocol.MethodUITranscriptAppend:
			var request extensions.UITranscriptAppendRequest
			err = json.Unmarshal(operation.Request, &request)
			if host, ok := s.host.(extensions.ExtensionUITranscriptHost); err == nil && ok {
				var result extensions.UITranscriptAppendResponse
				result, err = host.AppendTranscript(ctx, source, request)
				response = extensions.UIFrameResponse{Accepted: result.Accepted, Reason: result.Reason}
			}
		case protocol.MethodUISurfaceOpen:
			var request extensions.UISurfaceOpenRequest
			err = json.Unmarshal(operation.Request, &request)
			if err == nil {
				source.mu.Lock()
				source.routes[request.ScopeID+"\x00"+request.ID] = operation.RouteID
				source.mu.Unlock()
				response, err = s.host.OpenSurface(ctx, source, request)
			}
		case protocol.MethodUISurfaceFrame:
			var request extensions.UISurfaceFrameRequest
			err = json.Unmarshal(operation.Request, &request)
			if err == nil {
				response, err = s.host.UpdateSurface(ctx, source, request)
			}
		case protocol.MethodUISurfaceClose:
			var request extensions.UISurfaceCloseRequest
			err = json.Unmarshal(operation.Request, &request)
			if err == nil {
				response, err = s.host.CloseSurface(ctx, source, request)
				if response.Accepted {
					source.mu.Lock()
					delete(source.routes, request.ScopeID+"\x00"+request.ID)
					source.mu.Unlock()
				}
			}
		}
		if err != nil {
			response = extensions.UIFrameResponse{Reason: err.Error()}
		}
	}
	// UI failures are acknowledged as unavailable rather than failing the model stream.
	err := s.runner.postPersistentUI(ctx, conversationID, "ack", UIPersistentAck{RequestID: operation.RequestID, Response: response})
	if (operation.Method == protocol.MethodUISurfaceOpen && (!response.Accepted || err != nil)) || (operation.Method == protocol.MethodUISurfaceClose && response.Accepted) {
		s.cleanupSource(key)
	}
	return true, nil
}

func (r *ControlPlaneChatRunner) postPersistentUI(ctx context.Context, conversationID, action string, value any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	endpoint, err := controlPlaneEndpointURL(r.baseURL, "api", "conversations", conversationID, "ui-persistent", action)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	r.authorize(request)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ClientIDHeader, r.clientID)
	response, err := r.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return controlPlaneResponseError(response)
	}
	return nil
}
