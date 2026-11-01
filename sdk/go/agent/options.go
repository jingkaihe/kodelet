package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

// Ptr returns a pointer to v, for optional ExecutionOptions fields.
func Ptr[T any](v T) *T {
	return &v
}

// ClientOptions configures a Client.
type ClientOptions struct {
	// Server and Runner select an explicit daemon endpoint and registered
	// runner. Empty values use the CLI's normal configuration.
	Server string
	Runner string
	// Command is the Kodelet executable. It defaults to "kodelet".
	Command string
	// CWD is the default session working directory on the runner. It defaults
	// to the current directory.
	CWD string
	// Env adds environment variables for spawned kodelet processes.
	Env map[string]string
}

// SessionOptions configures CreateSession.
type SessionOptions struct {
	// Options are credential-free execution overrides for the session.
	Options ExecutionOptions
	// Profile names a daemon or extension-registered model profile.
	Profile string
	// EnvironmentProfile selects a runner environment profile.
	EnvironmentProfile string
	// Extensions are inline extensions hosted in this process. They are
	// identified as inline-1, inline-2, and so on, in order.
	Extensions []*extension.Extension
	// UI handles inline extension input, confirm, select, and notify requests
	// locally. A nil handler reports that interaction as unavailable.
	UI *UIHandlers
	// CWD is the runner working directory; it defaults to the client CWD.
	CWD string
	// Resume continues an existing conversation.
	Resume string
	// ParentConversationID makes a new conversation a child of another. It
	// cannot be combined with Resume.
	ParentConversationID string
}

// RunOptions configures one run.
type RunOptions struct {
	Message string
	// Images are data URLs (data:image/png;base64,...) or image URIs.
	Images []string
}

// UIHandlers serve basic UI requests from inline extension callbacks.
type UIHandlers struct {
	Input   func(ctx context.Context, request extension.InputRequest) (value string, ok bool, err error)
	Confirm func(ctx context.Context, request extension.ConfirmRequest) (bool, error)
	Select  func(ctx context.Context, request extension.SelectRequest) (value string, ok bool, err error)
	Notify  func(ctx context.Context, request extension.NotifyRequest) error
}

// ExecutionOptions are credential-free overrides for daemon-backed sessions.
// Unset pointer and slice fields keep the daemon defaults; set values,
// including false, zero, and empty slices, are sent explicitly.
type ExecutionOptions struct {
	// Provider is "openai" or "anthropic".
	Provider             string
	Model                string
	WeakModel            string
	MaxTokens            *int
	WeakModelMaxTokens   *int
	ThinkingBudgetTokens *int
	ReasoningEffort      string
	MaxTurns             *int
	UseWeakModel         *bool
	NoTools              *bool
	NoExtensions         *bool
	NoSkills             *bool
	AllowedTools         []string
	AllowedCommands      []string
	EnableFSSearchTools  *bool
	// CodeMode is "off", "on", or "only" for this run, instead of the runner's
	// configured code_mode.
	CodeMode string
}

func (o ExecutionOptions) validate() error {
	var problems []string
	if o.Provider != "" && o.Provider != "openai" && o.Provider != "anthropic" {
		problems = append(problems, "provider must be openai or anthropic")
	}
	positive := func(name string, value *int) {
		if value != nil && *value <= 0 {
			problems = append(problems, name+" must be positive")
		}
	}
	nonNegative := func(name string, value *int) {
		if value != nil && *value < 0 {
			problems = append(problems, name+" must not be negative")
		}
	}
	positive("maxTokens", o.MaxTokens)
	positive("weakModelMaxTokens", o.WeakModelMaxTokens)
	nonNegative("thinkingBudgetTokens", o.ThinkingBudgetTokens)
	nonNegative("maxTurns", o.MaxTurns)
	for name, values := range map[string][]string{"allowedTools": o.AllowedTools, "allowedCommands": o.AllowedCommands} {
		for _, value := range values {
			if value == "" {
				problems = append(problems, name+" must not contain empty names")
				break
			}
		}
	}
	if o.CodeMode != "" && o.CodeMode != "off" && o.CodeMode != "on" && o.CodeMode != "only" {
		problems = append(problems, "codeMode must be off, on, or only")
	}
	if len(problems) > 0 {
		return errors.New("invalid execution options: " + strings.Join(problems, "; "))
	}
	return nil
}

// args encodes options as kodelet acp flags, preserving explicit false and
// empty values.
func (o ExecutionOptions) args() []string {
	var args []string
	text := func(flag, value string) {
		if value != "" {
			args = append(args, "--"+flag+"="+value)
		}
	}
	number := func(flag string, value *int) {
		if value != nil {
			args = append(args, "--"+flag+"="+strconv.Itoa(*value))
		}
	}
	boolean := func(flag string, value *bool) {
		if value != nil {
			args = append(args, "--"+flag+"="+strconv.FormatBool(*value))
		}
	}
	list := func(flag string, values []string) {
		if values == nil {
			return
		}
		quoted := make([]string, len(values))
		for i, value := range values {
			quoted[i] = `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
		}
		args = append(args, "--"+flag+"="+strings.Join(quoted, ","))
	}
	text("provider", o.Provider)
	text("model", o.Model)
	text("weak-model", o.WeakModel)
	number("max-tokens", o.MaxTokens)
	number("weak-model-max-tokens", o.WeakModelMaxTokens)
	number("thinking-budget-tokens", o.ThinkingBudgetTokens)
	text("reasoning-effort", o.ReasoningEffort)
	number("max-turns", o.MaxTurns)
	boolean("use-weak-model", o.UseWeakModel)
	boolean("no-tools", o.NoTools)
	boolean("no-extensions", o.NoExtensions)
	boolean("no-skills", o.NoSkills)
	list("allowed-tools", o.AllowedTools)
	list("allowed-commands", o.AllowedCommands)
	boolean("enable-fs-search-tools", o.EnableFSSearchTools)
	text("code-mode", o.CodeMode)
	return args
}

func (o SessionOptions) validate() error {
	if o.ParentConversationID != "" {
		if strings.TrimSpace(o.ParentConversationID) == "" {
			return errors.New("parent conversation ID must be a non-empty conversation ID")
		}
		if o.Resume != "" {
			return errors.New("parent conversation ID cannot be combined with resume; existing conversations retain their parent")
		}
	}
	for i, ext := range o.Extensions {
		if ext == nil {
			return fmt.Errorf("extension %d is nil", i+1)
		}
	}
	return o.Options.validate()
}
