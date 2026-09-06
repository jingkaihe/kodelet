package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
)

type nativeCapabilitiesKey struct{}

func withNativeCapabilities(ctx context.Context, caps *chat.ChatClientCapabilities) context.Context {
	value := chat.ChatClientCapabilities{}
	if caps != nil {
		value = *caps
	}
	return context.WithValue(ctx, nativeCapabilitiesKey{}, value)
}

func nativeCapabilities(ctx context.Context) chat.ChatClientCapabilities {
	caps, _ := ctx.Value(nativeCapabilitiesKey{}).(chat.ChatClientCapabilities)
	return caps
}

func parseClientUICapabilities(header string) chat.ChatClientCapabilities {
	has := func(name string) bool { return strings.Contains(","+header+",", ","+name+",") }
	return chat.ChatClientCapabilities{InteractiveUI: has("interactive"), PersistentWidgets: has("widgets"), PersistentSurfaces: has("surfaces")}
}

type nativeUIRoute struct {
	owner                     *uiInputOwner
	identity                  runnerregistry.UIRequestIdentity
	extension                 runnerpayload.ExtensionOwner
	runID, scopeID, surfaceID string
	lifecycle                 uint64
	sequence                  uint64
	ctx                       context.Context
	cancel                    context.CancelFunc
}

type nativeUIRequest struct {
	owner     *uiInputOwner
	route     *nativeUIRoute
	identity  runnerregistry.UIRequestIdentity
	extension runnerpayload.ExtensionOwner
	response  chan extensions.UIFrameResponse
}

// All native state is protected by the existing ownership broker mutex.
type nativeUIState struct {
	epoch   string
	routes  map[string]*nativeUIRoute
	pending map[string]*nativeUIRequest
	release func(*nativeUIRoute)
}

func (b *webUIInputBroker) invalidateNativeUILocked() {
	if b.native == nil {
		return
	}
	for _, pending := range b.native.pending {
		select {
		case pending.response <- extensions.UIFrameResponse{Reason: "native UI owner changed or execution ended"}:
		default:
		}
	}
	for _, route := range b.native.routes {
		route.cancel()
		if b.native.release != nil {
			b.native.release(route)
		}
	}
	clear(b.native.pending)
	clear(b.native.routes)
	if b.owner != nil && b.owner.sink != nil {
		owner := b.owner
		epoch := b.native.epoch
		go func() {
			_ = owner.sink.Send(chat.ChatEvent{Kind: "ui-persistent-reset", ConversationID: b.conversationID, UIPersistent: &chat.UIPersistentEvent{Epoch: epoch}})
		}()
	}
	b.native.epoch = extensions.NewUIInputRequestID()
}

func (s *Server) handleNativeRunnerUI(ctx context.Context, identity runnerregistry.UIRequestIdentity, method string, data json.RawMessage) (any, *protocol.RPCError) {
	var value struct {
		RunID     string                       `json:"runId"`
		Owner     runnerpayload.ExtensionOwner `json:"owner"`
		Lifecycle uint64                       `json:"lifecycle"`
		Request   json.RawMessage              `json:"request"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	if value.Owner.ExtensionID == "" || value.Owner.Generation == 0 {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "extension owner is required"}
	}
	if method == protocol.MethodUIExtensionCleanup {
		s.cleanupRunnerPersistentUI(identity, value.Owner)
		return nil, nil
	}
	unavailable := func(reason string) any {
		if method == protocol.MethodUITranscriptAppend {
			return extensions.UITranscriptAppendResponse{Reason: reason}
		}
		return extensions.UIFrameResponse{Reason: reason}
	}
	run, found := s.runnerRegistry.Run(value.RunID)
	if !found || run.RunnerID != identity.RunnerID {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: "native UI belongs to another runner run"}
	}
	if run.Status != runnerregistry.RunStatusRunning && run.Status != runnerregistry.RunStatusOpening {
		return unavailable("native surfaces require an active execution; reopen on the next run"), nil
	}
	var request struct {
		ScopeID  string             `json:"scopeId"`
		ID       string             `json:"id"`
		Frame    extensions.UIFrame `json:"frame"`
		Sequence uint64             `json:"sequence"`
	}
	if err := json.Unmarshal(value.Request, &request); err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
	}
	if request.ScopeID != "" && request.ScopeID != run.ConversationID {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "native UI scope does not match conversation"}
	}
	if len(value.Request) > 512*1024 {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "native UI request exceeds limit"}
	}
	if method != protocol.MethodUITranscriptAppend {
		if err := extensions.ValidateUIObjectID(request.ID); err != nil {
			return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
		}
		sequence := request.Frame.Sequence
		if method == protocol.MethodUISurfaceClose {
			sequence = request.Sequence
		}
		if err := extensions.ValidateUISequence(sequence); err != nil {
			return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: err.Error()}
		}
	}
	// Normalize only scope; preserve each established extension request schema.
	var scoped map[string]json.RawMessage
	if err := json.Unmarshal(value.Request, &scoped); err != nil || scoped == nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "native UI request must be an object"}
	}
	scoped["scopeId"], _ = json.Marshal(run.ConversationID)
	value.Request, _ = json.Marshal(scoped)
	broker := s.uiInputBrokerForRun(run.ConversationID)
	if broker == nil {
		return unavailable("native UI has no owning execution"), nil
	}
	broker.mu.Lock()
	owner := broker.owner
	if broker.closed || owner == nil || owner.ctx.Err() != nil || !nativeCapabilities(owner.ctx).PersistentSurfaces {
		broker.mu.Unlock()
		return unavailable("selected client does not support native persistent surfaces"), nil
	}
	if rpcErr := validateRunnerUIIdentity(s, identity); rpcErr != nil {
		broker.mu.Unlock()
		return nil, rpcErr
	}
	if s.extensionUI != nil {
		source := runnerWebExtensionUISource{runnerID: identity.RunnerID, runnerGeneration: identity.Generation, owner: runnerExtensionUIOwner(value.Owner)}
		s.extensionUI.mu.Lock()
		closed := s.extensionUI.closed[webExtensionUIProcess{source.webExtensionUINamespace(), value.Owner.ExtensionID, webExtensionUIOwnerGeneration(source, source.owner)}]
		s.extensionUI.mu.Unlock()
		if closed {
			broker.mu.Unlock()
			return unavailable("extension process is closed"), nil
		}
	}
	if broker.native == nil {
		broker.native = &nativeUIState{epoch: extensions.NewUIInputRequestID(), routes: make(map[string]*nativeUIRoute), pending: make(map[string]*nativeUIRequest), release: s.invalidateRunnerSurface}
	}
	state := broker.native
	if len(state.pending) >= 64 {
		broker.mu.Unlock()
		return unavailable("native UI request limit reached"), nil
	}
	var routeID string
	var route *nativeUIRoute
	for id, candidate := range state.routes {
		if candidate.identity == identity && candidate.extension == value.Owner && candidate.surfaceID == request.ID && candidate.runID == value.RunID {
			routeID, route = id, candidate
			break
		}
	}
	sequence := request.Frame.Sequence
	if method == protocol.MethodUISurfaceClose {
		sequence = request.Sequence
	}
	if route != nil && sequence <= route.sequence {
		latest := route.sequence
		broker.mu.Unlock()
		return extensions.UIFrameResponse{LatestSequence: latest, Reason: "stale surface sequence"}, nil
	}
	if method == protocol.MethodUISurfaceOpen {
		if value.Lifecycle == 0 {
			broker.mu.Unlock()
			return unavailable("runner does not support native surface lifecycles"), nil
		}
		if route != nil {
			route.cancel()
			delete(state.routes, routeID)
			event := chat.ChatEvent{Kind: "ui-persistent", ConversationID: run.ConversationID, UIPersistent: &chat.UIPersistentEvent{Epoch: state.epoch, RouteID: routeID, Method: "ui.surface.invalidate", RunnerID: identity.RunnerID, RunnerGeneration: identity.Generation, Owner: value.Owner}}
			go func() { _ = owner.sink.Send(event) }()
		}
		if len(state.routes) >= 32 {
			broker.mu.Unlock()
			return unavailable("native surface limit reached"), nil
		}
		routeID = extensions.NewUIInputRequestID()
		routeCtx, cancel := context.WithCancel(owner.ctx)
		route = &nativeUIRoute{owner: owner, identity: identity, extension: value.Owner, runID: value.RunID, scopeID: run.ConversationID, surfaceID: request.ID, lifecycle: value.Lifecycle, ctx: routeCtx, cancel: cancel}
		state.routes[routeID] = route
	} else if method != protocol.MethodUITranscriptAppend && route == nil {
		broker.mu.Unlock()
		return unavailable("native surface is not open"), nil
	}
	if route != nil {
		route.sequence = sequence
	}
	requestID := extensions.NewUIInputRequestID()
	pending := &nativeUIRequest{owner: owner, route: route, identity: identity, extension: value.Owner, response: make(chan extensions.UIFrameResponse, 1)}
	state.pending[requestID] = pending
	epoch := state.epoch
	broker.mu.Unlock()
	defer func() { broker.mu.Lock(); delete(state.pending, requestID); broker.mu.Unlock() }()
	operation := &chat.UIPersistentEvent{RequestID: requestID, RouteID: routeID, Epoch: epoch, Method: method, RunnerID: identity.RunnerID, RunnerGeneration: identity.Generation, Owner: value.Owner, Request: value.Request}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	sent := make(chan error, 1)
	go func() {
		sent <- owner.sink.Send(chat.ChatEvent{Kind: "ui-persistent", ConversationID: run.ConversationID, UIPersistent: operation})
	}()
	response := extensions.UIFrameResponse{Reason: "native UI acknowledgement timed out or owner detached"}
	select {
	case err := <-sent:
		if err == nil {
			select {
			case response = <-pending.response:
			case <-callCtx.Done():
			case <-owner.ctx.Done():
			}
		} else {
			response.Reason = err.Error()
		}
	case <-callCtx.Done():
	case <-owner.ctx.Done():
	}
	broker.mu.Lock()
	if broker.owner != owner || broker.closed {
		response = extensions.UIFrameResponse{Reason: "native UI owner changed or execution ended"}
	}
	if route != nil && state.routes[routeID] != route {
		response = extensions.UIFrameResponse{Reason: "native surface lifecycle ended"}
	}
	if route != nil && ((method == protocol.MethodUISurfaceOpen && !response.Accepted) || (method == protocol.MethodUISurfaceClose && response.Accepted)) {
		if state.routes[routeID] == route {
			delete(state.routes, routeID)
			route.cancel()
		}
	}
	broker.mu.Unlock()
	if method == protocol.MethodUISurfaceOpen && !response.Accepted {
		invalidated := *operation
		invalidated.Method, invalidated.RequestID = "ui.surface.invalidate", ""
		go func() {
			_ = owner.sink.Send(chat.ChatEvent{Kind: "ui-persistent", ConversationID: run.ConversationID, UIPersistent: &invalidated})
		}()
	}
	if method == protocol.MethodUITranscriptAppend {
		return extensions.UITranscriptAppendResponse{Accepted: response.Accepted, Reason: response.Reason}, nil
	}
	return response, nil
}

func (s *Server) invalidateRunnerSurface(route *nativeUIRoute) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.runnerRegistry.CallRun(ctx, route.runID, protocol.MethodUISurfaceInvalidate, runnerpayload.UISurfaceInvalidateParams{RunID: route.runID, Owner: route.extension, ScopeID: route.scopeID, ID: route.surfaceID, Lifecycle: route.lifecycle}, nil)
	}()
}

func (s *Server) handlePersistentUIAck(w http.ResponseWriter, r *http.Request) {
	conversationID := mux.Vars(r)["id"]
	broker := s.uiInputBrokerForRun(conversationID)
	var ack chat.UIPersistentAck
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&ack); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid native UI acknowledgement", err)
		return
	}
	if broker == nil {
		s.writeErrorResponse(w, http.StatusConflict, "native UI execution ended", nil)
		return
	}
	broker.mu.Lock()
	if broker.native == nil {
		broker.mu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "native UI request is not pending", nil)
		return
	}
	pending := broker.native.pending[ack.RequestID]
	if pending == nil || pending.owner != broker.owner || broker.closed || broker.owner.ctx.Err() != nil || broker.owner.clientID != r.Header.Get(chat.ClientIDHeader) {
		broker.mu.Unlock()
		s.writeErrorResponse(w, http.StatusConflict, "native UI acknowledgement belongs to a stale owner", nil)
		return
	}
	delete(broker.native.pending, ack.RequestID)
	pending.response <- ack.Response
	broker.mu.Unlock()
	s.writeJSONResponse(w, map[string]bool{"success": true})
}

func (s *Server) handlePersistentUIInput(w http.ResponseWriter, r *http.Request) {
	conversationID := mux.Vars(r)["id"]
	broker := s.uiInputBrokerForRun(conversationID)
	var input chat.UIPersistentInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&input); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid native UI input", err)
		return
	}
	if broker == nil {
		s.writeErrorResponse(w, http.StatusConflict, "native UI execution ended", nil)
		return
	}
	broker.mu.Lock()
	var route *nativeUIRoute
	if broker.native != nil {
		route = broker.native.routes[input.RouteID]
	}
	valid := !broker.closed && route != nil && route.owner == broker.owner && route.ctx.Err() == nil && broker.owner.clientID == r.Header.Get(chat.ClientIDHeader)
	broker.mu.Unlock()
	if !valid {
		s.writeErrorResponse(w, http.StatusConflict, "native surface owner or lifecycle is stale", nil)
		return
	}
	if rpcErr := validateRunnerUIIdentity(s, route.identity); rpcErr != nil {
		s.writeErrorResponse(w, http.StatusConflict, rpcErr.Message, nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(route.ctx, cancel)
	defer stop()
	var params any
	var requestID, scopeID string
	var sequence uint64
	switch input.Method {
	case protocol.MethodUISurfaceInput:
		var request extensions.UISurfaceInputNotification
		if err := json.Unmarshal(input.Request, &request); err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "invalid surface input", err)
			return
		}
		requestID, scopeID, sequence = request.ID, request.ScopeID, request.Sequence
		params = runnerpayload.UISurfaceInputParams{RunID: route.runID, Owner: route.extension, Lifecycle: route.lifecycle, Request: request}
	case protocol.MethodUISurfaceResize:
		var request extensions.UISurfaceResizeNotification
		if err := json.Unmarshal(input.Request, &request); err != nil || request.Width < 1 || request.Height < 1 {
			s.writeErrorResponse(w, http.StatusBadRequest, "invalid surface resize", err)
			return
		}
		requestID, scopeID, sequence = request.ID, request.ScopeID, request.Sequence
		params = runnerpayload.UISurfaceResizeParams{RunID: route.runID, Owner: route.extension, Lifecycle: route.lifecycle, Request: request}
	default:
		s.writeErrorResponse(w, http.StatusBadRequest, "unsupported surface event", nil)
		return
	}
	if requestID != route.surfaceID || scopeID != conversationID || sequence == 0 {
		s.writeErrorResponse(w, http.StatusBadRequest, "surface input does not match its route", nil)
		return
	}
	if err := s.runnerRegistry.CallRun(ctx, route.runID, input.Method, params, nil); err != nil {
		s.writeErrorResponse(w, http.StatusConflict, "native surface input could not be delivered", err)
		return
	}
	s.writeJSONResponse(w, map[string]bool{"success": true})
}

func (s *Server) updateRunnerUICapabilities(ctx context.Context, conversationID string, caps chat.ChatClientCapabilities) error {
	if s.runnerRegistry == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, runner := range s.runnerRegistry.Runners() {
		for _, id := range runner.ActiveRunIDs {
			run, ok := s.runnerRegistry.Run(id)
			if !ok || run.ConversationID != conversationID {
				continue
			}
			return s.runnerRegistry.CallRun(ctx, id, protocol.MethodUICapabilities, protocol.UICapabilitiesParams{RunID: id, Capabilities: protocol.ClientCapabilities{InteractiveUI: caps.InteractiveUI, PersistentWidgets: caps.PersistentWidgets, PersistentSurfaces: caps.PersistentSurfaces}}, nil)
		}
	}
	return nil // The environment resolver reads the current owner if opening has not begun.
}

func (h *webExtensionUIHost) cleanupRunnerOwner(identity runnerregistry.UIRequestIdentity, owner runnerpayload.ExtensionOwner) {
	source := runnerWebExtensionUISource{runnerID: identity.RunnerID, runnerGeneration: identity.Generation, owner: runnerExtensionUIOwner(owner)}
	generation := webExtensionUIOwnerGeneration(source, source.owner)
	type removal struct {
		widget   webExtensionWidget
		revision string
	}
	var removed []removal
	h.mu.Lock()
	if h.closed == nil {
		h.closed = make(map[webExtensionUIProcess]bool)
	}
	h.closed[webExtensionUIProcess{source.webExtensionUINamespace(), owner.ExtensionID, generation}] = true
	for key, widget := range h.widgets {
		if key.namespace != source.webExtensionUINamespace() || widget.owner != source.owner || widget.ownerGeneration != generation {
			continue
		}
		delete(h.widgets, key)
		widget.frame = extensions.UIFrame{Sequence: widget.frame.Sequence + 1}
		removed = append(removed, removal{widget, h.nextRevisionLocked()})
	}
	h.mu.Unlock()
	for _, item := range removed {
		h.emitWidget(item.widget, true, item.revision)
	}
}

// RunnerUIDetached removes only UI belonging to the lost connection generation.
func (s *Server) RunnerUIDetached(identity runnerregistry.UIRequestIdentity) {
	owners := make(map[runnerpayload.ExtensionOwner]bool)
	if s.extensionUI != nil {
		s.extensionUI.mu.Lock()
		for _, widget := range s.extensionUI.widgets {
			if widget.key.namespace == "runner:"+identity.RunnerID && strings.HasPrefix(widget.ownerGeneration, strconv.FormatInt(identity.Generation, 10)+":") {
				owners[runnerpayload.ExtensionOwner{ExtensionID: widget.owner.ExtensionID, Generation: widget.owner.Generation}] = true
			}
		}
		s.extensionUI.mu.Unlock()
	}
	s.activeChatsMu.Lock()
	for _, run := range s.activeChats {
		if broker := run.uiInput; broker != nil {
			broker.mu.Lock()
			if broker.native != nil {
				for _, route := range broker.native.routes {
					if route.identity == identity {
						owners[route.extension] = true
					}
				}
				for _, pending := range broker.native.pending {
					if pending.identity == identity {
						owners[pending.extension] = true
					}
				}
			}
			broker.mu.Unlock()
		}
	}
	s.activeChatsMu.Unlock()
	for owner := range owners {
		s.cleanupRunnerPersistentUI(identity, owner)
	}
}

func (s *Server) cleanupRunnerPersistentUI(identity runnerregistry.UIRequestIdentity, owner runnerpayload.ExtensionOwner) {
	if s.extensionUI != nil {
		s.extensionUI.cleanupRunnerOwner(identity, owner)
	}
	s.activeChatsMu.Lock()
	brokers := make([]*webUIInputBroker, 0, len(s.activeChats))
	for _, run := range s.activeChats {
		if run.uiInput != nil {
			brokers = append(brokers, run.uiInput)
		}
	}
	s.activeChatsMu.Unlock()
	for _, broker := range brokers {
		broker.mu.Lock()
		if broker.native != nil {
			for id, route := range broker.native.routes {
				if route.identity == identity && route.extension == owner {
					route.cancel()
					delete(broker.native.routes, id)
				}
			}
			for id, pending := range broker.native.pending {
				if pending.identity == identity && pending.extension == owner {
					select {
					case pending.response <- extensions.UIFrameResponse{Reason: "extension process closed"}:
					default:
					}
					delete(broker.native.pending, id)
				}
			}
			if broker.owner != nil {
				sink := broker.owner.sink
				event := chat.ChatEvent{Kind: "ui-persistent", ConversationID: broker.conversationID, UIPersistent: &chat.UIPersistentEvent{Method: protocol.MethodUIExtensionCleanup, Epoch: broker.native.epoch, RunnerID: identity.RunnerID, RunnerGeneration: identity.Generation, Owner: owner}}
				go func() { _ = sink.Send(event) }()
			}
		}
		broker.mu.Unlock()
	}
}
