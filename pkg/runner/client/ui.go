package client

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

type runnerRunIDContextKey struct{}

func (s *Service) Input(ctx context.Context, request extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	peer, runID, capabilities, err := s.uiTarget(ctx)
	if err != nil {
		return extensions.UIInputResponse{}, err
	}
	if !capabilities.InteractiveUI {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui input is not available"}, nil
	}
	owner := interactiveUIOwner(ctx)
	request.ID = scopedInteractiveUIRequestID(owner, request.ID)
	var response extensions.UIInputResponse
	err = peer.Call(ctx, protocol.MethodUIInput, runnerpayload.UIInputParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) Confirm(ctx context.Context, request extensions.UIConfirmRequest) (extensions.UIInputResponse, error) {
	peer, runID, capabilities, err := s.uiTarget(ctx)
	if err != nil {
		return extensions.UIInputResponse{}, err
	}
	if !capabilities.InteractiveUI {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui confirmation is not available"}, nil
	}
	owner := interactiveUIOwner(ctx)
	request.ID = scopedInteractiveUIRequestID(owner, request.ID)
	var response extensions.UIInputResponse
	err = peer.Call(ctx, protocol.MethodUIConfirm, runnerpayload.UIConfirmParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) Select(ctx context.Context, request extensions.UISelectRequest) (extensions.UIInputResponse, error) {
	peer, runID, capabilities, err := s.uiTarget(ctx)
	if err != nil {
		return extensions.UIInputResponse{}, err
	}
	if !capabilities.InteractiveUI {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui selection is not available"}, nil
	}
	owner := interactiveUIOwner(ctx)
	request.ID = scopedInteractiveUIRequestID(owner, request.ID)
	var response extensions.UIInputResponse
	err = peer.Call(ctx, protocol.MethodUISelect, runnerpayload.UISelectParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) Notify(ctx context.Context, request extensions.UINotifyRequest) (extensions.UIInputResponse, error) {
	peer, runID, capabilities, err := s.uiTarget(ctx)
	if err != nil {
		return extensions.UIInputResponse{}, err
	}
	if !capabilities.InteractiveUI {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui notification is not available"}, nil
	}
	owner := interactiveUIOwner(ctx)
	var response extensions.UIInputResponse
	err = peer.Call(ctx, protocol.MethodUINotify, runnerpayload.UINotifyParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) SetWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetSetRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, capabilities, err := s.persistentUITarget(ctx, source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !capabilities.PersistentWidgets {
		return extensions.UIFrameResponse{Reason: "client persistent extension widgets are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUIWidgetSet, runnerpayload.UIWidgetSetParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) UpdateWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetFrameRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, capabilities, err := s.persistentUITarget(ctx, source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !capabilities.PersistentWidgets {
		return extensions.UIFrameResponse{Reason: "client persistent extension widgets are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUIWidgetFrame, runnerpayload.UIWidgetFrameParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) RemoveWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetRemoveRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, capabilities, err := s.persistentUITarget(ctx, source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !capabilities.PersistentWidgets {
		return extensions.UIFrameResponse{Reason: "client persistent extension widgets are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUIWidgetRemove, runnerpayload.UIWidgetRemoveParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) AppendTranscript(ctx context.Context, source extensions.UIExtensionSource, request extensions.UITranscriptAppendRequest) (extensions.UITranscriptAppendResponse, error) {
	peer, runID, owner, capabilities, err := s.persistentUITarget(ctx, source)
	if err != nil {
		return extensions.UITranscriptAppendResponse{}, err
	}
	if !capabilities.PersistentSurfaces {
		return extensions.UITranscriptAppendResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UITranscriptAppendResponse
	err = peer.Call(ctx, protocol.MethodUITranscriptAppend, runnerpayload.UITranscriptAppendParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) OpenSurface(ctx context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceOpenRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, capabilities, err := s.persistentUITarget(ctx, source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !capabilities.PersistentSurfaces {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUISurfaceOpen, runnerpayload.UISurfaceOpenParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) UpdateSurface(ctx context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceFrameRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, capabilities, err := s.persistentUITarget(ctx, source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !capabilities.PersistentSurfaces {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUISurfaceFrame, runnerpayload.UISurfaceFrameParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) CloseSurface(ctx context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceCloseRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, capabilities, err := s.persistentUITarget(ctx, source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !capabilities.PersistentSurfaces {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUISurfaceClose, runnerpayload.UISurfaceCloseParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

// CleanupExtensionUI is best-effort. Runner-proxied widgets are conversation-scoped
// and remain owned by the control plane after a top-level run ends.
func (s *Service) CleanupExtensionUI(extensions.UIExtensionOwner) {}

// ExtensionUIHostCapabilities reports the persistent UI features available to
// the client attached to the active runner run.
func (s *Service) ExtensionUIHostCapabilities(ctx context.Context) extensions.ExtensionUIHostCapabilities {
	_, _, capabilities, err := s.uiTarget(ctx)
	if err != nil {
		return extensions.ExtensionUIHostCapabilities{}
	}
	return extensions.ExtensionUIHostCapabilities{
		Widgets:    capabilities.PersistentWidgets,
		Surfaces:   capabilities.PersistentSurfaces,
		Transcript: capabilities.PersistentSurfaces,
	}
}

func (s *Service) uiTarget(ctx context.Context) (Peer, string, protocol.ClientCapabilities, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peer == nil {
		return nil, "", protocol.ClientCapabilities{}, errors.New("runner control connection is unavailable")
	}
	runID := runnerRunIDFromContext(ctx)
	if runID == "" && len(s.runs) == 1 {
		for candidate := range s.runs {
			runID = candidate
		}
	}
	run := s.runs[runID]
	if run == nil || run.closing {
		return nil, "", protocol.ClientCapabilities{}, errors.New("runner has no active UI run")
	}
	return s.peer, run.id, run.clientCaps, nil
}

func (s *Service) persistentUITarget(ctx context.Context, source extensions.UIExtensionSource) (Peer, string, runnerpayload.ExtensionOwner, protocol.ClientCapabilities, error) {
	if source == nil {
		return nil, "", runnerpayload.ExtensionOwner{}, protocol.ClientCapabilities{}, errors.New("extension UI source is required")
	}
	owner := source.ExtensionUIOwner()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peer == nil {
		return nil, "", runnerpayload.ExtensionOwner{}, protocol.ClientCapabilities{}, errors.New("runner control connection is unavailable")
	}
	runID := runnerRunIDFromContext(ctx)
	if runID == "" && len(s.runs) == 1 {
		for candidate := range s.runs {
			runID = candidate
		}
	}
	if run := s.runs[runID]; run != nil && !run.closing {
		return s.peer, run.id, runnerpayload.ExtensionOwner{ExtensionID: owner.ExtensionID, Generation: owner.Generation}, run.clientCaps, nil
	}
	resources := s.backgroundRunIDs[runID]
	if resources == nil || len(resources.leases) == 0 {
		return nil, "", runnerpayload.ExtensionOwner{}, protocol.ClientCapabilities{}, errors.New("runner has no active persistent UI scope")
	}
	targetRunID := resources.lastRunID
	if resources.attachedRunID != "" {
		targetRunID = resources.attachedRunID
	}
	if targetRunID == "" {
		return nil, "", runnerpayload.ExtensionOwner{}, protocol.ClientCapabilities{}, errors.New("runner background scope has no UI route")
	}
	return s.peer, targetRunID, runnerpayload.ExtensionOwner{ExtensionID: owner.ExtensionID, Generation: owner.Generation}, resources.clientCaps, nil
}

func interactiveUIOwner(ctx context.Context) runnerpayload.ExtensionOwner {
	owner, ok := extensions.UIExtensionOwnerFromContext(ctx)
	if !ok {
		return runnerpayload.ExtensionOwner{}
	}
	return runnerpayload.ExtensionOwner{ExtensionID: owner.ExtensionID, Generation: owner.Generation}
}

func scopedInteractiveUIRequestID(owner runnerpayload.ExtensionOwner, requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		requestID = extensions.NewUIInputRequestID()
	}
	if strings.TrimSpace(owner.ExtensionID) == "" || owner.Generation == 0 {
		return requestID
	}
	payload := owner.ExtensionID + "\x00" + strconv.FormatUint(owner.Generation, 10) + "\x00" + requestID
	return "runner-ui-" + base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func (s *Service) notifySurfaceInput(ctx context.Context, params runnerpayload.UISurfaceInputParams) error {
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return err
	}
	defer finish()
	if run.runtime == nil {
		return errors.New("runner extension runtime is unavailable")
	}
	return run.runtime.NotifySurfaceInput(operationCtx, extensions.UIExtensionOwner{
		ExtensionID: params.Owner.ExtensionID,
		Generation:  params.Owner.Generation,
	}, params.Lifecycle, params.Request)
}

func (s *Service) notifySurfaceResize(ctx context.Context, params runnerpayload.UISurfaceResizeParams) error {
	run, operationCtx, finish, err := s.beginRunOperation(ctx, params.RunID)
	if err != nil {
		return err
	}
	defer finish()
	if run.runtime == nil {
		return errors.New("runner extension runtime is unavailable")
	}
	return run.runtime.NotifySurfaceResize(operationCtx, extensions.UIExtensionOwner{
		ExtensionID: params.Owner.ExtensionID,
		Generation:  params.Owner.Generation,
	}, params.Lifecycle, params.Request)
}
