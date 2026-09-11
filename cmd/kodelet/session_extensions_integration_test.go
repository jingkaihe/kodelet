package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This opt-in acceptance gate runs a real SDK interpreter, ACP subprocess,
// authenticated daemon, selected runner, and deterministic model HTTP fixture.
// KODELET_TEST_EXTENSION_SDK=typescript|python selects the SDK installation.
func TestSessionExtensionsAcrossProcessBoundary(t *testing.T) {
	sdk := os.Getenv("KODELET_TEST_EXTENSION_SDK")
	if sdk != "typescript" && sdk != "python" {
		t.Skip("set KODELET_TEST_EXTENSION_SDK=typescript or python")
	}
	for _, placement := range []string{"embedded", "standalone"} {
		t.Run(placement, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
			defer cancel()
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "daemon-store"))
			t.Setenv("KODELET_TEST_PROVIDER_KEY", "daemon-only-key")
			workspace := filepath.Join(root, "runner-workspace")
			require.NoError(t, os.MkdirAll(workspace, 0o700))
			provider := sessionExtensionTestProvider(t)
			defer provider.Close()
			oldSettings := viper.AllSettings()
			viper.Reset()
			t.Cleanup(func() {
				viper.Reset()
				for key, value := range oldSettings {
					viper.Set(key, value)
				}
			})
			viper.Set("provider", "openai")
			viper.Set("model", "gpt-4o")
			viper.Set("max_tokens", 256)
			viper.Set("openai", map[string]any{"platform": "openai", "base_url": provider.URL, "api_key_env_var": "KODELET_TEST_PROVIDER_KEY", "api_mode": "chat_completions"})
			viper.Set("extensions.enabled", true)
			viper.Set("skills.enabled", false)
			viper.Set("allowed_tools", []string{"sdk_echo"})
			require.NoError(t, db.RunMigrations(ctx, migrations.All()))
			config := &controlplane.ServerConfig{Host: "127.0.0.1", Port: 0, CompactRatio: 0.8, AuthToken: "client-secret", RunnerAuthToken: "runner-secret"}
			settings := map[string]any{"allowed_tools": []string{"sdk_echo"}, "extensions": map[string]any{"enabled": true}, "skills": map[string]any{"enabled": false}}
			if placement == "embedded" {
				store, err := localstate.NewStore()
				require.NoError(t, err)
				config.EmbeddedRunner = &controlplane.EmbeddedRunnerConfig{Workspace: workspace, Settings: settings, Store: store}
			}
			daemon, err := controlplane.NewServer(ctx, config, nil)
			require.NoError(t, err)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			serverURL := "http://" + listener.Addr().String()
			serverCtx, stopServer := context.WithCancel(ctx)
			serverDone := make(chan error, 1)
			go func() { serverDone <- daemon.Serve(serverCtx, listener) }()
			t.Cleanup(func() {
				stopServer()
				select {
				case err := <-serverDone:
					assert.NoError(t, err)
				case <-time.After(10 * time.Second):
					assert.Fail(t, "daemon did not shut down")
				}
				assert.NoError(t, daemon.Close())
			})
			environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_AUTH_TOKEN=client-secret"}
			if placement == "standalone" {
				settingsJSON, err := json.Marshal(settings)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(workspace, "kodelet-config.yaml"), settingsJSON, 0o600))
				runnerCtx, stopRunner := context.WithCancel(ctx)
				process := daemonCLIProcess(runnerCtx, t, workspace, append(environment, "KODELET_TEST_CLI_PROCESS=1", "KODELET_BASE_PATH="+filepath.Join(root, "runner-state")), "runner", "start", "--server="+serverURL, "--auth-token=runner-secret")
				var logs bytes.Buffer
				process.Stdout, process.Stderr = &logs, &logs
				require.NoError(t, process.Start())
				t.Cleanup(func() {
					stopRunner()
					_ = process.Wait()
					if t.Failed() {
						t.Log(logs.String())
					}
				})
			}
			var runnerID string
			require.Eventually(t, func() bool {
				runners, _, err := fetchRunners(ctx, serverURL, "client-secret")
				if err != nil {
					return false
				}
				for _, runner := range runners {
					if runner.Connected && runner.Status == runnerregistry.RunnerStatusIdle && runner.SessionExtensions {
						runnerID = runner.ID
						return true
					}
				}
				return false
			}, 15*time.Second, 25*time.Millisecond)
			executable, err := os.Executable()
			require.NoError(t, err)
			wrapper := filepath.Join(root, "sdk-kodelet")
			require.NoError(t, os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nKODELET_TEST_CLI_PROCESS=1 exec %q -test.run '^TestDaemonFirstCLIProcess$' -- \"$@\"\n", executable)), 0o700))
			options, err := json.Marshal(map[string]string{"command": wrapper, "server": serverURL, "runner": runnerID})
			require.NoError(t, err)
			var interpreter, script, source string
			if sdk == "typescript" {
				interpreter, err = exec.LookPath("node")
				require.NoError(t, err)
				dist, err := filepath.Abs("../../sdk/dist/index.js")
				require.NoError(t, err)
				script = filepath.Join(root, "inline.mjs")
				source = fmt.Sprintf(sessionExtensionTypeScriptFixture, "file://"+dist, options, workspace)
			} else {
				sdkPath := os.Getenv("KODELET_PYTHON_SDK_PATH")
				require.NotEmpty(t, sdkPath, "set KODELET_PYTHON_SDK_PATH to the local Python SDK checkout")
				interpreter = filepath.Join(sdkPath, ".venv", "bin", "python")
				environment = append(environment, "PYTHONPATH="+filepath.Join(sdkPath, "src"))
				script = filepath.Join(root, "inline.py")
				source = fmt.Sprintf(sessionExtensionPythonFixture, options, workspace)
			}
			require.NoError(t, os.WriteFile(script, []byte(source), 0o600))
			process := exec.CommandContext(ctx, interpreter, script)
			process.Dir, process.Env = root, environment
			output, err := process.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Contains(t, string(output), "inline acceptance passed")
			assertSessionExtensionHierarchy(ctx, t, serverURL, string(output))
			if sdk == "typescript" {
				tsx, err := filepath.Abs("../../sdk/node_modules/.bin/tsx")
				require.NoError(t, err)
				example, err := filepath.Abs("../../sdk/examples/inline-extension-session.ts")
				require.NoError(t, err)
				process := exec.CommandContext(ctx, tsx, example, "Use sdk_echo to say hello")
				process.Dir = root
				process.Env = append(environment, "KODELET_BIN="+wrapper, "KODELET_SERVER="+serverURL, "KODELET_RUNNER="+runnerID, "KODELET_CWD="+workspace)
				output, err := process.CombinedOutput()
				require.NoError(t, err, "%s", output)
				assert.Contains(t, string(output), "inline answer")
				assert.Contains(t, string(output), "[SDK UI] Callback 1: ok")
				assert.Contains(t, string(output), "Echo callback 1 is running")
				assert.Contains(t, string(output), "Callbacks executed: 1")
			}
		})
	}
}

func assertSessionExtensionHierarchy(ctx context.Context, t *testing.T, serverURL, output string) {
	t.Helper()
	var ids struct {
		Main  string   `json:"main"`
		Plain string   `json:"plain"`
		Fresh string   `json:"fresh"`
		Forks []string `json:"forks"`
	}
	var manifest string
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, "hierarchy acceptance: "); ok {
			manifest = value
		}
	}
	require.NotEmpty(t, manifest, output)
	require.NoError(t, json.Unmarshal([]byte(manifest), &ids))
	require.NotEmpty(t, ids.Main)
	require.NotEmpty(t, ids.Plain)
	require.NotEmpty(t, ids.Fresh)
	require.Len(t, ids.Forks, 3, "successful callbacks must create explicit child forks")
	client, err := chat.NewClient(serverURL, "client-secret", "")
	require.NoError(t, err)
	// Copy an actual child: ordinary copies must clear inherited parent metadata,
	// even though their independent fork provenance still points to that child.
	copyID, err := client.ForkConversation(ctx, ids.Fresh)
	require.NoError(t, err)
	parents := map[string]string{ids.Main: "", ids.Plain: "", ids.Fresh: ids.Main, copyID: ""}
	for _, id := range ids.Forks {
		require.NotEmpty(t, id)
		parents[id] = ids.Main
	}
	require.Len(t, parents, 7, "fixture must create distinct conversations")
	summaries, err := client.ListConversations(ctx, 100)
	require.NoError(t, err)
	byID := make(map[string]convtypes.ConversationSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.ID] = summary
	}
	for id, parentID := range parents {
		summary, found := byID[id]
		require.True(t, found, "conversation %s must be durably listed", id)
		assert.Equal(t, parentID, summary.ParentConversationID, "list projection for %s", id)
		record, err := client.LoadConversationRecord(ctx, id)
		require.NoError(t, err)
		for _, metadata := range []map[string]any{summary.Metadata, record.Metadata} {
			if parentID == "" {
				assert.NotContains(t, metadata, "parent_conversation_id", "unparented conversation %s", id)
			} else {
				assert.Equal(t, parentID, metadata["parent_conversation_id"], "persisted parent for %s", id)
			}
		}
		history, err := client.LoadConversation(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, parentID, history.ParentConversationID, "history projection for %s", id)
		if id == ids.Fresh {
			assert.NotContains(t, record.Metadata, "conversation_fork", "fresh children must not acquire fork provenance")
			var messages []string
			for _, message := range history.Messages {
				if message.Role == "user" {
					messages = append(messages, message.Content)
				}
			}
			assert.Equal(t, []string{"plain fresh child", "plain resumed child"}, messages, "both child turns must persist without inheriting parent context")
		}
		if id == copyID {
			fork, ok := record.Metadata["conversation_fork"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, ids.Fresh, fork["source_conversation_id"])
		}
	}
}

func sessionExtensionTestProvider(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer daemon-only-key", r.Header.Get("Authorization"))
		var request struct {
			Tools []struct {
				Function struct{ Name string } `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, tool := range request.Tools {
			assert.Equal(t, "sdk_echo", tool.Function.Name, "daemon policy must exclude sdk_forbidden and installed tools")
		}
		message, result := "", ""
		for _, item := range request.Messages {
			if item.Role == "user" {
				message, result = string(item.Content), ""
			}
			if item.Role == "tool" {
				result = string(item.Content)
			}
		}
		var delta map[string]any
		finish := "stop"
		switch {
		case strings.Contains(message, "plain"):
			assert.Empty(t, request.Tools, "callbacks must not leak into an ordinary SDK session")
			delta = map[string]any{"role": "assistant", "content": "plain answer"}
		case strings.Contains(message, "restricted"):
			assert.Empty(t, request.Tools, "noTools request restriction must still apply")
			delta = map[string]any{"role": "assistant", "content": "restricted answer"}
		case result != "":
			if strings.Contains(message, "explode") {
				assert.Contains(t, result, "callback boom")
			} else {
				assert.Contains(t, result, "closure:")
			}
			delta = map[string]any{"role": "assistant", "content": "inline answer"}
		default:
			require.Len(t, request.Tools, 1)
			text := "ok"
			if strings.Contains(message, "explode") {
				text = "explode"
			}
			if strings.Contains(message, "cancel") {
				text = "cancel"
			}
			if strings.Contains(message, "disconnect") {
				text = "disconnect"
			}
			arguments, _ := json.Marshal(map[string]string{"text": text})
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "inline-call", "type": "function", "function": map[string]any{"name": "sdk_echo", "arguments": string(arguments)}}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{"id": "inline", "object": "chat.completion.chunk", "model": "gpt-4o", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}
		data, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}))
}

const sessionExtensionTypeScriptFixture = `import assert from "node:assert/strict";
import { Client } from %q;
const clientOptions = %s;
const client = new Client(clientOptions);
const cwd = %q;
async function assertParent(id, parentId) {
  const response = await fetch(clientOptions.server + "/api/conversations/" + id, { headers: { Authorization: "Bearer " + process.env.KODELET_AUTH_TOKEN } });
  assert.equal(response.status, 200);
  assert.equal((await response.json()).parentConversationId, parentId);
}
let calls = 0, confirms = 0;
const forks = [], updates = [];
let started = Promise.withResolvers(), stopped = Promise.withResolvers();
const ext = api => {
  api.registerTool({ name: "sdk_echo", description: "Echo local callback", inputSchema: { type: "object", properties: { text: { type: "string" } }, required: ["text"] }, execute: async (input, ctx) => {
    calls++;
    if (input.text === "cancel" || input.text === "disconnect") {
      started.resolve();
      try {
        await new Promise((resolve, reject) => {
          const abort = () => reject(new Error("callback cancelled"));
          if (ctx.signal.aborted) abort();
          else ctx.signal.addEventListener("abort", abort, { once: true });
        });
      } finally { stopped.resolve(); }
    }
    if (input.text === "explode") throw new Error("callback boom");
    assert.equal(ctx.cwd, cwd);
    assert.equal(await ctx.ui.confirm({ message: "Allow callback?" }), true);
    await ctx.update("callback progress");
    const fork = await ctx.forkConversation({ name: "inline fork", asChild: true });
    await assertParent(fork, ctx.conversationId);
    forks.push(fork);
    return "closure:" + calls;
  }});
  api.registerTool({ name: "sdk_forbidden", description: "Must be filtered", inputSchema: { type: "object" }, execute: () => { throw new Error("policy bypass"); } });
};
const options = { cwd, extensions: [ext], ui: { confirm: () => { confirms++; return true; } } };
try {
  const session = await client.createSession(options);
  session.on("tool.update", e => updates.push(e.data.result));
  assert.equal((await session.runAndWait({ message: "inline first" })).content, "inline answer");
  assert.equal((await session.runAndWait({ message: "inline second" })).content, "inline answer");
  assert.equal(calls, 2); assert.equal(confirms, 2); assert.equal(forks.length, 2);
  assert.ok(updates.includes("callback progress"));
  const id = session.id;
  await session.close();
  const missing = await client.createSession({ cwd, resume: id });
  await assert.rejects(missing.runAndWait({ message: "inline missing callbacks" }), /reattach|requires.*session extensions/i);
  await missing.close();
  const resumed = await client.createSession({ ...options, resume: id });
  assert.equal((await resumed.runAndWait({ message: "inline resumed" })).content, "inline answer");
  assert.equal(calls, 3);
  assert.equal((await resumed.runAndWait({ message: "inline explode" })).content, "inline answer");
  assert.equal(calls, 4);
  await resumed.close();
  const plain = await client.createSession({ cwd });
  assert.equal((await plain.runAndWait({ message: "plain" })).content, "plain answer");
  const plainId = plain.id;
  await plain.close();
  const fresh = await client.createSession({ cwd, parentConversationId: id, options: { noTools: true } });
  assert.equal((await fresh.runAndWait({ message: "plain fresh child" })).content, "plain answer");
  const freshId = fresh.id;
  await assertParent(freshId, id);
  await fresh.close();
  const freshResumed = await client.createSession({ cwd, resume: freshId, options: { noTools: true } });
  assert.equal(freshResumed.id, freshId);
  assert.equal((await freshResumed.runAndWait({ message: "plain resumed child" })).content, "plain answer");
  await assertParent(freshId, id);
  await freshResumed.close();
  const restricted = await client.createSession({ ...options, options: { noTools: true } });
  assert.equal((await restricted.runAndWait({ message: "restricted" })).content, "restricted answer");
  assert.equal(calls, 4);
  await restricted.close();
  const cancelled = await client.createSession(options);
  const controller = new AbortController();
  const running = cancelled.runAndWait({ message: "inline cancel", signal: controller.signal });
  await started.promise;
  const concurrent = await client.createSession({ cwd });
  assert.equal((await concurrent.runAndWait({ message: "plain concurrent" })).content, "plain answer");
  await concurrent.close();
  controller.abort();
  await running.catch(() => {});
  await stopped.promise;
  await cancelled.close();
  assert.equal(calls, 5);
  started = Promise.withResolvers(); stopped = Promise.withResolvers();
  const disconnected = await client.createSession(options);
  const abandoned = disconnected.runAndWait({ message: "inline disconnect" }).catch(() => {});
  await started.promise;
  await disconnected.close();
  await abandoned; await stopped.promise;
  assert.equal(calls, 6);
  console.log("hierarchy acceptance: " + JSON.stringify({ main: id, plain: plainId, fresh: freshId, forks }));
  console.log("inline acceptance passed");
} finally { await client.close(); }
`

const sessionExtensionPythonFixture = `import asyncio
import json
import os
import urllib.request
from kodelet_sdk import Client, Extension, BaseModel, ToolContext
client_options = %s
client = Client(client_options)
cwd = %q
def assert_parent(id, parent_id):
    request = urllib.request.Request(client_options["server"] + "/api/conversations/" + id, headers={"Authorization": "Bearer " + os.environ["KODELET_AUTH_TOKEN"]})
    with urllib.request.urlopen(request, timeout=5) as response:
        assert json.load(response)["parentConversationId"] == parent_id
calls = 0
confirms = 0
forks = []
updates = []
started = asyncio.Event()
stopped = asyncio.Event()
class EchoInput(BaseModel):
    text: str
ext = Extension(name="inline-acceptance")
@ext.tool("sdk_echo", description="Echo local callback", input_schema=EchoInput)
async def echo(input: EchoInput, ctx: ToolContext):
    global calls
    calls += 1
    if input.text in ("cancel", "disconnect"):
        started.set()
        try:
            await asyncio.Event().wait()
        finally:
            stopped.set()
    if input.text == "explode":
        raise RuntimeError("callback boom")
    assert ctx.cwd == cwd
    assert await ctx.ui.confirm({"message": "Allow callback?"})
    await ctx.update("callback progress")
    fork = await ctx.fork_conversation("inline fork", as_child=True)
    await asyncio.to_thread(assert_parent, fork, ctx.conversation_id)
    forks.append(fork)
    return "closure:" + str(calls)
@ext.tool("sdk_forbidden", description="Must be filtered", input_schema={"type": "object"})
async def forbidden(input, ctx):
    raise RuntimeError("policy bypass")
def confirm(request):
    global confirms
    confirms += 1
    return True
options = {"cwd": cwd, "extensions": [ext], "ui": {"confirm": confirm}}
async def main():
    try:
        session = await client.create_session(**options)
        session.on("tool.update", lambda event: updates.append(event.data.result))
        assert (await session.run_and_wait(message="inline first")).content == "inline answer"
        assert (await session.run_and_wait(message="inline second")).content == "inline answer"
        assert calls == 2 and confirms == 2 and len(forks) == 2
        assert "callback progress" in updates
        id = session.id
        await session.close()
        missing = await client.create_session(cwd=cwd, resume=id)
        try:
            await missing.run_and_wait(message="inline missing callbacks")
            raise AssertionError("missing callbacks were silently dropped")
        except RuntimeError as exc:
            assert "reattach" in str(exc) or "requires its session extensions" in str(exc)
        await missing.close()
        resumed = await client.create_session(**options, resume=id)
        assert (await resumed.run_and_wait(message="inline resumed")).content == "inline answer"
        assert calls == 3
        assert (await resumed.run_and_wait(message="inline explode")).content == "inline answer"
        assert calls == 4
        await resumed.close()
        plain = await client.create_session(cwd=cwd)
        assert (await plain.run_and_wait(message="plain")).content == "plain answer"
        plain_id = plain.id
        await plain.close()
        fresh = await client.create_session(cwd=cwd, parent_conversation_id=id, options={"noTools": True})
        assert (await fresh.run_and_wait(message="plain fresh child")).content == "plain answer"
        fresh_id = fresh.id
        await asyncio.to_thread(assert_parent, fresh_id, id)
        await fresh.close()
        fresh_resumed = await client.create_session(cwd=cwd, resume=fresh_id, options={"noTools": True})
        assert fresh_resumed.id == fresh_id
        assert (await fresh_resumed.run_and_wait(message="plain resumed child")).content == "plain answer"
        await asyncio.to_thread(assert_parent, fresh_id, id)
        await fresh_resumed.close()
        restricted = await client.create_session(**options, options={"noTools": True})
        assert (await restricted.run_and_wait(message="restricted")).content == "restricted answer"
        assert calls == 4
        await restricted.close()
        cancelled = await client.create_session(**options)
        running = asyncio.create_task(cancelled.run_and_wait(message="inline cancel"))
        await asyncio.wait_for(started.wait(), 5)
        concurrent = await client.create_session(cwd=cwd)
        assert (await concurrent.run_and_wait(message="plain concurrent")).content == "plain answer"
        await concurrent.close()
        running.cancel()
        try:
            await running
        except asyncio.CancelledError:
            pass
        await asyncio.wait_for(stopped.wait(), 5)
        await cancelled.close()
        assert calls == 5
        started.clear()
        stopped.clear()
        disconnected = await client.create_session(**options)
        abandoned = asyncio.create_task(disconnected.run_and_wait(message="inline disconnect"))
        await asyncio.wait_for(started.wait(), 5)
        await disconnected.close()
        await asyncio.gather(abandoned, return_exceptions=True)
        await asyncio.wait_for(stopped.wait(), 5)
        assert calls == 6
        print("hierarchy acceptance: " + json.dumps({"main": id, "plain": plain_id, "fresh": fresh_id, "forks": forks}))
        print("inline acceptance passed")
    finally:
        await client.close()
asyncio.run(main())
`
