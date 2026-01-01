package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Initialize(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": 1,
			"clientCapabilities": map[string]any{
				"fs": map[string]any{
					"readTextFile":  true,
					"writeTextFile": true,
				},
				"terminal": true,
			},
			"clientInfo": map[string]any{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}

	reqData, err := json.Marshal(initReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.NotNil(t, resp["result"])
	assert.Nil(t, resp["error"])

	result := resp["result"].(map[string]any)
	assert.Equal(t, float64(1), result["protocolVersion"])
	assert.NotNil(t, result["agentCapabilities"])
	assert.NotNil(t, result["agentInfo"])

	agentInfo := result["agentInfo"].(map[string]any)
	assert.Equal(t, "kodelet", agentInfo["name"])
	assert.Equal(t, "Kodelet", agentInfo["title"])

	assert.True(t, server.initialized)
	assert.NotNil(t, server.clientCaps)
	assert.True(t, server.clientCaps.Terminal)
}

func TestServer_SessionNew_NotInitialized(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	sessionReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "session/new",
		"params": map[string]any{
			"cwd": "/test",
		},
	}

	reqData, err := json.Marshal(sessionReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeInternalError), errObj["code"])
	assert.Contains(t, errObj["message"], "Not initialized")
}

func TestServer_UnknownMethod(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	unknownReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "unknown/method",
		"params":  map[string]any{},
	}

	reqData, err := json.Marshal(unknownReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeMethodNotFound), errObj["code"])
}

func TestServer_ParseError(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	invalidJSON := []byte("not valid json\n")

	err := server.handleMessage(invalidJSON)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["error"])
	errObj := resp["error"].(map[string]any)
	assert.Equal(t, float64(acptypes.ErrCodeParseError), errObj["code"])
}

func TestServer_Authenticate(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	authReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "authenticate",
		"params":  map[string]any{},
	}

	reqData, err := json.Marshal(authReq)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var resp map[string]any
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp["result"])
	assert.Nil(t, resp["error"])
}

func TestServer_SendUpdate(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	sessionID := acptypes.SessionID("test-session")
	update := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content": map[string]any{
			"type": "text",
			"text": "Hello",
		},
	}

	err := server.SendUpdate(sessionID, update)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var notif map[string]any
	err = json.Unmarshal(scanner.Bytes(), &notif)
	require.NoError(t, err)

	assert.Equal(t, "2.0", notif["jsonrpc"])
	assert.Equal(t, "session/update", notif["method"])
	assert.Nil(t, notif["id"])

	params := notif["params"].(map[string]any)
	assert.Equal(t, "test-session", params["sessionId"])
	assert.NotNil(t, params["update"])
}

func TestServer_Notification_Cancel(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	cancelNotif := map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params": map[string]any{
			"sessionId": "nonexistent-session",
		},
	}

	reqData, err := json.Marshal(cancelNotif)
	require.NoError(t, err)
	reqData = append(reqData, '\n')

	err = server.handleMessage(reqData)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "session not found"))
}

func TestServer_Shutdown(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	assert.NotNil(t, server.ctx)
	assert.NotNil(t, server.cancel)

	server.Shutdown()

	select {
	case <-server.ctx.Done():
	default:
		t.Error("context should be cancelled after Shutdown")
	}
}

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		name        string
		prompt      []acptypes.ContentBlock
		wantCommand string
		wantArgs    string
		wantFound   bool
	}{
		{
			name: "simple command",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/test"},
			},
			wantCommand: "test",
			wantArgs:    "",
			wantFound:   true,
		},
		{
			name: "command with args",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/init fix the bug"},
			},
			wantCommand: "init",
			wantArgs:    "fix the bug",
			wantFound:   true,
		},
		{
			name: "command with whitespace",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "  /commit  "},
			},
			wantCommand: "commit",
			wantArgs:    "",
			wantFound:   true,
		},
		{
			name: "recipe name with slashes",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/github/pr create a pr"},
			},
			wantCommand: "github/pr",
			wantArgs:    "create a pr",
			wantFound:   true,
		},
		{
			name: "not a command",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "hello world"},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
		{
			name: "empty prompt",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: ""},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
		{
			name: "image block ignored",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeImage, Data: "base64data"},
				{Type: acptypes.ContentTypeText, Text: "/test"},
			},
			wantCommand: "test",
			wantArgs:    "",
			wantFound:   true,
		},
		{
			name: "non-text blocks only",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeImage, Data: "base64data"},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
		{
			name: "just slash",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/"},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
		{
			name: "slash with space only",
			prompt: []acptypes.ContentBlock{
				{Type: acptypes.ContentTypeText, Text: "/ "},
			},
			wantCommand: "",
			wantArgs:    "",
			wantFound:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, args, found := parseSlashCommand(tt.prompt)
			assert.Equal(t, tt.wantCommand, command)
			assert.Equal(t, tt.wantArgs, args)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestServer_GetAvailableCommands(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	commands := server.getAvailableCommands()
	assert.NotNil(t, commands)
	assert.Greater(t, len(commands), 0)

	for _, cmd := range commands {
		assert.NotEmpty(t, cmd.Name)
		assert.NotEmpty(t, cmd.Description)
		assert.NotNil(t, cmd.Input)
		assert.NotEmpty(t, cmd.Input.Hint)
	}
}

func TestServer_SendAvailableCommands(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	sessionID := acptypes.SessionID("test-session")
	err := server.sendAvailableCommands(sessionID)
	require.NoError(t, err)

	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan())

	var notif map[string]any
	err = json.Unmarshal(scanner.Bytes(), &notif)
	require.NoError(t, err)

	assert.Equal(t, "2.0", notif["jsonrpc"])
	assert.Equal(t, "session/update", notif["method"])
	assert.Nil(t, notif["id"])

	params := notif["params"].(map[string]any)
	assert.Equal(t, "test-session", params["sessionId"])

	update := params["update"].(map[string]any)
	assert.Equal(t, acptypes.UpdateAvailableCommands, update["sessionUpdate"])
	assert.NotNil(t, update["availableCommands"])

	availableCommands := update["availableCommands"].([]interface{})
	assert.Greater(t, len(availableCommands), 0)
}

func TestParseSlashCommandArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           string
		wantKVArgs     map[string]string
		wantAdditional string
	}{
		{
			name:           "empty args",
			args:           "",
			wantKVArgs:     map[string]string{},
			wantAdditional: "",
		},
		{
			name:           "only additional text",
			args:           "fix the bug please",
			wantKVArgs:     map[string]string{},
			wantAdditional: "fix the bug please",
		},
		{
			name:           "single key=value",
			args:           "target=main",
			wantKVArgs:     map[string]string{"target": "main"},
			wantAdditional: "",
		},
		{
			name:           "multiple key=value",
			args:           "target=main draft=true",
			wantKVArgs:     map[string]string{"target": "main", "draft": "true"},
			wantAdditional: "",
		},
		{
			name:           "key=value with additional text",
			args:           "target=develop fix the authentication bug",
			wantKVArgs:     map[string]string{"target": "develop"},
			wantAdditional: "fix the authentication bug",
		},
		{
			name:           "quoted value with spaces",
			args:           `title="my feature branch" draft=false`,
			wantKVArgs:     map[string]string{"title": "my feature branch", "draft": "false"},
			wantAdditional: "",
		},
		{
			name:           "mixed order",
			args:           "target=main please review draft=true carefully",
			wantKVArgs:     map[string]string{"target": "main", "draft": "true"},
			wantAdditional: "please review carefully",
		},
		{
			name:           "empty value",
			args:           "key=",
			wantKVArgs:     map[string]string{"key": ""},
			wantAdditional: "",
		},
		{
			name:           "multiple spaces",
			args:           "  target=main   draft=true  ",
			wantKVArgs:     map[string]string{"target": "main", "draft": "true"},
			wantAdditional: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kvArgs, additional := parseSlashCommandArgs(tt.args)
			assert.Equal(t, tt.wantKVArgs, kvArgs)
			assert.Equal(t, tt.wantAdditional, additional)
		})
	}
}

func TestBuildCommandHint(t *testing.T) {
	tests := []struct {
		name     string
		defaults map[string]string
		want     string
	}{
		{
			name:     "no defaults",
			defaults: nil,
			want:     "additional instructions (optional)",
		},
		{
			name:     "empty defaults",
			defaults: map[string]string{},
			want:     "additional instructions (optional)",
		},
		{
			name:     "single default",
			defaults: map[string]string{"target": "main"},
			want:     "[target=main] additional instructions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCommandHint(tt.defaults)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildCommandHint_MultipleDefaults(t *testing.T) {
	t.Run("two keys", func(t *testing.T) {
		defaults := map[string]string{"target": "main", "draft": "false"}
		got := buildCommandHint(defaults)
		assert.Equal(t, "[draft=false target=main] additional instructions", got)
	})

	t.Run("three or more keys - deterministic ordering", func(t *testing.T) {
		defaults := map[string]string{
			"zebra":  "last",
			"alpha":  "first",
			"middle": "center",
			"beta":   "second",
		}
		got := buildCommandHint(defaults)
		assert.Equal(t, "[alpha=first beta=second middle=center zebra=last] additional instructions", got)
	})
}

func TestServer_TransformSlashCommandPrompt(t *testing.T) {
	input := bytes.NewBuffer(nil)
	output := bytes.NewBuffer(nil)

	server := NewServer(
		WithInput(input),
		WithOutput(output),
		WithContext(context.Background()),
	)

	t.Run("transforms valid recipe command", func(t *testing.T) {
		if server.fragmentProcessor == nil {
			t.Skip("fragment processor not available")
		}

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/init"},
		}

		result, err := server.transformSlashCommandPrompt("init", "", originalPrompt)
		require.NoError(t, err)
		require.NotEmpty(t, result)
		assert.Equal(t, acptypes.ContentTypeText, result[0].Type)
		assert.NotEmpty(t, result[0].Text)
	})

	t.Run("includes additional text", func(t *testing.T) {
		if server.fragmentProcessor == nil {
			t.Skip("fragment processor not available")
		}

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/init please focus on tests"},
		}

		result, err := server.transformSlashCommandPrompt("init", "please focus on tests", originalPrompt)
		require.NoError(t, err)
		require.NotEmpty(t, result)
		assert.Contains(t, result[0].Text, "Additional instructions:")
		assert.Contains(t, result[0].Text, "please focus on tests")
	})

	t.Run("preserves non-text blocks", func(t *testing.T) {
		if server.fragmentProcessor == nil {
			t.Skip("fragment processor not available")
		}

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/init"},
			{Type: acptypes.ContentTypeImage, Data: "base64imagedata", MimeType: "image/png"},
		}

		result, err := server.transformSlashCommandPrompt("init", "", originalPrompt)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Equal(t, acptypes.ContentTypeText, result[0].Type)
		assert.Equal(t, acptypes.ContentTypeImage, result[1].Type)
		assert.Equal(t, "base64imagedata", result[1].Data)
	})

	t.Run("returns error for unknown recipe with available recipes", func(t *testing.T) {
		if server.fragmentProcessor == nil {
			t.Skip("fragment processor not available")
		}

		originalPrompt := []acptypes.ContentBlock{
			{Type: acptypes.ContentTypeText, Text: "/nonexistent-recipe-xyz"},
		}

		_, err := server.transformSlashCommandPrompt("nonexistent-recipe-xyz", "", originalPrompt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown recipe '/nonexistent-recipe-xyz'")
		assert.Contains(t, err.Error(), "Available recipes:")
	})
}
