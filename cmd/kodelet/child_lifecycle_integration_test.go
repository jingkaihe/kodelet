package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Opt in after building the selected SDK. Both placements use an actual daemon
// process, and standalone additionally uses an independently configured runner.
func TestChildLifecycleAcrossRunnerPlacements(t *testing.T) {
	sdk := os.Getenv("KODELET_TEST_CHILD_LIFECYCLE_SDK")
	if sdk == "" {
		t.Skip("set KODELET_TEST_CHILD_LIFECYCLE_SDK=typescript|python|subagent to test built consumers")
	}
	for _, placement := range []string{"embedded", "standalone"} {
		t.Run(placement, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
			defer cancel()
			root := t.TempDir()
			workspace := filepath.Join(root, "workspace")
			extensionDir := filepath.Join(workspace, ".kodelet", "extensions")
			require.NoError(t, os.MkdirAll(extensionDir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "marker.txt"), []byte("child tool evidence"), 0o600))
			wrapper := childLifecycleExtension(t, extensionDir, sdk)
			require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "kodelet-extension-lifecycle"), []byte(wrapper), 0o700))
			var childCalls atomic.Int32
			provider := childLifecycleProvider(t, workspace, &childCalls, sdk == "subagent")
			t.Cleanup(provider.Close)
			settings := map[string]any{"tool_mode": "patch", "enable_fs_search_tools": false, "extensions": map[string]any{"enabled": true}, "skills": map[string]any{"enabled": false}}
			if sdk == "subagent" {
				settings["tool_mode"] = "full"
				settings["allowed_tools"] = []string{"exercise_child", "file_read", "spawn_agent", "wait_agent", "list_agents", "followup_agent", "steer_agent", "cancel_agent"}
			}
			daemonSettings := map[string]any{
				"provider": "openai", "model": "gpt-4o", "weak_model": "gpt-4o", "max_tokens": 256,
				"openai": map[string]any{"platform": "openai", "base_url": provider.URL, "api_key_env_var": "KODELET_TEST_CHILD_PROVIDER_KEY", "api_mode": "chat_completions"},
				"serve":  map[string]any{"runner_settings": settings},
			}
			for key, value := range settings {
				daemonSettings[key] = value
			}
			writeConfig := func(path string, value any) {
				t.Helper()
				data, err := json.Marshal(value)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, data, 0o600))
			}
			configPath := filepath.Join(root, "daemon.json")
			writeConfig(configPath, daemonSettings)
			writeConfig(filepath.Join(workspace, "kodelet-config.yaml"), settings)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			port := listener.Addr().(*net.TCPAddr).Port
			require.NoError(t, listener.Close())
			serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)
			baseEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1"}
			daemonEnv := append(append([]string{}, baseEnv...), "KODELET_BASE_PATH="+filepath.Join(root, "daemon-state"), "KODELET_CONFIG_FILE="+configPath, "KODELET_CONFIG_FILE_MODE=isolated", "KODELET_TEST_CHILD_PROVIDER_KEY=daemon-only-key")
			startReceiptProcess(t, daemonCLIProcess(ctx, t, root, daemonEnv, "serve", "--host=127.0.0.1", "--port="+strconv.Itoa(port), "--auth-token=client-secret", "--runner-auth-token=runner-secret", "--embedded-runner="+strconv.FormatBool(placement == "embedded"), "--runner-workspace="+workspace))
			if placement == "standalone" {
				runnerEnv := append(append([]string{}, baseEnv...), "KODELET_BASE_PATH="+filepath.Join(root, "runner-state"))
				startReceiptProcess(t, daemonCLIProcess(ctx, t, workspace, runnerEnv, "runner", "start", "--server="+serverURL, "--auth-token=runner-secret"))
			}
			var runnerID string
			require.Eventually(t, func() bool {
				runners, _, err := fetchRunners(ctx, serverURL, "client-secret")
				if err != nil {
					return false
				}
				for _, runner := range runners {
					if runner.Connected && runner.Status == runnerregistry.RunnerStatusIdle {
						runnerID = runner.ID
						return true
					}
				}
				return false
			}, 20*time.Second, 20*time.Millisecond)
			client, err := chat.NewControlPlaneChatRunner(serverURL, "client-secret", runnerID)
			require.NoError(t, err)
			invoke := func(parent, stage, childID string) map[string]any {
				t.Helper()
				input := map[string]string{"stage": stage, "childId": childID}
				if stage == "first" {
					input["history"] = "fork-parent-history"
				}
				data, err := json.Marshal(input)
				require.NoError(t, err)
				run := func() error {
					_, err := client.Run(ctx, chat.ChatRequest{ConversationID: parent, TurnID: parent + "-" + stage, RunnerID: runnerID, CWD: workspace, Message: "exercise-child:" + string(data)}, &receiptDiscardSink{})
					return err
				}
				if stage == "httpcancel" {
					done := make(chan error, 1)
					go func() { done <- run() }()
					var admitted map[string]string
					require.Eventually(t, func() bool {
						state, err := os.ReadFile(filepath.Join(workspace, "child-httpcancel-admitted.json"))
						if err != nil || json.Unmarshal(state, &admitted) != nil {
							return false
						}
						_, err = os.Stat(filepath.Join(workspace, "httpcancel-started"))
						return err == nil
					}, 10*time.Second, 10*time.Millisecond)
					require.Equal(t, childID, admitted["conversationId"])
					require.NoError(t, client.StopConversationTurn(ctx, childID, admitted["runId"]))
					receipt, err := client.GetTurnReceipt(ctx, childID, admitted["runId"])
					require.NoError(t, err)
					require.Equal(t, "cancelled", receipt.Status)
					select {
					case err = <-done:
						require.NoError(t, err)
					case <-ctx.Done():
						t.Fatal("parent did not finish after exact child HTTP cancellation")
					}
				} else {
					err = run()
				}
				require.NoError(t, err, "stage %s", stage)
				state, err := os.ReadFile(filepath.Join(workspace, "child-"+stage+".json"))
				if err != nil {
					history, loadErr := client.LoadConversation(ctx, parent)
					encoded, _ := json.Marshal(history)
					t.Logf("stage %s missing result; parent history (%v): %s", stage, loadErr, encoded)
				}
				require.NoError(t, err, "stage %s did not finish", stage)
				var result map[string]any
				require.NoError(t, json.Unmarshal(state, &result))
				if stage != "denied" {
					require.NotContains(t, result, "error", "stage %s: %v", stage, result)
				}
				return result
			}
			first := invoke("lifecycle-parent", "first", "")
			childID, ok := first["conversationId"].(string)
			require.True(t, ok, "%v", first)
			require.NotEmpty(t, childID)
			if sdk == "subagent" {
				// The parent HTTP turn has ended. Nothing in a second foreground
				// call may be responsible for keeping this worker alive.
				launch := first
				require.Eventually(t, func() bool {
					_, err := os.Stat(filepath.Join(workspace, "first-started"))
					return err == nil
				}, 10*time.Second, 10*time.Millisecond)
				process, err := os.FindProcess(int(launch["extensionPID"].(float64)))
				require.NoError(t, err)
				require.NoError(t, process.Signal(syscall.Signal(0)), "retained extension survives parent turn completion")
				pending, err := client.LoadConversation(ctx, childID)
				require.NoError(t, err)
				encoded, err := json.Marshal(pending.Messages)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), "child-first-answer")
				require.NoError(t, os.WriteFile(filepath.Join(workspace, "first-released"), nil, 0o600))
				first = invoke("lifecycle-parent", "collect", childID)
				require.Equal(t, childID, first["conversationId"])
				require.Equal(t, launch["runId"], first["runId"], "collection must not start a follow-up")
			}
			require.Equal(t, "child-first-answer", first["output"])
			previousRun := first["runId"]
			stages := []string{"followup", "steer", "cancel"}
			if sdk != "subagent" {
				stages = append(stages, "httpcancel")
			}
			for _, stage := range append(stages, "aftercancel") {
				result := invoke("lifecycle-parent", stage, childID)
				require.Equal(t, childID, result["conversationId"], "%v", result)
				require.NotEqual(t, previousRun, result["runId"])
				previousRun = result["runId"]
				if stage == "cancel" || stage == "httpcancel" {
					assert.Equal(t, true, result["cancelled"])
				} else {
					assert.Equal(t, "child-"+stage+"-answer", result["output"])
				}
				if stage == "steer" {
					assert.Equal(t, "injected", result["steerOutcome"])
					if sdk != "subagent" {
						assert.Equal(t, "promptRequired", result["terminalSteerOutcome"])
					}
				}
			}
			before := childCalls.Load()
			denied := invoke("other-parent", "denied", childID)
			assert.NotEmpty(t, denied["error"], "another parent cannot resume this child")
			assert.Equal(t, before, childCalls.Load(), "denial must precede a child provider effect")
			fresh := invoke("lifecycle-parent", "fresh", "")
			assert.NotEqual(t, childID, fresh["conversationId"])
			assert.Equal(t, "child-fresh-answer", fresh["output"])
			history, err := client.LoadConversation(ctx, childID)
			require.NoError(t, err)
			assert.Equal(t, runnerID, history.RunnerID)
			assert.Equal(t, workspace, history.CWD)
			encoded, err := json.Marshal(history.Messages)
			require.NoError(t, err)
			for _, text := range []string{"fork-parent-history", "child-first-answer", "child-followup-answer", "child-steer-answer", "child-aftercancel-answer"} {
				assert.Contains(t, string(encoded), text)
			}
		})
	}
}

func childLifecycleProvider(t *testing.T, workspace string, childCalls *atomic.Int32, consumer bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer daemon-only-key", r.Header.Get("Authorization"))
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role       string          `json:"role"`
				Content    json.RawMessage `json:"content"`
				ToolCallID string          `json:"tool_call_id"`
				ToolCalls  []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"messages"`
			Tools []map[string]any `json:"tools"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) || !assert.NotEmpty(t, request.Messages) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var input, all, lastUser string
		pending := make(map[string]bool)
		steerReadDone := false
		for _, message := range request.Messages {
			for _, call := range message.ToolCalls {
				pending[call.ID] = true
			}
			if message.Role == "tool" {
				assert.True(t, pending[message.ToolCallID], "tool result must have a matching call")
				delete(pending, message.ToolCallID)
				steerReadDone = steerReadDone || message.ToolCallID == "child-steer-read"
			}
			var text string
			if err := json.Unmarshal(message.Content, &text); len(message.Content) != 0 && err != nil {
				var blocks []struct {
					Text string `json:"text"`
				}
				if !assert.NoError(t, json.Unmarshal(message.Content, &blocks)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				for _, block := range blocks {
					text += block.Text
				}
			}
			all += text + "\n"
			if message.Role == "user" {
				lastUser = text
				if strings.HasPrefix(text, "child-") {
					input = text
				}
			}
		}
		assert.Empty(t, pending, "provider history must not contain unfinished tool calls, including live forks")
		last := request.Messages[len(request.Messages)-1]
		var delta map[string]any
		finish := "stop"
		if input != "" {
			childCalls.Add(1)
			var tools []string
			for _, tool := range request.Tools {
				tools = append(tools, tool["function"].(map[string]any)["name"].(string))
			}
			if consumer {
				assert.Equal(t, "gpt-4o", request.Model, "subagent inherits the daemon's model")
				assert.ElementsMatch(t, []string{"file_read", "exercise_child"}, tools, "child provenance disables all six recursive subagent tools")
			} else {
				assert.Equal(t, "gpt-4o-mini", request.Model)
				assert.Contains(t, all, "FROZEN_CHILD_PROMPT")
				assert.ElementsMatch(t, []string{"file_read", "grep_tool", "glob_tool"}, tools, "explicit readonly child tools are independent of parent patch defaults")
			}
			if input == "child-fresh" {
				assert.NotContains(t, all, "fork-parent-history")
			} else {
				assert.Contains(t, all, "fork-parent-history")
			}
			if input != "child-first" && input != "child-fresh" {
				assert.Contains(t, all, "child-first-answer")
			}
			if input == "child-cancel" || input == "child-httpcancel" {
				assert.NoError(t, os.WriteFile(filepath.Join(workspace, strings.TrimPrefix(input, "child-")+"-started"), nil, 0o600))
				<-r.Context().Done()
				return
			}
			if consumer && input == "child-first" {
				assert.NoError(t, os.WriteFile(filepath.Join(workspace, "first-started"), nil, 0o600))
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(filepath.Join(workspace, "first-released")); err == nil {
						break
					}
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
				}
			}
			if input == "child-steer" && !steerReadDone {
				assert.NoError(t, os.WriteFile(filepath.Join(workspace, "steer-started"), nil, 0o600))
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(filepath.Join(workspace, "steer-released")); err == nil {
						break
					}
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
				}
				args, _ := json.Marshal(map[string]string{"file_path": filepath.Join(workspace, "marker.txt")})
				delta = childLifecycleToolDelta("child-steer-read", "file_read", string(args))
				finish = "tool_calls"
			} else {
				if input == "child-steer" {
					assert.Equal(t, 1, strings.Count(all, "unique-child-guidance"), "same steering request must enqueue once")
				}
				delta = map[string]any{"role": "assistant", "content": input + "-answer"}
			}
		} else if last.Role == "tool" {
			delta = map[string]any{"role": "assistant", "content": "parent-stage-complete"}
		} else {
			args := strings.TrimPrefix(lastUser, "exercise-child:")
			var values map[string]any
			if !assert.NoError(t, json.Unmarshal([]byte(args), &values)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			delete(values, "history")
			encoded, _ := json.Marshal(values)
			delta = childLifecycleToolDelta("parent-child-call", "exercise_child", string(encoded))
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{"id": "completion", "object": "chat.completion.chunk", "model": request.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
}

func childLifecycleToolDelta(id, name, arguments string) map[string]any {
	return map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}}}
}

func childLifecycleExtension(t *testing.T, dir, sdk string) string {
	t.Helper()
	var executable, filename, source string
	switch sdk {
	case "typescript":
		var err error
		executable, err = exec.LookPath("node")
		require.NoError(t, err)
		dist, err := filepath.Abs("../../sdk/dist")
		require.NoError(t, err)
		filename = "lifecycle.mjs"
		source = fmt.Sprintf(`import {defineExtension, z} from %q;
import {runExtension} from %q;
import {existsSync, writeFileSync} from "node:fs";
import {join} from "node:path";
import {setTimeout as delay} from "node:timers/promises";
await runExtension(defineExtension(ext => {
  ext.registerProfile({name:"worker", systemPrompt:"FROZEN_CHILD_PROMPT", options:{model:"gpt-4o-mini",allowedTools:["file_read","grep_tool","glob_tool"],enableFSSearchTools:true,noExtensions:true,noSkills:true}});
  ext.registerTool({name:"exercise_child",description:"Exercise delegated child lifecycle",inputSchema:z.object({stage:z.string(),childId:z.string()}),async execute(input,ctx) {
    let result;
    try {
      const request={profile:"worker",message:"child-"+input.stage,requestId:"operation-"+input.stage};
      if(input.childId) request.resume=input.childId;
      else request.contextMode=input.stage==="first"?"fork":"fresh";
      const child=await ctx.children.start(request);
      if(input.stage==="httpcancel")writeFileSync(join(ctx.cwd,"child-httpcancel-admitted.json"),JSON.stringify({conversationId:child.conversationId,runId:child.runId}));
      const waitMarker=async name=>{const end=Date.now()+10000;while(!existsSync(join(ctx.cwd,name))){if(Date.now()>end)throw Error("provider marker timeout");await delay(10);}};
      let steering;
      if(input.stage==="steer"){
        await waitMarker("steer-started");
        steering=await child.steer("unique-child-guidance",{requestId:"guidance-once"});
        const duplicate=await child.steer("unique-child-guidance",{requestId:"guidance-once"});
        if(JSON.stringify(steering)!==JSON.stringify(duplicate))throw Error("steer replay mismatch");
        writeFileSync(join(ctx.cwd,"steer-released"),"");
      }
      if(input.stage==="cancel"||input.stage==="httpcancel"){
        await waitMarker(input.stage+"-started");if(input.stage==="cancel")await child.cancel();
        const end=Date.now()+10000;
        do{result=await child.read();if(!result.done)await delay(10);if(Date.now()>end)throw Error("cancel drain timeout");}while(!result.done);
      }else result=await child.wait({signal:ctx.signal});
      if(steering){result.steerOutcome=steering.outcome;result.terminalSteerOutcome=(await child.steer("too late",{requestId:"late-guidance"})).outcome;}
    }catch(error){result={error:String(error)};}
    writeFileSync(join(ctx.cwd,"child-"+input.stage+".json"),JSON.stringify(result));
    return JSON.stringify(result);
  }});
}));
`, "file://"+filepath.Join(dist, "index.js"), "file://"+filepath.Join(dist, "runtime.js"))
	case "python":
		root := os.Getenv("KODELET_PYTHON_SDK_PATH")
		require.NotEmpty(t, root, "set KODELET_PYTHON_SDK_PATH to a uv-synced SDK checkout")
		executable = filepath.Join(root, ".venv", "bin", "python")
		filename = "lifecycle.py"
		source = `import asyncio, json
from pathlib import Path
from kodelet_sdk import BaseModel, Extension
ext=Extension(name="lifecycle")
ext.register_profile({"name":"worker","systemPrompt":"FROZEN_CHILD_PROMPT","options":{"model":"gpt-4o-mini","allowedTools":["file_read","grep_tool","glob_tool"],"enableFSSearchTools":True,"noExtensions":True,"noSkills":True}})
class Input(BaseModel):
    stage: str
    childId: str
@ext.tool("exercise_child",description="Exercise delegated child lifecycle",input_schema=Input)
async def exercise(input,ctx):
    root=Path(ctx.cwd)
    try:
        request={"profile":"worker","message":"child-"+input.stage,"request_id":"operation-"+input.stage}
        if input.childId: request["resume"]=input.childId
        else: request["context_mode"]="fork" if input.stage=="first" else "fresh"
        child=await ctx.children.start(**request)
        if input.stage=="httpcancel": (root/"child-httpcancel-admitted.json").write_text(json.dumps({"conversationId":child.conversation_id,"runId":child.run_id}))
        async def wait_marker(name):
            async with asyncio.timeout(10):
                while not (root/name).exists(): await asyncio.sleep(.01)
        steering=None
        if input.stage=="steer":
            await wait_marker("steer-started")
            steering=await child.steer("unique-child-guidance",request_id="guidance-once")
            duplicate=await child.steer("unique-child-guidance",request_id="guidance-once")
            assert steering==duplicate, "steer replay mismatch"
            (root/"steer-released").write_text("")
        if input.stage in ("cancel","httpcancel"):
            await wait_marker(input.stage+"-started")
            if input.stage=="cancel": await child.cancel()
            async with asyncio.timeout(10):
                while True:
                    result=await child.read()
                    if result["done"]: break
                    await asyncio.sleep(.01)
        else: result=await child.wait()
        if steering:
            result["steerOutcome"]=steering["outcome"]
            result["terminalSteerOutcome"]=(await child.steer("too late",request_id="late-guidance"))["outcome"]
    except Exception as error: result={"error":str(error)}
    (root/("child-"+input.stage+".json")).write_text(json.dumps(result))
    return json.dumps(result)
ext.run_sync()
`
	case "subagent":
		executable = os.Getenv("KODELET_TEST_SUBAGENT_PYTHON")
		require.NotEmpty(t, executable, "set KODELET_TEST_SUBAGENT_PYTHON to an interpreter with the consumer dependencies")
		root := os.Getenv("KODELET_TEST_SUBAGENT_PATH")
		require.NotEmpty(t, root, "set KODELET_TEST_SUBAGENT_PATH to the migrated consumer checkout")
		filename = "subagent-lifecycle.py"
		// Exercise the consumer's actual public handlers, runtime and SQLite store
		// with real host RPC. Only the deterministic driver tool is test-specific.
		source = fmt.Sprintf(`import asyncio, json, os, sys
from pathlib import Path
sys.path.insert(0,%q)
from kodelet_sdk import BaseModel
from kodelet_subagent.extension import SubagentApplication, SpawnAgentInput, FollowupAgentInput, WaitAgentInput, ListAgentsInput, SteerAgentInput, CancelAgentInput
app=SubagentApplication()
class Input(BaseModel):
    stage: str
    childId: str
@app.extension.tool("exercise_child",description="Drive actual subagent lifecycle handlers",input_schema=Input)
async def exercise(input,ctx):
    root=Path(ctx.cwd)
    phase="start"
    try:
        identity_path=root/"subagent-identity.json"
        if input.stage=="collect":
            started={"data":json.loads(identity_path.read_text())}
        elif input.childId:
            agent_id=json.loads(identity_path.read_text())["agent_id"]
            started=await app.followup_agent(FollowupAgentInput(agent_id=agent_id,task="child-"+input.stage),ctx)
        else:
            request={"name":"fresh-worker" if input.stage=="fresh" else "fixture-worker","task":"child-"+input.stage}
            if input.stage=="fresh": request["context_mode"]="fresh"
            started=await app.spawn_agent(SpawnAgentInput(**request),ctx)
            if "error" not in started: identity_path.write_text(json.dumps(started["data"]))
        if "error" in started: raise RuntimeError(started["error"])
        run_id=started["data"]["run_id"]
        if input.stage=="first":
            result={"conversationId":started["data"]["conversation_id"],"runId":run_id,"extensionPID":os.getpid()}
            (root/"child-first.json").write_text(json.dumps(result))
            return json.dumps(result)
        agent_id=started["data"]["agent_id"]
        store=await app.runtime.store_for_context(ctx)
        live=app.runtime.get_live_run(store,agent_id)
        async def wait_marker(name):
            async with asyncio.timeout(10):
                while not (root/name).exists(): await asyncio.sleep(.01)
        if input.stage=="steer":
            await wait_marker("steer-started")
            steered=await app.steer_agent(SteerAgentInput(agent_id=agent_id,message="unique-child-guidance"),ctx)
            if "error" in steered: raise RuntimeError(steered["error"])
            async with asyncio.timeout(10):
                while await store.next_steering(live.lease) is not None: await asyncio.sleep(.01)
            (root/"steer-released").write_text("")
        if input.stage=="cancel":
            await wait_marker("cancel-started")
            phase="cancel_agent"
            canceled=await app.cancel_agent(CancelAgentInput(agent_id=agent_id),ctx)
            if "error" in canceled: raise RuntimeError(canceled["error"])
        phase="wait_agent"
        waited=await app.wait_agent(WaitAgentInput(agent_id=agent_id,timeout_ms=10000),ctx)
        if "error" in waited: raise RuntimeError(waited["error"])
        phase="list_agents"
        listing=await app.list_agents(ListAgentsInput(),ctx)
        if "error" in listing: raise RuntimeError(listing["error"])
        snapshot=waited["data"]
        assert snapshot["status"]==("canceled" if input.stage=="cancel" else "completed"), snapshot
        result={"conversationId":snapshot["conversation_id"],"runId":run_id,"output":waited["content"],"cancelled":snapshot["status"]=="canceled"}
        if input.stage=="steer": result["steerOutcome"]="injected"
    except asyncio.CancelledError:
        task=asyncio.current_task()
        result={"error":"driver cancelled","phase":phase,"task":task.get_name(),"cancelling":task.cancelling()}
        (root/("child-"+input.stage+".json")).write_text(json.dumps(result))
        raise
    except Exception as error: result={"error":str(error)}
    (root/("child-"+input.stage+".json")).write_text(json.dumps(result))
    return json.dumps(result)
app.extension.run_sync()
`, filepath.Join(root, "src"))
	default:
		t.Fatalf("unknown child lifecycle SDK %q", sdk)
	}
	path := filepath.Join(dir, filename)
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return fmt.Sprintf("#!/bin/sh\nexec %q %q\n", executable, path)
}
