package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

type workspaceRunnerTarget struct {
	Runner             runnerregistry.Runner
	CWD                string
	Profile            string
	EnvironmentProfile string
	ExtensionProfile   string
}

type workspaceRunnerTargetError struct {
	status  int
	message string
	err     error
}

// Discard only unsent drafts. Use the same admission guard as conversation deletion
// so a first turn cannot start while its workspace resources are being removed.
func (s *Server) handleDiscardDraftWorkspace(w http.ResponseWriter, r *http.Request) {
	conversationID := r.URL.Query().Get("conversationId")
	runnerID := strings.TrimSpace(r.URL.Query().Get("runnerId"))
	if !validReceiptID(conversationID) || runnerID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "runnerId and a valid conversationId are required", nil)
		return
	}
	if s.conversationService == nil || s.runnerRegistry == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "conversation workspace is unavailable", nil)
		return
	}
	if !s.reserveConversationDeletion(conversationID) {
		s.writeErrorResponse(w, http.StatusConflict, "conversation is running or being removed", nil)
		return
	}
	defer s.releaseConversationDeletion(conversationID)
	if _, err := s.conversationService.GetConversation(r.Context(), conversationID); !errors.Is(err, convtypes.ErrConversationNotFound) {
		if err != nil {
			s.writeErrorResponse(w, http.StatusInternalServerError, "failed to check draft conversation", err)
		} else {
			s.writeErrorResponse(w, http.StatusConflict, "saved conversations cannot be discarded as drafts", nil)
		}
		return
	}
	if _, reserved, err := s.runnerRegistry.ResolveConversationAffinity(r.Context(), conversationID); err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "failed to check conversation runner", err)
		return
	} else if reserved {
		s.writeErrorResponse(w, http.StatusConflict, "conversation already has a runner assignment", nil)
		return
	}
	runner, found := s.runnerRegistry.Runner(runnerID)
	if !found {
		s.writeErrorResponse(w, http.StatusNotFound, "runner not found", nil)
		return
	}
	if err := s.runnerRegistry.ValidateRunnerCall(runnerID, runner.Generation, protocol.MethodWorkspaceSessionsDiscard); err != nil {
		if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
			s.writeErrorResponse(w, http.StatusNotImplemented, "runner does not support draft workspace cleanup; upgrade the runner", nil)
		} else {
			s.writeErrorResponse(w, http.StatusServiceUnavailable, "runner workspace cleanup is unavailable", err)
		}
		return
	}
	// Once accepted, navigating away must not interrupt subprocess cleanup.
	ctx, cancel := context.WithTimeout(s.chatExecutionContext(r.Context()), time.Minute)
	defer cancel()
	if err := s.runnerRegistry.CallRunner(ctx, runnerID, runner.Generation, protocol.MethodWorkspaceSessionsDiscard,
		protocol.WorkspaceSessionsDiscardParams{ConversationID: conversationID}, nil); err != nil {
		s.writeErrorResponse(w, http.StatusBadGateway, "could not clean up draft workspace sessions", err)
		return
	}
	s.browserMu.Lock()
	for _, handle := range s.browserHandles {
		if handle.runnerID == runnerID && handle.generation == runner.Generation && handle.conversationID == conversationID {
			s.removeBrowserHandleLocked(handle)
		}
	}
	s.browserMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// Heartbeats only renew an existing draft lease. The runner owns expiry and
// promotion to a started conversation; stale heartbeats cannot create sessions
// or turn a promoted session back into a draft.
func (s *Server) handleDraftWorkspaceHeartbeat(w http.ResponseWriter, r *http.Request) {
	conversationID := r.URL.Query().Get("conversationId")
	runnerID := strings.TrimSpace(r.URL.Query().Get("runnerId"))
	if !validReceiptID(conversationID) || runnerID == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "runnerId and a valid conversationId are required", nil)
		return
	}
	if s.runnerRegistry == nil {
		s.writeErrorResponse(w, http.StatusServiceUnavailable, "runner registry is unavailable", nil)
		return
	}
	runner, found := s.runnerRegistry.Runner(runnerID)
	if !found {
		s.writeErrorResponse(w, http.StatusNotFound, "runner not found", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	err := s.runnerRegistry.CallRunner(ctx, runnerID, runner.Generation, protocol.MethodWorkspaceSessionsHeartbeat,
		protocol.WorkspaceSessionsHeartbeatParams{ConversationID: conversationID}, nil)
	if err != nil {
		var rpcErr *protocol.RPCError
		switch {
		case errors.As(err, &rpcErr) && rpcErr.Reason() == protocol.ErrorReasonWorkspaceSessionDiscarded:
			s.writeErrorResponse(w, http.StatusGone, "draft workspace sessions have expired or were discarded", nil)
		case errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported):
			s.writeErrorResponse(w, http.StatusNotImplemented, "runner does not support draft workspace leases; upgrade the runner", nil)
		default:
			s.writeErrorResponse(w, http.StatusBadGateway, "could not renew draft workspace sessions", err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resolveWorkspaceRunnerTarget(r *http.Request) (*workspaceRunnerTarget, *workspaceRunnerTargetError) {
	target, err := s.resolveRunnerTarget(r)
	if err != nil {
		return target, err
	}
	if target.CWD != "" && target.CWD != target.Runner.Workspace.Path && !target.Runner.WorkspaceCWD {
		return nil, &workspaceRunnerTargetError{status: http.StatusNotImplemented, message: "runner does not support selected-directory workspace tools; upgrade the runner"}
	}
	return target, nil
}

// Terminals and browsers use the draft's eventual conversation ID. Saved
// conversations must use their persisted workspace; drafts resolve on the runner.
// The boolean distinguishes saved workspaces from unresolved draft paths.
func (s *Server) resolveConversationWorkspaceTarget(r *http.Request) (*workspaceRunnerTarget, bool, *workspaceRunnerTargetError) {
	conversationID := r.URL.Query().Get("conversationId")
	if !validReceiptID(conversationID) {
		return nil, false, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "a valid conversationId is required for workspace sessions"}
	}
	if s.runnerRegistry == nil || s.conversationService == nil {
		return nil, false, &workspaceRunnerTargetError{status: http.StatusServiceUnavailable, message: "conversation workspace is unavailable"}
	}
	affinity, found, err := s.runnerRegistry.ResolveConversationAffinity(r.Context(), conversationID)
	if err != nil {
		return nil, false, &workspaceRunnerTargetError{status: http.StatusInternalServerError, message: "failed to resolve conversation runner", err: err}
	}
	if _, err := s.conversationService.GetConversation(r.Context(), conversationID); !errors.Is(err, convtypes.ErrConversationNotFound) {
		if err != nil {
			return nil, false, &workspaceRunnerTargetError{status: http.StatusInternalServerError, message: "failed to load workspace conversation", err: err}
		}
		target, targetErr := s.resolveWorkspaceRunnerTarget(r)
		return target, true, targetErr
	}
	check := r.Clone(r.Context())
	query := r.URL.Query()
	query.Del("conversationId")
	if found {
		if runnerID := strings.TrimSpace(query.Get("runnerId")); runnerID != "" && runnerID != affinity.RunnerID {
			return nil, false, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "the runner differs from the conversation's reserved runner"}
		}
		query.Set("runnerId", affinity.RunnerID)
	}
	check.URL = &url.URL{RawQuery: query.Encode()}
	target, targetErr := s.resolveWorkspaceRunnerTarget(check)
	if targetErr != nil {
		return nil, false, targetErr
	}
	if target.CWD == "" {
		target.CWD = target.Runner.Workspace.Path
	}
	return target, false, nil
}

func (s *Server) resolveRunnerTarget(r *http.Request) (*workspaceRunnerTarget, *workspaceRunnerTargetError) {
	return s.resolveRunnerDiscoveryTarget(r, false)
}

func (s *Server) resolveRunnerDiscoveryTarget(r *http.Request, discoverProfiles bool) (*workspaceRunnerTarget, *workspaceRunnerTargetError) {
	if r == nil {
		return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "invalid workspace target", err: errors.New("request is required")}
	}
	runnerID := strings.TrimSpace(r.URL.Query().Get("runnerId"))
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
	if runnerID == "" && conversationID == "" {
		status := s.EmbeddedRunnerStatus()
		if !status.Ready || status.RunnerID == "" {
			return nil, &workspaceRunnerTargetError{status: http.StatusServiceUnavailable, message: "the default runner is unavailable; check /api/status and the server logs, or select another runner with runnerId"}
		}
		runnerID = status.RunnerID
	}
	cwd := strings.TrimSpace(r.URL.Query().Get("cwd"))
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))
	if conversationID == "" && profile == "" {
		profile = getCurrentWebUIProfile()
	}
	environmentProfile := chat.NormalizeEnvironmentProfile(r.URL.Query().Get("environmentProfile"))
	extensionProfile := false
	if s == nil || s.runnerRegistry == nil {
		return nil, &workspaceRunnerTargetError{status: http.StatusServiceUnavailable, message: "runner registry is unavailable"}
	}

	if conversationID != "" {
		affinity, found, err := s.runnerRegistry.ResolveConversationAffinity(r.Context(), conversationID)
		if err != nil {
			return nil, &workspaceRunnerTargetError{status: http.StatusInternalServerError, message: "failed to resolve conversation runner", err: err}
		}
		if found {
			if runnerID != "" && runnerID != affinity.RunnerID {
				return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "invalid workspace target", err: errors.New("the runner differs from the conversation's saved runner")}
			}
			runnerID = affinity.RunnerID
			if r.URL.Query().Has("environmentProfile") && environmentProfile != affinity.EnvironmentProfile {
				return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "the runner profile differs from the conversation's saved profile"}
			}
			environmentProfile = affinity.EnvironmentProfile
			if s.conversationService == nil {
				return nil, &workspaceRunnerTargetError{status: http.StatusServiceUnavailable, message: "conversation store is unavailable"}
			}
			record, err := s.conversationService.GetConversation(r.Context(), conversationID)
			if err != nil {
				return nil, &workspaceRunnerTargetError{status: http.StatusNotFound, message: "conversation not found", err: err}
			}
			snapshot, hasSnapshot, err := conversations.ConfigSnapshotFromMetadata(record.Metadata)
			if err != nil {
				return nil, &workspaceRunnerTargetError{status: http.StatusInternalServerError, message: "failed to load the conversation's saved settings", err: err}
			}
			storedProfile, hasStoredProfile := record.Metadata["profile"].(string)
			if hasSnapshot {
				storedProfile = snapshot.Profile
				extensionProfile = snapshot.ExtensionProfile
			} else if !hasStoredProfile {
				storedProfile = getCurrentWebUIProfile()
			}
			storedProfile = strings.TrimSpace(storedProfile)
			if profile != "" && profile != storedProfile {
				return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "the model profile differs from the conversation's saved profile"}
			}
			profile = storedProfile
			if hasSnapshot && !extensionProfile && s.missingEmbeddedModelProfile(runnerID, storedProfile) {
				profile = ""
			}
			if strings.TrimSpace(record.CWD) == "" {
				return nil, &workspaceRunnerTargetError{status: http.StatusConflict, message: "the conversation has no saved working directory on its runner"}
			}
			if cwd != "" && cwd != record.CWD {
				return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "cwd differs from the conversation's saved working directory"}
			}
			cwd = record.CWD
		} else {
			return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "conversation has no remote runner workspace"}
		}
	}

	runner, found := s.runnerRegistry.Runner(runnerID)
	if !found {
		return nil, &workspaceRunnerTargetError{status: http.StatusNotFound, message: "runner not found"}
	}
	if !runner.Connected {
		return nil, &workspaceRunnerTargetError{status: http.StatusServiceUnavailable, message: "runner is offline"}
	}
	if conversationID == "" && chat.NormalizeRequestedProfile(profile) != "" && !llm.HasConfiguredProfile(profile) {
		if !discoverProfiles {
			if _, err := s.resolveModelProfile(r.Context(), runnerID, profile, ""); err != nil {
				return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "could not resolve model profile", err: err}
			}
		}
		extensionProfile = true
	}
	target := &workspaceRunnerTarget{Runner: runner, CWD: cwd, Profile: profile, EnvironmentProfile: environmentProfile}
	if extensionProfile {
		// Registered model profiles have no runner-local environment overlay.
		target.ExtensionProfile, target.Profile = profile, ""
	}
	return target, nil
}

// Missing profiles may fall back only for validated saved snapshots, matching
// chat.ResolveConfigForExistingConversation. Standalone runners own their policy.
func (s *Server) missingEmbeddedModelProfile(runnerID, profile string) bool {
	profile = chat.NormalizeRequestedProfile(profile)
	if profile == "" {
		return false
	}
	status := s.EmbeddedRunnerStatus()
	return status.Enabled && status.RunnerID == runnerID && !llm.HasConfiguredProfile(profile)
}

func (s *Server) handleRunnerDiscovery(w http.ResponseWriter, r *http.Request, method string) {
	var options *llmtypes.ExecutionOptions
	if values, present := r.URL.Query()["options"]; present {
		if method != protocol.MethodWorkspaceDiscover {
			s.writeErrorResponse(w, http.StatusBadRequest, "options are only supported for command discovery", nil)
			return
		}
		if len(values) != 1 || len(values[0]) > 16*1024 {
			s.writeErrorResponse(w, http.StatusBadRequest, "discovery options must be one JSON object of at most 16 KiB", nil)
			return
		}
		options = &llmtypes.ExecutionOptions{}
		if err := json.Unmarshal([]byte(values[0]), options); err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "invalid discovery options", err)
			return
		}
		if err := (protocol.WorkspaceDiscoverParams{Options: options}).Validate(); err != nil {
			s.writeErrorResponse(w, http.StatusBadRequest, "invalid discovery options", err)
			return
		}
	}
	target, targetErr := s.resolveRunnerDiscoveryTarget(r, method == protocol.MethodWorkspaceDiscover)
	if targetErr != nil {
		s.writeWorkspaceRunnerTargetError(w, targetErr)
		return
	}
	if method == protocol.MethodWorkspaceDiscover && r.URL.Query().Get("conversationId") != "" {
		run, manifest, active, err := s.pinnedWorkspaceRun(target, r.URL.Query().Get("conversationId"))
		if err != nil {
			s.writeErrorResponse(w, http.StatusConflict, "could not load the conversation's available commands and tools", err)
			return
		}
		if active {
			// Never start a second runtime or ignore stricter restrictions
			// when discovering the immutable active environment.
			if err := validatePinnedDiscoveryOptions(options, manifest); err != nil {
				s.writeErrorResponse(w, http.StatusConflict, "permissions cannot be changed while this conversation is running", err)
				return
			}
			digest, err := runnerpayload.ComputeDiscoveryDigest(manifest)
			if err != nil {
				s.writeErrorResponse(w, http.StatusInternalServerError, "could not load the conversation's available commands and tools", err)
				return
			}
			s.writeJSONResponse(w, protocol.WorkspaceDiscoverResult{RunID: run.ID, CWD: manifest.WorkingDirectory, EnvironmentProfile: target.EnvironmentProfile, Digest: digest, Commands: manifest.Commands, Shortcuts: manifest.Shortcuts, ExtensionCount: manifest.ExtensionCount})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result any
	var err error
	if method == protocol.MethodWorkspaceDiscover {
		result, err = s.discoverWorkspace(ctx, target.Runner, protocol.WorkspaceDiscoverParams{
			CWD: target.CWD, Profile: target.Profile, EnvironmentProfile: target.EnvironmentProfile, Options: options,
		})
		if err == nil && target.ExtensionProfile != "" {
			if _, err := s.resolveModelProfile(ctx, target.Runner.ID, target.ExtensionProfile, ""); err != nil {
				s.writeErrorResponse(w, http.StatusBadRequest, "could not resolve model profile", err)
				return
			}
		}
	} else {
		result = &protocol.WorkspaceCWDHintsResult{}
		params := protocol.WorkspaceCWDHintsParams{CWD: target.CWD, Profile: target.Profile, EnvironmentProfile: target.EnvironmentProfile, Query: r.URL.Query().Get("q")}
		err = s.runnerRegistry.CallRunner(ctx, target.Runner.ID, target.Runner.Generation, method, params, result)
	}
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
			status = http.StatusNotImplemented
		}
		s.writeErrorResponse(w, status, "could not load the runner's available commands and tools", err)
		return
	}
	s.writeJSONResponse(w, result)
}

func (s *Server) discoverWorkspace(ctx context.Context, runner runnerregistry.Runner, params protocol.WorkspaceDiscoverParams) (protocol.WorkspaceDiscoverResult, error) {
	if err := params.Validate(); err != nil {
		return protocol.WorkspaceDiscoverResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result runnerpayload.WorkspaceDiscoverResult
	if err := s.runnerRegistry.CallRunner(ctx, runner.ID, runner.Generation, protocol.MethodWorkspaceDiscover, params, &result); err != nil {
		return protocol.WorkspaceDiscoverResult{}, err
	}
	// The authenticated runner supplies definitions; the requesting caller owns
	// the registration. Never borrow another caller's cached profiles.
	if err := s.registerExtensionProfiles(ctx, runnerpayload.Manifest{
		RunnerID: runner.ID, Generation: runner.Generation, Profiles: result.Profiles,
	}); err != nil {
		return protocol.WorkspaceDiscoverResult{}, err
	}
	return result.WorkspaceDiscoverResult, nil
}

func (s *Server) writeWorkspaceRunnerTargetError(w http.ResponseWriter, targetErr *workspaceRunnerTargetError) {
	if targetErr == nil {
		return
	}
	s.writeErrorResponse(w, targetErr.status, targetErr.message, targetErr.err)
}
