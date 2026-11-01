package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/internal/jsonrpc"
)

// Metadata names an extension. An empty Name defaults to the extension ID
// assigned by Kodelet.
type Metadata struct {
	Name    string
	Version string
}

// Extension holds an extension's registrations. It is safe to serve one
// Extension from multiple runtimes, for example across inline SDK sessions;
// state captured by handlers is shared by all of them.
type Extension struct {
	mu        sync.RWMutex
	metadata  Metadata
	profiles  []ProfileDeclaration
	tools     []*registeredTool
	commands  []*registeredCommand
	shortcuts []*registeredShortcut
	handlers  []*registeredHandler
	names     map[string]struct{}
}

type registeredTool struct {
	declaration ToolDeclaration
	schema      *inputSchema
	execute     func(context.Context, json.RawMessage, *ToolContext) (ToolResult, error)
}

type registeredCommand struct {
	declaration CommandDeclaration
	names       []string
	execute     func(context.Context, json.RawMessage, *CommandContext) (CommandResult, error)
}

type registeredShortcut struct {
	key      string
	shortcut Shortcut
}

type registeredHandler struct {
	event    string
	priority int
	timeout  *float64
	order    int
	invoke   func(context.Context, map[string]any, *EventContext) (EventResult, error)
}

// New creates an extension.
func New(metadata Metadata) *Extension {
	return &Extension{metadata: metadata, names: make(map[string]struct{})}
}

// SetMetadata replaces the non-empty metadata fields.
func (e *Extension) SetMetadata(metadata Metadata) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if metadata.Name != "" {
		e.metadata.Name = metadata.Name
	}
	if metadata.Version != "" {
		e.metadata.Version = metadata.Version
	}
}

// ToolHandler executes a tool with validated, decoded input.
type ToolHandler[In any] func(ctx context.Context, input In, tc *ToolContext) (ToolResult, error)

// Tool describes a model-callable tool whose input decodes into In.
type Tool[In any] struct {
	Name        string
	Description string
	// InputSchema optionally replaces the schema inferred from In. A raw schema
	// is advertised unchanged and input is decoded into In without validation.
	InputSchema map[string]any
	// OutputSchema optionally describes StructuredContent, not presentation data.
	OutputSchema map[string]any
	// Group is the catalog group; the host defaults it to the extension.
	Group string
	// Short is a one-line summary for compact code-mode listings.
	Short string
	// Timeout overrides the host timeout; NoTimeout disables it.
	Timeout time.Duration
	Execute ToolHandler[In]
}

// RegisterTool registers a tool. Without an explicit InputSchema, the schema is
// inferred from In; map[string]any, json.RawMessage, and any accept any object.
// It panics if the registration is invalid or the name is already registered.
func RegisterTool[In any](e *Extension, tool Tool[In]) {
	if tool.Name == "" {
		panic("extension: tool name is required")
	}
	if tool.Execute == nil {
		panic(fmt.Sprintf("extension: tool %q has no Execute handler", tool.Name))
	}
	var schema *inputSchema
	inputType := reflect.TypeFor[In]()
	switch {
	case tool.InputSchema != nil:
		schema = &inputSchema{document: mustCloneMap(tool.InputSchema)}
	case isRawInputType(inputType):
		schema = &inputSchema{document: map[string]any{"type": "object", "additionalProperties": true}}
	default:
		inferred, err := inferInputSchema(inputType)
		if err != nil {
			panic(fmt.Sprintf("extension: tool %q input schema: %v", tool.Name, err))
		}
		schema = inferred
	}
	handler := tool.Execute
	registered := &registeredTool{
		declaration: ToolDeclaration{
			Name:         tool.Name,
			Description:  tool.Description,
			OutputSchema: mustCloneMap(tool.OutputSchema),
			Group:        tool.Group,
			Short:        tool.Short,
			TimeoutInSec: timeoutSeconds(tool.Timeout),
		},
		schema: schema,
		execute: func(ctx context.Context, raw json.RawMessage, tc *ToolContext) (ToolResult, error) {
			if err := schema.validate(raw); err != nil {
				return ToolResult{}, fmt.Errorf("invalid input for tool %s: %w", tool.Name, err)
			}
			input, err := decodeInput[In](raw)
			if err != nil {
				return ToolResult{}, fmt.Errorf("invalid input for tool %s: %w", tool.Name, err)
			}
			return handler(ctx, input, tc)
		},
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if slices.ContainsFunc(e.tools, func(existing *registeredTool) bool { return existing.declaration.Name == tool.Name }) {
		panic("extension: duplicate tool registration: " + tool.Name)
	}
	e.tools = append(e.tools, registered)
}

// CommandKind distinguishes ordinary commands from dynamic recipes.
type CommandKind string

// Command kinds.
const (
	CommandKindCommand CommandKind = "command"
	CommandKindRecipe  CommandKind = "recipe"
)

// CommandHandler executes a command. A zero CommandResult passes the prompt on.
type CommandHandler[In any] func(ctx context.Context, input In, cc *CommandContext) (CommandResult, error)

// Command describes a slash command whose structured input decodes into In.
type Command[In any] struct {
	Name        string
	Aliases     []string
	Description string
	Kind        CommandKind
	// Timeout overrides the host timeout; NoTimeout disables it.
	Timeout time.Duration
	Execute CommandHandler[In]
}

// RegisterCommand registers a command. For struct inputs, a schema is inferred
// and input that fails validation passes the prompt to the next route. Use
// map[string]any to receive the raw input without advertising a schema.
// It panics if the registration is invalid or any name is already registered.
func RegisterCommand[In any](e *Extension, command Command[In]) {
	primary := normalizeCommandName(command.Name)
	if primary == "" {
		panic("extension: command name is required")
	}
	if command.Execute == nil {
		panic(fmt.Sprintf("extension: command %q has no Execute handler", command.Name))
	}
	names := []string{primary}
	for _, alias := range command.Aliases {
		if name := normalizeCommandName(alias); name != "" && name != primary {
			if slices.Contains(names, name) {
				panic("extension: duplicate command registration: " + command.Name)
			}
			names = append(names, name)
		}
	}
	var schema *inputSchema
	inputType := reflect.TypeFor[In]()
	if !isRawInputType(inputType) {
		inferred, err := inferInputSchema(inputType)
		if err != nil {
			panic(fmt.Sprintf("extension: command %q input schema: %v", command.Name, err))
		}
		schema = inferred
	}
	handler := command.Execute
	registered := &registeredCommand{
		declaration: CommandDeclaration{
			Name:         command.Name,
			Aliases:      slices.Clone(command.Aliases),
			Description:  command.Description,
			Kind:         command.Kind,
			TimeoutInSec: timeoutSeconds(command.Timeout),
		},
		names: names,
		execute: func(ctx context.Context, raw json.RawMessage, cc *CommandContext) (CommandResult, error) {
			if !jsonrpc.HasValue(raw) {
				raw = json.RawMessage("{}")
			}
			if err := schema.validate(raw); err != nil {
				return Pass(), nil
			}
			input, err := decodeInput[In](raw)
			if err != nil {
				return Pass(), nil
			}
			return handler(ctx, input, cc)
		},
	}
	if schema != nil {
		registered.declaration.InputSchema = schema.document
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, name := range names {
		if _, exists := e.names[name]; exists {
			panic("extension: duplicate command registration: " + name)
		}
	}
	for _, name := range names {
		e.names[name] = struct{}{}
	}
	e.commands = append(e.commands, registered)
}

// ShortcutHandler runs a native TUI shortcut. Return Submit to send a message,
// or the zero ShortcutResult for no follow-up.
type ShortcutHandler func(ctx context.Context, sc *ShortcutContext) (ShortcutResult, error)

// Shortcut describes a native TUI shortcut.
type Shortcut struct {
	Description string
	Handler     ShortcutHandler
}

// RegisterShortcut registers a shortcut such as "ctrl+g", "alt+1",
// "ctrl+alt+k", or "f5". It panics if the key is unsupported or duplicated.
func (e *Extension) RegisterShortcut(key string, shortcut Shortcut) {
	normalized, err := normalizeShortcutKey(key)
	if err != nil {
		panic("extension: " + err.Error())
	}
	if shortcut.Handler == nil {
		panic(fmt.Sprintf("extension: shortcut %q has no handler", normalized))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if slices.ContainsFunc(e.shortcuts, func(existing *registeredShortcut) bool { return existing.key == normalized }) {
		panic("extension: duplicate shortcut registration: " + normalized)
	}
	e.shortcuts = append(e.shortcuts, &registeredShortcut{key: normalized, shortcut: shortcut})
}

// ProfileRegistration declares a self-contained model profile. Settings holds
// additional native snake_case configuration such as max_tokens,
// reasoning_effort, or an openai/anthropic block. Registration grants no
// credentials; the daemon resolves and authorizes profiles.
type ProfileRegistration struct {
	Name     string
	Provider string
	Model    string
	// Hidden hides the profile from ordinary pickers; it is not access control.
	Hidden   bool
	Settings map[string]any
}

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// RegisterProfile registers a profile and returns its name for use in
// agent.SessionOptions.Profile. Hosts must advertise remote profile support.
// It panics if the registration is invalid or duplicated.
func (e *Extension) RegisterProfile(profile ProfileRegistration) string {
	options, err := profileOptions(profile)
	if err != nil {
		panic("extension: " + err.Error())
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if slices.ContainsFunc(e.profiles, func(existing ProfileDeclaration) bool { return existing.Name == profile.Name }) {
		panic("extension: duplicate profile registration: " + profile.Name)
	}
	e.profiles = append(e.profiles, ProfileDeclaration{Name: profile.Name, Options: options, Hidden: profile.Hidden})
	return profile.Name
}

func profileOptions(profile ProfileRegistration) (map[string]any, error) {
	if !profileNamePattern.MatchString(profile.Name) {
		return nil, errors.New("profile names must be ASCII slugs matching [A-Za-z0-9][A-Za-z0-9._-]* (1-128 characters)")
	}
	if profile.Provider != "openai" && profile.Provider != "anthropic" {
		return nil, fmt.Errorf("profile %s provider must be openai or anthropic", profile.Name)
	}
	if strings.TrimSpace(profile.Model) == "" || strings.ContainsRune(profile.Model, 0) {
		return nil, fmt.Errorf("profile %s model must be nonempty and contain no NUL", profile.Name)
	}
	options, err := cloneJSON(profile.Settings)
	if err != nil {
		return nil, fmt.Errorf("profile %s settings must be JSON: %w", profile.Name, err)
	}
	if options == nil {
		options = map[string]any{}
	}
	for _, reserved := range []string{"name", "hidden", "provider", "model"} {
		if _, exists := options[reserved]; exists {
			return nil, fmt.Errorf("profile %s settings must not set %q; use the ProfileRegistration field", profile.Name, reserved)
		}
	}
	options["provider"] = profile.Provider
	options["model"] = profile.Model
	return options, nil
}

func (e *Extension) initialize(params InitializeParams) (InitializeResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.profiles) > 0 && !capabilityTrue(params.Capabilities, "profiles", "remote") {
		return InitializeResult{}, errors.New("remote extension profiles are not supported by this host; update the Kodelet daemon and runner (capabilities.profiles.remote is required)")
	}
	name := e.metadata.Name
	if name == "" {
		name = params.Extension.ID
	}
	result := InitializeResult{
		Name:          name,
		Version:       e.metadata.Version,
		Tools:         make([]ToolDeclaration, 0, len(e.tools)),
		Commands:      make([]CommandDeclaration, 0, len(e.commands)),
		Shortcuts:     make([]ShortcutDeclaration, 0, len(e.shortcuts)),
		Subscriptions: e.subscriptions(),
	}
	for _, profile := range e.profiles {
		profile.Options = mustCloneMap(profile.Options)
		result.Profiles = append(result.Profiles, profile)
	}
	for _, tool := range e.tools {
		declaration := tool.declaration
		declaration.InputSchema = mustCloneMap(tool.schema.document)
		declaration.OutputSchema = mustCloneMap(declaration.OutputSchema)
		result.Tools = append(result.Tools, declaration)
	}
	for _, command := range e.commands {
		declaration := command.declaration
		declaration.InputSchema = mustCloneMap(declaration.InputSchema)
		result.Commands = append(result.Commands, declaration)
	}
	for _, shortcut := range e.shortcuts {
		result.Shortcuts = append(result.Shortcuts, ShortcutDeclaration{Key: shortcut.key, Description: shortcut.shortcut.Description})
	}
	return result, nil
}

func (e *Extension) subscriptions() []SubscriptionDeclaration {
	subscriptions := make([]SubscriptionDeclaration, 0)
	index := make(map[string]int)
	for _, handler := range e.handlers {
		position, exists := index[handler.event]
		if !exists {
			index[handler.event] = len(subscriptions)
			subscriptions = append(subscriptions, SubscriptionDeclaration{Event: handler.event, Priority: handler.priority, TimeoutInSec: handler.timeout})
			continue
		}
		existing := &subscriptions[position]
		existing.Priority = max(existing.Priority, handler.priority)
		existing.TimeoutInSec = mergeTimeout(existing.TimeoutInSec, handler.timeout)
	}
	return subscriptions
}

func (e *Extension) executeTool(ctx context.Context, env *callEnv, params executeToolParams) (ToolResult, error) {
	e.mu.RLock()
	index := slices.IndexFunc(e.tools, func(tool *registeredTool) bool { return tool.declaration.Name == params.Name })
	var tool *registeredTool
	if index >= 0 {
		tool = e.tools[index]
	}
	e.mu.RUnlock()
	if tool == nil {
		return ToolResult{}, fmt.Errorf("unknown extension tool: %s", params.Name)
	}
	return tool.execute(ctx, params.Input, &ToolContext{CallContext: env.callContext(params.Context)})
}

func (e *Extension) executeCommand(ctx context.Context, env *callEnv, params executeCommandParams) (CommandResult, error) {
	name := normalizeCommandName(params.Name)
	e.mu.RLock()
	var command *registeredCommand
	for _, candidate := range e.commands {
		if slices.Contains(candidate.names, name) {
			command = candidate
			break
		}
	}
	e.mu.RUnlock()
	if command == nil {
		return CommandResult{}, fmt.Errorf("unknown extension command: %s", params.Name)
	}
	result, err := command.execute(ctx, params.Input, &CommandContext{CallContext: env.callContext(params.Context), Invocation: params.Invocation})
	if err != nil {
		return CommandResult{}, err
	}
	if result.Action == "" {
		result.Action = ActionPass
	}
	return result, nil
}

// executeShortcut runs a shortcut; a zero result means no follow-up.
func (e *Extension) executeShortcut(ctx context.Context, env *callEnv, params executeShortcutParams) (ShortcutResult, error) {
	key, err := normalizeShortcutKey(params.Key)
	if err != nil {
		return ShortcutResult{}, err
	}
	e.mu.RLock()
	index := slices.IndexFunc(e.shortcuts, func(shortcut *registeredShortcut) bool { return shortcut.key == key })
	var shortcut *registeredShortcut
	if index >= 0 {
		shortcut = e.shortcuts[index]
	}
	e.mu.RUnlock()
	if shortcut == nil {
		return ShortcutResult{}, fmt.Errorf("unknown extension shortcut: %s", key)
	}
	result, err := shortcut.shortcut.Handler(ctx, &ShortcutContext{CallContext: env.callContext(params.Context)})
	if err != nil {
		return ShortcutResult{}, err
	}
	if result.Action != "" && !capabilityTrue(env.capabilities(), "shortcuts", "submit") {
		return ShortcutResult{}, errors.New("shortcut submit results are not supported by this host")
	}
	return result, nil
}

func (e *Extension) handleEvent(ctx context.Context, env *callEnv, params handleEventParams) (*EventResult, error) {
	e.mu.RLock()
	handlers := make([]*registeredHandler, 0, len(e.handlers))
	for _, handler := range e.handlers {
		if handler.event == params.Event {
			handlers = append(handlers, handler)
		}
	}
	e.mu.RUnlock()
	slices.SortStableFunc(handlers, func(a, b *registeredHandler) int {
		if a.priority != b.priority {
			return b.priority - a.priority
		}
		return a.order - b.order
	})

	payload := map[string]any{}
	if jsonrpc.HasValue(params.Payload) {
		if err := unmarshalPreservingNumbers(params.Payload, &payload); err != nil {
			return nil, fmt.Errorf("invalid payload for event %s: %w", params.Event, err)
		}
		if payload == nil {
			payload = map[string]any{}
		}
	}
	payload["id"] = params.ID
	payload["event"] = params.Event
	ec := &EventContext{CallContext: env.callContext(params.Context)}
	aggregate := &EventResult{}
	for _, handler := range handlers {
		result, err := handler.invoke(ctx, payload, ec)
		if err != nil {
			return nil, err
		}
		if result.Input != nil {
			input, err := toJSONValue(result.Input)
			if err != nil {
				return nil, fmt.Errorf("invalid input result for event %s: %w", params.Event, err)
			}
			aggregate.Input = input
			setNestedToolField(payload, "input", input)
		}
		if result.Output != nil {
			output, err := toJSONValue(result.Output)
			if err != nil {
				return nil, fmt.Errorf("invalid output result for event %s: %w", params.Event, err)
			}
			if record, ok := output.(map[string]any); ok && (params.Event == EventToolResult.name || params.Event == EventToolUpdate.name) {
				// A redactor may replace display output while copying the original
				// result. Clear its machine copy before subsequent hooks run.
				copied := make(map[string]any, len(record))
				for key, value := range record {
					if key != "data" {
						copied[key] = value
					}
				}
				output = copied
			}
			aggregate.Output = output
			setNestedToolField(payload, "output", output)
		}
		if result.Message != nil {
			aggregate.Message = result.Message
		}
		if result.SystemPrompt != nil {
			aggregate.SystemPrompt = result.SystemPrompt
		}
		if result.Tools != nil {
			aggregate.Tools = &ToolListPatch{
				Disable: append(slices.Clone(toolPatchField(aggregate.Tools, true)), result.Tools.Disable...),
				Enable:  append(slices.Clone(toolPatchField(aggregate.Tools, false)), result.Tools.Enable...),
			}
		}
		if result.FollowUpMessages != nil {
			aggregate.FollowUpMessages = append(aggregate.FollowUpMessages, result.FollowUpMessages...)
		}
		if result.Resources != nil {
			aggregate.Resources = result.Resources
		}
		if result.Block != nil {
			aggregate.Block = result.Block
			return aggregate, nil
		}
	}
	return aggregate, nil
}

func toolPatchField(patch *ToolListPatch, disable bool) []string {
	if patch == nil {
		return nil
	}
	if disable {
		return patch.Disable
	}
	return patch.Enable
}

func setNestedToolField(payload map[string]any, field string, value any) {
	if tool, ok := payload["tool"].(map[string]any); ok {
		tool[field] = value
	}
}

func toJSONValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	err = unmarshalPreservingNumbers(data, &decoded)
	return decoded, err
}

// unmarshalPreservingNumbers decodes like json.Unmarshal but keeps numbers as
// json.Number, so integers above 2^53 survive a round trip through any.
func unmarshalPreservingNumbers(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid data after the top-level JSON value")
	}
	return nil
}

func normalizeCommandName(name string) string {
	return strings.TrimLeft(strings.TrimSpace(name), "/")
}
