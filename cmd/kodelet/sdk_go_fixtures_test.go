package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// goSDKBuildHome keeps fixture builds on the developer's module and build
// caches after tests point HOME at a temporary directory.
var goSDKBuildHome, _ = os.UserHomeDir()

// buildGoSDKFixture compiles a Go SDK program against the in-repository
// sdk/go module. target is a source file or a package path inside the module.
func buildGoSDKFixture(ctx context.Context, t *testing.T, output, target string) string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	require.NoError(t, err)
	module, err := filepath.Abs("../../sdk/go")
	require.NoError(t, err)
	build := exec.CommandContext(ctx, goTool, "-C", module, "build", "-o", output, target)
	build.Env = append(os.Environ(), "HOME="+goSDKBuildHome)
	result, err := build.CombinedOutput()
	require.NoError(t, err, "%s", result)
	return output
}

// buildGoSDKSource writes source to dir and compiles it as an executable.
func buildGoSDKSource(ctx context.Context, t *testing.T, dir, name, source string) string {
	t.Helper()
	path := filepath.Join(dir, name+".go")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return buildGoSDKFixture(ctx, t, filepath.Join(dir, name), path)
}

// sessionExtensionGoFixture mirrors the TypeScript and Python inline callback
// acceptance fixtures. Format with the runner workspace and client options JSON.
const sessionExtensionGoFixture = `package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

var (
	cwd             = %q
	clientOptions   map[string]string
	calls, confirms atomic.Int32
	mu              sync.Mutex
	forks, updates  []string
	started         = make(chan struct{}, 4)
	stopped         = make(chan struct{}, 4)
)

func fail(message string) {
	fmt.Println("acceptance failure: " + message)
	os.Exit(1)
}

func check(ok bool, message string) {
	if !ok {
		fail(message)
	}
}

func must[T any](value T, err error) T {
	if err != nil {
		fail(err.Error())
	}
	return value
}

func wait(ch <-chan struct{}, what string) {
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		fail("timed out waiting for " + what)
	}
}

func parentOf(id string) (string, error) {
	request, err := http.NewRequest(http.MethodGet, clientOptions["server"]+"/api/conversations/"+id, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+os.Getenv("KODELET_AUTH_TOKEN"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("conversation lookup returned " + response.Status)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return "", err
	}
	parent, _ := body["parentConversationId"].(string)
	return parent, nil
}

func newExtension() *extension.Extension {
	ext := extension.New(extension.Metadata{Name: "inline-acceptance"})
	extension.RegisterTool(ext, extension.Tool[map[string]any]{
		Name:        "sdk_echo",
		Description: "Echo local callback",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []any{"text"}},
		Execute: func(ctx context.Context, input map[string]any, tc *extension.ToolContext) (extension.ToolResult, error) {
			call := calls.Add(1)
			text, _ := input["text"].(string)
			if text == "cancel" || text == "disconnect" {
				started <- struct{}{}
				<-ctx.Done()
				stopped <- struct{}{}
				return extension.ToolResult{}, errors.New("callback cancelled")
			}
			if text == "explode" {
				return extension.ToolResult{}, errors.New("callback boom")
			}
			if tc.CWD != cwd {
				return extension.ToolResult{}, errors.New("unexpected cwd " + tc.CWD)
			}
			confirmed, err := tc.UI.Confirm(ctx, extension.ConfirmRequest{Message: "Allow callback?"})
			if err != nil || !confirmed {
				return extension.ToolResult{}, errors.Join(errors.New("callback was not confirmed"), err)
			}
			if err := tc.Update(ctx, "callback progress", nil); err != nil {
				return extension.ToolResult{}, err
			}
			fork, err := tc.ForkConversation(ctx, extension.ForkOptions{Name: "inline fork", AsChild: true})
			if err != nil {
				return extension.ToolResult{}, err
			}
			parent, err := parentOf(fork)
			if err != nil || parent != tc.ConversationID {
				return extension.ToolResult{}, errors.Join(errors.New("fork parent mismatch: "+parent), err)
			}
			mu.Lock()
			forks = append(forks, fork)
			mu.Unlock()
			return extension.Text("closure:" + strconv.Itoa(int(call))), nil
		},
	})
	extension.RegisterTool(ext, extension.Tool[map[string]any]{
		Name:        "sdk_forbidden",
		Description: "Must be filtered",
		InputSchema: map[string]any{"type": "object"},
		Execute: func(context.Context, map[string]any, *extension.ToolContext) (extension.ToolResult, error) {
			return extension.ToolResult{}, errors.New("policy bypass")
		},
	})
	return ext
}

func main() {
	check(json.Unmarshal([]byte(%q), &clientOptions) == nil, "invalid client options")
	client := agent.NewClient(agent.ClientOptions{Command: clientOptions["command"], Server: clientOptions["server"], Runner: clientOptions["runner"]})
	defer client.Close()
	ctx := context.Background()
	options := agent.SessionOptions{
		CWD:        cwd,
		Extensions: []*extension.Extension{newExtension()},
		UI: &agent.UIHandlers{Confirm: func(context.Context, extension.ConfirmRequest) (bool, error) {
			confirms.Add(1)
			return true, nil
		}},
	}
	with := func(change func(*agent.SessionOptions)) agent.SessionOptions {
		copied := options
		change(&copied)
		return copied
	}
	run := func(session *agent.Session, message string) string {
		return must(session.RunAndWait(ctx, agent.RunOptions{Message: message})).Content
	}

	session := must(client.CreateSession(ctx, options))
	session.Subscribe(func(event agent.Event) {
		if result, ok := event.Data.(agent.ToolResult); ok && event.Type == agent.EventToolUpdate {
			mu.Lock()
			updates = append(updates, result.Result)
			mu.Unlock()
		}
	})
	check(run(session, "inline first") == "inline answer", "first run")
	check(run(session, "inline second") == "inline answer", "second run")
	mu.Lock()
	check(calls.Load() == 2 && confirms.Load() == 2 && len(forks) == 2, "callbacks, confirms, and forks after two runs")
	check(slices.Contains(updates, "callback progress"), "tool updates reach session subscribers")
	mu.Unlock()
	id := session.ID()
	check(session.Close() == nil, "close session")

	missing := must(client.CreateSession(ctx, agent.SessionOptions{CWD: cwd, Resume: id}))
	_, err := missing.RunAndWait(ctx, agent.RunOptions{Message: "inline missing callbacks"})
	check(err != nil && regexp.MustCompile("(?i)reattach|requires.*session extensions").MatchString(err.Error()), "missing callbacks must not be dropped silently")
	_ = missing.Close()

	resumed := must(client.CreateSession(ctx, with(func(o *agent.SessionOptions) { o.Resume = id })))
	check(run(resumed, "inline resumed") == "inline answer" && calls.Load() == 3, "resumed run")
	check(run(resumed, "inline explode") == "inline answer" && calls.Load() == 4, "callback errors reach the model")
	_ = resumed.Close()

	plain := must(client.CreateSession(ctx, agent.SessionOptions{CWD: cwd}))
	check(run(plain, "plain") == "plain answer", "plain run")
	plainID := plain.ID()
	_ = plain.Close()

	fresh := must(client.CreateSession(ctx, agent.SessionOptions{CWD: cwd, ParentConversationID: id, Options: agent.ExecutionOptions{NoTools: agent.Ptr(true)}}))
	check(run(fresh, "plain fresh child") == "plain answer", "fresh child run")
	freshID := fresh.ID()
	check(must(parentOf(freshID)) == id, "fresh child parent")
	_ = fresh.Close()

	freshResumed := must(client.CreateSession(ctx, agent.SessionOptions{CWD: cwd, Resume: freshID, Options: agent.ExecutionOptions{NoTools: agent.Ptr(true)}}))
	check(freshResumed.ID() == freshID, "resumed child ID")
	check(run(freshResumed, "plain resumed child") == "plain answer", "resumed child run")
	check(must(parentOf(freshID)) == id, "resumed child keeps its parent")
	_ = freshResumed.Close()

	restricted := must(client.CreateSession(ctx, with(func(o *agent.SessionOptions) { o.Options.NoTools = agent.Ptr(true) })))
	check(run(restricted, "restricted") == "restricted answer" && calls.Load() == 4, "noTools restriction")
	_ = restricted.Close()

	cancelled := must(client.CreateSession(ctx, options))
	runCtx, cancel := context.WithCancel(ctx)
	running := make(chan error, 1)
	go func() {
		_, err := cancelled.RunAndWait(runCtx, agent.RunOptions{Message: "inline cancel"})
		running <- err
	}()
	wait(started, "the cancellable callback")
	concurrent := must(client.CreateSession(ctx, agent.SessionOptions{CWD: cwd}))
	check(run(concurrent, "plain concurrent") == "plain answer", "concurrent plain session")
	_ = concurrent.Close()
	cancel()
	check(errors.Is(<-running, context.Canceled), "cancelled run returns the context error")
	wait(stopped, "the cancelled callback to stop")
	_ = cancelled.Close()
	check(calls.Load() == 5, "cancelled callback ran once")

	disconnected := must(client.CreateSession(ctx, options))
	abandoned := make(chan error, 1)
	go func() {
		_, err := disconnected.RunAndWait(ctx, agent.RunOptions{Message: "inline disconnect"})
		abandoned <- err
	}()
	wait(started, "the disconnecting callback")
	_ = disconnected.Close()
	check(<-abandoned != nil, "abandoned run fails")
	wait(stopped, "the disconnected callback to stop")
	check(calls.Load() == 6, "disconnected callback ran once")

	mu.Lock()
	manifest, _ := json.Marshal(map[string]any{"main": id, "plain": plainID, "fresh": freshID, "forks": forks})
	mu.Unlock()
	fmt.Println("hierarchy acceptance: " + string(manifest))
	fmt.Println("inline acceptance passed")
}
`

// daemonSDKSearchExtensionGoFixture mirrors the TypeScript and Python search
// extensions. Format with the provider URL and the replacement system prompt.
const daemonSDKSearchExtensionGoFixture = `package main

import (
	"context"
	"errors"
	"os"
	"sync/atomic"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
	"github.com/jingkaihe/kodelet/sdk/go/extension"
)

func main() {
	ext := extension.New(extension.Metadata{})
	profile := ext.RegisterProfile(extension.ProfileRegistration{
		Name:     "code-search",
		Provider: "openai",
		Model:    "gpt-5.6-luna",
		Hidden:   true,
		Settings: map[string]any{
			"max_tokens":       512,
			"reasoning_effort": "none",
			"openai": map[string]any{
				"platform":        "openai",
				"base_url":        %q,
				"api_key_env_var": "KODELET_TEST_PROVIDER_KEY",
				"api_mode":        "responses",
				"service_tier":    "fast",
				"websocket_mode":  false,
			},
		},
	})
	var parentSearch atomic.Bool
	extension.On(ext, extension.EventUserMessage, func(_ context.Context, event extension.UserMessageEvent, _ *extension.EventContext) (extension.EventResult, error) {
		parentSearch.Store(event.Message == "delegate code search")
		return extension.EventResult{}, nil
	})
	extension.On(ext, extension.EventAgentInit, func(context.Context, extension.AgentInitEvent, *extension.EventContext) (extension.EventResult, error) {
		if parentSearch.Load() {
			return extension.EventResult{Tools: &extension.ToolListPatch{Disable: []string{"file_read", "grep_tool", "glob_tool"}}}, nil
		}
		return extension.EventResult{}, nil
	})
	prompt := extension.New(extension.Metadata{Name: "search-prompt"})
	extension.On(prompt, extension.EventAgentInit, func(context.Context, extension.AgentInitEvent, *extension.EventContext) (extension.EventResult, error) {
		return extension.EventResult{SystemPrompt: &extension.SystemPromptPatch{Replace: extension.Ptr(%q)}}, nil
	})
	extension.RegisterTool(ext, extension.Tool[struct{}]{
		Name:        "code_search",
		Description: "Run restricted code search",
		Execute: func(ctx context.Context, _ struct{}, tc *extension.ToolContext) (extension.ToolResult, error) {
			if tc.RunnerID == "" {
				return extension.ToolResult{}, errors.New("missing extension runner metadata")
			}
			client := agent.NewClient(agent.ClientOptions{Command: os.Getenv("KODELET_BIN"), CWD: tc.CWD, Runner: tc.RunnerID})
			defer client.Close()
			session, err := client.CreateSession(ctx, agent.SessionOptions{
				Profile:              profile,
				ParentConversationID: tc.ConversationID,
				Options: agent.ExecutionOptions{
					AllowedTools:        []string{"file_read", "grep_tool", "glob_tool"},
					NoSkills:            agent.Ptr(true),
					EnableFSSearchTools: agent.Ptr(true),
					MaxTurns:            agent.Ptr(3),
				},
				Extensions: []*extension.Extension{prompt},
			})
			if err != nil {
				return extension.ToolResult{}, err
			}
			response, err := session.RunAndWait(ctx, agent.RunOptions{Message: "child code search"})
			if err != nil {
				return extension.ToolResult{}, err
			}
			return extension.Text("extension-runner:" + tc.RunnerID + "\n" + response.Content), nil
		},
	})
	ext.Main()
}
`

// daemonSDKClientGoFixture runs one tool-free session with typed options.
// Format with the client options JSON and the runner workspace.
const daemonSDKClientGoFixture = `package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/sdk/go/agent"
)

func main() {
	var options map[string]string
	if err := json.Unmarshal([]byte(%q), &options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	client := agent.NewClient(agent.ClientOptions{Command: options["command"], Server: options["server"], Runner: options["runner"]})
	defer client.Close()
	ctx := context.Background()
	session, err := client.CreateSession(ctx, agent.SessionOptions{
		CWD:     %q,
		Options: agent.ExecutionOptions{Model: "gpt-4o-mini", NoTools: agent.Ptr(true), MaxTurns: agent.Ptr(1)},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	response, err := session.RunAndWait(ctx, agent.RunOptions{Message: "sdk typed options"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(response.Content)
}
`
