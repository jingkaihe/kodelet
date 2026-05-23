package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/goals"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type plainResponseWriter struct {
	header     http.Header
	body       strings.Builder
	statusCode int
}

func newPlainResponseWriter() *plainResponseWriter {
	return &plainResponseWriter{header: make(http.Header)}
}

func (w *plainResponseWriter) Header() http.Header {
	return w.header
}

func (w *plainResponseWriter) Write(payload []byte) (int, error) {
	return w.body.Write(payload)
}

func (w *plainResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

type failingChatSink struct {
	err error
}

func (s failingChatSink) Send(ChatEvent) error {
	return s.err
}

type fakeMetadataThread struct {
	metadata map[string]any
}

func (f *fakeMetadataThread) SetState(tooltypes.State) {}

func (f *fakeMetadataThread) GetState() tooltypes.State { return nil }

func (f *fakeMetadataThread) AddUserMessage(context.Context, string, ...string) {}

func (f *fakeMetadataThread) SendMessage(context.Context, string, llmtypes.MessageHandler, llmtypes.MessageOpt) (string, error) {
	return "", nil
}

func (f *fakeMetadataThread) GetUsage() llmtypes.Usage { return llmtypes.Usage{} }

func (f *fakeMetadataThread) GetConversationID() string { return "" }

func (f *fakeMetadataThread) SetConversationID(string) {}

func (f *fakeMetadataThread) SaveConversation(context.Context, bool) error { return nil }

func (f *fakeMetadataThread) IsPersisted() bool { return false }

func (f *fakeMetadataThread) EnablePersistence(context.Context, bool) {}

func (f *fakeMetadataThread) Provider() string { return "" }

func (f *fakeMetadataThread) GetMessages() ([]llmtypes.Message, error) { return nil, nil }

func (f *fakeMetadataThread) GetConfig() llmtypes.Config { return llmtypes.Config{} }

func (f *fakeMetadataThread) AggregateSubagentUsage(llmtypes.Usage) {}

func (f *fakeMetadataThread) SetMetadataValue(key string, value any) {
	if f.metadata == nil {
		f.metadata = make(map[string]any)
	}
	f.metadata[key] = value
}

func (f *fakeMetadataThread) GetMetadata() map[string]any {
	return f.metadata
}

func TestNewDefaultChatRunnerStoresDefaultCWD(t *testing.T) {
	runner := NewDefaultChatRunner("/workspace")

	require.NotNil(t, runner)
	assert.Equal(t, "/workspace", runner.defaultCWD)
}

func TestServiceStoreAdapterLoadAndUnsupportedMethods(t *testing.T) {
	now := time.Now().UTC()
	toolResults := map[string]tooltypes.StructuredToolResult{
		"tool-1": {ToolName: "bash", Success: true},
	}
	service := &mockConversationService{
		getFunc: func(_ context.Context, id string) (*conversations.GetConversationResponse, error) {
			assert.Equal(t, "conv-123", id)
			return &conversations.GetConversationResponse{
				ID:          id,
				CWD:         "/workspace/project",
				Provider:    "openai",
				Metadata:    map[string]any{"profile": "work"},
				RawMessages: json.RawMessage(`[{}]`),
				CreatedAt:   now,
				UpdatedAt:   now.Add(time.Minute),
				Usage:       llmtypes.Usage{InputTokens: 11, OutputTokens: 7},
				Summary:     "summary",
				ToolResults: toolResults,
			}, nil
		},
	}
	adapter := serviceStoreAdapter{service: service}

	record, err := adapter.Load(context.Background(), "conv-123")
	require.NoError(t, err)
	assert.Equal(t, "conv-123", record.ID)
	assert.Equal(t, "/workspace/project", record.CWD)
	assert.Equal(t, "openai", record.Provider)
	assert.Equal(t, map[string]any{"profile": "work"}, record.Metadata)
	assert.Equal(t, json.RawMessage(`[{}]`), record.RawMessages)
	assert.Equal(t, now, record.CreatedAt)
	assert.Equal(t, now.Add(time.Minute), record.UpdatedAt)
	assert.Equal(t, llmtypes.Usage{InputTokens: 11, OutputTokens: 7}, record.Usage)
	assert.Equal(t, "summary", record.Summary)
	assert.Equal(t, toolResults, record.ToolResults)

	require.ErrorContains(t, adapter.Save(context.Background(), convtypes.ConversationRecord{}), "save not implemented")
	require.ErrorContains(t, adapter.Delete(context.Background(), "conv-123"), "delete not implemented")
	_, err = adapter.Query(context.Background(), convtypes.QueryOptions{})
	require.ErrorContains(t, err, "query not implemented")
	assert.NoError(t, adapter.Close())
}

func TestNormalizeChatRequestAdditionalBranches(t *testing.T) {
	tests := []struct {
		name          string
		req           ChatRequest
		wantMessage   string
		wantImages    []string
		wantErrSubstr string
	}{
		{
			name:        "message only trims whitespace",
			req:         ChatRequest{Message: "  hello  "},
			wantMessage: "hello",
		},
		{
			name: "text content replaces message and joins blocks",
			req: ChatRequest{
				Message: "ignored",
				Content: []ChatContentBlock{
					{Type: "text", Text: " first "},
					{Type: "text", Text: ""},
					{Type: "text", Text: "second"},
				},
			},
			wantMessage: "first\n\nsecond",
			wantImages:  []string{},
		},
		{
			name: "image url keeps caption message",
			req: ChatRequest{
				Message: " caption ",
				Content: []ChatContentBlock{{
					Type:     "image",
					ImageURL: &ChatImageURLSource{URL: " https://example.com/image.png "},
				}},
			},
			wantMessage: "caption",
			wantImages:  []string{"https://example.com/image.png"},
		},
		{
			name: "image source becomes data url",
			req: ChatRequest{Content: []ChatContentBlock{{
				Type:   "image",
				Source: &ChatImageSource{Data: " aGVsbG8= ", MediaType: " image/png "},
			}}},
			wantImages: []string{"data:image/png;base64,aGVsbG8="},
		},
		{
			name: "image source requires data",
			req: ChatRequest{Content: []ChatContentBlock{{
				Type:   "image",
				Source: &ChatImageSource{MediaType: "image/png"},
			}}},
			wantErrSubstr: "image source must include data and media_type",
		},
		{
			name: "image url requires url",
			req: ChatRequest{Content: []ChatContentBlock{{
				Type:     "image",
				ImageURL: &ChatImageURLSource{},
			}}},
			wantErrSubstr: "image_url must include url",
		},
		{
			name:          "image block requires source",
			req:           ChatRequest{Content: []ChatContentBlock{{Type: "image"}}},
			wantErrSubstr: "image block must include source or image_url",
		},
		{
			name:          "unsupported block type",
			req:           ChatRequest{Content: []ChatContentBlock{{Type: "audio"}}},
			wantErrSubstr: "unsupported content block type: audio",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, images, err := normalizeChatRequest(tt.req)
			if tt.wantErrSubstr != "" {
				require.ErrorContains(t, err, tt.wantErrSubstr)
				assert.Empty(t, message)
				assert.Nil(t, images)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMessage, message)
			assert.Equal(t, tt.wantImages, images)
		})
	}
}

func TestResolveWebChatConfigForNewConversationProfileBranches(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "base-model")
	viper.Set("profile", "active")
	viper.Set("profiles", map[string]any{
		"active": map[string]any{"provider": "openai", "model": "active-model"},
		"work":   map[string]any{"provider": "openai", "model": "work-model"},
	})

	config, err := resolveWebChatConfigForNewConversation(" work ")
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "work-model", config.Model)
	assert.Equal(t, "work", config.Profile)

	config, err = resolveWebChatConfigForNewConversation("   ")
	require.NoError(t, err)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, "active-model", config.Model)
	assert.Equal(t, "active", config.Profile)

	_, err = resolveWebChatConfigForNewConversation("missing")
	require.ErrorContains(t, err, "profile 'missing' not found")

	assert.Equal(t, "", normalizeRequestedProfile(""))
	assert.Equal(t, "", normalizeRequestedProfile(" default "))
	assert.Equal(t, "team", normalizeRequestedProfile(" team "))
}

func TestResolveWebChatConfigForExistingConversationNilAndFallbackBranches(t *testing.T) {
	originalSettings := viper.AllSettings()
	defer func() {
		viper.Reset()
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	}()

	viper.Reset()
	viper.Set("provider", "anthropic")
	viper.Set("model", "base-model")
	viper.Set("profiles", map[string]any{
		"work": map[string]any{"provider": "openai", "model": "work-model"},
	})

	config, err := resolveWebChatConfigForExistingConversation(nil)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "base-model", config.Model)

	config, err = resolveWebChatConfigForExistingConversation(&conversations.GetConversationResponse{
		ID:       "conv-123",
		Provider: "  anthropic  ",
		Metadata: map[string]any{"profile": " work ", "model": " stored-model "},
	})
	require.NoError(t, err)
	assert.Equal(t, "anthropic", config.Provider)
	assert.Equal(t, "stored-model", config.Model)
	assert.Equal(t, "work", config.Profile)

	_, err = resolveWebChatConfigForExistingConversation(&conversations.GetConversationResponse{
		Metadata: map[string]any{"profile": "missing"},
	})
	require.ErrorContains(t, err, "profile 'missing' not found")
}

func TestServiceStoreAdapterLoadPropagatesServiceError(t *testing.T) {
	wantErr := errors.New("conversation missing")
	adapter := serviceStoreAdapter{service: &mockConversationService{
		getFunc: func(context.Context, string) (*conversations.GetConversationResponse, error) {
			return nil, wantErr
		},
	}}

	_, err := adapter.Load(context.Background(), "missing")
	assert.ErrorIs(t, err, wantErr)
}

func TestAddWebChatDisplayMetadata(t *testing.T) {
	thread := &fakeMetadataThread{}
	expansion := &slashcommands.Expansion{
		Command: "limited",
		Prompt:  "Rendered recipe prompt",
		Display: "/limited name=Web",
	}

	addWebChatSlashCommandDisplay(thread, expansion)

	display, ok := conversations.LookupMessageDisplay(thread.metadata, expansion.Prompt)
	require.True(t, ok)
	assert.Equal(t, conversations.MessageDisplayKindSlashCommand, display.Kind)
	assert.Equal(t, expansion.Display, display.Text)
	assert.Equal(t, expansion.Command, display.Command)

	goalUpdate := &goals.CommandUpdate{
		ModelPrompt: goals.ModelPrompt("find cores"),
		Display:     goals.DisplayText("find cores"),
		Goal:        goals.New("find cores", time.Now()),
	}
	addWebChatGoalDisplay(thread, goalUpdate)

	assert.Equal(t, goalUpdate.Goal, thread.metadata[goals.MetadataKey])
	display, ok = conversations.LookupMessageDisplay(thread.metadata, goalUpdate.ModelPrompt)
	require.True(t, ok)
	assert.Equal(t, conversations.MessageDisplayKindGoal, display.Kind)
	assert.Equal(t, goalUpdate.Display, display.Text)
	assert.Equal(t, goals.SlashCommandName, display.Command)
}

func TestChatMessageHandlerEmitsStreamingEventsAndBroadcasts(t *testing.T) {
	sink := &recordingChatSink{}
	var broadcasted []ChatEvent
	handler := &chatMessageHandler{
		conversationID: "conv-123",
		sink:           sink,
		broadcast: func(conversationID string, event ChatEvent) {
			assert.Equal(t, "conv-123", conversationID)
			broadcasted = append(broadcasted, event)
		},
	}

	handler.HandleText("   ")
	handler.HandleText("hello")
	handler.HandleToolUse("tool-1", "bash", `{"command":"pwd"}`)
	handler.HandleThinking("thought")
	handler.HandleThinking("   ")
	handler.HandleTextDelta("delta")
	handler.HandleTextDelta("")
	handler.HandleThinkingStart()
	handler.HandleThinkingDelta("think")
	handler.HandleThinkingDelta("")
	handler.HandleThinkingBlockEnd()
	handler.HandleContentBlockEnd()
	handler.HandleDone()

	wantKinds := []string{
		"text",
		"tool-use",
		"thinking",
		"text-delta",
		"thinking-start",
		"thinking-delta",
		"thinking-end",
		"content-end",
	}
	require.Len(t, sink.events, len(wantKinds))
	require.Len(t, broadcasted, len(wantKinds))
	for i, wantKind := range wantKinds {
		assert.Equal(t, wantKind, sink.events[i].Kind)
		assert.Equal(t, sink.events[i], broadcasted[i])
		assert.Equal(t, "conv-123", sink.events[i].ConversationID)
		assert.Equal(t, "assistant", sink.events[i].Role)
	}
	assert.Equal(t, "hello", sink.events[0].Content)
	assert.Equal(t, "tool-1", sink.events[1].ToolCallID)
	assert.Equal(t, "bash", sink.events[1].ToolName)
	assert.Equal(t, "delta", sink.events[3].Delta)
	assert.Equal(t, "think", sink.events[5].Delta)
}

func TestWebContentBlocksForUserInputHandlesURLsAndLocalFiles(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "shot.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png-bytes"), 0o644))

	blocks := webContentBlocksForUserInput("  see this  ", []string{
		"https://example.com/shot.png",
		imagePath,
		"file://" + imagePath,
		"relative-missing.png",
		"   ",
	})

	require.Len(t, blocks, 5)
	assert.Equal(t, WebContentBlock{Type: "text", Text: "see this"}, blocks[0])
	require.NotNil(t, blocks[1].ImageURL)
	assert.Equal(t, "https://example.com/shot.png", blocks[1].ImageURL.URL)

	require.NotNil(t, blocks[2].Source)
	assert.Equal(t, "image/png", blocks[2].Source.MediaType)
	assert.Equal(t, "cG5nLWJ5dGVz", blocks[2].Source.Data)
	require.NotNil(t, blocks[3].Source)
	assert.Equal(t, blocks[2].Source, blocks[3].Source)

	require.NotNil(t, blocks[4].ImageURL)
	assert.Equal(t, "relative-missing.png", blocks[4].ImageURL.URL)

	assert.Nil(t, webContentBlocksForUserInput("text only", nil))
	assert.Empty(t, webContentBlocksForUserInput("   ", []string{"   "}))
}

func TestNDJSONEventSinkRequiresFlusherAndWritesLines(t *testing.T) {
	_, err := newNDJSONEventSink(newPlainResponseWriter())
	require.ErrorContains(t, err, "streaming is not supported")

	recorder := httptest.NewRecorder()
	sink, err := newNDJSONEventSink(recorder)
	require.NoError(t, err)
	require.NoError(t, sink.Send(ChatEvent{Kind: "text", Content: "hello", Role: "assistant"}))

	assert.True(t, recorder.Flushed)
	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	require.Len(t, lines, 1)
	var event ChatEvent
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &event))
	assert.Equal(t, "text", event.Kind)
	assert.Equal(t, "hello", event.Content)
	assert.Equal(t, "assistant", event.Role)
}

func TestNDJSONEventSinkReportsMarshalErrors(t *testing.T) {
	sink, err := newNDJSONEventSink(httptest.NewRecorder())
	require.NoError(t, err)

	err = sink.Send(ChatEvent{Kind: "bad", Content: func() {}})
	require.ErrorContains(t, err, "failed to marshal chat event")
}

func TestSubscriberEventSinkBufferFullAndCloseIdempotent(t *testing.T) {
	sink := newSubscriberEventSink()
	for i := 0; i < cap(sink.ch); i++ {
		require.NoError(t, sink.Send(ChatEvent{Kind: "text"}))
	}

	require.ErrorContains(t, sink.Send(ChatEvent{Kind: "overflow"}), "subscriber buffer full")
	sink.Close()
	sink.Close()

	for i := 0; i < cap(sink.ch); i++ {
		<-sink.ch
	}
	_, ok := <-sink.ch
	assert.False(t, ok)
}

func TestBroadcastingEventSinkBroadcastsOnSuccessAndFailure(t *testing.T) {
	primary := &recordingChatSink{}
	var broadcasted []ChatEvent
	sink := &broadcastingEventSink{
		primary:        primary,
		conversationID: "conv-123",
		broadcast: func(conversationID string, event ChatEvent) {
			assert.Equal(t, "conv-123", conversationID)
			broadcasted = append(broadcasted, event)
		},
	}
	event := ChatEvent{Kind: "text", ConversationID: "conv-123", Role: "assistant", Content: "hi"}

	require.NoError(t, sink.Send(event))
	assert.Equal(t, []ChatEvent{event}, primary.events)
	assert.Equal(t, []ChatEvent{event}, broadcasted)

	wantErr := errors.New("write failed")
	failing := &broadcastingEventSink{
		primary:        failingChatSink{err: wantErr},
		conversationID: "conv-123",
		broadcast: func(_ string, event ChatEvent) {
			broadcasted = append(broadcasted, event)
		},
	}

	require.ErrorIs(t, failing.Send(event), wantErr)
	assert.Equal(t, []ChatEvent{event, event}, broadcasted)
}
