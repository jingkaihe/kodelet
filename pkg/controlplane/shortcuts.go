package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

// pinnedWorkspaceRun only reads the selected runner's current leases. A stale
// caller never falls back to another run or to a new idle environment.
func (s *Server) pinnedWorkspaceRun(target *workspaceRunnerTarget, conversationID string) (runnerregistry.Run, runnerpayload.Manifest, bool, error) {
	if conversationID == "" {
		return runnerregistry.Run{}, runnerpayload.Manifest{}, false, nil
	}
	for _, runID := range target.Runner.ActiveRunIDs {
		run, found := s.runnerRegistry.Run(runID)
		if !found || run.ConversationID != conversationID {
			continue
		}
		if run.Status != runnerregistry.RunStatusRunning || run.ManifestJSON == "" {
			return run, runnerpayload.Manifest{}, true, errors.New("conversation environment is not ready; refresh discovery")
		}
		var manifest runnerpayload.Manifest
		err := json.Unmarshal([]byte(run.ManifestJSON), &manifest)
		return run, manifest, true, err
	}
	return runnerregistry.Run{}, runnerpayload.Manifest{}, false, nil
}

func (s *Server) handleWorkspaceShortcut(w http.ResponseWriter, r *http.Request) {
	var request chat.WorkspaceShortcutRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
	if err := decoder.Decode(&request); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid shortcut request", err)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		s.writeErrorResponse(w, http.StatusBadRequest, "shortcut requires a single JSON object", err)
		return
	}
	clientID := strings.TrimSpace(r.Header.Get(chat.ClientIDHeader))
	if clientID == "" || len(clientID) > 256 {
		s.writeErrorResponse(w, http.StatusBadRequest, "shortcut requires a client identity", nil)
		return
	}
	query := r.URL.Query()
	query.Set("runnerId", request.Target.RunnerID)
	query.Set("conversationId", request.Target.ConversationID)
	query.Set("cwd", request.Target.CWD)
	if request.Target.EnvironmentProfile != "" {
		query.Set("environmentProfile", request.Target.EnvironmentProfile)
	}
	targetRequest := r.Clone(r.Context())
	targetRequest.URL.RawQuery = query.Encode()
	target, targetErr := s.resolveRunnerTarget(targetRequest)
	if targetErr != nil {
		s.writeWorkspaceRunnerTargetError(w, targetErr)
		return
	}
	if target == nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "shortcut runner is required", nil)
		return
	}
	activeRun, manifest, active, err := s.pinnedWorkspaceRun(target, request.Target.ConversationID)
	if err != nil || (active && request.RunID != activeRun.ID) || (!active && request.RunID != "") {
		s.writeErrorResponse(w, http.StatusConflict, "shortcut run changed; refresh discovery", err)
		return
	}
	if !active && s.isActiveChat(request.Target.ConversationID) {
		s.writeErrorResponse(w, http.StatusConflict, "conversation environment is opening or closing; refresh discovery", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if active {
		if err := validatePinnedDiscoveryOptions(request.Target.Options, manifest); err != nil {
			s.writeErrorResponse(w, http.StatusConflict, "active environment restrictions are pinned", err)
			return
		}
		broker := s.uiInputBrokerForRun(request.Target.ConversationID)
		if broker == nil {
			s.writeErrorResponse(w, http.StatusConflict, "shortcut requires an active UI owner", nil)
			return
		}
		finish, err := broker.beginOwnedShortcut(ctx, clientID, cancel)
		if err != nil {
			s.writeErrorResponse(w, http.StatusConflict, err.Error(), nil)
			return
		}
		defer finish()
		if err := validateShortcutManifest(request, manifest, true); err != nil {
			s.writeErrorResponse(w, http.StatusConflict, "shortcut discovery changed", err)
			return
		}
	}
	sink, err := newNDJSONEventSink(w)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "shortcut stream unavailable", err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	var result runnerpayload.ShortcutExecuteResult
	defer func() {
		if err != nil {
			_ = sink.Send(chat.ChatEvent{Kind: "error", Error: err.Error()})
		} else {
			_ = sink.Send(chat.ChatEvent{Kind: "shortcut-result", Content: result})
		}
		_ = sink.Send(chat.ChatEvent{Kind: "done"})
	}()
	if !active {
		// A distinct scope cannot attach to, cancel, or retain another
		// conversation's background workers. No conversation record is created.
		scopeID := convtypes.GenerateID()
		runID := convtypes.GenerateID()
		uiRun := newActiveChatRun(cancel)
		uiRun.uiInput = newWebUIInputBroker(scopeID, sink)
		caps := parseClientUICapabilities(r.Header.Get(chat.UICapabilitiesHeader))
		detach := uiRun.uiInput.setOwner(withNativeCapabilities(ctx, &caps), clientID, sink)
		defer detach()
		defer uiRun.uiInput.close()
		if !s.registerActiveChat(scopeID, uiRun) {
			err = errors.New("shortcut environment unavailable")
			return
		}
		defer s.unregisterActiveChat(scopeID, uiRun)
		defer s.runnerRegistry.ReleasePendingConversationAffinity(scopeID)
		manifest, err = s.runnerRegistry.OpenRun(ctx, target.Runner.ID, protocol.RunOpenParams{
			RunID: runID, ConversationID: scopeID, CWD: target.CWD, ExpectedCWD: target.CWD,
			Agent:              protocol.AgentDescriptor{EnvironmentProfile: target.EnvironmentProfile},
			Options:            request.Target.Options.Clone(),
			ClientCapabilities: protocol.ClientCapabilities{InteractiveUI: caps.InteractiveUI, PersistentWidgets: caps.PersistentWidgets, PersistentSurfaces: caps.PersistentSurfaces},
		})
		if err != nil {
			return
		}
		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cleanupCancel()
			cancelErr := s.runnerRegistry.CancelRun(cleanupCtx, runID, "temporary shortcut environment finished")
			closeErr := s.runnerRegistry.CloseRun(cleanupCtx, runID, runnerregistry.RunStatusSucceeded, err)
			if err == nil {
				if cancelErr != nil {
					err = errors.Wrap(cancelErr, "shortcut finished but temporary resources could not be cancelled")
				} else if closeErr != nil {
					err = errors.Wrap(closeErr, "shortcut finished but temporary environment could not be closed")
				}
			}
		}()
		if err = validateShortcutManifest(request, manifest, false); err != nil {
			return
		}
		request.RunID = runID
		for _, shortcut := range manifest.Shortcuts {
			if shortcut.Key == request.Shortcut.Key && shortcut.ExtensionID == request.Shortcut.ExtensionID {
				request.Shortcut = shortcut
				break
			}
		}
	}
	err = s.runnerRegistry.CallRun(ctx, request.RunID, protocol.MethodShortcutExecute, runnerpayload.ShortcutExecuteParams{
		RunID: request.RunID, Digest: manifest.Digest, Shortcut: request.Shortcut,
	}, &result)
	if err == nil {
		err = ctx.Err()
	}
}

// Active environments are immutable. Accept equivalent or weaker requests
// without widening host policy; reject new restrictions rather than silently
// invoking extensions under broader settings than the caller requested.
func validatePinnedDiscoveryOptions(options *llmtypes.ExecutionOptions, manifest runnerpayload.Manifest) error {
	host := llmtypes.Config{ExecutionOptions: manifest.Config.Options, EnableFSSearchTools: manifest.Config.EnableFSSearchTools}
	effective, err := llmtypes.ApplyEnvironmentOptions(host, options)
	if err != nil {
		return err
	}
	base, narrowed := host.EnvironmentOptions(), effective.EnvironmentOptions()
	for _, value := range []*llmtypes.ExecutionOptions{base, narrowed} {
		for _, field := range []**bool{&value.NoTools, &value.NoExtensions, &value.NoSkills} {
			if *field != nil && !**field {
				*field = nil
			}
		}
		value.EnableFSSearchTools = nil
	}
	if !reflect.DeepEqual(base, narrowed) || host.EnableFSSearchTools != effective.EnableFSSearchTools {
		return errors.New("new restrictions require a new execution; active discovery uses the pinned environment")
	}
	return nil
}

func validateShortcutManifest(request chat.WorkspaceShortcutRequest, manifest runnerpayload.Manifest, active bool) error {
	if request.Digest != manifest.Digest {
		return errors.New("shortcut environment changed; refresh discovery")
	}
	for _, shortcut := range manifest.Shortcuts {
		if shortcut.Key == request.Shortcut.Key && shortcut.ExtensionID == request.Shortcut.ExtensionID && shortcut.Generation != 0 && (!active || shortcut.Generation == request.Shortcut.Generation) {
			return nil
		}
	}
	return errors.New("shortcut registration changed; refresh discovery")
}

func (b *webUIInputBroker) beginOwnedShortcut(ctx context.Context, clientID string, cancel context.CancelFunc) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.owner == nil || b.owner.ctx.Err() != nil || b.owner.clientID != clientID || ctx.Err() != nil {
		return nil, errors.New("shortcut requires the current UI owner; take control first")
	}
	if len(b.shortcutCancels) >= 8 {
		return nil, errors.New("too many active shortcut requests")
	}
	if b.shortcutCancels == nil {
		b.shortcutCancels = make(map[string]context.CancelFunc)
	}
	id := extensions.NewUIInputRequestID()
	b.shortcutCancels[id] = cancel
	return func() { b.mu.Lock(); delete(b.shortcutCancels, id); b.mu.Unlock() }, nil
}
