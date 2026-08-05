# Kodelet Workspace-Bound Runner and Agent Environment Design

## Status

Draft.

## Summary

This document proposes adding workspace-bound runners to Kodelet. A central `kodelet serve` process acts as the control plane: it owns the API layer, provider threads, provider credentials, conversation persistence, and the core agentic loop. A runner is a long-running Kodelet process bound to exactly one canonical workspace directory and exposes that workspace as a remote agent environment containing context, tools, skills, extension behavior, and workspace-local commands.

The runner initiates one persistent WebSocket connection to the control plane. Messages use JSON-RPC 2.0 encoded as one JSON object per WebSocket text frame. The connection carries runner registration, heartbeats, run manifests, extension lifecycle proxy calls, tool execution and progress, cancellation, and extension UI requests.

At the start of each top-level run, the runner returns a versioned environment manifest. The manifest contains a snapshot of workspace context such as `AGENTS.md`, runner-scoped skill and tool definitions, extension-provided resources, and relevant workspace configuration. The control plane pins that manifest for the complete run. Changes discovered by later manifest beats apply to subsequent runs rather than mutating an active run's prompt or tool catalog.

The initial implementation executes runner-side tools directly in the registered workspace and permits one active run per runner. It deliberately does not introduce worktrees, containers, micro-VMs, filesystem snapshots, or network namespaces.

The long-term execution model is that every top-level run receives a fresh ephemeral environment created by its workspace-bound runner. Each environment will have dedicated filesystem, process, and network state, allowing independent runs to use the same conventional internal ports such as `3000`, `5432`, and `6379` without collisions. The control-plane protocol should not depend on how that environment is implemented.

## Context

Kodelet currently runs the provider loop, context discovery, tools, skills, extensions, and conversation persistence in one process. Provider threads directly discover context from `tooltypes.State`, call the local tool executor, and access a concrete extension runtime through shared helpers.

The proposed design introduces an explicit boundary between the **agent** and its **environment**:

- the control plane owns the agent, including model requests, provider-native history, continuation decisions, goals, steering, usage, and client APIs;
- the runner owns the environment, including workspace context, runner-local configuration, tools, runner-global and workspace skills, plugins, extensions, and eventually an ephemeral run environment.

This boundary is richer than a remote tool API because Kodelet extensions participate in user-message processing, system-prompt construction, tool policy, tool-result shaping, agent lifecycle events, follow-up messages, and interactive UI requests. The runner therefore exposes a remote **agent environment**, not merely a command executor.

## Decision

Kodelet will use the following model:

1. `kodelet serve` acts as the control plane and client-facing API.
2. The control plane owns provider clients, provider-native conversation state, the complete model/tool continuation loop, goals, steering, usage accounting, and conversation persistence.
3. `kodelet runner start` starts a long-running process bound to the command's canonical current working directory.
4. A runner owns exactly one workspace and never accepts an arbitrary workspace path from the control plane.
5. The runner discovers and hosts workspace context, runner-scoped skills, runner tools, workspace plugins, extension processes, extension commands, and extension lifecycle handlers.
6. The runner keeps the existing extension subprocess protocol local: extensions continue using stdio JSON-RPC, while the runner proxies aggregate lifecycle and tool behavior to the control plane.
7. The control plane and runner communicate over one runner-initiated WebSocket using JSON-RPC 2.0 messages.
8. Each run begins with a full environment manifest that is pinned for the duration of that run.
9. Model-invoked tools have an explicit placement: control-plane tools execute beside central conversation state, while runner tools execute in the workspace environment.
10. Runner-owned extensions retain visibility over tool lifecycle events, including control-plane tools, so remote execution preserves current extension policy semantics.
11. The initial runner executes directly in its workspace with capacity one.
12. A future runner implementation creates one fresh ephemeral environment per top-level run without changing control-plane ownership of the agent loop.

## Goals

- Enable a central Kodelet server to operate an agent against a workspace hosted by another Kodelet process.
- Keep provider credentials and provider-native conversation history in the control plane.
- Couple runner identity and execution behavior to one workspace directory.
- Preserve Kodelet's current context, skill, tool, extension, lifecycle, goal, steering, and UI semantics where practical.
- Snapshot `AGENTS.md` and related prompt inputs once per run for deterministic behavior and stable inference-prefix caching.
- Treat runner-global skills as resources belonging only to that runner.
- Keep existing extensions and extension SDKs unaware of the network boundary.
- Use a simple, inspectable wire protocol with no code-generation requirement.
- Preserve ordinary direct-local `kodelet run`, `kodelet chat`, ACP, and Web UI behavior through a local agent-environment implementation.
- Leave a clean path to fresh per-run ephemeral environments with isolated internal ports.

## Non-goals

The initial implementation will not:

- create Git worktrees, clones, containers, micro-VMs, or filesystem snapshots per run;
- provide process or network isolation;
- allow more than one active top-level run on a runner;
- run several unrelated workspaces from one runner process;
- dynamically select an arbitrary CWD for an assigned run;
- provide port forwarding, public preview URLs, or artifact transfer protocols;
- transparently retry a runner tool whose side effects are uncertain after connection loss;
- provide multi-tenant or untrusted-code isolation;
- make the control plane highly available;
- require Protobuf, gRPC, or Connect-Go;
- automatically start a control-plane daemon for ordinary direct-local CLI commands.

## Terminology

### Control plane

The `kodelet serve` process that exposes client APIs, owns provider threads and conversation state, runs the core agentic loop, stores runs and conversations, registers runners, and routes agent-environment operations.

### Runner

A long-running Kodelet process permanently bound to one canonical workspace directory. It hosts the agent environment for that workspace.

### Workspace

The runner's canonical startup directory and the source of workspace-local code, configuration, context, skills, plugins, extensions, commands, and dependencies. Workspace is an intrinsic property of the runner rather than an independently schedulable resource in the initial design.

### Run

One accepted top-level user submission. A run can contain multiple provider turns, parallel tool calls, steering received during execution, extension follow-up messages, and automatic goal continuation. A later top-level user submission is another run.

### Conversation

The durable provider-native message history, metadata, usage, goal state, and structured tool results associated with a sequence of runs.

### Agent environment

The runner-hosted interface used by the central agent loop. It provides a pinned manifest, extension lifecycle dispatch, runner-side tool execution, workspace command handling, and interactive extension callbacks.

### Environment manifest

A versioned snapshot of the resources and prompt inputs exposed by a runner for one run, including context files, tool definitions, skill definitions, commands, relevant configuration, capabilities, and content digests.

### Ephemeral environment

A future fresh run-scoped environment with dedicated filesystem, process, network, and internal port state. It is created by the workspace-bound runner and destroyed when the run ends.

## Core Invariants

### One runner, one workspace

A runner resolves and canonicalizes its current working directory at startup. That workspace is fixed for the stable runner registration and every live connection serving it.

The control plane assigns work to a runner ID. It never sends an absolute CWD that can redirect the runner to a different workspace.

Starting runners from different canonical directories creates separate runners even when they execute on the same machine:

```bash
(cd ~/src/kodelet && kodelet runner start --name kodelet)
(cd ~/src/another-project && kodelet runner start --name another-project)
```

### The control plane owns the agent loop

Provider requests, provider-native messages, reasoning streams, tool-continuation decisions, auto-compaction, goals, steering, extension follow-up continuation, usage aggregation, and final conversation persistence remain in the control plane.

The runner never needs OpenAI or Anthropic credentials for centrally operated runs.

### The runner owns the agent environment

The runner is authoritative for the workspace resources exposed to an agent:

- `AGENTS.md` and configured context files;
- runner-global and workspace-local skills;
- built-in workspace tools such as Bash and file operations;
- extension-provided tools and commands;
- extension lifecycle handling and policy;
- workspace-local configuration relevant to the environment;
- future ephemeral environment creation.

### The manifest is pinned for one run

The full environment manifest returned by `run.open` is immutable from the control plane's perspective for the lifetime of that run. In particular, `AGENTS.md` and the model-facing tool catalog do not silently change between provider turns.

If the workspace, skills, extension registrations, or configuration change, the runner increments its manifest generation and advertises a new digest. The new manifest applies to the next run. If a required capability disappears during an active run, the runner reports an environment error instead of silently changing the pinned contract.

### One active top-level run per runner initially

The initial runner executes directly in its registered workspace, so the control plane assigns at most one top-level run to it at a time. A run may still contain provider-requested parallel tool calls.

### One active run per conversation

A conversation cannot have more than one active top-level run. The control plane already owns the provider thread and enforces this globally rather than relying on runner-local memory.

### Every future run receives a fresh environment

When ephemeral environment support is introduced, every top-level run receives a newly created environment that is not reused by another run. Any state needed by a later run must be durable outside the previous ephemeral environment.

## Architecture

### Initial architecture

```text
┌──────────────────┐      HTTP / event stream      ┌──────────────────────────────┐
│ CLI, TUI, Web UI │ ────────────────────────────▶ │ Control plane                │
└──────────────────┘                               │ `kodelet serve`              │
                                                   │                              │
                                                   │ provider thread              │
                                                   │ central agent loop           │
                                                   │ conversation persistence     │
                                                   │ goals and steering           │
                                                   │ control-plane tools          │
                                                   │ client event fan-out         │
                                                   └──────────────┬───────────────┘
                                                                  │ WebSocket + JSON-RPC 2.0
                                                                  │ runner initiates
                                                                  ▼
                                                   ┌──────────────────────────────┐
                                                   │ Workspace-bound runner       │
                                                   │ `/home/user/src/kodelet`     │
                                                   │                              │
                                                   │ manifest and AGENTS.md       │
                                                   │ runner tools and skills      │
                                                   │ extension runtime proxy      │
                                                   │ direct workspace execution   │
                                                   └──────────────────────────────┘
```

### Long-term architecture

The control-plane ownership and wire protocol remain the same. The runner changes how it backs a run's agent environment:

```text
┌──────────────────────────────┐
│ Workspace-bound runner       │
│ `/home/user/src/kodelet`     │
└──────────────┬───────────────┘
               │ create fresh environment for run
               ▼
┌──────────────────────────────┐
│ Ephemeral run environment    │
│                              │
│ dedicated filesystem         │
│ dedicated processes          │
│ dedicated network            │
│ localhost:3000               │
│ localhost:5432               │
│ localhost:6379               │
└──────────────────────────────┘
```

The mechanism used to provision the environment is deliberately unspecified.

## Responsibility Split

| Concern | Control plane | Runner |
|---|---|---|
| Browser, CLI, TUI, and API | Owns | Does not expose directly |
| Provider credentials and clients | Owns | Does not receive |
| Provider-native messages | Owns | Receives only lifecycle payloads required by extensions |
| Core agentic loop | Owns | Services environment operations |
| Conversation persistence | Owns | Does not open the authoritative conversation store |
| Goals, steering, and metadata | Owns | Participates only through lifecycle context |
| Runner registration and status | Stores | Connects and heartbeats |
| `AGENTS.md` and context discovery | Consumes pinned snapshot | Discovers and snapshots |
| Runner-global and workspace skills | Consumes definitions/results | Discovers and executes |
| Workspace tools | Routes model calls | Executes |
| Control-plane tools | Executes | Applies extension lifecycle policy where required |
| Extensions | Proxies lifecycle operations | Discovers, starts, and hosts processes |
| Extension UI | Routes to clients | Proxies extension requests |
| Future environment creation | Tracks run state | Creates and destroys environment |

## Agent Environment Contract

Provider loops should depend on an agent-environment abstraction rather than directly depending on `tools.BasicState` and a concrete `*extensions.Runtime`.

Conceptually:

```go
type AgentEnvironment interface {
    Open(ctx context.Context, spec RunSpec) (EnvironmentManifest, error)
    ExecuteCommand(ctx context.Context, request CommandRequest) (CommandResult, error)
    DispatchLifecycle(ctx context.Context, request LifecycleRequest) (LifecycleResult, error)
    ExecuteTool(ctx context.Context, request ToolRequest, updates ToolUpdateSink) (ToolResult, error)
    Close(ctx context.Context) error
}
```

Kodelet will provide at least two implementations:

```text
LocalAgentEnvironment
  wraps BasicState, local context discovery, skills, and extensions

RemoteAgentEnvironment
  proxies the same logical operations to a workspace-bound runner
```

The interface is conceptual; implementation should prefer the smallest set of operations that preserves current behavior without mirroring every internal Go type over the network.

## Run Flow

The normal remote run follows this sequence:

```text
client submits top-level message
    ↓
control plane creates run and selects conversation's runner
    ↓
control plane → runner: run.open
    ↓
runner initializes or acquires its extension runtime and snapshots context, skills, tools, commands, extensions, and config
    ↓
runner → control plane: pinned environment manifest
    ↓
control plane merges runner and control-plane tools
    ↓
control plane proxies a matching workspace or extension command when the message invokes one
    ↓
control plane → runner: user.message lifecycle
    ↓
control plane stores effective user message and starts central provider loop
    ↓
agent.start lifecycle
    ↓
for each provider turn:
    turn.start lifecycle
    construct system prompt from pinned context
    agent.init lifecycle and tool-list patch
    call model
    route model tool calls by placement
    turn.end lifecycle
    ↓
agent.end lifecycle may return follow-up messages
    ↓
control plane applies goal and steering continuation rules
    ↓
control plane persists provider-native conversation and closes run environment
```

The control plane streams provider text, reasoning, tool activity, usage, UI requests, and final status to attached clients through its existing client-facing event APIs.

## Environment Manifest

### Manifest contents

An initial manifest should contain at least:

```go
type EnvironmentManifest struct {
    ProtocolVersion     int
    RunnerID            string
    RunID               string
    Generation          int64
    Digest              string
    ContextFiles        []ContextFile
    Tools               []ToolDefinition
    Skills              []SkillDefinition
    Commands            []CommandDefinition
    Config              EnvironmentConfig
    ExtensionGeneration int64
    Capabilities        EnvironmentCapabilities
}
```

Open-ended schemas and extension payloads remain JSON-native rather than being translated into a second schema language.

### Context snapshot

The runner loads configured context files, including workspace and runner-home `AGENTS.md`, when opening a run. It returns their full model-facing content, paths suitable for display, and content digests.

The control plane persists the context snapshot or the complete relevant manifest with the run and uses it for every provider turn in that run. This produces deterministic run instructions and a stable prompt prefix that is friendlier to provider prompt caching than re-reading context before every turn.

An agent may edit `AGENTS.md` during a run, but those edits do not change the active run's instructions. They are reflected in a later manifest and therefore affect the next run.

### Manifest beats

The runner computes a current manifest digest at registration, before accepting a run, and periodically while connected. Application heartbeats include the current generation and digest without repeatedly sending the full manifest.

When the digest changes, the runner sends a `runner.manifestChanged` notification. The control plane can request a refreshed idle manifest for display or compatibility checks, but an active run remains pinned to the manifest returned by its own `run.open` response.

## Skills

Skills belong to the runner that discovers them. This includes:

- workspace skills under the registered workspace;
- runner-global skills under that runner user's Kodelet home;
- skills installed through workspace or runner-global plugins.

Runner-global skills are not implicitly available to other runners registered with the same control plane.

The manifest exposes skill names, descriptions, sources, and digests. The `skill` tool executes on the runner. When invoked, it returns instructions matching the digest pinned in the manifest. The runner may eagerly snapshot skill content at `run.open` or lazily cache and verify it on first use, but it must not silently serve different content under the same pinned definition.

## Extensions

### Local extension processes remain unchanged

Extensions continue to run as subprocesses owned by the runner and communicate with the runner using the existing stdio JSON-RPC protocol. Extension SDKs do not connect to or know about the control plane.

```text
control plane ← WebSocket JSON-RPC → runner ← stdio JSON-RPC → extension
```

The runner remains responsible for extension discovery, initialization, deterministic handler ordering, timeouts, process restarts, tool registration, commands, and local extension state.

### Lifecycle proxying

The control plane sends lifecycle requests to the runner. The runner dispatches them through its local extension runtime and returns the aggregate result.

Lifecycle events include:

```text
session.start
resources.discover
user.message
agent.init
agent.start
turn.start
tool.call
tool.update
tool.result
turn.end
agent.end
session.end
```

The runner hides individual extension processes and intermediate mutations from the control plane. The control plane sees the same effective decision a local extension runtime would return: modified or blocked user messages, system-prompt patches, tool-list patches, modified tool input or results, and follow-up messages.

### Runner-side tool lifecycle

For a runner-side tool, one `tool.execute` request causes the runner to perform the complete local lifecycle:

```text
extension tool.call
    ↓
tool validation and execution
    ↓
extension tool.update for transient snapshots
    ↓
extension tool.result
    ↓
authoritative assistant-facing and structured result
```

Only post-policy updates and results cross the runner boundary.

### Control-plane tool lifecycle

Control-plane tools execute beside central conversation state, but runner-owned extensions must retain the opportunity to observe, block, or sanitize them when they subscribe to the relevant tool events.

For a control-plane tool, the control plane therefore proxies `tool.call` to the runner before execution, proxies transient `tool.update` values through the runner before client display, and proxies `tool.result` before inserting the authoritative result into provider history.

This adds network calls for central tools but preserves the current rule that workspace extension policy applies to every model-invoked tool. A later explicit policy may exempt trusted internal tools, but that is not the default behavior in this design.

### Extension UI

An extension can issue UI requests while handling a lifecycle event or runner tool:

```text
extension → runner → control plane → browser or TUI
extension ← runner ← control plane ← browser or TUI
```

The runner sends a bidirectional JSON-RPC request such as `ui.input`, `ui.confirm`, or `ui.select`. The control plane routes it to the client attached to the run and returns the response. Cancellation must unblock the pending extension request.

## Tool Placement

The control plane builds one model-facing tool catalog by merging central tool definitions with the pinned runner manifest.

### Initial control-plane tools

Tools that operate on central conversation or provider state execute in the control plane:

| Tool | Reason |
|---|---|
| `get_goal` | Reads central conversation metadata |
| `update_goal` | Mutates central conversation metadata |
| `read_conversation` | Reads the authoritative conversation store and invokes a utility model |
| Provider-native search tools | Executed by the provider API |

### Initial runner tools

Tools that operate on the workspace or runner environment execute on the runner:

| Tool | Reason |
|---|---|
| `bash` | Runs workspace processes |
| File read, write, edit, and patch tools | Access runner filesystem |
| Filesystem search tools | Search runner filesystem |
| `skill` | Loads runner-owned skill content |
| `view_image` for workspace files | Reads runner-local files |
| Extension-provided tools | Execute in runner extension processes |
| `web_fetch` initially | Uses the runner environment and future environment network policy |

Every tool definition carries placement internally, but placement is not exposed to the model.

Tool names must be unique across the merged catalog. A collision between a control-plane tool and a runner tool fails `run.open` rather than silently selecting one implementation.

## Configuration Ownership

The control plane is authoritative for provider credentials, provider accounts, server-enforced model policy, cost limits, and conversation-selected provider settings.

The runner is authoritative for workspace and runner-local configuration affecting context, tools, allowed commands, skills, plugins, extensions, recipes, and environment behavior.

At `run.open`, the runner materializes a sanitized environment configuration projection into the manifest. It never sends secrets, environment variables, runner authentication credentials, or arbitrary runner-global provider credentials.

Workspace-derived prompt inputs that the central model requires, such as a custom system-prompt file, must be loaded by the runner and included as content in the manifest. The control plane must not interpret runner-local paths as server-local paths.

The exact precedence between server policy, conversation profile, and workspace-requested model defaults remains subject to existing Kodelet configuration rules, with server policy acting as the final authority.

## Runner Connection Protocol

### Transport and encoding

The runner initiates one persistent WebSocket connection to the control plane:

```text
endpoint:    /api/runner/v1/connect
transport:   WebSocket, using WSS across hosts
subprotocol: kodelet.runner.v1.jsonrpc
encoding:    one JSON-RPC 2.0 object per WebSocket text frame
```

WebSocket frames provide message boundaries, so the protocol does not use extension-style `Content-Length` framing or ACP-style newline framing.

One socket initially carries control traffic and the single active run. Every run-scoped request includes `runId`. If future runner capacity creates meaningful head-of-line or backpressure problems, run-specific sockets can be added without changing the method payloads.

### Why JSON-RPC 2.0

JSON-RPC provides request IDs, correlated responses, structured errors, notifications, and symmetric requests in either direction without code generation. It also matches Kodelet's existing extension and ACP conventions.

Method payloads use typed Go structs with a small `json.RawMessage` envelope rather than unstructured `map[string]any` throughout the implementation.

### Registration

The runner's first request registers the stable runner and negotiates the application protocol:

```json
{
  "jsonrpc": "2.0",
  "id": "runner:1",
  "method": "runner.register",
  "params": {
    "protocolVersions": [1],
    "runnerId": "runner_abc",
    "name": "kodelet",
    "workspace": "/home/user/src/kodelet",
    "kodeletVersion": "1.2.3",
    "manifestDigest": "sha256:4d7e..."
  }
}
```

`runnerId` is optional on first registration. When omitted, the control plane creates a stable opaque runner ID and returns it. A reconnect sends the previously assigned ID.

The control plane responds with the selected version, stable runner identity, and a live connection generation:

```json
{
  "jsonrpc": "2.0",
  "id": "runner:1",
  "result": {
    "runnerId": "runner_abc",
    "protocolVersion": 1,
    "connectionId": "conn_xyz",
    "generation": 8,
    "heartbeatIntervalMs": 15000
  }
}
```

Only one live connection can serve a stable runner registration. The generation fences requests and notifications from a stale process after reconnect or replacement.

### Run open

The control plane opens a run with a request:

```json
{
  "jsonrpc": "2.0",
  "id": "server:42",
  "method": "run.open",
  "params": {
    "runId": "run_123",
    "conversationId": "conv_456",
    "reservedToolNames": ["get_goal", "update_goal", "read_conversation"]
  }
}
```

The runner returns the full pinned manifest in the response. Returning the manifest as the `run.open` result makes successful environment initialization a prerequisite for starting the central model loop.

### Tool execution and updates

The control plane executes a runner tool with a normal request:

```json
{
  "jsonrpc": "2.0",
  "id": "server:44",
  "method": "tool.execute",
  "params": {
    "runId": "run_123",
    "toolCallId": "call_789",
    "name": "bash",
    "input": {
      "command": "mise run test"
    }
  }
}
```

While the request is pending, the runner sends transient notifications:

```json
{
  "jsonrpc": "2.0",
  "method": "tool.update",
  "params": {
    "runId": "run_123",
    "requestId": "server:44",
    "toolCallId": "call_789",
    "sequence": 1,
    "result": {
      "output": "running tests..."
    }
  }
}
```

The eventual JSON-RPC response is the authoritative final result. Transient updates are replaceable and are never inserted into provider history as the final tool result.

### Bidirectional UI requests

The runner can issue requests on the same connection:

```json
{
  "jsonrpc": "2.0",
  "id": "runner:75",
  "method": "ui.confirm",
  "params": {
    "runId": "run_123",
    "requestId": "confirm_1",
    "title": "Apply migration?",
    "message": "This will modify the local database."
  }
}
```

The control plane responds after the attached client answers. Runner- and server-originated request IDs should use distinct prefixes or UUIDs to make logs and diagnostics unambiguous.

### Method groups

The initial protocol requires methods in these groups:

```text
runner.register
runner.heartbeat
runner.manifestChanged
runner.goodbye

run.open
run.close
run.cancel
run.environmentError

command.execute
lifecycle.dispatch

tool.execute
tool.update

ui.input
ui.confirm
ui.select
ui.notify

operation.cancel
```

Requests are used when the sender requires a result. Notifications are used for replaceable progress, heartbeat, manifest change, cancellation intent, and informational status.

### Heartbeat and liveness

WebSocket ping and pong frames detect transport liveness. A separate `runner.heartbeat` notification reports application health, runner state, active run, connection generation, and current manifest digest.

```json
{
  "jsonrpc": "2.0",
  "method": "runner.heartbeat",
  "params": {
    "runnerId": "runner_abc",
    "generation": 8,
    "state": "running",
    "activeRunId": "run_123",
    "manifestDigest": "sha256:9a21..."
  }
}
```

A live socket is not sufficient evidence that the runner can accept work; the scheduler uses application heartbeat state.

### Concurrency and backpressure

Each connection has exactly one WebSocket reader goroutine and one writer goroutine. Request handlers run independently so a long tool call or UI request does not block receipt of cancellation or responses.

All outbound messages pass through bounded queues. Cancellation, close, and heartbeat traffic should have priority over replaceable tool updates. A slow or unreachable peer eventually fails the relevant operation rather than causing unbounded memory growth.

The connection enforces read and write deadlines, maximum frame size, ping and pong handling, and context cancellation for pending requests. WebSocket compression remains disabled initially.

### Protocol evolution

Compatibility rules are:

1. Negotiate a whole-number protocol version during `runner.register`.
2. Changes within one version are additive.
3. Unknown JSON fields are ignored.
4. Required fields and semantic constraints are validated explicitly.
5. Unknown methods return the standard JSON-RPC method-not-found error.
6. Breaking semantic changes introduce a new protocol version.
7. Optional behavior is advertised through capabilities.

### Large and binary data

The WebSocket protocol is intended for manifests, lifecycle payloads, schemas, tool input, text output, structured results, and UI messages. Initial implementations enforce conservative message-size limits.

Arbitrarily large files, images, logs, database dumps, and future build artifacts should use a separate upload or artifact protocol rather than unbounded JSON or base64 on the control socket. That protocol is outside the initial scope.

## Runner Identity and Conversation Affinity

A runner has a stable registration identity and an ephemeral live connection generation. Restarting `kodelet runner start` from the same workspace and control-plane identity should normally reconnect as the same runner without writing identity files into the source repository.

A new remote conversation selects a runner. The binding is durable because the runner defines the workspace, context, available skills, extensions, and future environment source.

Subsequent runs for that conversation default to the same runner. The control plane must not silently move a conversation to another runner because two paths or display names appear similar. Moving work to another runner initially requires an explicit conversation fork or migration operation.

The storage representation of runner affinity can be a dedicated conversation field or provider-neutral metadata during the first implementation, but it must be treated as an authoritative binding rather than a UI hint.

## Run Lifecycle

The control plane stores a run separately from its conversation:

```text
created → opening → running → succeeded
                          ├→ failed
                          ├→ canceled
                          └→ lost
```

The runner is considered busy from successful `run.open` until `run.close` completes or the connection is lost.

The control plane persists provider-native conversation state directly because the provider loop is central. Runner events and tool results are inputs to that loop rather than an independent conversation checkpoint.

Client-stream disconnection does not automatically cancel a run. An explicit stop action cancels the central provider request and sends `run.cancel` or `operation.cancel` for active runner operations.

## Failure and Reconnection Semantics

### Runner disconnect while idle

The runner becomes offline and receives no work until it reconnects and registers a new generation.

### Runner disconnect during lifecycle dispatch

The active environment operation fails. The control plane stops the run unless the lifecycle event is explicitly defined as observational and safe to ignore.

### Runner disconnect during a side-effecting tool

If the runner may have started a tool but its authoritative response was not received, the tool outcome is uncertain. The control plane stops the run and reports the uncertainty. It does not retry the tool automatically.

The runner best-effort cancels active processes when its control connection closes, but cancellation cannot prove that a side effect did not already occur.

### Runner restart

A restarted runner reconnects with its stable runner ID and a new generation. Messages from older generations are rejected. The initial implementation does not resume an in-flight run across runner restart.

### Control-plane restart

Conversation state is durable, but active WebSocket connections and in-flight model or tool operations are lost. Active runs are marked lost or failed according to persisted run state. Transparent run resumption is deferred.

## Security Model

The initial design assumes the control plane and runners are mutually trusted and operated by the same user or trusted team.

Connections across hosts require WSS and runner authentication during the WebSocket upgrade. The runner credential authorizes only that runner's registration and assigned environment operations.

Provider credentials remain in the control plane. Runner registration credentials must not be included in tool contexts, subprocess environments, extension initialization payloads, or logs. The runner must scrub its control-plane credential before spawning extensions or workspace processes.

Binding a runner to one workspace prevents accidental path routing but is not a sandbox. Runner-side Bash, extensions, build systems, and package managers execute with the runner process's host permissions until ephemeral environment support is introduced.

## User Experience

### Starting the control plane

```bash
kodelet serve
```

`kodelet serve` continues to expose the Web UI and conversation APIs. Whether it also exposes its startup CWD through an embedded local environment is an open product decision.

### Starting a workspace-bound runner

```bash
cd ~/src/kodelet
kodelet runner start --server https://kodelet.example --name kodelet
```

The runner binds to the canonical current directory. The initial command does not need a `--cwd` flag; starting from another directory creates another runner.

### Selecting a runner

The Web UI and TUI present connected runners when creating a remote conversation and display the selected runner on existing conversations.

```bash
kodelet chat --runner kodelet
kodelet run --runner kodelet "implement the requested change"
```

`kodelet run --runner` remains a target UX, but the existing one-shot command has options not represented by `ChatRequest`. Remote Web UI or TUI chat may land first; remote one-shot execution requires a request contract covering the supported CLI options and output modes.

Direct local commands without `--runner` retain their existing behavior and do not require a running control plane.

### Inspecting runners

```bash
kodelet runner list
kodelet runner inspect kodelet
```

Status includes connected or offline state, workspace, Kodelet version, manifest digest, current run, and last heartbeat.

### Initial remote Web UI limitations

Several existing Web UI features execute directly on the `kodelet serve` host. For remote conversations, the initial UI replaces CWD selection with runner selection and hides or disables the integrated terminal, server-local Git diff, server-local CWD suggestions, and server-side workspace command discovery until those features have runner-backed protocols.

Commands submitted in the conversation still resolve through the selected runner's command manifest and `command.execute` operation.

## Future Ephemeral Run Environments

The long-term goal is one fresh ephemeral environment per top-level run. The runner remains bound to one source workspace but creates a dedicated environment before returning the run manifest.

The required invariant is limited to:

- a dedicated writable filesystem;
- dedicated process state;
- a dedicated network namespace;
- an independent internal port space;
- cleanup when the run ends.

This permits concurrent runs to use identical internal service addresses:

```text
Run A: localhost:3000, localhost:5432
Run B: localhost:3000, localhost:5432
```

No environment implementation, provisioning mechanism, state-transfer mechanism, port publication model, resource policy, or artifact model is selected by this document.

The runner-side implementation changes from direct workspace execution to environment-backed execution, but the control plane continues using the same manifest, lifecycle, tool, command, UI, and cancellation protocol.

## Integration with the Current Codebase

### `pkg/llm` and `pkg/llm/base`

Provider loops currently read `State.DiscoverContexts()` and call local extension and tool helpers directly. Refactor shared turn flow and tool execution to depend on `AgentEnvironment` and a placement-aware tool router.

The provider thread remains in the control plane and continues owning provider-native messages, compaction, usage, and persistence.

### `pkg/tools`

Keep existing tool implementations for `LocalAgentEnvironment`. Add serializable tool definitions and proxy tool implementations for runner manifests. Split central conversation tools from workspace tools without exposing placement to the model.

### `pkg/extensions`

Keep extension discovery, subprocess management, stdio JSON-RPC, event ordering, and extension SDK behavior on the runner. Introduce an adapter that exposes aggregate extension lifecycle behavior through the agent-environment protocol.

### `pkg/chat`

`ChatRunner` remains the client-facing persisted-run interface, but `DefaultChatRunner` must be refactored so central run setup can open either a local or remote `AgentEnvironment` before constructing the provider turn flow.

### `pkg/webui`

The Web server owns runner registration, run state, client event fan-out, cancellation, and extension UI routing. Current process-local active-run maps should become views over the central run service where necessary.

### `pkg/acp`

ACP continues to be a client-facing protocol. ACP sessions can use the same local or remote agent-environment abstraction without making the runner protocol itself ACP-specific.

### `cmd/kodelet`

Add runner start, list, and inspect commands plus optional runner selection for interactive clients. Preserve direct-local defaults.

Potential package boundaries are:

```text
pkg/agentenv/          local and remote agent-environment contracts
pkg/runs/              durable run model and coordination
pkg/runner/protocol/   JSON-RPC methods and typed payloads
pkg/runner/client/     workspace-bound runner process
pkg/runner/registry/   control-plane connections and scheduling
```

These paths are illustrative and should be adjusted to existing package ownership during implementation.

## Delivery Plan

### Phase 1: local agent-environment abstraction

- Extract an `AgentEnvironment` abstraction from provider turn flow and tool execution.
- Implement `LocalAgentEnvironment` with existing context, skills, tools, commands, and extensions.
- Split control-plane tools from environment tools.
- Snapshot context and tool definitions once per run.
- Preserve current direct-local behavior with focused provider and extension tests.

### Phase 2: WebSocket JSON-RPC protocol

- Add typed JSON-RPC envelopes and method payloads.
- Add the runner-initiated WebSocket endpoint, authentication, registration, generation fencing, heartbeat, and bounded connection queues.
- Add `run.open`, manifest snapshotting, lifecycle dispatch, runner tool execution, tool updates, cancellation, and run close.
- Add bidirectional extension UI requests.
- Enforce one active run per runner and one active run per conversation.

### Phase 3: external runner and client integration

- Add `kodelet runner start`, list, and inspect.
- Add remote runner selection to the Web UI and TUI.
- Persist runner affinity and run records centrally.
- Expose offline, idle, busy, incompatible, and manifest-changed states.
- Disable or hide control-plane-local workspace features for remote conversations.
- Define remote one-shot CLI behavior before enabling `kodelet run --runner` broadly.

### Future phase: ephemeral environments

- Add an environment-provider interface inside the runner.
- Create one fresh environment per top-level run.
- Discover the manifest and execute tools and extensions inside that environment.
- Destroy the environment when the run ends.
- Increase runner capacity only when independent environments make concurrent runs safe.

## Alternatives Considered

### Complete agent loop on the runner

This is closer to the current in-process code and would require a smaller initial refactor, but it moves provider credentials and provider-native conversation state to every runner. The selected design prefers central model policy, central persistence, and a runner that is specifically an execution environment.

### gRPC, Connect-Go, and Protobuf

Typed generated RPC would provide standard streaming and flow-control machinery, but the initial system has one first-party Go runner, capacity one, low message volume, and no high-performance requirement. WebSocket plus JSON-RPC reuses existing Kodelet dependencies and conventions, avoids code generation, and remains easy to inspect while debugging.

The application still requires run IDs, request correlation, generation fencing, heartbeats, cancellation, and uncertain-side-effect handling under either transport. Those requirements dominate logical reliability.

### Refreshing `AGENTS.md` every provider turn

This would more closely match current behavior but introduces repeated runner round trips, makes instructions change during a run, and weakens stable provider prompt prefixes. The selected design snapshots context at `run.open` and applies changes to the next run.

### One runner serving multiple workspaces

This is not selected because Kodelet resources and configuration are strongly associated with one CWD. A workspace-bound runner gives users a clear execution identity and prevents arbitrary remote path selection.

### Per-run ephemeral environments in the first release

This remains the long-term target but is deliberately deferred. The environment protocol and central agent-loop split should be validated before selecting and implementing an isolation backend.

### Sharing one workspace across concurrent initial runs

This is not selected because filesystem locks cannot coordinate Bash, Git, build systems, extension processes, or services binding ports. Initial capacity is one.

## Open Questions

- Should `kodelet serve` expose its startup CWD through an embedded `LocalAgentEnvironment` by default?
- Should a run targeting an offline or busy runner queue or fail immediately in the first release?
- What local registration format should retain stable runner IDs across process restarts?
- Should runner affinity be stored in a dedicated conversation column or provider-neutral metadata initially?
- How should server model policy, conversation profiles, and workspace-requested model defaults be merged exactly?
- Should extension runtimes be runner-lived, run-lived, or configurable once ephemeral environments exist?
- What heartbeat and lost-run timeouts should be used initially?
- Which auxiliary Web UI features should receive runner-backed protocols first: Git diff, terminal, command discovery, or file browsing?
- What subset of existing `kodelet run` flags should the first remote one-shot request support?
