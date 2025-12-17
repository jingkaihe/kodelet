// Package anthropic provides a client implementation for interacting with Anthropic's Claude AI models.
// It implements the LLM Thread interface for managing conversations, tool execution, and message processing.
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"golang.org/x/sync/errgroup"

	"github.com/jingkaihe/kodelet/pkg/auth"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/feedback"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/ide"
	"github.com/jingkaihe/kodelet/pkg/llm/prompts"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/sysprompt"
	"github.com/jingkaihe/kodelet/pkg/telemetry"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/jingkaihe/kodelet/pkg/tools/renderers"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jingkaihe/kodelet/pkg/usage"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ConversationStore is an alias for the conversations.ConversationStore interface
// to avoid direct dependency on the conversations package
type ConversationStore = conversations.ConversationStore

// Constants for image processing
const (
	MaxImageFileSize = 5 * 1024 * 1024 // 5MB limit
	MaxImageCount    = 10              // Maximum 10 images per message
)

// Thread implements the Thread interface using Anthropic's Claude API
type Thread struct {
	client                 anthropic.Client
	config                 llmtypes.Config
	state                  tooltypes.State
	messages               []anthropic.MessageParam
	usage                  *llmtypes.Usage
	conversationID         string
	summary                string
	isPersisted            bool
	store                  ConversationStore
	mu                     sync.Mutex
	conversationMu         sync.Mutex
	useSubscription        bool
	toolResults            map[string]tooltypes.StructuredToolResult // Maps tool_call_id to structured result
	subagentContextFactory llmtypes.SubagentContextFactory           // Injected function for cross-provider subagent creation
	ideStore               *ide.Store                                // IDE context store (nil if IDE mode disabled)
	hookTrigger            hooks.Trigger                             // Hook trigger for lifecycle hooks (zero-value = no-op)
}

// Provider returns the provider name for this thread
func (t *Thread) Provider() string {
	return "anthropic"
}

// NewAnthropicThread creates a new thread with Anthropic's Claude API
func NewAnthropicThread(config llmtypes.Config, subagentContextFactory llmtypes.SubagentContextFactory) (*Thread, error) {
	// Apply defaults if not provided
	if config.Model == "" {
		config.Model = string(anthropic.ModelClaudeSonnet4_20250514)
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 8192
	}

	opts := []option.RequestOption{}
	if isThinkingModel(anthropic.Model(config.Model)) {
		opts = append(opts, option.WithHeaderAdd("anthropic-beta", "interleaved-thinking-2025-05-14"))
	}

	logger := logger.G(context.Background())
	var client anthropic.Client
	var useSubscription bool

	// Determine authentication method based on access mode configuration
	switch config.AnthropicAPIAccess {
	case llmtypes.AnthropicAPIAccessAPIKey:
		// Force API key usage
		logger.Debug("using API key authentication (forced by configuration)")
		client = anthropic.NewClient(opts...)
		useSubscription = false

	case llmtypes.AnthropicAPIAccessSubscription:
		// Force subscription usage - no fallbacks allowed
		antCredsExists, _ := auth.GetAnthropicCredentialsExists()
		if !antCredsExists {
			return nil, errors.New("subscription authentication forced but no credentials found")
		}
		accessToken, err := auth.AnthropicAccessToken(context.Background())
		if err != nil {
			return nil, errors.Wrap(err, "subscription authentication forced but failed to get access token")
		}
		logger.Debug("using anthropic access token (forced by configuration)")
		opts = append(opts, auth.AnthropicHeader(accessToken)...)
		client = anthropic.NewClient(opts...)
		useSubscription = true

	case llmtypes.AnthropicAPIAccessAuto:
		fallthrough
	default:
		// Auto mode: try subscription first, then fall back to API key
		antCredsExists, _ := auth.GetAnthropicCredentialsExists()
		if antCredsExists {
			accessToken, err := auth.AnthropicAccessToken(context.Background())
			if err != nil {
				logger.WithError(err).Error("failed to get anthropic access token, falling back to use API key")
				client = anthropic.NewClient()
				useSubscription = false
			} else {
				logger.Debug("using anthropic access token")
				opts = append(opts, auth.AnthropicHeader(accessToken)...)
				client = anthropic.NewClient(opts...)
				useSubscription = true
			}
		} else {
			logger.Debug("no anthropic credentials found, falling back to use API key")
			client = anthropic.NewClient(opts...)
			useSubscription = false
		}
	}

	var ideStore *ide.Store
	if config.IDE && !config.IsSubAgent {
		store, err := ide.NewIDEStore()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create IDE store")
		}
		ideStore = store
	}

	// Initialize hook trigger (zero-value if discovery fails or disabled - hooks disabled)
	var hookTrigger hooks.Trigger
	conversationID := convtypes.GenerateID()
	if !config.IsSubAgent && !config.NoHooks {
		// Only main agent discovers hooks; subagents inherit from parent
		// Hooks can be disabled via NoHooks config
		hookManager, err := hooks.NewHookManager()
		if err != nil {
			logger.WithError(err).Warn("Failed to initialize hook manager, hooks disabled")
		} else {
			hookTrigger = hooks.NewTrigger(hookManager, conversationID, config.IsSubAgent)
		}
	}

	return &Thread{
		client:                 client,
		config:                 config,
		useSubscription:        useSubscription,
		conversationID:         conversationID,
		isPersisted:            false,
		usage:                  &llmtypes.Usage{},
		toolResults:            make(map[string]tooltypes.StructuredToolResult),
		subagentContextFactory: subagentContextFactory,
		ideStore:               ideStore,
		hookTrigger:            hookTrigger,
	}, nil
}

// SetState sets the state for the thread
func (t *Thread) SetState(s tooltypes.State) {
	t.state = s
}

// GetState returns the current state of the thread
func (t *Thread) GetState() tooltypes.State {
	return t.state
}

// AddUserMessage adds a user message with optional images to the thread
func (t *Thread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
	contentBlocks := []anthropic.ContentBlockParamUnion{}

	// Validate image count
	if len(imagePaths) > MaxImageCount {
		logger.G(ctx).Warnf("Too many images provided (%d), maximum is %d. Only processing first %d images", len(imagePaths), MaxImageCount, MaxImageCount)
		imagePaths = imagePaths[:MaxImageCount]
	}

	// Process images and add them as content blocks
	for _, imagePath := range imagePaths {
		imageBlock, err := t.processImage(imagePath)
		if err != nil {
			logger.G(ctx).Warnf("Failed to process image %s: %v", imagePath, err)
			continue
		}
		contentBlocks = append(contentBlocks, *imageBlock)
	}
	contentBlocks = append(contentBlocks, anthropic.NewTextBlock(message))

	t.messages = append(t.messages, anthropic.NewUserMessage(contentBlocks...))
}

func (t *Thread) cacheMessages() {
	// remove cache control from the messages
	for msgIdx, msg := range t.messages {
		for blkIdx, block := range msg.Content {
			if block.OfText != nil {
				block.OfText.CacheControl = anthropic.CacheControlEphemeralParam{}
				t.messages[msgIdx].Content[blkIdx] = block
			}
		}
	}
	if len(t.messages) > 0 {
		lastMsg := t.messages[len(t.messages)-1]
		if len(lastMsg.Content) > 0 {
			lastBlock := lastMsg.Content[len(lastMsg.Content)-1]
			if lastBlock.OfText != nil {
				lastBlock.OfText.CacheControl = anthropic.CacheControlEphemeralParam{}
				t.messages[len(t.messages)-1].Content[len(lastMsg.Content)-1] = lastBlock
			}
		}
	}
}

// SendMessage sends a message to the LLM and processes the response
func (t *Thread) SendMessage(
	ctx context.Context,
	message string,
	handler llmtypes.MessageHandler,
	opt llmtypes.MessageOpt,
) (finalOutput string, err error) {
	// Check if tracing is enabled and wrap the handler
	tracer := telemetry.Tracer("kodelet.llm")

	ctx, span := t.createMessageSpan(ctx, tracer, message, opt)
	defer t.finalizeMessageSpan(span, err)

	if opt.PromptCache {
		t.cacheMessages()
	}

	var originalMessages []anthropic.MessageParam
	if opt.PromptCache {
		originalMessages = make([]anthropic.MessageParam, len(t.messages))
		copy(originalMessages, t.messages)
	}

	if !t.config.IsSubAgent && t.ideStore != nil {
		if err := t.processIDEContext(ctx, handler); err != nil {
			return "", errors.Wrap(err, "failed to process IDE context")
		}
	}

	// Trigger user_message_send hook before adding user message
	if blocked, reason := t.hookTrigger.TriggerUserMessageSend(ctx, message); blocked {
		return "", errors.Errorf("message blocked by hook: %s", reason)
	}

	t.AddUserMessage(ctx, message, opt.Images...)

	model, maxTokens := t.getModelAndTokens(opt)

	turnCount := 0
	maxTurns := max(opt.MaxTurns, 0)

	cacheEvery := t.config.CacheEvery

OUTER:
	for {
		select {
		case <-ctx.Done():
			logger.G(ctx).Info("stopping kodelet.llm.anthropic")
			break OUTER
		default:
			// Check turn limit (0 means no limit)
			logger.G(ctx).WithField("turn_count", turnCount).WithField("max_turns", maxTurns).Debug("checking turn limit")

			if maxTurns > 0 && turnCount >= maxTurns {
				logger.G(ctx).
					WithField("turn_count", turnCount).
					WithField("max_turns", maxTurns).
					Warn("reached maximum turn limit, stopping interaction")
				break OUTER
			}

			if opt.PromptCache && turnCount > 0 && cacheEvery > 0 && turnCount%cacheEvery == 0 {
				logger.G(ctx).WithField("turn_count", turnCount).WithField("cache_every", cacheEvery).Debug("caching messages")
				t.cacheMessages()
			}

			// Check if auto-compact should be triggered before each exchange
			if !opt.DisableAutoCompact && t.shouldAutoCompact(opt.CompactRatio) {
				logger.G(ctx).WithField("context_utilization", float64(t.GetUsage().CurrentContextWindow)/float64(t.GetUsage().MaxContextWindow)).Info("triggering auto-compact")
				err := t.CompactContext(ctx)
				if err != nil {
					logger.G(ctx).WithError(err).Error("failed to auto-compact context")
				} else {
					logger.G(ctx).Info("auto-compact completed successfully")
				}
			}

			// Get relevant contexts from state and regenerate system prompt
			var contexts map[string]string
			if t.state != nil {
				contexts = t.state.DiscoverContexts()
			}
			var systemPrompt string
			if t.config.IsSubAgent {
				systemPrompt = sysprompt.SubAgentPrompt(string(model), t.config, contexts)
			} else {
				systemPrompt = sysprompt.SystemPrompt(string(model), t.config, contexts)
			}

			var exchangeOutput string
			exchangeOutput, toolsUsed, err := t.processMessageExchange(ctx, handler, model, maxTokens, systemPrompt, opt)
			if err != nil {
				logger.G(ctx).WithError(err).Error("error processing message exchange")
				// xxx: based on the observation, the anthropic sdk swallows context cancellation, and return empty message
				if errors.Is(err, context.Canceled) {
					logger.G(ctx).Info("Request to anthropic cancelled, stopping kodelet.llm.anthropic")
					// remove the last tool use from the messages
					if len(t.messages) > 0 {
						lastMsg := t.messages[len(t.messages)-1]
						if isMessageToolUse(lastMsg) {
							t.messages = t.messages[:len(t.messages)-1]
						}
					}
					break OUTER
				}
				return "", err
			}

			// Increment turn count after each exchange
			turnCount++

			// Update finalOutput with the most recent output
			finalOutput = exchangeOutput

			// If no tools were used, check for hook follow-ups before stopping
			if !toolsUsed {
				logger.G(ctx).Debug("no tools used, checking agent_stop hook")

				// Trigger agent_stop hook to see if there are follow-up messages
				if messages, err := t.GetMessages(); err == nil {
					if followUps := t.hookTrigger.TriggerAgentStop(ctx, messages); len(followUps) > 0 {
						logger.G(ctx).WithField("count", len(followUps)).Info("agent_stop hook returned follow-up messages, continuing conversation")
						// Append follow-up messages as user messages and continue
						for _, msg := range followUps {
							t.AddUserMessage(ctx, msg)
							handler.HandleText(fmt.Sprintf("\n📨 Hook follow-up: %s\n", msg))
						}
						continue OUTER
					}
				}

				break OUTER
			}
		}
	}

	if opt.NoSaveConversation {
		t.messages = originalMessages
	}

	// Save conversation state after completing the interaction
	if t.isPersisted && t.store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background() // use new context to avoid cancellation
		t.SaveConversation(saveCtx, true)
	}

	if !t.config.IsSubAgent {
		// only main agaent can signal done
		handler.HandleDone()
	}
	return finalOutput, nil
}

func isMessageToolUse(msg anthropic.MessageParam) bool {
	if len(msg.Content) == 0 {
		return false
	}
	for _, block := range msg.Content {
		if block.OfToolUse != nil {
			return true
		}
	}
	return false
}

// toolExecResult holds the result of a single tool execution
type toolExecResult struct {
	index          int
	blockID        string
	toolName       string
	input          string
	output         tooltypes.ToolResult
	structuredData tooltypes.StructuredToolResult
	renderedOutput string
}

// executeToolsParallel runs multiple tool calls concurrently and streams results as they complete.
// It returns results in original order for consistent message building.
func (t *Thread) executeToolsParallel(
	ctx context.Context,
	handler llmtypes.MessageHandler,
	toolBlocks []struct {
		block   anthropic.ContentBlockUnion
		variant anthropic.ToolUseBlock
	},
	opt llmtypes.MessageOpt,
) ([]toolExecResult, error) {
	if len(toolBlocks) == 0 {
		return nil, nil
	}

	// Show all tool invocations upfront so user knows what's about to run
	for _, tb := range toolBlocks {
		handler.HandleToolUse(tb.block.Name, tb.variant.JSON.Input.Raw())
	}

	results := make([]toolExecResult, len(toolBlocks))
	resultCh := make(chan toolExecResult, len(toolBlocks))

	g, gctx := errgroup.WithContext(ctx)

	for i, tb := range toolBlocks {
		i, tb := i, tb
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}

			telemetry.AddEvent(gctx, "tool_execution_start",
				attribute.String("tool_name", tb.block.Name),
				attribute.Int("tool_index", i),
			)

			// Trigger before_tool_call hook
			toolInput := tb.variant.JSON.Input.Raw()
			blocked, reason, toolInput := t.hookTrigger.TriggerBeforeToolCall(gctx, tb.block.Name, toolInput, tb.block.ID)

			var output tooltypes.ToolResult
			if blocked {
				output = tooltypes.NewBlockedToolResult(tb.block.Name, reason)
			} else {
				// Use a per-goroutine silent handler to avoid race conditions on shared handler
				// XXX: It's tricky to visualise agent streaming in the terminal therefore we disable it for now
				parallelHandler := &llmtypes.StringCollectorHandler{Silent: true}
				runToolCtx := t.subagentContextFactory(gctx, t, parallelHandler, opt.CompactRatio, opt.DisableAutoCompact)
				output = tools.RunTool(runToolCtx, t.state, tb.block.Name, toolInput)
			}

			if err := gctx.Err(); err != nil {
				return err
			}

			structuredResult := output.StructuredData()

			// Trigger after_tool_call hook
			if modified := t.hookTrigger.TriggerAfterToolCall(gctx, tb.block.Name, toolInput, tb.block.ID, structuredResult); modified != nil {
				structuredResult = *modified
			}

			registry := renderers.NewRendererRegistry()
			renderedOutput := registry.Render(structuredResult)

			telemetry.AddEvent(gctx, "tool_execution_complete",
				attribute.String("tool_name", tb.block.Name),
				attribute.Int("tool_index", i),
				attribute.String("result", output.AssistantFacing()),
			)

			result := toolExecResult{
				index:          i,
				blockID:        tb.block.ID,
				toolName:       tb.block.Name,
				input:          toolInput,
				output:         output,
				structuredData: structuredResult,
				renderedOutput: renderedOutput,
			}

			select {
			case resultCh <- result:
			case <-gctx.Done():
				return gctx.Err()
			}

			return nil
		})
	}

	// Consumer goroutine: stream results as they complete
	var consumerWg sync.WaitGroup
	consumerWg.Add(1)
	go func() {
		defer consumerWg.Done()
		for result := range resultCh {
			handler.HandleToolResult(result.toolName, result.renderedOutput)
			results[result.index] = result // preserve original order
		}
	}()

	err := g.Wait()
	close(resultCh)
	consumerWg.Wait()

	if err != nil {
		return nil, err
	}

	return results, nil
}

// processMessageExchange handles a single message exchange with the LLM, including
// preparing message parameters, making the API call, and processing the response
func (t *Thread) processMessageExchange(
	ctx context.Context,
	handler llmtypes.MessageHandler,
	model anthropic.Model,
	maxTokens int,
	systemPrompt string,
	opt llmtypes.MessageOpt,
) (string, bool, error) {
	var finalOutput string

	systemPromptBlocks := []anthropic.TextBlockParam{}
	if t.useSubscription {
		systemPromptBlocks = append(systemPromptBlocks, auth.AnthropicSystemPrompt()...)
	}
	systemPromptBlocks = append(systemPromptBlocks, anthropic.TextBlockParam{
		Text: systemPrompt,
		CacheControl: anthropic.CacheControlEphemeralParam{
			Type: "ephemeral",
		},
	})

	// Prepare message parameters
	messageParams := anthropic.MessageNewParams{
		MaxTokens: int64(maxTokens),
		System:    systemPromptBlocks,
		Messages:  t.messages,
		Model:     model,
		Tools:     tools.ToAnthropicTools(t.tools(opt)),
	}
	if t.shouldUtiliseThinking(model) {
		messageParams.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{
				Type:         "enabled",
				BudgetTokens: int64(t.config.ThinkingBudgetTokens),
			},
		}
	}

	if !t.config.IsSubAgent {
		if err := t.processPendingFeedback(ctx, &messageParams, handler); err != nil {
			return "", false, errors.Wrap(err, "failed to process pending feedback")
		}
	}

	// Add a tracing event for API call start
	telemetry.AddEvent(ctx, "api_call_start",
		attribute.String("model", string(model)),
		attribute.Int("max_tokens", maxTokens),
	)

	// Record start time for usage logging
	apiStartTime := time.Now()

	// Check if handler supports streaming for skipping post-stream calls
	_, isStreamingHandler := handler.(llmtypes.StreamingMessageHandler)

	response, err := t.NewMessage(ctx, messageParams, handler)
	if err != nil {
		if t.isPersisted && t.store != nil && !opt.NoSaveConversation {
			t.SaveConversation(ctx, false)
		}
		return "", false, err
	}

	// Record API call completion
	telemetry.AddEvent(ctx, "api_call_complete",
		attribute.Int("input_tokens", int(response.Usage.InputTokens)),
		attribute.Int("output_tokens", int(response.Usage.OutputTokens)),
	)

	// Add the assistant response to history
	t.messages = append(t.messages, response.ToParam())

	t.updateUsage(response, model)

	// Process the response content blocks - first pass: handle text/thinking, collect tool blocks
	var toolBlocks []struct {
		block   anthropic.ContentBlockUnion
		variant anthropic.ToolUseBlock
	}

	for _, block := range response.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			if !isStreamingHandler {
				handler.HandleText(variant.Text)
			}
			finalOutput = variant.Text
		case anthropic.ThinkingBlock:
			if !isStreamingHandler {
				handler.HandleThinking(variant.Thinking)
			}
		case anthropic.ToolUseBlock:
			toolBlocks = append(toolBlocks, struct {
				block   anthropic.ContentBlockUnion
				variant anthropic.ToolUseBlock
			}{block, variant})
		}
	}

	// Execute tools in parallel - handler calls (HandleToolUse/HandleToolResult) happen inside
	// as each tool completes for real-time feedback
	toolResults, err := t.executeToolsParallel(ctx, handler, toolBlocks, opt)
	if err != nil {
		return "", false, errors.Wrap(err, "failed to execute tools in parallel")
	}

	// Build tool result blocks for LLM message (in original order)
	toolResultBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(toolResults))
	for _, result := range toolResults {
		t.SetStructuredToolResult(result.blockID, result.structuredData)

		logger.G(ctx).
			WithField("tool_name", result.toolName).
			WithField("result", result.output.AssistantFacing()).
			Debug("Adding tool result to messages")

		toolResultBlocks = append(toolResultBlocks,
			anthropic.NewToolResultBlock(result.blockID, result.output.AssistantFacing(), false),
		)
	}

	// Add all tool results as a single user message (required by Anthropic API)
	if len(toolResultBlocks) > 0 {
		t.messages = append(t.messages, anthropic.NewUserMessage(toolResultBlocks...))
	}

	toolUseCount := len(toolResults)

	// Log structured LLM usage after all content processing is complete (main agent only)
	if !t.config.IsSubAgent && !opt.DisableUsageLog {
		usage.LogLLMUsage(ctx, t.GetUsage(), string(model), apiStartTime, int(response.Usage.OutputTokens))
	}

	if t.isPersisted && t.store != nil && !opt.NoSaveConversation {
		t.SaveConversation(ctx, false)
	}

	// Return whether tools were used in this exchange
	return finalOutput, toolUseCount > 0, nil
}

func (t *Thread) processIDEContext(ctx context.Context, handler llmtypes.MessageHandler) error {
	ideContext, err := t.ideStore.ReadContext(t.conversationID)
	if err != nil && !errors.Is(err, ide.ErrContextNotFound) {
		return errors.Wrap(err, "failed to read IDE context")
	}

	if ideContext != nil {
		logger.G(ctx).WithFields(map[string]any{
			"open_files_count":  len(ideContext.OpenFiles),
			"has_selection":     ideContext.Selection != nil,
			"diagnostics_count": len(ideContext.Diagnostics),
		}).Info("processing IDE context")

		ideContextPrompt := ide.FormatContextPrompt(ideContext)
		if ideContextPrompt != "" {
			t.AddUserMessage(ctx, ideContextPrompt)
			handler.HandleText(fmt.Sprintf("📋 IDE Context: %d files, %d diagnostics",
				len(ideContext.OpenFiles), len(ideContext.Diagnostics)))
		}

		if err := t.ideStore.ClearContext(t.conversationID); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to clear IDE context, may be processed again")
		} else {
			logger.G(ctx).Debug("successfully cleared IDE context")
		}
	}

	return nil
}

func (t *Thread) processPendingFeedback(ctx context.Context, messageParams *anthropic.MessageNewParams, handler llmtypes.MessageHandler) error {
	feedbackStore, err := feedback.NewFeedbackStore()
	if err != nil {
		return errors.Wrap(err, "failed to create feedback store")
	}

	pendingFeedback, err := feedbackStore.ReadPendingFeedback(t.conversationID)
	if err != nil {
		return errors.Wrap(err, "failed to read pending feedback")
	}

	if len(pendingFeedback) > 0 {
		logger.G(ctx).WithField("feedback_count", len(pendingFeedback)).Info("processing pending feedback messages")

		for i, fbMsg := range pendingFeedback {
			if fbMsg.Content == "" {
				logger.G(ctx).WithField("message_index", i).Warn("skipping empty feedback message")
				continue
			}

			userMessage := anthropic.NewUserMessage(
				anthropic.NewTextBlock(fbMsg.Content),
			)
			messageParams.Messages = append(messageParams.Messages, userMessage)
			handler.HandleText(fmt.Sprintf("🗣️ User feedback: %s", fbMsg.Content))
		}

		if err := feedbackStore.ClearPendingFeedback(t.conversationID); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to clear pending feedback, may be processed again")
		} else {
			logger.G(ctx).Debug("successfully cleared pending feedback")
		}
	}

	return nil
}

func (t *Thread) getModelAndTokens(opt llmtypes.MessageOpt) (anthropic.Model, int) {
	model := t.config.Model
	maxTokens := t.config.MaxTokens

	if opt.UseWeakModel && t.config.WeakModel != "" {
		model = t.config.WeakModel
		if t.config.WeakModelMaxTokens > 0 {
			maxTokens = t.config.WeakModelMaxTokens
		}
	}

	return anthropic.Model(model), maxTokens
}

func (t *Thread) shouldUtiliseThinking(model anthropic.Model) bool {
	if !isThinkingModel(model) {
		return false
	}
	if t.config.ThinkingBudgetTokens == 0 {
		return false
	}
	return true
}

func isThinkingModel(model anthropic.Model) bool {
	thinkingModels := []anthropic.Model{
		// sonnet 4.5 models
		anthropic.ModelClaudeSonnet4_5,
		anthropic.ModelClaudeSonnet4_5_20250929,

		// sonnet 4 models
		anthropic.ModelClaudeSonnet4_0,
		anthropic.ModelClaudeSonnet4_20250514,
		// opus 4 models
		anthropic.ModelClaudeOpus4_0,
		anthropic.ModelClaudeOpus4_1_20250805,
		anthropic.ModelClaudeOpus4_5_20251101,
		anthropic.ModelClaude4Opus20250514,
		anthropic.ModelClaudeOpus4_20250514,
		anthropic.ModelClaude4Opus20250514,
	}
	return slices.Contains(thinkingModels, model)
}

// NewMessage sends a message to Anthropic with OTEL tracing.
// If handler implements StreamingMessageHandler, content will be streamed as it arrives.
func (t *Thread) NewMessage(ctx context.Context, params anthropic.MessageNewParams, handler llmtypes.MessageHandler) (*anthropic.Message, error) {
	tracer := telemetry.Tracer("kodelet.llm.anthropic")

	// Create attributes for the span
	spanAttrs := []attribute.KeyValue{
		attribute.String("model", string(params.Model)),
		attribute.Int64("max_tokens", params.MaxTokens),
	}

	if t.shouldUtiliseThinking(params.Model) {
		spanAttrs = append(spanAttrs,
			attribute.Bool("thinking", params.Thinking.OfEnabled.BudgetTokens > 0),
			attribute.Int64("budget_tokens", params.Thinking.OfEnabled.BudgetTokens),
		)
	}
	for i, sys := range params.System {
		spanAttrs = append(spanAttrs, attribute.String(fmt.Sprintf("system.%d", i), sys.Text))
	}

	logFields := logrus.Fields{
		"model":      string(params.Model),
		"max_tokens": params.MaxTokens,
	}
	if t.shouldUtiliseThinking(params.Model) {
		logFields["thinking"] = params.Thinking.OfEnabled.BudgetTokens > 0
		logFields["budget_tokens"] = params.Thinking.OfEnabled.BudgetTokens
	}
	log := logger.G(ctx).WithFields(logFields)
	log.Debug("new message")

	// Add the last 10 messages (or fewer if there aren't 10) to the span attributes
	spanAttrs = append(spanAttrs, t.getLastMessagesAttributes(params.Messages, 10)...)

	// Create a new span for the API call
	ctx, span := tracer.Start(ctx, "llm.anthropic.new_message", trace.WithAttributes(spanAttrs...))
	defer span.End()

	retryAttempts := t.config.Retry.Attempts

	stream := t.client.Messages.NewStreaming(ctx, params, option.WithMaxRetries(retryAttempts))
	defer stream.Close()

	if stream.Err() != nil {
		log.WithError(stream.Err()).Error("failed to start streaming messages")
		telemetry.RecordError(ctx, stream.Err())
		span.SetStatus(codes.Error, stream.Err().Error())
		return nil, stream.Err()
	}

	message := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		err := message.Accumulate(event)
		if err != nil {
			// issue: https://github.com/anthropics/anthropic-sdk-go/issues/187
			// from the observation this typically happens when the tool call string payload is complicated which confuses the llm
			// this is the best effort to handle the error as right now there is no obvious way to handle it
			// the behaviour is:
			// - message is not accumulated properly
			// - tool call becomes empty thus the tool call executiong returns error
			// - the agentic loop will retry until it succeeds
			// we can also wrap this into a more fancy retry func, but the effect is more or less the same
			//
			// the alternative approach is to return the error, however it will cause all the progress to be lost
			logger.G(ctx).WithError(err).Error("error accumulating message")
			telemetry.RecordError(ctx, err)
			span.SetStatus(codes.Error, err.Error())
			continue
		}

		if stream.Err() != nil {
			logger.G(ctx).WithError(stream.Err()).Error("error streaming message from anthropic")
			telemetry.RecordError(ctx, stream.Err())
			span.SetStatus(codes.Error, stream.Err().Error())
			return nil, stream.Err()
		}

		if streamHandler, ok := handler.(llmtypes.StreamingMessageHandler); ok {
			switch eventVariant := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				switch eventVariant.ContentBlock.AsAny().(type) {
				case anthropic.ThinkingBlock:
					streamHandler.HandleThinkingStart()
				}
			case anthropic.ContentBlockDeltaEvent:
				switch deltaVariant := eventVariant.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					streamHandler.HandleTextDelta(deltaVariant.Text)
				case anthropic.ThinkingDelta:
					streamHandler.HandleThinkingDelta(deltaVariant.Thinking)
				}
			case anthropic.ContentBlockStopEvent:
				streamHandler.HandleContentBlockEnd()
			}
		}
	}

	// Add response data to the span
	span.SetAttributes(
		attribute.Int64("input_tokens", message.Usage.InputTokens),
		attribute.Int64("output_tokens", message.Usage.OutputTokens),
		attribute.Int64("cache_creation_tokens", message.Usage.CacheCreationInputTokens),
		attribute.Int64("cache_read_tokens", message.Usage.CacheReadInputTokens),
	)
	span.SetStatus(codes.Ok, "")

	return &message, nil
}

// getLastMessagesAttributes extracts information from the last n messages for telemetry purposes
func (t *Thread) getLastMessagesAttributes(messages []anthropic.MessageParam, lastN int) []attribute.KeyValue {
	attrs := []attribute.KeyValue{}

	// Determine how many messages to process (last n or all if fewer than n)
	startIdx := 0
	if len(messages) > lastN {
		startIdx = len(messages) - lastN
	}

	// Process the last n messages
	for i := startIdx; i < len(messages); i++ {
		msg := messages[i]
		idx := i - startIdx // relative index for attribute naming

		// Add message role
		attrs = append(attrs, attribute.String(fmt.Sprintf("message.%d.role", idx), string(msg.Role)))

		contentJSON, err := json.Marshal(msg.Content)
		if err != nil {
			attrs = append(attrs, attribute.String(
				fmt.Sprintf("message.%d.content", idx),
				fmt.Sprintf("error marshalling content: %s", err),
			))
		} else {
			attrs = append(attrs, attribute.String(
				fmt.Sprintf("message.%d.content", idx),
				string(contentJSON),
			))
		}
	}

	return attrs
}

func (t *Thread) tools(opt llmtypes.MessageOpt) []tooltypes.Tool {
	if opt.NoToolUse {
		return []tooltypes.Tool{}
	}
	return t.state.Tools()
}

func (t *Thread) updateUsage(response *anthropic.Message, model anthropic.Model) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Track usage statistics
	t.usage.InputTokens += int(response.Usage.InputTokens)
	t.usage.OutputTokens += int(response.Usage.OutputTokens)
	t.usage.CacheCreationInputTokens += int(response.Usage.CacheCreationInputTokens)
	t.usage.CacheReadInputTokens += int(response.Usage.CacheReadInputTokens)

	// Calculate costs based on model pricing
	pricing := getModelPricing(model)

	// showing the usage regardless of subscription
	var (
		inputPricing         = pricing.Input
		outputPricing        = pricing.Output
		cacheCreationPricing = pricing.PromptCachingWrite
		cacheReadPricing     = pricing.PromptCachingRead
	)

	// Calculate individual costs
	t.usage.InputCost += float64(response.Usage.InputTokens) * inputPricing
	t.usage.OutputCost += float64(response.Usage.OutputTokens) * outputPricing
	t.usage.CacheCreationCost += float64(response.Usage.CacheCreationInputTokens) * cacheCreationPricing
	t.usage.CacheReadCost += float64(response.Usage.CacheReadInputTokens) * cacheReadPricing

	t.usage.CurrentContextWindow = int(response.Usage.InputTokens) + int(response.Usage.OutputTokens) + int(response.Usage.CacheCreationInputTokens) + int(response.Usage.CacheReadInputTokens)
	t.usage.MaxContextWindow = pricing.ContextWindow
}

// NewSubAgent creates a new subagent thread that shares the parent's client and usage tracking
func (t *Thread) NewSubAgent(_ context.Context, config llmtypes.Config) llmtypes.Thread {
	conversationID := convtypes.GenerateID()

	// Create subagent thread reusing the parent's client instead of creating a new one
	thread := &Thread{
		client:                 t.client, // Reuse parent's client
		config:                 config,
		useSubscription:        t.useSubscription, // Reuse parent's subscription status
		conversationID:         conversationID,
		isPersisted:            false,                                                         // subagent is not persisted
		usage:                  t.usage,                                                       // Share usage tracking with parent
		toolResults:            make(map[string]tooltypes.StructuredToolResult),               // Initialize tool results map
		subagentContextFactory: t.subagentContextFactory,                                      // Propagate the injected function
		hookTrigger:            hooks.NewTrigger(t.hookTrigger.Manager, conversationID, true), // Create new trigger with shared hook manager
	}

	return thread
}

// getLastAssistantMessageText extracts text content from the most recent assistant message
func (t *Thread) getLastAssistantMessageText() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.messages) == 0 {
		return "", errors.New("no messages found")
	}

	// Find the last assistant message
	var messageText string
	for i := len(t.messages) - 1; i >= 0; i-- {
		msg := t.messages[i]
		if msg.Role == anthropic.MessageParamRoleAssistant {
			// Extract text from the assistant message content blocks
			for _, block := range msg.Content {
				if block.OfText != nil {
					messageText += block.OfText.Text
				}
			}
			break
		}
	}

	if messageText == "" {
		return "", errors.New("no text content found in assistant message")
	}

	return messageText, nil
}

// ShortSummary generates a short summary of the conversation using a weak model
func (t *Thread) ShortSummary(ctx context.Context) string {
	summaryThread, err := NewAnthropicThread(t.GetConfig(), nil)
	if err != nil {
		logger.G(ctx).WithError(err).Error("failed to create summary thread")
		return "Could not generate summary."
	}

	summaryThread.messages = t.messages
	summaryThread.EnablePersistence(ctx, false)
	summaryThread.hookTrigger = hooks.Trigger{} // disable hooks for summary

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	summaryThread.SendMessage(ctx, prompts.ShortSummaryPrompt, handler, llmtypes.MessageOpt{
		UseWeakModel:       true,
		PromptCache:        false,
		NoToolUse:          true,
		DisableAutoCompact: true,
		DisableUsageLog:    true,
		NoSaveConversation: true,
	})

	return handler.CollectedText()
}

// shouldAutoCompact checks if auto-compact should be triggered based on context window utilization
func (t *Thread) shouldAutoCompact(compactRatio float64) bool {
	if compactRatio <= 0.0 || compactRatio > 1.0 {
		return false
	}

	usage := t.GetUsage()
	if usage.MaxContextWindow == 0 {
		return false
	}

	utilizationRatio := float64(usage.CurrentContextWindow) / float64(usage.MaxContextWindow)
	return utilizationRatio >= compactRatio
}

// CompactContext performs comprehensive context compacting by creating a detailed summary
func (t *Thread) CompactContext(ctx context.Context) error {
	// Temporarily disable persistence during compacting
	wasPersistedOriginal := t.isPersisted
	t.isPersisted = false
	defer func() {
		t.isPersisted = wasPersistedOriginal
	}()

	// Use the strong model for comprehensive compacting (opposite of ShortSummary)
	_, err := t.SendMessage(ctx, prompts.CompactPrompt, &llmtypes.StringCollectorHandler{Silent: true}, llmtypes.MessageOpt{
		UseWeakModel:       false, // Use strong model for comprehensive compacting
		PromptCache:        false, // Don't cache the compacting prompt
		NoToolUse:          true,
		DisableAutoCompact: true, // Prevent recursion
		DisableUsageLog:    true, // Don't log usage for internal compact operations
		// Note: Not using NoSaveConversation so we can access the assistant response
	})
	if err != nil {
		return errors.Wrap(err, "failed to generate compact summary")
	}

	// Get the compact summary from the last assistant message
	compactSummary, err := t.getLastAssistantMessageText()
	if err != nil {
		return errors.Wrap(err, "failed to get compact summary from assistant message")
	}

	// Replace the conversation history with the compact summary
	t.mu.Lock()
	defer t.mu.Unlock()

	t.messages = []anthropic.MessageParam{
		{
			Role: anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock(compactSummary),
			},
		},
	}

	// Clear stale tool results - they reference tool calls that no longer exist
	t.toolResults = make(map[string]tooltypes.StructuredToolResult)

	// Get state reference while under mutex protection
	state := t.state

	// Clear file access tracking to start fresh with context retrieval
	if state != nil {
		state.SetFileLastAccess(make(map[string]time.Time))
	}

	return nil
}

// GetUsage returns the current token usage for the thread
func (t *Thread) GetUsage() llmtypes.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.usage
}

// GetConfig returns the configuration of the thread
func (t *Thread) GetConfig() llmtypes.Config {
	return t.config
}

// GetConversationID returns the current conversation ID
func (t *Thread) GetConversationID() string {
	return t.conversationID
}

// SetConversationID sets the conversation ID
func (t *Thread) SetConversationID(id string) {
	t.conversationID = id
	t.hookTrigger.SetConversationID(id)
}

// IsPersisted returns whether this thread is being persisted
func (t *Thread) IsPersisted() bool {
	return t.isPersisted
}

// GetMessages returns the current messages in the thread
func (t *Thread) GetMessages() ([]llmtypes.Message, error) {
	b, err := json.Marshal(t.messages)
	if err != nil {
		return nil, err
	}
	return ExtractMessages(b, t.GetStructuredToolResults())
}

// EnablePersistence enables conversation persistence for this thread
func (t *Thread) EnablePersistence(ctx context.Context, enabled bool) {
	t.isPersisted = enabled

	// Initialize the store if enabling persistence and it's not already initialized
	if enabled && t.store == nil {
		store, err := conversations.GetConversationStore(ctx)
		if err != nil {
			// Log the error but continue without persistence
			fmt.Printf("Error initializing conversation store: %v\n", err)
			t.isPersisted = false
			return
		}
		t.store = store
	}

	// If enabling persistence and there's an existing conversation ID,
	// try to load it from the store
	if enabled && t.store != nil {
		t.loadConversation(ctx)
	}
}

// createMessageSpan creates and configures a tracing span for message handling
func (t *Thread) createMessageSpan(
	ctx context.Context,
	tracer trace.Tracer,
	message string,
	opt llmtypes.MessageOpt,
) (context.Context, trace.Span) {
	attributes := []attribute.KeyValue{
		attribute.String("model", t.config.Model),
		attribute.Int("max_tokens", t.config.MaxTokens),
		attribute.Int("weak_model_max_tokens", t.config.WeakModelMaxTokens),
		attribute.Int("thinking_budget_tokens", t.config.ThinkingBudgetTokens),
		attribute.Bool("prompt_cache", opt.PromptCache),
		attribute.Bool("use_weak_model", opt.UseWeakModel),
		attribute.Bool("is_sub_agent", t.config.IsSubAgent),
		attribute.String("conversation_id", t.conversationID),
		attribute.Bool("is_persisted", t.isPersisted),
		attribute.Int("message_length", len(message)),
	}

	return tracer.Start(ctx, "llm.send_message", trace.WithAttributes(attributes...))
}

// finalizeMessageSpan records final metrics and status to the span before ending it
func (t *Thread) finalizeMessageSpan(span trace.Span, err error) {
	// Record usage metrics after completion
	usage := t.GetUsage()
	span.SetAttributes(
		attribute.Int("tokens.input", usage.InputTokens),
		attribute.Int("tokens.output", usage.OutputTokens),
		attribute.Int("tokens.cache_creation", usage.CacheCreationInputTokens),
		attribute.Int("tokens.cache_read", usage.CacheReadInputTokens),
		attribute.Float64("cost.total", usage.TotalCost()),
		attribute.Int("context_window.current", usage.CurrentContextWindow),
		attribute.Int("context_window.max", usage.MaxContextWindow),
	)

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	} else {
		span.SetStatus(codes.Ok, "")
		span.AddEvent("message_processing_completed")
	}
	span.End()
}

// processImage converts an image path/URL to an Anthropic image content block
func (t *Thread) processImage(imagePath string) (*anthropic.ContentBlockParamUnion, error) {
	// Only allow HTTPS URLs for security
	if strings.HasPrefix(imagePath, "https://") {
		return t.processImageURL(imagePath)
	}
	if filePath, ok := strings.CutPrefix(imagePath, "file://"); ok {
		// Remove file:// prefix and process as file
		return t.processImageFile(filePath)
	}
	// Treat as a local file path
	return t.processImageFile(imagePath)
}

func (t *Thread) processImageURL(url string) (*anthropic.ContentBlockParamUnion, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, errors.Errorf("only HTTPS URLs are supported for security: %s", url)
	}

	block := anthropic.NewImageBlock(anthropic.URLImageSourceParam{
		Type: "url",
		URL:  url,
	})
	return &block, nil
}

func (t *Thread) processImageFile(filePath string) (*anthropic.ContentBlockParamUnion, error) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, errors.Errorf("image file not found: %s", filePath)
	}

	// Determine media type from file extension first
	mediaType, err := getMediaTypeFromExtension(filepath.Ext(filePath))
	if err != nil {
		return nil, errors.Errorf("unsupported image format: %s (supported: .jpg, .jpeg, .png, .gif, .webp)", filepath.Ext(filePath))
	}

	// Check file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get file info")
	}
	if fileInfo.Size() > MaxImageFileSize {
		return nil, errors.Errorf("image file too large: %d bytes (max: %d bytes)", fileInfo.Size(), MaxImageFileSize)
	}

	// Read and encode the file
	imageData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read image file")
	}

	// Encode to base64
	base64Data := base64.StdEncoding.EncodeToString(imageData)

	block := anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
		Type:      "base64",
		MediaType: mediaType,
		Data:      base64Data,
	})
	return &block, nil
}

// getMediaTypeFromExtension returns the Anthropic media type for supported image formats only
func getMediaTypeFromExtension(ext string) (anthropic.Base64ImageSourceMediaType, error) {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return anthropic.Base64ImageSourceMediaTypeImageJPEG, nil
	case ".png":
		return anthropic.Base64ImageSourceMediaTypeImagePNG, nil
	case ".gif":
		return anthropic.Base64ImageSourceMediaTypeImageGIF, nil
	case ".webp":
		return anthropic.Base64ImageSourceMediaTypeImageWebP, nil
	default:
		return "", errors.New("unsupported format")
	}
}

// SetStructuredToolResult stores the structured result for a tool call
func (t *Thread) SetStructuredToolResult(toolCallID string, result tooltypes.StructuredToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	}
	t.toolResults[toolCallID] = result
}

// GetStructuredToolResults returns all structured tool results
func (t *Thread) GetStructuredToolResults() map[string]tooltypes.StructuredToolResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults == nil {
		return make(map[string]tooltypes.StructuredToolResult)
	}
	// Return a copy to avoid race conditions
	result := make(map[string]tooltypes.StructuredToolResult)
	maps.Copy(result, t.toolResults)
	return result
}

// SetStructuredToolResults sets all structured tool results (for loading from conversation)
func (t *Thread) SetStructuredToolResults(results map[string]tooltypes.StructuredToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if results == nil {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
	} else {
		t.toolResults = make(map[string]tooltypes.StructuredToolResult)
		maps.Copy(t.toolResults, results)
	}
}
