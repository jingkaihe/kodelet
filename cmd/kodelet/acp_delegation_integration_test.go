package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	daemonACPSearchName   = "code-search-worker"
	daemonACPSearchPrompt = "ACP_CODE_SEARCH_PROMPT: Search the repository without modifying files."
)

// TestDaemonFirstRunAcrossProcessBoundary exercises this gate with embedded and
// standalone runners, using either the Go fixture below or a real TS/Python SDK.
// Conversation observers use NDJSON (not WebSocket) in the current daemon API.
func assertDaemonACPSearchBroadcast(ctx context.Context, t *testing.T, client *chat.Client, server, workspace string, release, finish func()) {
	t.Helper()
	history, err := client.ListConversationsInCWD(ctx, 10, workspace)
	require.NoError(t, err)
	var conversationID string
	for _, summary := range history {
		if summary.Summary == daemonACPSearchName || (os.Getenv("KODELET_TEST_EXTENSION_SDK") != "" && summary.Metadata["profile"] == "code-search") {
			conversationID = summary.ID
			assert.True(t, summary.IsRunning, "ACP must register a normal active chat")
		}
	}
	require.NotEmpty(t, conversationID, "the live ACP child must appear in ordinary conversation history")
	var observers []<-chan chat.ChatEvent
	for range 2 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/conversations/"+conversationID+"/stream", nil)
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer client-secret")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		assert.Equal(t, "true", response.Header.Get(chat.ConversationStreamActiveHeader))
		events := make(chan chat.ChatEvent, 32)
		observers = append(observers, events)
		go func() {
			defer close(events)
			decoder := json.NewDecoder(response.Body)
			for {
				var event chat.ChatEvent
				if decoder.Decode(&event) != nil {
					return
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	release()
	for _, events := range observers {
		var text string
		var used, read bool
		for !strings.Contains(text, "runner-file-evidence") {
			select {
			case event, ok := <-events:
				require.True(t, ok, "observer disconnected before live content")
				assert.Equal(t, conversationID, event.ConversationID)
				require.NotEqual(t, "done", event.Kind, "provider is still blocked")
				require.Empty(t, event.Error)
				if event.Kind == "tool-use" && event.ToolName == "file_read" {
					used = true
					assert.Contains(t, event.Input, "marker.txt")
				}
				if event.Kind == "tool-result" && event.ToolName == "file_read" {
					read = true
					assert.Contains(t, event.ToolOutput, "runner-file-evidence")
				}
				if event.Kind == "text-delta" {
					text += event.Delta
				}
			case <-ctx.Done():
				require.FailNow(t, "observer did not receive live ACP output")
			}
		}
		assert.True(t, used, "explicit file_read must run despite the parent's tool presentation")
		assert.True(t, read, "tool results must reach ordinary conversation observers")
	}
	history, err = client.ListConversationsInCWD(ctx, 10, workspace)
	require.NoError(t, err)
	for _, summary := range history {
		if summary.ID == conversationID {
			assert.True(t, summary.IsRunning, "live deltas must arrive before completion")
			if os.Getenv("KODELET_TEST_EXTENSION_SDK") != "" {
				assert.Equal(t, "code-search", summary.Metadata["profile"])
			} else {
				assert.Equal(t, daemonACPSearchName, summary.Summary)
			}
		}
	}
	finish()
	for _, events := range observers {
		for {
			select {
			case event, ok := <-events:
				require.True(t, ok, "observer disconnected before completion")
				require.Empty(t, event.Error)
				if event.Kind == "done" {
					assert.False(t, event.Cancelled)
					goto completed
				}
			case <-ctx.Done():
				require.FailNow(t, "observer did not receive ACP completion")
			}
		}
	completed:
	}
}

// This runner-installed extension uses normal client credentials to launch ACP.
// It exists only to keep the primary Go acceptance test independent of SDK installs.
func TestDaemonACPSearchExtensionProcess(t *testing.T) {
	if os.Getenv("KODELET_TEST_ACP_EXTENSION") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	read := func() map[string]json.RawMessage {
		length := 0
		for {
			line, err := reader.ReadString('\n')
			if err == io.EOF {
				os.Exit(0)
			}
			require.NoError(t, err)
			if strings.TrimSpace(line) == "" {
				break
			}
			if value, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				length, err = strconv.Atoi(strings.TrimSpace(value))
				require.NoError(t, err)
			}
		}
		data := make([]byte, length)
		_, err := io.ReadFull(reader, data)
		require.NoError(t, err)
		var message map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &message))
		return message
	}
	write := func(value any) {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		_, err = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
		require.NoError(t, err)
	}
	parentSearch := false
	for {
		message := read()
		var method string
		require.NoError(t, json.Unmarshal(message["method"], &method))
		var result any = map[string]any{}
		switch method {
		case "extension.initialize":
			result = extensions.InitializeResult{
				Name: "code_search", Tools: []extensions.ToolRegistration{{Name: "code_search", Description: "Run restricted code search", InputSchema: map[string]any{"type": "object"}}},
				Subscriptions: []extensions.Subscription{{Event: "user.message"}, {Event: "agent.init"}},
			}
		case "extension.event.handle":
			var event struct {
				Event   string `json:"event"`
				Payload struct {
					Message string `json:"message"`
				} `json:"payload"`
			}
			require.NoError(t, json.Unmarshal(message["params"], &event))
			if event.Event == "user.message" {
				parentSearch = event.Payload.Message == "delegate code search"
			} else if event.Event == "agent.init" && parentSearch {
				result = map[string]any{"tools": map[string]any{"disable": []string{"file_read", "grep_tool", "glob_tool"}}}
			}
		case "extension.tool.execute":
			var request struct {
				Context extensions.ExtensionCallContext `json:"context"`
			}
			require.NoError(t, json.Unmarshal(message["params"], &request))
			write(map[string]any{"jsonrpc": "2.0", "id": 1000, "parentId": message["id"], "method": extensions.ConversationForkMethod, "params": map[string]string{"name": daemonACPSearchName}})
			response := read()
			require.Empty(t, response["error"])
			var fork struct {
				ConversationID string `json:"conversationId"`
			}
			require.NoError(t, json.Unmarshal(response["result"], &fork))
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			output, err := daemonACPSearch(ctx, request.Context.CWD, fork.ConversationID)
			cancel()
			if err != nil {
				result = extensions.ToolExecutionResult{Error: err.Error()}
			} else {
				result = extensions.ToolExecutionResult{Content: output}
			}
		}
		write(map[string]any{"jsonrpc": "2.0", "id": message["id"], "result": result})
	}
}

// Minimal ACP client: initialize, load a normal named fork, attach an inline
// agent.init prompt callback, and consume session/update until prompt completion.
func daemonACPSearch(ctx context.Context, cwd, conversationID string) (string, error) {
	process := exec.CommandContext(ctx, os.Getenv("KODELET_BIN"), "acp", "--allowed-tools=file_read,grep_tool,glob_tool", "--no-skills=true", "--enable-fs-search-tools=true", "--max-turns=3")
	input, err := process.StdinPipe()
	if err != nil {
		return "", err
	}
	output, err := process.StdoutPipe()
	if err != nil {
		return "", err
	}
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		return "", err
	}
	defer func() { _ = input.Close(); _ = process.Process.Kill(); _ = process.Wait() }()
	encoder, decoder := json.NewEncoder(input), json.NewDecoder(output)
	var content strings.Builder
	call := func(id int, method string, params any) error {
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			return err
		}
		for {
			var message struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
				Error  json.RawMessage `json:"error"`
			}
			if err := decoder.Decode(&message); err != nil {
				return errors.Wrap(err, "read ACP response")
			}
			if message.Method == "kodelet/extensionFrame" {
				if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{}}); err != nil {
					return err
				}
				var frame map[string]json.RawMessage
				if err := json.Unmarshal(message.Params, &frame); err != nil {
					return err
				}
				if string(frame["close"]) == "true" {
					continue
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.Unmarshal(frame["message"], &request); err != nil {
					return err
				}
				if len(request.ID) == 0 {
					continue
				}
				var result any = map[string]any{}
				switch request.Method {
				case "extension.initialize":
					result = extensions.InitializeResult{Name: "search-prompt", Subscriptions: []extensions.Subscription{{Event: "agent.init"}}}
				case "extension.event.handle":
					result = map[string]any{"systemPrompt": map[string]string{"replace": daemonACPSearchPrompt}}
				}
				frame["message"], _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
				if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "inline-response", "method": "kodelet/extensionFrame", "params": frame}); err != nil {
					return err
				}
			} else if message.Method == "session/update" {
				var params struct {
					Update struct {
						SessionUpdate string          `json:"sessionUpdate"`
						Content       json.RawMessage `json:"content"`
					} `json:"update"`
				}
				if err := json.Unmarshal(message.Params, &params); err != nil {
					return err
				}
				if id == 3 && params.Update.SessionUpdate == "agent_message_chunk" {
					var chunk struct {
						Text string `json:"text"`
					}
					if err := json.Unmarshal(params.Update.Content, &chunk); err != nil {
						return err
					}
					content.WriteString(chunk.Text)
				}
			} else if message.Method == "" && string(message.ID) == strconv.Itoa(id) {
				if len(message.Error) > 0 && string(message.Error) != "null" {
					return errors.Errorf("ACP %s: %s", method, message.Error)
				}
				return nil
			}
		}
	}
	meta := map[string]any{"sessionExtensions": map[string]any{"version": 1, "extensionIds": []string{"inline-1"}}}
	if err := call(1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{"_meta": map[string]any{"sessionExtensions": map[string]any{"version": 1}}}}); err != nil {
		return "", err
	}
	if err := call(2, "session/load", map[string]any{"sessionId": conversationID, "cwd": cwd, "_meta": meta}); err != nil {
		return "", err
	}
	if err := call(3, "session/prompt", map[string]any{"sessionId": conversationID, "prompt": []any{map[string]string{"type": "text", "text": "child code search"}}}); err != nil {
		return "", err
	}
	return content.String(), nil
}
