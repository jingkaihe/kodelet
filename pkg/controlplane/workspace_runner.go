package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
)

type workspaceRunnerTarget struct {
	Runner             runnerregistry.Runner
	CWD                string
	Profile            string
	EnvironmentProfile string
}

type workspaceRunnerTargetError struct {
	status  int
	message string
	err     error
}

func (s *Server) resolveWorkspaceRunnerTarget(r *http.Request) (*workspaceRunnerTarget, *workspaceRunnerTargetError) {
	target, err := s.resolveRunnerTarget(r)
	if err != nil {
		return target, err
	}
	if r.URL.Query().Get("conversationId") == "" && r.URL.Query().Get("cwd") != "" {
		return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "cwd is not accepted for runner-wide workspace tools; select a conversation"}
	}
	if target.CWD != "" && target.CWD != target.Runner.Workspace.Path && !target.Runner.WorkspaceCWD {
		return nil, &workspaceRunnerTargetError{status: http.StatusNotImplemented, message: "runner does not support conversation-directory workspace tools; upgrade the runner"}
	}
	return target, nil
}

func (s *Server) resolveRunnerTarget(r *http.Request) (*workspaceRunnerTarget, *workspaceRunnerTargetError) {
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
	if strings.EqualFold(profile, "default") {
		profile = "default"
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
				storedProfile, hasStoredProfile = snapshot.Profile, true
				extensionProfile = snapshot.ExtensionProfile
			}
			storedProfile = strings.TrimSpace(storedProfile)
			// An empty persisted profile means the base configuration, not the
			// daemon's current active profile. Only legacy records without any
			// stored profile continue to inherit the daemon default.
			if hasStoredProfile && (storedProfile == "" || strings.EqualFold(storedProfile, "default")) {
				storedProfile = "default"
			}
			if profile != "" && profile != storedProfile {
				return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "the model profile differs from the conversation's saved profile"}
			}
			profile = storedProfile
			if hasSnapshot && s.missingEmbeddedModelProfile(runnerID, storedProfile) {
				profile = "default"
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
		if principal, ok := principalFromContext(r.Context()); ok {
			registered, found := s.registeredProfile(extensionProfileKey{principal.ID, runnerID, profile}, runner.Generation)
			if found {
				if _, err := chat.ResolveExtensionProfile(registered, ""); err != nil {
					return nil, &workspaceRunnerTargetError{status: http.StatusBadRequest, message: "could not resolve extension profile", err: err}
				}
				extensionProfile = true
			}
		}
	}
	if extensionProfile {
		// Registered model profiles have no runner-local environment overlay.
		profile = "default"
	}
	return &workspaceRunnerTarget{Runner: runner, CWD: cwd, Profile: profile, EnvironmentProfile: environmentProfile}, nil
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
	target, targetErr := s.resolveRunnerTarget(r)
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
	var result any
	var params any
	if method == protocol.MethodWorkspaceDiscover {
		result = &protocol.WorkspaceDiscoverResult{}
		params = protocol.WorkspaceDiscoverParams{CWD: target.CWD, Profile: target.Profile, EnvironmentProfile: target.EnvironmentProfile, Options: options}
	} else {
		result = &protocol.WorkspaceCWDHintsResult{}
		params = protocol.WorkspaceCWDHintsParams{CWD: target.CWD, Profile: target.Profile, EnvironmentProfile: target.EnvironmentProfile, Query: r.URL.Query().Get("q")}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.runnerRegistry.CallRunner(ctx, target.Runner.ID, target.Runner.Generation, method, params, result); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
			status = http.StatusNotImplemented
		}
		s.writeErrorResponse(w, status, "could not load the runner's available commands and tools", err)
		return
	}
	s.writeJSONResponse(w, result)
}

func (s *Server) writeWorkspaceRunnerTargetError(w http.ResponseWriter, targetErr *workspaceRunnerTargetError) {
	if targetErr == nil {
		return
	}
	s.writeErrorResponse(w, targetErr.status, targetErr.message, targetErr.err)
}
