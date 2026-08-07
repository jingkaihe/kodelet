package client

import (
	"context"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

func (s *Service) Input(ctx context.Context, request extensions.UIInputRequest) (extensions.UIInputResponse, error) {
	peer, runID, capabilities, err := s.uiTarget()
	if err != nil {
		return extensions.UIInputResponse{}, err
	}
	if !capabilities.InteractiveUI {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui input is not available"}, nil
	}
	var response extensions.UIInputResponse
	err = peer.Call(ctx, protocol.MethodUIInput, protocol.UIInputParams{RunID: runID, Request: request}, &response)
	return response, err
}

func (s *Service) Confirm(ctx context.Context, request extensions.UIConfirmRequest) (extensions.UIInputResponse, error) {
	peer, runID, capabilities, err := s.uiTarget()
	if err != nil {
		return extensions.UIInputResponse{}, err
	}
	if !capabilities.InteractiveUI {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui confirmation is not available"}, nil
	}
	var response extensions.UIInputResponse
	err = peer.Call(ctx, protocol.MethodUIConfirm, protocol.UIConfirmParams{RunID: runID, Request: request}, &response)
	return response, err
}

func (s *Service) Select(ctx context.Context, request extensions.UISelectRequest) (extensions.UIInputResponse, error) {
	peer, runID, capabilities, err := s.uiTarget()
	if err != nil {
		return extensions.UIInputResponse{}, err
	}
	if !capabilities.InteractiveUI {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui selection is not available"}, nil
	}
	var response extensions.UIInputResponse
	err = peer.Call(ctx, protocol.MethodUISelect, protocol.UISelectParams{RunID: runID, Request: request}, &response)
	return response, err
}

func (s *Service) Notify(ctx context.Context, request extensions.UINotifyRequest) (extensions.UIInputResponse, error) {
	peer, runID, capabilities, err := s.uiTarget()
	if err != nil {
		return extensions.UIInputResponse{}, err
	}
	if !capabilities.InteractiveUI {
		return extensions.UIInputResponse{Status: extensions.UIInputStatusUnavailable, Reason: "client ui notification is not available"}, nil
	}
	var response extensions.UIInputResponse
	err = peer.Call(ctx, protocol.MethodUINotify, protocol.UINotifyParams{RunID: runID, Request: request}, &response)
	return response, err
}

func (s *Service) SetWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetSetRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, available, err := s.persistentUITarget(source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !available {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUIWidgetSet, protocol.UIWidgetSetParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) UpdateWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetFrameRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, available, err := s.persistentUITarget(source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !available {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUIWidgetFrame, protocol.UIWidgetFrameParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) RemoveWidget(ctx context.Context, source extensions.UIExtensionSource, request extensions.UIWidgetRemoveRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, available, err := s.persistentUITarget(source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !available {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUIWidgetRemove, protocol.UIWidgetRemoveParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) AppendTranscript(ctx context.Context, source extensions.UIExtensionSource, request extensions.UITranscriptAppendRequest) (extensions.UITranscriptAppendResponse, error) {
	peer, runID, owner, available, err := s.persistentUITarget(source)
	if err != nil {
		return extensions.UITranscriptAppendResponse{}, err
	}
	if !available {
		return extensions.UITranscriptAppendResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UITranscriptAppendResponse
	err = peer.Call(ctx, protocol.MethodUITranscriptAppend, protocol.UITranscriptAppendParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) OpenSurface(ctx context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceOpenRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, available, err := s.persistentUITarget(source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !available {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUISurfaceOpen, protocol.UISurfaceOpenParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) UpdateSurface(ctx context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceFrameRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, available, err := s.persistentUITarget(source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !available {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUISurfaceFrame, protocol.UISurfaceFrameParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

func (s *Service) CloseSurface(ctx context.Context, source extensions.UIExtensionSource, request extensions.UISurfaceCloseRequest) (extensions.UIFrameResponse, error) {
	peer, runID, owner, available, err := s.persistentUITarget(source)
	if err != nil {
		return extensions.UIFrameResponse{}, err
	}
	if !available {
		return extensions.UIFrameResponse{Reason: "client persistent extension surfaces are not available"}, nil
	}
	var response extensions.UIFrameResponse
	err = peer.Call(ctx, protocol.MethodUISurfaceClose, protocol.UISurfaceCloseParams{RunID: runID, Owner: owner, Request: request}, &response)
	return response, err
}

// CleanupExtensionUI is best-effort; the control plane also closes run-scoped UI when the run ends.
func (s *Service) CleanupExtensionUI(extensions.UIExtensionOwner) {}

func (s *Service) uiTarget() (Peer, string, protocol.ClientCapabilities, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peer == nil {
		return nil, "", protocol.ClientCapabilities{}, errors.New("runner control connection is unavailable")
	}
	if s.active == nil || s.active.closing {
		return nil, "", protocol.ClientCapabilities{}, errors.New("runner has no active UI run")
	}
	return s.peer, s.active.id, s.active.clientCaps, nil
}

func (s *Service) persistentUITarget(source extensions.UIExtensionSource) (Peer, string, protocol.ExtensionOwner, bool, error) {
	if source == nil {
		return nil, "", protocol.ExtensionOwner{}, false, errors.New("extension UI source is required")
	}
	peer, runID, capabilities, err := s.uiTarget()
	if err != nil {
		return nil, "", protocol.ExtensionOwner{}, false, err
	}
	owner := source.ExtensionUIOwner()
	return peer, runID, protocol.ExtensionOwner{ExtensionID: owner.ExtensionID, Generation: owner.Generation}, capabilities.PersistentSurfaces, nil
}

func (s *Service) notifySurfaceInput(ctx context.Context, params protocol.UISurfaceInputParams) error {
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

func (s *Service) notifySurfaceResize(ctx context.Context, params protocol.UISurfaceResizeParams) error {
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
