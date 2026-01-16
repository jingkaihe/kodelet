// Package anthropic provides a client implementation for interacting with Anthropic's Claude AI models.
// It implements the LLM Thread interface for managing conversations, tool execution, and message processing.
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/jingkaihe/kodelet/pkg/feedback"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/hooks"
	"github.com/jingkaihe/kodelet/pkg/llm/base"
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

// Thread implements the Thread interface using Anthropic's Claude API
// It embeds base.Thread to inherit common functionality.
type Thread struct {
	*base.Thread                     // Embedded base thread for shared functionality
	client          anthropic.Client // Anthropic API client
	messages        []anthropic.MessageParam
	summary         string // Conversation summary
	useSubscription bool   // Whether using Anthropic subscription vs API key
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
		headerOpts, err := auth.AnthropicHeader(context.Background(), config.AnthropicAccount)
		if err != nil {
			return nil, errors.Wrap(err, "subscription authentication forced but failed to get access token")
		}
		if config.AnthropicAccount != "" {
			logger.WithField("account", config.AnthropicAccount).Debug("using anthropic access token for specified account")
		} else {
			logger.Debug("using anthropic access token (forced by configuration)")
		}
		opts = append(opts, headerOpts...)
		client = anthropic.NewClient(opts...)
		useSubscription = true

	case llmtypes.AnthropicAPIAccessAuto:
		fallthrough
	default:
		// Auto mode: try subscription first, then fall back to API key
		antCredsExists, _ := auth.GetAnthropicCredentialsExists()
		if antCredsExists {
			headerOpts, err := auth.AnthropicHeader(context.Background(), config.AnthropicAccount)
			if err != nil {
				logger.WithError(err).Error("failed to get anthropic access token, falling back to use API key")
				client = anthropic.NewClient()
				useSubscription = false
			} else {
				if config.AnthropicAccount != "" {
					logger.WithField("account", config.AnthropicAccount).Debug("using anthropic access token for specified account")
				} else {
					logger.Debug("using anthropic access token")
				}
				opts = append(opts, headerOpts...)
				client = anthropic.NewClient(opts...)
				useSubscription = true
			}
		} else {
			logger.Debug("no anthropic credentials found, falling back to use API key")
			client = anthropic.NewClient(opts...)
			useSubscription = false
		}
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

	// Create the base thread with shared functionality
	baseThread := base.NewThread(config, conversationID, subagentContextFactory, hookTrigger)

	thread := &Thread{
		Thread:          baseThread,
		client:          client,
		useSubscription: useSubscription,
	}

	// Set the LoadConversation callback for provider-specific loading
	baseThread.LoadConversation = thread.loadConversation

	return thread, nil
}

// AddUserMessage adds a user message with optional images to the thread
func (t *Thread) AddUserMessage(ctx context.Context, message string, imagePaths ...string) {
	contentBlocks := []anthropic.ContentBlockParamUnion{}

	// Validate image count
	if len(imagePaths) > base.MaxImageCount {
		logger.G(ctx).Warnf("Too many images provided (%d), maximum is %d. Only processing first %d images", len(imagePaths), base.MaxImageCount, base.MaxImageCount)
		imagePaths = imagePaths[:base.MaxImageCount]
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

	// Anthropic-specific attributes for tracing
	extraSpanAttrs := []attribute.KeyValue{
		attribute.Int("thinking_budget_tokens", t.Config.ThinkingBudgetTokens),
		attribute.Bool("prompt_cache", opt.PromptCache),
	}
	ctx, span := t.CreateMessageSpan(ctx, tracer, message, opt, extraSpanAttrs...)
	defer func() {
		// Anthropic-specific cache attributes for finalization
		usage := t.GetUsage()
		extraFinalizeAttrs := []attribute.KeyValue{
			attribute.Int("tokens.cache_creation", usage.CacheCreationInputTokens),
			attribute.Int("tokens.cache_read", usage.CacheReadInputTokens),
		}
		t.FinalizeMessageSpan(span, err, extraFinalizeAttrs...)
	}()

	if opt.PromptCache {
		t.cacheMessages()
	}

	var originalMessages []anthropic.MessageParam
	if opt.PromptCache {
		originalMessages = make([]anthropic.MessageParam, len(t.messages))
		copy(originalMessages, t.messages)
	}

	// Trigger user_message_send hook before adding user message
	if blocked, reason := t.HookTrigger.TriggerUserMessageSend(ctx, t, message, t.GetRecipeHooks()); blocked {
		return "", errors.Errorf("message blocked by hook: %s", reason)
	}

	t.AddUserMessage(ctx, message, opt.Images...)

	model, maxTokens := t.getModelAndTokens(opt)

	turnCount := 0
	maxTurns := max(opt.MaxTurns, 0)

	cacheEvery := t.Config.CacheEvery

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
			if !opt.DisableAutoCompact && t.ShouldAutoCompact(opt.CompactRatio) {
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
			if t.State != nil {
				contexts = t.State.DiscoverContexts()
			}
			var systemPrompt string
			if t.Config.IsSubAgent {
				systemPrompt = sysprompt.SubAgentPrompt(string(model), t.Config, contexts)
			} else {
				systemPrompt = sysprompt.SystemPrompt(string(model), t.Config, contexts)
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

			// Trigger turn_end hook after assistant response is complete
			if finalOutput != "" {
				t.HookTrigger.TriggerTurnEnd(ctx, t, finalOutput, turnCount, t.GetRecipeHooks())
			}

			// If no tools were used, check for hook follow-ups before stopping
			if !toolsUsed {
				logger.G(ctx).Debug("no tools used, checking agent_stop hook")

				// Trigger agent_stop hook to see if there are follow-up messages
				if messages, err := t.GetMessages(); err == nil {
					if followUps := t.HookTrigger.TriggerAgentStop(ctx, t, messages, t.GetRecipeHooks()); len(followUps) > 0 {
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
	if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
		saveCtx := context.Background() // use new context to avoid cancellation
		t.SaveConversation(saveCtx, true)
	}

	if !t.Config.IsSubAgent {
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

// normalizeToolName maps subscription tool names to internal tool names.
func (t *Thread) normalizeToolName(name string) string {
	if t.useSubscription {
		return decapitalizeToolName(name)
	}
	return name
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
		toolName := t.normalizeToolName(tb.block.Name)
		handler.HandleToolUse(tb.block.ID, toolName, tb.variant.JSON.Input.Raw())
	}

	results := make([]toolExecResult, len(toolBlocks))
	resultCh := make(chan toolExecResult, len(toolBlocks))

	g, gctx := errgroup.WithContext(ctx)

	for i, tb := range toolBlocks {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}

			// Strip subscription prefix from tool name for internal use
			toolName := t.normalizeToolName(tb.block.Name)

			telemetry.AddEvent(gctx, "tool_execution_start",
				attribute.String("tool_name", toolName),
				attribute.Int("tool_index", i),
			)

			// Trigger before_tool_call hook
			toolInput := tb.variant.JSON.Input.Raw()
			blocked, reason, toolInput := t.HookTrigger.TriggerBeforeToolCall(gctx, t, toolName, toolInput, tb.block.ID, t.GetRecipeHooks())

			var output tooltypes.ToolResult
			if blocked {
				output = tooltypes.NewBlockedToolResult(toolName, reason)
			} else {
				// Use a per-goroutine silent handler to avoid race conditions on shared handler
				// XXX: It's tricky to visualise agent streaming in the terminal therefore we disable it for now
				parallelHandler := &llmtypes.StringCollectorHandler{Silent: true}
				runToolCtx := t.SubagentContextFactory(gctx, t, parallelHandler, opt.CompactRatio, opt.DisableAutoCompact)
				output = tools.RunTool(runToolCtx, t.State, toolName, toolInput)
			}

			if err := gctx.Err(); err != nil {
				return err
			}

			structuredResult := output.StructuredData()

			// Trigger after_tool_call hook
			if modified := t.HookTrigger.TriggerAfterToolCall(gctx, t, toolName, toolInput, tb.block.ID, structuredResult, t.GetRecipeHooks()); modified != nil {
				structuredResult = *modified
			}

			registry := renderers.NewRendererRegistry()
			renderedOutput := registry.Render(structuredResult)

			telemetry.AddEvent(gctx, "tool_execution_complete",
				attribute.String("tool_name", toolName),
				attribute.Int("tool_index", i),
				attribute.String("result", output.AssistantFacing()),
			)

			result := toolExecResult{
				index:          i,
				blockID:        tb.block.ID,
				toolName:       toolName,
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
			handler.HandleToolResult(result.blockID, result.toolName, result.output)
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
		Tools:     toAnthropicTools(t.tools(opt), t.useSubscription),
	}
	if t.shouldUtiliseThinking(model) {
		messageParams.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{
				Type:         "enabled",
				BudgetTokens: int64(t.Config.ThinkingBudgetTokens),
			},
		}
	}

	if !t.Config.IsSubAgent {
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
		if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
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
	if !t.Config.IsSubAgent && !opt.DisableUsageLog {
		usage.LogLLMUsage(ctx, t.GetUsage(), string(model), apiStartTime, int(response.Usage.OutputTokens))
	}

	if t.Persisted && t.Store != nil && !opt.NoSaveConversation {
		t.SaveConversation(ctx, false)
	}

	// Return whether tools were used in this exchange
	return finalOutput, toolUseCount > 0, nil
}

func (t *Thread) processPendingFeedback(ctx context.Context, messageParams *anthropic.MessageNewParams, handler llmtypes.MessageHandler) error {
	feedbackStore, err := feedback.NewFeedbackStore()
	if err != nil {
		return errors.Wrap(err, "failed to create feedback store")
	}

	pendingFeedback, err := feedbackStore.ReadPendingFeedback(t.ConversationID)
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

		if err := feedbackStore.ClearPendingFeedback(t.ConversationID); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to clear pending feedback, may be processed again")
		} else {
			logger.G(ctx).Debug("successfully cleared pending feedback")
		}
	}

	return nil
}

func (t *Thread) getModelAndTokens(opt llmtypes.MessageOpt) (anthropic.Model, int) {
	model := t.Config.Model
	maxTokens := t.Config.MaxTokens

	if opt.UseWeakModel && t.Config.WeakModel != "" {
		model = t.Config.WeakModel
		if t.Config.WeakModelMaxTokens > 0 {
			maxTokens = t.Config.WeakModelMaxTokens
		}
	}

	return anthropic.Model(model), maxTokens
}

func (t *Thread) shouldUtiliseThinking(model anthropic.Model) bool {
	if !isThinkingModel(model) {
		return false
	}
	if t.Config.ThinkingBudgetTokens == 0 {
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

	retryAttempts := t.Config.Retry.Attempts

	stream := t.client.Messages.NewStreaming(ctx, params, option.WithMaxRetries(retryAttempts))
	defer stream.Close()

	if stream.Err() != nil {
		log.WithError(stream.Err()).Error("failed to start streaming messages")
		telemetry.RecordError(ctx, stream.Err())
		span.SetStatus(codes.Error, stream.Err().Error())
		return nil, stream.Err()
	}

	message := anthropic.Message{}
	inThinkingBlock := false
	for stream.Next() {
		// Check for context cancellation - Anthropic SDK may not propagate it properly
		if ctx.Err() != nil {
			log.WithError(ctx.Err()).Info("context cancelled during streaming")
			return nil, ctx.Err()
		}

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
					inThinkingBlock = true
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
				if inThinkingBlock {
					streamHandler.HandleThinkingBlockEnd()
					inThinkingBlock = false
				} else {
					streamHandler.HandleContentBlockEnd()
				}
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
	return t.State.Tools()
}

func (t *Thread) updateUsage(response *anthropic.Message, model anthropic.Model) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	// Track usage statistics
	t.Usage.InputTokens += int(response.Usage.InputTokens)
	t.Usage.OutputTokens += int(response.Usage.OutputTokens)
	t.Usage.CacheCreationInputTokens += int(response.Usage.CacheCreationInputTokens)
	t.Usage.CacheReadInputTokens += int(response.Usage.CacheReadInputTokens)

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
	t.Usage.InputCost += float64(response.Usage.InputTokens) * inputPricing
	t.Usage.OutputCost += float64(response.Usage.OutputTokens) * outputPricing
	t.Usage.CacheCreationCost += float64(response.Usage.CacheCreationInputTokens) * cacheCreationPricing
	t.Usage.CacheReadCost += float64(response.Usage.CacheReadInputTokens) * cacheReadPricing

	t.Usage.CurrentContextWindow = int(response.Usage.InputTokens) + int(response.Usage.OutputTokens) + int(response.Usage.CacheCreationInputTokens) + int(response.Usage.CacheReadInputTokens)
	t.Usage.MaxContextWindow = pricing.ContextWindow
}

// NewSubAgent creates a new subagent thread that shares the parent's client and usage tracking
func (t *Thread) NewSubAgent(_ context.Context, config llmtypes.Config) llmtypes.Thread {
	conversationID := convtypes.GenerateID()

	// Create subagent hook trigger using parent's manager
	hookTrigger := hooks.NewTrigger(t.HookTrigger.Manager, conversationID, true)

	baseThread := base.NewThread(config, conversationID, t.SubagentContextFactory, hookTrigger)

	// Create subagent thread reusing the parent's client instead of creating a new one
	thread := &Thread{
		Thread:          baseThread,
		client:          t.client,          // Reuse parent's client
		useSubscription: t.useSubscription, // Reuse parent's subscription status
	}

	return thread
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
	summaryThread.HookTrigger = hooks.Trigger{} // disable hooks for summary

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

// SwapContext replaces the conversation history with a summary message.
// This implements the hooks.ContextSwapper interface.
func (t *Thread) SwapContext(_ context.Context, summary string) error {
	t.Mu.Lock()
	defer t.Mu.Unlock()

	t.messages = []anthropic.MessageParam{
		{
			Role: anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock(summary),
			},
		},
	}

	// Clear stale tool results - they reference tool calls that no longer exist
	t.ToolResults = make(map[string]tooltypes.StructuredToolResult)

	// heuristic estimation of context window size based on summary length
	t.EstimateContextWindowFromMessage(summary)

	// Get state reference while under mutex protection
	state := t.State

	// Clear file access tracking to start fresh with context retrieval
	if state != nil {
		state.SetFileLastAccess(make(map[string]time.Time))
	}

	return nil
}

// CompactContext performs comprehensive context compacting by creating a detailed summary
func (t *Thread) CompactContext(ctx context.Context) error {
	compactPrompt, err := fragments.LoadCompactPrompt(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to load compact prompt")
	}

	summaryThread, err := NewAnthropicThread(t.GetConfig(), nil)
	if err != nil {
		return errors.Wrap(err, "failed to create summary thread")
	}

	summaryThread.messages = t.messages
	summaryThread.EnablePersistence(ctx, false)
	summaryThread.HookTrigger = hooks.Trigger{}

	handler := &llmtypes.StringCollectorHandler{Silent: true}
	_, err = summaryThread.SendMessage(ctx, compactPrompt, handler, llmtypes.MessageOpt{
		UseWeakModel:       false,
		PromptCache:        false,
		NoToolUse:          true,
		DisableAutoCompact: true,
		DisableUsageLog:    true,
		NoSaveConversation: true,
	})
	if err != nil {
		return errors.Wrap(err, "failed to generate compact summary")
	}

	return t.SwapContext(ctx, handler.CollectedText())
}

// GetMessages returns the current messages in the thread
func (t *Thread) GetMessages() ([]llmtypes.Message, error) {
	b, err := json.Marshal(t.messages)
	if err != nil {
		return nil, err
	}
	return ExtractMessages(b, t.GetStructuredToolResults())
}

// processImage converts an image path/URL to an Anthropic image content block
func (t *Thread) processImage(imagePath string) (*anthropic.ContentBlockParamUnion, error) {
	// Only allow HTTPS URLs for security
	if strings.HasPrefix(imagePath, "https://") {
		return t.processImageURL(imagePath)
	}
	if strings.HasPrefix(imagePath, "data:") {
		return t.processImageDataURL(imagePath)
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

func (t *Thread) processImageDataURL(dataURL string) (*anthropic.ContentBlockParamUnion, error) {
	// Parse data URL format: data:<mediatype>;base64,<data>
	if !strings.HasPrefix(dataURL, "data:") {
		return nil, errors.New("invalid data URL: must start with 'data:'")
	}

	// Remove "data:" prefix
	rest := strings.TrimPrefix(dataURL, "data:")

	// Split by ";base64,"
	parts := strings.SplitN(rest, ";base64,", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid data URL: must contain ';base64,' separator")
	}

	mimeType := parts[0]
	base64Data := parts[1]

	// Validate mime type is a supported image type
	mediaType, err := mimeTypeToAnthropicMediaType(mimeType)
	if err != nil {
		return nil, errors.Wrapf(err, "unsupported image mime type: %s", mimeType)
	}

	block := anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
		Type:      "base64",
		MediaType: mediaType,
		Data:      base64Data,
	})
	return &block, nil
}

// mimeTypeToAnthropicMediaType converts a MIME type string to Anthropic's Base64ImageSourceMediaType
func mimeTypeToAnthropicMediaType(mimeType string) (anthropic.Base64ImageSourceMediaType, error) {
	switch strings.ToLower(mimeType) {
	case "image/jpeg":
		return anthropic.Base64ImageSourceMediaTypeImageJPEG, nil
	case "image/png":
		return anthropic.Base64ImageSourceMediaTypeImagePNG, nil
	case "image/gif":
		return anthropic.Base64ImageSourceMediaTypeImageGIF, nil
	case "image/webp":
		return anthropic.Base64ImageSourceMediaTypeImageWebP, nil
	default:
		return "", errors.New("unsupported image type")
	}
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
	if fileInfo.Size() > base.MaxImageFileSize {
		return nil, errors.Errorf("image file too large: %d bytes (max: %d bytes)", fileInfo.Size(), base.MaxImageFileSize)
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
