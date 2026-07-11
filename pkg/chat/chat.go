// Package chat implements shared persisted chat orchestration for Kodelet UIs.
package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	conversationservice "github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/llm"
	llmbase "github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/tools"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

// ChatRequest is the payload for a streamed chat turn.
type ChatRequest struct {
	Message        string             `json:"message"`
	Content        []ChatContentBlock `json:"content,omitempty"`
	ConversationID string             `json:"conversationId,omitempty"`
	Profile        string             `json:"profile,omitempty"`
	CWD            string             `json:"cwd,omitempty"`
}

// ChatContentBlock represents a typed chat content block.
type ChatContentBlock struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	Command  string              `json:"command,omitempty"`
	Source   *ChatImageSource    `json:"source,omitempty"`
	ImageURL *ChatImageURLSource `json:"image_url,omitempty"`
}

// ChatImageSource represents embedded image data.
type ChatImageSource struct {
	Data      string `json:"data"`
	MediaType string `json:"media_type"`
}

// ChatImageURLSource represents URL-based image input.
type ChatImageURLSource struct {
	URL string `json:"url"`
}

// ChatEvent is a single streaming chat event.
type ChatEvent struct {
	Kind           string                          `json:"kind"`
	ConversationID string                          `json:"conversation_id,omitempty"`
	Role           string                          `json:"role,omitempty"`
	Delta          string                          `json:"delta,omitempty"`
	Content        any                             `json:"content,omitempty"`
	Usage          *llmtypes.Usage                 `json:"usage,omitempty"`
	ToolName       string                          `json:"tool_name,omitempty"`
	ToolCallID     string                          `json:"tool_call_id,omitempty"`
	Input          string                          `json:"input,omitempty"`
	ToolResult     *tooltypes.StructuredToolResult `json:"tool_result,omitempty"`
	UIInput        *UIInputEvent                   `json:"ui_input,omitempty"`
	UIConfirm      *UIConfirmEvent                 `json:"ui_confirm,omitempty"`
	UISelect       *UISelectEvent                  `json:"ui_select,omitempty"`
	UINotify       *UINotifyEvent                  `json:"ui_notify,omitempty"`
	Error          string                          `json:"error,omitempty"`
}

// UIInputEvent describes an extension-requested input prompt.
type UIInputEvent struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	HelpText         string `json:"helpText,omitempty"`
	Message          string `json:"message,omitempty"`
	Placeholder      string `json:"placeholder,omitempty"`
	DefaultValue     string `json:"defaultValue,omitempty"`
	SubmitButtonText string `json:"submitButtonText,omitempty"`
	CancelButtonText string `json:"cancelButtonText,omitempty"`
	Required         bool   `json:"required,omitempty"`
	Secret           bool   `json:"secret,omitempty"`
}

// UIConfirmEvent describes an extension-requested confirmation prompt.
type UIConfirmEvent struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	Message           string `json:"message,omitempty"`
	ConfirmButtonText string `json:"confirmButtonText,omitempty"`
	CancelButtonText  string `json:"cancelButtonText,omitempty"`
}

// UISelectEvent describes an extension-requested single-choice prompt.
type UISelectEvent struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Message          string   `json:"message,omitempty"`
	Options          []string `json:"options"`
	SubmitButtonText string   `json:"submitButtonText,omitempty"`
	CancelButtonText string   `json:"cancelButtonText,omitempty"`
}

// UINotifyEvent describes an extension-requested notification.
type UINotifyEvent struct {
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
}

// ChatEventSink receives streamed chat events.
type ChatEventSink interface {
	Send(ChatEvent) error
}

// ChatRunner executes a single persisted chat turn.
type ChatRunner interface {
	Run(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error)
}

// ExtensionRuntimeProvider supplies extension runtimes for chat turns.
type ExtensionRuntimeProvider interface {
	Runtime(ctx context.Context, cwd string) (*extensions.Runtime, error)
}

// DefaultChatRunner executes chat turns using the same LLM/tool stack as the CLI.
type DefaultChatRunner struct {
	defaultCWD        string
	extensionRuntimes ExtensionRuntimeProvider
}

// NewDefaultChatRunner creates a default chat runner.
func NewDefaultChatRunner(defaultCWD string, extensionRuntimes ...ExtensionRuntimeProvider) *DefaultChatRunner {
	var provider ExtensionRuntimeProvider
	if len(extensionRuntimes) > 0 {
		provider = extensionRuntimes[0]
	}
	return &DefaultChatRunner{
		defaultCWD:        defaultCWD,
		extensionRuntimes: provider,
	}
}

// Run executes a single persisted chat turn and streams events to the sink.
func (r *DefaultChatRunner) Run(ctx context.Context, req ChatRequest, sink ChatEventSink) (string, error) {
	return RunDefaultChat(ctx, req, sink, r.defaultCWD, r.extensionRuntimes)
}

// DefaultCWD returns the runner's configured default working directory.
func (r *DefaultChatRunner) DefaultCWD() string {
	if r == nil {
		return ""
	}
	return r.defaultCWD
}

// ExtensionRuntimeProvider returns the runner's configured extension runtime provider.
func (r *DefaultChatRunner) ExtensionRuntimeProvider() ExtensionRuntimeProvider {
	if r == nil {
		return nil
	}
	return r.extensionRuntimes
}

// RunDefaultChat executes a single persisted chat turn and streams events to the sink.
func RunDefaultChat(ctx context.Context, req ChatRequest, sink ChatEventSink, defaultCWD string, extensionRuntimes ExtensionRuntimeProvider) (string, error) {
	message, imageInputs, err := NormalizeRequest(req)
	if err != nil {
		return "", err
	}

	if message == "" && len(imageInputs) == 0 {
		return "", errors.New("message cannot be empty")
	}

	sessionID := strings.TrimSpace(req.ConversationID)
	if sessionID == "" {
		sessionID = convtypes.GenerateID()
	}

	llmConfig, resolvedCWD, err := ResolveConfig(ctx, sessionID, strings.TrimSpace(req.Profile), strings.TrimSpace(req.CWD), defaultCWD)
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to load configuration")
	}
	llmConfig.WorkingDirectory = resolvedCWD

	var extensionRuntime *extensions.Runtime
	if extensionRuntimes != nil {
		extensionRuntime, err = extensionRuntimes.Runtime(ctx, resolvedCWD)
	} else {
		extensionRuntime, err = extensions.NewRuntimeFromViper(ctx, resolvedCWD)
		if extensionRuntime != nil {
			defer func() {
				_ = extensionRuntime.Close()
			}()
		}
	}
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to initialize extensions")
	}
	if extensionRuntime != nil {
		llmConfig.Extensions = extensionRuntime
	}

	expandSlashCommand := true
	var extensionCommandResult *extensions.RoutedCommandResult
	if commandResult, handled, err := TryExtensionCommand(ctx, message, extensionRuntime, llmConfig, sessionID, resolvedCWD); err != nil {
		return sessionID, err
	} else if handled {
		switch commandResult.Action {
		case extensions.CommandActionRespond:
			if err := sink.Send(ChatEvent{Kind: "conversation", ConversationID: sessionID, Role: "assistant"}); err != nil {
				logger.G(ctx).WithError(err).Debug("failed to send extension command conversation event")
			}
			if strings.TrimSpace(commandResult.Response) != "" {
				if err := sink.Send(ChatEvent{Kind: "text", ConversationID: sessionID, Role: "assistant", Content: commandResult.Response}); err != nil {
					return sessionID, err
				}
			}
			return sessionID, nil
		case extensions.CommandActionRunAgent:
			message = commandResult.Prompt
			expandSlashCommand = false
			extensionCommandResult = commandResult
			if strings.TrimSpace(commandResult.RecipeName) != "" {
				llmConfig.RecipeName = commandResult.RecipeName
			}
		default:
			logger.G(ctx).WithField("command", commandResult.CommandName).WithField("action", commandResult.Action).Warn("extension command returned unknown action")
		}
	}

	message, slashExpansion, goalUpdate, err := TransformSlashCommandIfNeeded(ctx, message, resolvedCWD, expandSlashCommand)
	if err != nil {
		return sessionID, err
	}
	if slashExpansion != nil {
		ApplyFragmentRestrictions(ctx, &llmConfig, &slashExpansion.Metadata)
		llmConfig.RecipeName = slashExpansion.Command
	}

	appState, err := BuildState(ctx, llmConfig, sessionID, resolvedCWD, extensionRuntime)
	if err != nil {
		return sessionID, err
	}

	thread, err := llm.NewThread(llmConfig)
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to create LLM thread")
	}

	thread.SetState(appState)
	thread.SetConversationID(sessionID)
	thread.EnablePersistence(ctx, true)
	if slashExpansion != nil {
		AddSlashCommandDisplay(thread, slashExpansion)
	}
	if extensionCommandResult != nil {
		AddExtensionCommandDisplay(thread, extensionCommandResult)
	}
	if goalUpdate != nil {
		AddGoalDisplay(thread, goalUpdate)
	}

	if err := sink.Send(ChatEvent{
		Kind:           "conversation",
		ConversationID: sessionID,
		Role:           "assistant",
	}); err != nil {
		logger.G(ctx).WithError(err).Debug("failed to send initial conversation event")
	}

	handler := &chatMessageHandler{
		conversationID: sessionID,
		sink:           sink,
	}
	_, err = thread.SendMessage(ctx, message, handler, llmtypes.MessageOpt{
		PromptCache: true,
		Images:      imageInputs,
	})
	if err != nil {
		return sessionID, errors.Wrap(err, "failed to process chat message")
	}

	return sessionID, nil
}

func ResolveConfig(ctx context.Context, conversationID, requestedProfile, requestedCWD, defaultCWDInput string) (llmtypes.Config, string, error) {
	defaultCWD, err := ResolveConfiguredDefaultCWD(defaultCWDInput)
	if err != nil {
		return llmtypes.Config{}, "", err
	}

	expandedRequestedCWD, err := ExpandCWDInput(requestedCWD, defaultCWD)
	if err != nil {
		return llmtypes.Config{}, "", err
	}

	if strings.TrimSpace(conversationID) == "" {
		config, err := ResolveConfigForNewConversation(requestedProfile)
		if err != nil {
			return llmtypes.Config{}, "", err
		}
		resolution, err := conversationservice.ResolveCWD(ctx, nil, "", expandedRequestedCWD, defaultCWD, false)
		if err != nil {
			return llmtypes.Config{}, "", err
		}
		config.WorkingDirectory = resolution.CWD
		return config, resolution.CWD, nil
	}

	service, err := conversationservice.GetDefaultConversationService(ctx)
	if err != nil {
		return llmtypes.Config{}, "", errors.Wrap(err, "failed to open conversation service")
	}
	defer func() {
		_ = service.Close()
	}()

	resolution, err := conversationservice.ResolveCWD(ctx, ServiceStoreAdapter{Service: service}, conversationID, expandedRequestedCWD, defaultCWD, false)
	if err != nil {
		return llmtypes.Config{}, "", err
	}

	record, err := service.GetConversation(ctx, conversationID)
	if err != nil {
		config, newErr := ResolveConfigForNewConversation(requestedProfile)
		if newErr != nil {
			return llmtypes.Config{}, "", newErr
		}
		config.WorkingDirectory = resolution.CWD
		return config, resolution.CWD, nil
	}

	config, err := ResolveConfigForExistingConversation(record)
	if err != nil {
		return llmtypes.Config{}, "", err
	}
	config.WorkingDirectory = resolution.CWD
	return config, resolution.CWD, nil
}

type ServiceStoreAdapter struct {
	Service conversationservice.ConversationServiceInterface
}

func (s ServiceStoreAdapter) Save(context.Context, convtypes.ConversationRecord) error {
	return errors.New("save not implemented")
}

func (s ServiceStoreAdapter) Delete(context.Context, string) error {
	return errors.New("delete not implemented")
}

func (s ServiceStoreAdapter) Query(context.Context, convtypes.QueryOptions) (convtypes.QueryResult, error) {
	return convtypes.QueryResult{}, errors.New("query not implemented")
}

func (s ServiceStoreAdapter) Close() error { return nil }

func (s ServiceStoreAdapter) Load(ctx context.Context, id string) (convtypes.ConversationRecord, error) {
	record, err := s.Service.GetConversation(ctx, id)
	if err != nil {
		return convtypes.ConversationRecord{}, err
	}
	return convtypes.ConversationRecord{
		ID:          record.ID,
		CWD:         record.CWD,
		Provider:    record.Provider,
		Metadata:    record.Metadata,
		RawMessages: record.RawMessages,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
		Usage:       record.Usage,
		Summary:     record.Summary,
		ToolResults: record.ToolResults,
	}, nil
}

func ResolveConfigForNewConversation(requestedProfile string) (llmtypes.Config, error) {
	requestedProfile = strings.TrimSpace(requestedProfile)
	if strings.EqualFold(requestedProfile, "default") {
		config, err := llm.GetConfigFromViperWithoutProfile()
		if err != nil {
			return llmtypes.Config{}, err
		}
		config.Profile = "default"
		return config, nil
	}

	profileName := NormalizeRequestedProfile(requestedProfile)
	if profileName != "" {
		return llm.GetConfigFromViperWithProfile(profileName)
	}

	return llm.GetConfigFromViper()
}

func ResolveConfigForExistingConversation(record *conversationservice.GetConversationResponse) (llmtypes.Config, error) {
	profileName := ""
	hasStoredProfile := false
	if record != nil && record.Metadata != nil {
		if rawProfile, ok := record.Metadata["profile"].(string); ok {
			hasStoredProfile = true
			profileName = NormalizeRequestedProfile(rawProfile)
		}
	}

	var (
		config llmtypes.Config
		err    error
	)
	if hasStoredProfile {
		if profileName != "" {
			config, err = llm.GetConfigFromViperWithProfile(profileName)
		} else {
			config, err = llm.GetConfigFromViperWithoutProfile()
		}
	} else if profileName != "" {
		config, err = llm.GetConfigFromViperWithProfile(profileName)
	} else {
		config, err = llm.GetConfigFromViper()
	}
	if err != nil {
		return llmtypes.Config{}, err
	}

	if record == nil {
		return config, nil
	}

	if strings.TrimSpace(record.Provider) != "" {
		config.Provider = strings.TrimSpace(record.Provider)
	}
	if record.Metadata != nil {
		if model, ok := record.Metadata["model"].(string); ok && strings.TrimSpace(model) != "" {
			config.Model = strings.TrimSpace(model)
		}

		if strings.EqualFold(config.Provider, "openai") {
			if config.OpenAI == nil {
				config.OpenAI = &llmtypes.OpenAIConfig{}
			}
			if platform, ok := record.Metadata["platform"].(string); ok && strings.TrimSpace(platform) != "" {
				config.OpenAI.Platform = strings.TrimSpace(platform)
			}
			if apiMode, ok := record.Metadata["api_mode"].(string); ok && strings.TrimSpace(apiMode) != "" {
				config.OpenAI.APIMode = llmtypes.OpenAIAPIMode(strings.TrimSpace(apiMode))
			}
			if serviceTier, ok := record.Metadata["service_tier"].(string); ok && strings.TrimSpace(serviceTier) != "" {
				config.OpenAI.ServiceTier = llmtypes.OpenAIServiceTier(strings.TrimSpace(serviceTier))
			}
		}
	}

	if hasStoredProfile && profileName == "" {
		config.Profile = "default"
	} else {
		config.Profile = profileName
	}
	return config, nil
}

func NormalizeRequestedProfile(profile string) string {
	normalized := strings.TrimSpace(profile)
	if normalized == "" || strings.EqualFold(normalized, "default") {
		return ""
	}
	return normalized
}

func NormalizeRequest(req ChatRequest) (string, []string, error) {
	message := strings.TrimSpace(req.Message)
	if len(req.Content) == 0 {
		return message, nil, nil
	}

	textParts := make([]string, 0, len(req.Content))
	imageInputs := make([]string, 0, len(req.Content))

	for _, block := range req.Content {
		switch block.Type {
		case "text":
			if trimmed := strings.TrimSpace(block.Text); trimmed != "" {
				textParts = append(textParts, trimmed)
			}
		case "image":
			if block.Source != nil {
				data := strings.TrimSpace(block.Source.Data)
				mediaType := strings.TrimSpace(block.Source.MediaType)
				if data == "" || mediaType == "" {
					return "", nil, errors.New("image source must include data and media_type")
				}
				imageInputs = append(imageInputs, fmt.Sprintf("data:%s;base64,%s", mediaType, data))
				continue
			}

			if block.ImageURL != nil {
				url := strings.TrimSpace(block.ImageURL.URL)
				if url == "" {
					return "", nil, errors.New("image_url must include url")
				}
				imageInputs = append(imageInputs, url)
				continue
			}

			return "", nil, errors.New("image block must include source or image_url")
		default:
			return "", nil, errors.Errorf("unsupported content block type: %s", block.Type)
		}
	}

	if len(textParts) > 0 {
		message = strings.Join(textParts, "\n\n")
	}

	return message, imageInputs, nil
}

func newFragmentProcessor(cwd string) (*fragments.Processor, error) {
	return fragments.NewFragmentProcessor(fragments.WithDefaultDirsForCWD(cwd))
}

func TransformSlashCommand(ctx context.Context, message string, cwd string) (string, *slashcommands.Expansion, *goals.CommandUpdate, error) {
	command, args, found := slashcommands.Parse(message)
	if !found {
		return message, nil, nil, nil
	}

	goalUpdate, handled, err := goals.ParseSlashCommand(command, args, time.Now())
	if handled {
		if err != nil {
			return "", nil, nil, err
		}
		return goalUpdate.ModelPrompt, nil, &goalUpdate, nil
	}

	message, expansion, err := ExpandSlashCommand(ctx, message, cwd)
	return message, expansion, nil, err
}

func TransformSlashCommandIfNeeded(ctx context.Context, message string, cwd string, enabled bool) (string, *slashcommands.Expansion, *goals.CommandUpdate, error) {
	if !enabled {
		return message, nil, nil, nil
	}
	return TransformSlashCommand(ctx, message, cwd)
}

func TryExtensionCommand(
	ctx context.Context,
	message string,
	extensionRuntime *extensions.Runtime,
	llmConfig llmtypes.Config,
	conversationID string,
	workingDir string,
) (*extensions.RoutedCommandResult, bool, error) {
	if extensionRuntime == nil {
		return nil, false, nil
	}

	command, args, found := slashcommands.Parse(message)
	if !found {
		return nil, false, nil
	}

	result, err := extensionRuntime.TryCommand(ctx, message, command, args, extensions.ExtensionCallContext{
		ConversationID: conversationID,
		CWD:            workingDir,
		Provider:       llmConfig.Provider,
		Model:          llmConfig.Model,
		Profile:        llmConfig.Profile,
		RecipeName:     llmConfig.RecipeName,
		InvokedBy:      "main",
	})
	if err != nil {
		return nil, false, err
	}
	if result == nil || !result.Matched {
		return nil, false, nil
	}
	return result, true, nil
}

func ExpandSlashCommand(ctx context.Context, message string, cwd string) (string, *slashcommands.Expansion, error) {
	command, args, found := slashcommands.Parse(message)
	if !found {
		return message, nil, nil
	}

	processor, err := newFragmentProcessor(cwd)
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to initialize slash commands")
	}

	expansion, err := slashcommands.Expand(ctx, processor, command, args)
	if err != nil {
		return "", nil, err
	}

	return expansion.Prompt, expansion, nil
}

func ApplyFragmentRestrictions(ctx context.Context, llmConfig *llmtypes.Config, fragmentMetadata *fragments.Metadata) {
	if fragmentMetadata == nil {
		return
	}

	if len(fragmentMetadata.AllowedTools) > 0 {
		if err := tools.ValidateTools(fragmentMetadata.AllowedTools); err != nil {
			logger.G(ctx).WithError(err).Warn("Invalid tools in fragment metadata, ignoring allowed_tools")
		} else {
			llmConfig.AllowedTools = fragmentMetadata.AllowedTools
		}
	}

	if len(fragmentMetadata.AllowedCommands) > 0 {
		llmConfig.AllowedCommands = fragmentMetadata.AllowedCommands
	}
}

func AddSlashCommandDisplay(thread llmtypes.Thread, expansion *slashcommands.Expansion) {
	if thread == nil || expansion == nil {
		return
	}

	metadata := conversationservice.AddSlashCommandDisplay(thread.GetMetadata(), expansion.Prompt, expansion.Display, expansion.Command)
	for key, value := range metadata {
		thread.SetMetadataValue(key, value)
	}
}

func AddExtensionCommandDisplay(thread llmtypes.Thread, result *extensions.RoutedCommandResult) {
	if thread == nil || result == nil {
		return
	}

	metadata := conversationservice.AddSlashCommandDisplay(thread.GetMetadata(), result.Prompt, result.Display, result.CommandName)
	for key, value := range metadata {
		thread.SetMetadataValue(key, value)
	}
}

func AddGoalDisplay(thread llmtypes.Thread, update *goals.CommandUpdate) {
	if thread == nil || update == nil {
		return
	}

	thread.SetMetadataValue(goals.MetadataKey, update.Goal)
	metadata := conversationservice.AddMessageDisplay(thread.GetMetadata(), update.ModelPrompt, update.Display, conversationservice.MessageDisplayKindGoal, goals.SlashCommandName)
	for key, value := range metadata {
		thread.SetMetadataValue(key, value)
	}
}

func BuildState(
	ctx context.Context,
	llmConfig llmtypes.Config,
	sessionID string,
	workingDir string,
	extensionRuntime *extensions.Runtime,
) (*tools.BasicState, error) {
	stateOpts := []tools.BasicStateOption{
		tools.WithWorkingDirectory(workingDir),
		tools.WithLLMConfig(llmConfig),
		tools.WithMainTools(),
		tools.WithSkillTool(),
	}
	if extensionRuntime != nil {
		stateOpts = append(stateOpts, tools.WithExtensionTools(extensionRuntime.Tools()))
	}

	return tools.NewBasicState(ctx, stateOpts...), nil
}

type chatMessageHandler struct {
	conversationID string
	sink           ChatEventSink
	broadcast      func(string, ChatEvent)
	usageMu        sync.Mutex
	hasLastUsage   bool
	lastUsage      llmtypes.Usage
}

func (h *chatMessageHandler) sendEvent(event ChatEvent) {
	_ = h.sink.Send(event)
	if h.broadcast != nil {
		h.broadcast(h.conversationID, event)
	}
}

func (h *chatMessageHandler) HandleText(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}

	event := ChatEvent{
		Kind:           "text",
		Content:        text,
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleUserMessage(content string, images []string) {
	contentBlocks := ContentBlocksForUserInput(content, images)

	var eventContent any = content
	if len(contentBlocks) > 0 {
		eventContent = contentBlocks
	}

	h.sendEvent(ChatEvent{
		Kind:           "user-message",
		Content:        eventContent,
		ConversationID: h.conversationID,
		Role:           "user",
	})
}

func ContentBlocksForUserInput(text string, imageInputs []string) []ChatContentBlock {
	hasImages := false
	for _, imageInput := range imageInputs {
		if strings.TrimSpace(imageInput) != "" {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return nil
	}

	contentBlocks := make([]ChatContentBlock, 0, len(imageInputs)+1)
	if trimmed := strings.TrimSpace(text); trimmed != "" {
		contentBlocks = append(contentBlocks, ChatContentBlock{Type: "text", Text: trimmed})
	}

	for _, imageInput := range imageInputs {
		imageInput = strings.TrimSpace(imageInput)
		if imageInput == "" {
			continue
		}
		if strings.HasPrefix(imageInput, "data:") {
			if source, ok := ParseDataURL(imageInput); ok {
				contentBlocks = append(contentBlocks, ChatContentBlock{Type: "image", Source: source})
				continue
			}
		}
		if !strings.HasPrefix(imageInput, "https://") {
			dataURL, err := llmbase.ReadImageFileAsDataURL(strings.TrimPrefix(imageInput, "file://"))
			if err == nil {
				if source, ok := ParseDataURL(dataURL); ok {
					contentBlocks = append(contentBlocks, ChatContentBlock{Type: "image", Source: source})
					continue
				}
			}
		}

		contentBlocks = append(contentBlocks, ChatContentBlock{
			Type:     "image",
			ImageURL: &ChatImageURLSource{URL: imageInput},
		})
	}

	return contentBlocks
}

func (h *chatMessageHandler) HandleToolUse(toolCallID string, toolName string, input string) {
	event := ChatEvent{
		Kind:           "tool-use",
		ConversationID: h.conversationID,
		Role:           "assistant",
		ToolCallID:     toolCallID,
		ToolName:       toolName,
		Input:          input,
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleToolResult(toolCallID string, toolName string, result tooltypes.ToolResult) {
	structuredResult := result.StructuredData()
	if structuredResult.ToolName == "" || structuredResult.ToolName == "unknown" {
		structuredResult.ToolName = toolName
	}

	event := ChatEvent{
		Kind:           "tool-result",
		ConversationID: h.conversationID,
		Role:           "assistant",
		ToolCallID:     toolCallID,
		ToolName:       structuredResult.ToolName,
		ToolResult:     &structuredResult,
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleThinking(thinking string) {
	if strings.TrimSpace(thinking) == "" {
		return
	}

	event := ChatEvent{
		Kind:           "thinking",
		Content:        thinking,
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleDone() {}

func (h *chatMessageHandler) HandleUsage(usage llmtypes.Usage) {
	h.usageMu.Lock()
	if h.hasLastUsage && h.lastUsage == usage {
		h.usageMu.Unlock()
		return
	}
	h.lastUsage = usage
	h.hasLastUsage = true
	h.usageMu.Unlock()

	h.sendEvent(ChatEvent{
		Kind:           "usage",
		ConversationID: h.conversationID,
		Role:           "assistant",
		Usage:          &usage,
	})
}

func (h *chatMessageHandler) HandleTextDelta(delta string) {
	if delta == "" {
		return
	}

	event := ChatEvent{
		Kind:           "text-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleThinkingStart() {
	event := ChatEvent{
		Kind:           "thinking-start",
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleThinkingDelta(delta string) {
	if delta == "" {
		return
	}

	event := ChatEvent{
		Kind:           "thinking-delta",
		Delta:          delta,
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleThinkingBlockEnd() {
	event := ChatEvent{
		Kind:           "thinking-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

func (h *chatMessageHandler) HandleContentBlockEnd() {
	event := ChatEvent{
		Kind:           "content-end",
		ConversationID: h.conversationID,
		Role:           "assistant",
	}
	h.sendEvent(event)
}

// ResolveConfiguredDefaultCWD resolves a configured default working directory.
func ResolveConfiguredDefaultCWD(configuredCWD string) (string, error) {
	trimmed := strings.TrimSpace(configuredCWD)
	if trimmed == "" {
		return conversationservice.CurrentWorkingDirectory()
	}

	return conversationservice.NormalizeCWD(trimmed)
}

// ExpandCWDInput expands user-entered working-directory text relative to a default cwd.
func ExpandCWDInput(query, defaultCWD string) (string, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "", nil
	}

	if strings.HasPrefix(trimmed, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", errors.Wrap(err, "failed to resolve home directory")
		}
		if trimmed == "~" {
			return homeDir, nil
		}
		if strings.HasPrefix(trimmed, "~/") {
			return filepath.Join(homeDir, strings.TrimPrefix(trimmed, "~/")), nil
		}
	}

	if filepath.IsAbs(trimmed) {
		return trimmed, nil
	}

	if IsNaturalDirectoryQuery(trimmed) {
		exactCandidates := []string{
			filepath.Join(defaultCWD, trimmed),
			filepath.Join(filepath.Dir(defaultCWD), trimmed),
		}

		for _, candidate := range exactCandidates {
			resolved, err := conversationservice.NormalizeCWD(candidate)
			if err == nil {
				return resolved, nil
			}
		}
	}

	return filepath.Join(defaultCWD, trimmed), nil
}

// IsNaturalDirectoryQuery reports whether query is a bare directory name.
func IsNaturalDirectoryQuery(query string) bool {
	trimmed := strings.TrimSpace(query)
	return trimmed != "" &&
		!strings.HasPrefix(trimmed, "~") &&
		!filepath.IsAbs(trimmed) &&
		!strings.ContainsRune(trimmed, os.PathSeparator)
}

// ParseDataURL parses a base64 data URL into a chat image source.
func ParseDataURL(dataURL string) (*ChatImageSource, bool) {
	if !strings.HasPrefix(dataURL, "data:") {
		return nil, false
	}

	prefix, data, found := strings.Cut(dataURL, ",")
	if !found {
		return nil, false
	}

	mediaType, hasBase64 := strings.CutPrefix(prefix, "data:")
	if !hasBase64 {
		return nil, false
	}

	mediaType = strings.TrimSuffix(mediaType, ";base64")
	if mediaType == "" || !strings.HasSuffix(prefix, ";base64") {
		return nil, false
	}

	return &ChatImageSource{Data: data, MediaType: mediaType}, true
}
