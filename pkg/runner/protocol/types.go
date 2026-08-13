// Package protocol defines the versioned JSON-RPC protocol shared by Kodelet
// control planes and workspace-bound runners.
package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pkg/errors"
)

const (
	// Version is the initial runner application protocol version.
	Version = 1
	// JSONRPCVersion is the JSON-RPC wire version used by the runner protocol.
	JSONRPCVersion = "2.0"
	// Subprotocol is required during the WebSocket upgrade.
	Subprotocol = "kodelet.runner.v1.jsonrpc"
	// Endpoint is the control-plane WebSocket endpoint used by runners.
	Endpoint = "/api/runner/v1/connect"
)

const (
	MethodRunnerRegister        = "runner.register"
	MethodRunnerHeartbeat       = "runner.heartbeat"
	MethodRunnerManifestChanged = "runner.manifestChanged"
	MethodRunnerGoodbye         = "runner.goodbye"
	MethodRunOpen               = "run.open"
	MethodRunClose              = "run.close"
	MethodRunCancel             = "run.cancel"
	MethodRunEnvironmentError   = "run.environmentError"
	MethodCommandExecute        = "command.execute"
	MethodLifecycleDispatch     = "lifecycle.dispatch"
	MethodToolExecute           = "tool.execute"
	MethodToolUpdate            = "tool.update"
	MethodUIInput               = "ui.input"
	MethodUIConfirm             = "ui.confirm"
	MethodUISelect              = "ui.select"
	MethodUINotify              = "ui.notify"
	MethodUIWidgetSet           = "ui.widget.set"
	MethodUIWidgetFrame         = "ui.widget.frame"
	MethodUIWidgetRemove        = "ui.widget.remove"
	MethodUITranscriptAppend    = "ui.transcript.append"
	MethodUISurfaceOpen         = "ui.surface.open"
	MethodUISurfaceFrame        = "ui.surface.frame"
	MethodUISurfaceClose        = "ui.surface.close"
	MethodUISurfaceInput        = "ui.surface.input"
	MethodUISurfaceResize       = "ui.surface.resize"
	MethodOperationCancel       = "operation.cancel"
)

const (
	ErrorCodeParseError     = -32700
	ErrorCodeInvalidRequest = -32600
	ErrorCodeMethodNotFound = -32601
	ErrorCodeInvalidParams  = -32602
	ErrorCodeInternal       = -32603
	ErrorCodeUnavailable    = -32000
	ErrorCodeConflict       = -32001
	ErrorCodeStale          = -32002
	ErrorCodeBusy           = -32003
)

const (
	ErrorReasonRunnerNotFound        = "runner_not_found"
	ErrorReasonRunNotActive          = "run_not_active"
	ErrorReasonResultTooLarge        = "result_too_large"
	ErrorReasonLegacyAuthKeyEnrolled = "legacy_auth_key_enrolled"
)

// RPCErrorData carries stable machine-readable error details.
type RPCErrorData struct {
	Reason string `json:"reason,omitempty"`
}

// Message is the small JSON-native envelope used for requests, responses, and notifications.
// Runner request identifiers are strings with an origin-specific prefix.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *string         `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("runner rpc error %d: %s", e.Code, e.Message)
}

// Reason returns the stable machine-readable reason from an RPC error.
func (e *RPCError) Reason() string {
	if e == nil {
		return ""
	}
	switch data := e.Data.(type) {
	case RPCErrorData:
		return strings.TrimSpace(data.Reason)
	case *RPCErrorData:
		if data != nil {
			return strings.TrimSpace(data.Reason)
		}
	case map[string]any:
		reason, _ := data["reason"].(string)
		return strings.TrimSpace(reason)
	case json.RawMessage:
		var decoded RPCErrorData
		if json.Unmarshal(data, &decoded) == nil {
			return strings.TrimSpace(decoded.Reason)
		}
	}
	return ""
}

// DecodeMessage decodes and validates one complete WebSocket text frame.
func DecodeMessage(payload []byte) (Message, error) {
	var message Message
	if err := json.Unmarshal(payload, &message); err != nil {
		return Message{}, errors.Wrap(err, "failed to decode runner rpc message")
	}
	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// Validate checks the JSON-RPC envelope shape used by this first-party protocol.
func (m Message) Validate() error {
	if m.JSONRPC != JSONRPCVersion {
		return errors.Errorf("unsupported jsonrpc version %q", m.JSONRPC)
	}
	if m.ID != nil && strings.TrimSpace(*m.ID) == "" {
		return errors.New("rpc id must not be empty")
	}
	if strings.TrimSpace(m.Method) != "" {
		if len(m.Result) > 0 || m.Error != nil {
			return errors.New("rpc request cannot contain result or error")
		}
		return nil
	}
	if m.ID == nil {
		return errors.New("rpc response id is required")
	}
	if len(m.Result) > 0 && m.Error != nil {
		return errors.New("rpc response cannot contain both result and error")
	}
	if len(m.Result) == 0 && m.Error == nil {
		return errors.New("rpc response must contain result or error")
	}
	return nil
}

// Host describes one stable runner installation and its mutable display metadata.
type Host struct {
	InstanceID string `json:"instanceId"`
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	PID        int    `json:"pid,omitempty"`
}

// Workspace describes the one canonical workspace bound to a runner process.
type Workspace struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// RunnerCapabilities declares optional behavior supported by this runner process.
type RunnerCapabilities struct {
	ConcurrentRuns bool `json:"concurrentRuns,omitempty"`
}

// RegisterParams is the first request sent by a runner connection.
type RegisterParams struct {
	ProtocolVersions []int              `json:"protocolVersions"`
	RunnerID         string             `json:"runnerId,omitempty"`
	DisplayName      string             `json:"displayName,omitempty"`
	Host             Host               `json:"host"`
	Workspace        Workspace          `json:"workspace"`
	Capabilities     RunnerCapabilities `json:"capabilities,omitempty"`
	KodeletVersion   string             `json:"kodeletVersion"`
	ManifestDigest   string             `json:"manifestDigest,omitempty"`
}

// Validate checks registration identity and version negotiation fields.
func (p RegisterParams) Validate() error {
	if !SupportsVersion(p.ProtocolVersions, Version) {
		return errors.Errorf("runner does not support protocol version %d", Version)
	}
	if strings.TrimSpace(p.Host.InstanceID) == "" {
		return errors.New("host.instanceId is required")
	}
	if strings.TrimSpace(p.Workspace.Path) == "" {
		return errors.New("workspace.path is required")
	}
	if strings.TrimSpace(p.Workspace.Name) == "" {
		return errors.New("workspace.name is required")
	}
	return nil
}

// SupportsVersion reports whether a peer advertised a protocol version.
func SupportsVersion(versions []int, version int) bool {
	for _, candidate := range versions {
		if candidate == version {
			return true
		}
	}
	return false
}

// RegisterResult establishes the stable runner ID and live connection generation.
type RegisterResult struct {
	RunnerID            string `json:"runnerId"`
	ProtocolVersion     int    `json:"protocolVersion"`
	ConnectionID        string `json:"connectionId"`
	Generation          int64  `json:"generation"`
	HeartbeatIntervalMS int64  `json:"heartbeatIntervalMs"`
}

// RunnerState is the application-level availability reported by heartbeats.
type RunnerState string

const (
	RunnerStateIdle     RunnerState = "idle"
	RunnerStateRunning  RunnerState = "running"
	RunnerStateStopping RunnerState = "stopping"
	RunnerStateError    RunnerState = "error"
)

// RunStatus is the durable-shape state machine shared by remote environments and the control plane.
type RunStatus string

const (
	RunStatusOpening   RunStatus = "opening"
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCanceled  RunStatus = "canceled"
	RunStatusLost      RunStatus = "lost"
)

// HeartbeatParams reports application health separately from WebSocket liveness.
type HeartbeatParams struct {
	RunnerID       string      `json:"runnerId"`
	Generation     int64       `json:"generation"`
	State          RunnerState `json:"state"`
	ActiveRunID    string      `json:"activeRunId,omitempty"`
	ActiveRunIDs   []string    `json:"activeRunIds,omitempty"`
	ManifestDigest string      `json:"manifestDigest,omitempty"`
}

// Validate checks heartbeat identity and application state fields.
func (p HeartbeatParams) Validate() error {
	if strings.TrimSpace(p.RunnerID) == "" {
		return errors.New("runnerId is required")
	}
	if p.Generation <= 0 {
		return errors.New("generation must be positive")
	}
	switch p.State {
	case RunnerStateIdle, RunnerStateRunning, RunnerStateStopping, RunnerStateError:
	default:
		return errors.Errorf("unsupported runner state %q", p.State)
	}
	_, err := p.NormalizedActiveRunIDs()
	return err
}

// NormalizedActiveRunIDs returns the deterministic active-run set advertised by a heartbeat.
// ActiveRunID remains accepted for compatibility with singular-run runner clients.
func (p HeartbeatParams) NormalizedActiveRunIDs() ([]string, error) {
	seen := make(map[string]struct{}, len(p.ActiveRunIDs)+1)
	result := make([]string, 0, len(p.ActiveRunIDs)+1)
	for _, raw := range p.ActiveRunIDs {
		runID := strings.TrimSpace(raw)
		if runID == "" {
			return nil, errors.New("activeRunIds must not contain empty values")
		}
		if _, exists := seen[runID]; exists {
			return nil, errors.Errorf("activeRunIds contains duplicate run %s", runID)
		}
		seen[runID] = struct{}{}
		result = append(result, runID)
	}
	if runID := strings.TrimSpace(p.ActiveRunID); runID != "" {
		if _, exists := seen[runID]; !exists {
			if len(result) > 0 {
				return nil, errors.New("activeRunId does not match activeRunIds")
			}
			result = append(result, runID)
		}
	}
	sort.Strings(result)
	return result, nil
}

// ManifestChangedParams reports an idle-manifest digest transition.
type ManifestChangedParams struct {
	RunnerID       string `json:"runnerId"`
	Generation     int64  `json:"generation"`
	ManifestDigest string `json:"manifestDigest"`
}

// GoodbyeParams reports an intentional runner disconnect.
type GoodbyeParams struct {
	RunnerID   string `json:"runnerId"`
	Generation int64  `json:"generation"`
	Reason     string `json:"reason,omitempty"`
}

// AgentDescriptor carries only provider-sensitive identifiers needed by runner resources.
type AgentDescriptor struct {
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	Profile            string `json:"profile,omitempty"`
	EnvironmentProfile string `json:"environmentProfile,omitempty"`
	RecipeName         string `json:"recipeName,omitempty"`
	InvokedBy          string `json:"invokedBy,omitempty"`
}

// ClientCapabilities describes the interactive client attached to a run.
type ClientCapabilities struct {
	InteractiveUI      bool `json:"interactiveUI"`
	PersistentSurfaces bool `json:"persistentSurfaces"`
}

// RunOpenParams asks a runner to pin one environment snapshot.
type RunOpenParams struct {
	RunID              string             `json:"runId"`
	ConversationID     string             `json:"conversationId"`
	Agent              AgentDescriptor    `json:"agent"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ReservedToolNames  []string           `json:"reservedToolNames"`
}

func (p RunOpenParams) Validate() error {
	if strings.TrimSpace(p.RunID) == "" {
		return errors.New("runId is required")
	}
	if strings.TrimSpace(p.ConversationID) == "" {
		return errors.New("conversationId is required")
	}
	return nil
}

// RunCloseParams releases a pinned run environment.
type RunCloseParams struct {
	RunID string `json:"runId"`
}

// RunCancelParams cancels active runner operations for one run.
type RunCancelParams struct {
	RunID  string `json:"runId"`
	Reason string `json:"reason,omitempty"`
}

// EnvironmentErrorParams reports a runner-side asynchronous run failure.
type EnvironmentErrorParams struct {
	RunID   string `json:"runId"`
	Message string `json:"message"`
}

// OperationCancelParams cancels one in-flight JSON-RPC request by its wire ID.
type OperationCancelParams struct {
	RequestID string `json:"requestId"`
}
