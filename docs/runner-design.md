# Kodelet Workspace-Bound Runner and Agent Environment Design

## Status

Implemented through the initial capacity-one Phase 3 release. The future ephemeral execution-instance phase remains deliberately deferred until an isolation and durability backend is selected.

The current implementation preserves the design's direct-workspace constraints: no worktrees, containers, micro-VMs, filesystem snapshots, process namespaces, network namespaces, or same-workspace concurrent top-level runs. Remote Web UI and TUI conversations are supported, including control-plane conversation browsing and resume; `kodelet run --runner` remains disabled pending a broader one-shot client contract. The runner now provisions every run through an `ExecutionInstanceProvider`, but the only built-in provider returns a fresh lifecycle handle backed by the same registered workspace. This establishes creation and cleanup ordering without claiming filesystem, process, network, or port isolation.

The implemented robustness model includes generation-fenced open reconciliation, immediate control-plane cancellation when an active runner connection is lost or replaced, a context-backed run-lease watchdog, asynchronous bounded manifest refresh, symmetric message-size enforcement, bounded handler-draining peer shutdown, pending-to-durable conversation affinity, and lease-aware extension runtime replacement.

## Summary

This document defines Kodelet's workspace-bound runner architecture. A central `kodelet serve` process acts as the control plane: it owns the API layer, provider threads, provider credentials, conversation persistence, and the core agentic loop. A runner is a long-running Kodelet process bound to exactly one canonical workspace directory and exposes that workspace as a remote agent environment containing context, tools, skills, extension behavior, and workspace-local commands.

The runner initiates one persistent WebSocket connection to the control plane. Messages use JSON-RPC 2.0 encoded as one JSON object per WebSocket text frame. The connection carries runner registration, heartbeats, run manifests, extension lifecycle proxy calls, tool execution and progress, cancellation, and extension UI requests.

At the start of each top-level run, the runner returns a versioned environment manifest. The manifest contains a snapshot of workspace context such as `AGENTS.md`, runner-scoped skill and tool definitions, extension-provided resources, and relevant workspace configuration. The control plane pins that manifest for the complete run. Changes discovered by later manifest beats apply to subsequent runs rather than mutating an active run's prompt or tool catalog.

The initial implementation executes runner-side tools directly in the registered workspace and permits one active run per runner. It deliberately does not introduce worktrees, containers, micro-VMs, filesystem snapshots, or network namespaces.

Each runner has a stable opaque control-plane ID, mutable display metadata, host metadata, and one live connection generation. Startup takes an OS-backed advisory file lock for the canonical workspace and records diagnostic PID metadata in that locked file, preventing two local runner processes from serving the same workspace while allowing crash-safe recovery.

The long-term execution model is that every top-level run receives a fresh ephemeral execution instance created by its workspace-bound runner. Each instance will have dedicated filesystem, process, and network state, allowing independent runs to use the same conventional internal ports such as `3000`, `5432`, and `6379` without collisions. The control-plane protocol should not depend on how that instance is implemented.

## Context

Kodelet currently runs the provider loop, context discovery, tools, skills, extensions, and conversation persistence in one process. Provider threads directly discover context from `tooltypes.State`, call the local tool executor, and access a concrete extension runtime through shared helpers.

The proposed design introduces an explicit boundary between the **agent** and its **environment**:

- the control plane owns the agent, including model requests, provider-native history, continuation decisions, goals, steering, usage, and client APIs;
- the runner owns the environment, including workspace context, runner-local configuration, tools, runner-global and workspace skills, plugins, extensions, and eventually creation of an ephemeral execution instance.

This boundary is richer than a remote tool API because Kodelet extensions participate in user-message processing, system-prompt construction, tool policy, tool-result shaping, agent lifecycle events, follow-up messages, and interactive UI requests. The runner therefore exposes a remote **agent environment**, not merely a command executor.

## Feasibility Assessment

The design is feasible, but it is a medium-to-large architectural change rather than primarily a transport feature. Kodelet already has useful seams in its provider `Thread` implementations, `BasicState`, extension runtime, and `ChatRunner`; however, provider loops currently reach directly into local context discovery, tool execution, and extension helpers. Extracting those dependencies behind one environment contract is the critical path.

WebSocket and JSON-RPC are not the principal technical risk. The harder work is preserving provider-specific turn behavior and extension ordering while allowing some tools to execute centrally and others remotely. Implementing `agentenv.LocalEnvironment` first keeps that refactor testable without introducing a network boundary at the same time.

The initial capacity-one runner provides parallelism across multiple workspace-bound runners. It does not provide concurrent runs against one workspace. Same-workspace parallelism arrives only after the runner can provision independent per-run environments.

## Decision

Kodelet will use the following model:

1. `kodelet serve` acts as the control plane and client-facing API.
2. The control plane owns provider clients, provider-native conversation state, the complete model/tool continuation loop, goals, steering, usage accounting, and conversation persistence.
3. `kodelet runner start` starts a long-running process bound to the command's canonical current working directory.
4. A runner owns exactly one workspace and never accepts an arbitrary workspace path from the control plane.
5. Runner startup takes an exclusive OS-backed advisory file lock for its canonical workspace. The locked file contains diagnostic metadata such as PID, but lock ownership rather than file existence or PID state determines whether a runner is active.
6. The control plane assigns each runner a stable opaque ID. Hostname, workspace basename, and optional display name are metadata rather than identity, and registration is deduplicated by authenticated owner, stable host instance ID, and canonical workspace path.
7. The runner discovers and hosts workspace context, runner-scoped skills, runner tools, workspace plugins, extension processes, extension commands, and extension lifecycle handlers.
8. The runner keeps the existing extension subprocess protocol local: extensions continue using stdio JSON-RPC, while the runner proxies aggregate lifecycle and tool behavior to the control plane.
9. The control plane and runner communicate over one runner-initiated WebSocket using JSON-RPC 2.0 messages.
10. Each run begins with a full environment manifest that is pinned for the duration of that run.
11. Host-executed model tools have an explicit placement: control-plane tools execute beside central conversation state and are governed by control-plane model policy, while runner tools execute in the workspace environment and are governed by runner environment policy. Provider-native capabilities remain provider-owned.
12. Runner-owned extensions retain visibility over lifecycle events for host-executed tools, including control-plane tools, so remote execution preserves current extension policy semantics. Provider-native tools are subject to the hooks exposed by their provider API and cannot be assumed to support host-side `tool.call` and `tool.result` interception.
13. The initial runner executes directly in its workspace with capacity one.
14. A future runner implementation creates one fresh ephemeral execution instance per top-level run without changing control-plane ownership of the agent loop.

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
- Leave a clean path to fresh per-run execution instances with isolated internal ports.

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

In Go, this abstraction belongs in package `agentenv` and should be named `agentenv.Environment` rather than `agentenv.AgentEnvironment`. The package qualifier keeps call sites explicit without stuttering.

### Environment manifest

A versioned snapshot of the resources and prompt inputs exposed by a runner for one run, including context files, tool definitions, skill definitions, commands, relevant configuration, capabilities, and content digests.

### Execution instance

The concrete backing used by an agent environment to execute one run. Initially, the execution instance is the runner's workspace and host process environment. In the future, it can be a fresh ephemeral instance with dedicated filesystem, process, network, and internal port state. Distinguishing the logical agent environment from its physical execution instance prevents the term “environment” from carrying both meanings in implementation APIs.

## Core Invariants

### One runner, one workspace

A runner resolves and canonicalizes its current working directory at startup. That workspace is fixed for the stable runner registration and every live connection serving it.

The control plane assigns work to a runner ID. It never sends an absolute CWD that can redirect the runner to a different workspace.

Starting runners from different canonical directories creates separate runners even when they execute on the same machine:

```bash
(cd ~/src/kodelet && kodelet runner start)
(cd ~/src/another-project && kodelet runner start)
```

### One local runner process per workspace

Before connecting to a control plane, `kodelet runner start` acquires an exclusive OS-backed advisory file lock keyed by the canonical workspace path. The lock coordinates Kodelet runner processes on the local host; the implementation must not treat lock-file existence as proof that a runner is active.

The lock file lives outside the source repository, preferably under the platform runtime directory such as `$XDG_RUNTIME_DIR/kodelet/runners/<workspace-hash>.lock`, with a Kodelet user-state fallback when no runtime directory is available. The lock key is based on the canonical path alone rather than the target server, preventing two runner processes from serving the same mutable workspace to different control planes.

After acquiring the lock, the runner writes diagnostic metadata into the locked file:

```json
{
  "pid": 12345,
  "startedAt": "2026-08-06T10:15:00Z",
  "workspace": "/home/user/src/kodelet",
  "runnerId": "runner_abc",
  "server": "https://kodelet.example"
}
```

On first startup, `runnerId` can be omitted until registration completes and then written into the same file. The runner retains the locked file handle for its complete process lifetime. Metadata refreshes truncate and rewrite that same locked file; they must not atomically rename another file over the path because the OS lock applies to the opened file or inode rather than to the pathname.

The PID and other contents exist only to produce a useful error and support inspection. The held OS lock is authoritative: PIDs can be reused, metadata can be stale, and a process must never break or steal a lock merely because the recorded PID appears absent. A crash or clean process exit releases the kernel lock even though the file remains. The next runner acquires the existing file and overwrites its stale metadata.

The lock file is intentionally persistent and is not unlinked during normal shutdown. Removing it around lock release creates a race in which another process can lock the old file while a third process creates and locks a new inode at the same path. The file and containing directory use user-only permissions, such as `0600` and `0700`, and never contain credentials.

On Windows, the mandatory byte-range lock is placed beyond the diagnostic JSON region rather than over byte zero. This preserves the one-runner invariant while allowing `runner inspect` and duplicate-start diagnostics to read metadata from a lock file that is currently owned by another process.

If acquisition fails, startup exits before registration and reports the canonical workspace, recorded PID, runner ID, server, and start time when those fields can be read. This local lock is a safety mechanism for cooperative Kodelet processes, not a sandbox or a general filesystem lock for other applications.

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
- future ephemeral execution-instance creation.

### The manifest is pinned for one run

The full environment manifest returned by `run.open` is immutable from the control plane's perspective for the lifetime of that run. In particular, `AGENTS.md` and the model-facing tool catalog do not silently change between provider turns.

If the workspace, skills, extension registrations, or configuration change, the runner advertises a new manifest digest. The live connection generation continues to fence stale sockets; the new manifest applies to the next run. If a required capability disappears during an active run, the runner reports an environment error instead of silently changing the pinned contract.

### One active top-level run per runner initially

The initial runner executes directly in its registered workspace, so the control plane assigns at most one top-level run to it at a time. A run may still contain provider-requested parallel tool calls.

### One active run per conversation

A conversation cannot have more than one active top-level run. The control plane already owns the provider thread and enforces this globally rather than relying on runner-local memory.

### Every future run receives a fresh execution instance

When ephemeral execution support is introduced, every top-level run receives a newly created execution instance that is not reused by another run. Any state needed by a later run must be durable outside the previous instance.

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
               │ create fresh execution instance for run
               ▼
┌──────────────────────────────┐
│ Ephemeral execution instance │
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
| Model-facing system information (git status, OS/version, date) | Consumes pinned snapshot | Discovers and snapshots |
| Runner-global and workspace skills | Consumes definitions/results | Discovers and executes |
| Workspace tools | Routes model calls | Executes |
| Host-executed control-plane tools | Executes | Applies extension lifecycle policy where required |
| Provider-native tools | Configures and receives provider events | Applies catalog policy where supported; does not execute |
| Extensions | Proxies lifecycle operations | Discovers, starts, and hosts processes |
| Extension UI | Routes to clients | Proxies extension requests |
| Future execution instances | Tracks run state | Creates and destroys instances |

## Agent Environment Contract

Provider loops should depend on an agent-environment abstraction rather than directly depending on `tools.BasicState` and a concrete `*extensions.Runtime`.

Conceptually, the package API is:

```go
package agentenv

type Environment interface {
    Open(ctx context.Context, spec RunSpec) (EnvironmentManifest, error)
    ExecuteCommand(ctx context.Context, request CommandRequest) (CommandResult, error)
    DispatchLifecycle(ctx context.Context, request LifecycleRequest) (LifecycleResult, error)
    ExecuteTool(ctx context.Context, request ToolRequest, updates ToolUpdateSink) (ToolResult, error)
    Close(ctx context.Context) error
}
```

Kodelet will provide at least two implementations:

```text
agentenv.LocalEnvironment
  wraps BasicState, local context discovery, skills, and extensions

agentenv.RemoteEnvironment
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
runner initializes or acquires its workspace extension runtime; a new runtime dispatches session.start and resources.discover
    ↓
runner snapshots context, skills, tools, commands, extensions, and config
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

A runner command can return a direct response or an agent prompt. A direct response is streamed and persisted without calling the provider; an agent prompt replaces the submitted command text and may select recipe metadata before `user.message` and the provider loop continues. Central conversation commands such as goal mutation remain control-plane operations.

The initial runner reuses one extension runtime for its workspace and environment-profile variant, matching the current persistent interactive-host behavior. `session.start` and `resources.discover` run once for that runtime generation. When configuration or executable fingerprints change, later callers receive a replacement generation while an active run keeps its prior runtime generation leased through run cleanup; runtime construction cancellation remains tied to the individual `run.open` operation. The retired generation receives `session.end` and closes after its run leases end, or when the host shuts down. A transient fingerprint/discovery failure continues serving an existing cached generation rather than terminating active extension processes. Per-run extension runtimes can be introduced with future ephemeral execution instances without changing the control-plane lifecycle methods.

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
    WorkingDirectory    string
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

For remote runs, model-facing workspace and system context comes from the pinned runner manifest rather than the control-plane host.

### Context snapshot

The runner loads configured context files, including workspace and runner-home `AGENTS.md`, when opening a run. It returns their full model-facing content, paths suitable for display, and content digests.

The control plane persists the context snapshot or the complete relevant manifest with the run and uses it for every provider turn in that run. This produces deterministic run instructions and a stable prompt prefix that is friendlier to provider prompt caching than re-reading context before every turn.

An agent may edit `AGENTS.md` during a run, but those edits do not change the active run's instructions. They are reflected in a later manifest and therefore affect the next run.

### Manifest beats

The runner computes a current manifest digest at registration, before accepting a run, and periodically while connected. Application heartbeats include the current connection generation and digest without repeatedly sending the full manifest. Discovery-only probes do not start extension session lifecycle; the first real run supplies the call context for `session.start` and `resources.discover`. The initial cold probe is bounded by the runner process context so first-time extension startup is not constrained by the short refresh deadline. Periodic probes run asynchronously from the connection select loop with a bounded context, so slow discovery cannot suppress heartbeats. Only one probe or `run.open` owns the snapshot gate at a time; a concurrent `run.open` waits for that gate with its own bounded timeout instead of surfacing ordinary refresh contention as an immediate busy failure.

When the digest changes, the runner sends a `runner.manifestChanged` notification. The control plane can request a refreshed idle manifest for display or compatibility checks, but an active run remains pinned to the manifest returned by its own `run.open` response. Connection identity, run identity, and extension runtime generation are excluded from the resource digest so reconnects and runtime-generation bookkeeping do not create false manifest-change notifications.

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

The control plane sends run-scoped lifecycle requests to the runner, and the runner dispatches them through its local extension runtime and returns the aggregate result. Tool events for runner-executed tools are dispatched locally as part of `tool.execute` rather than making a second control-plane round trip.

The extension runtime still exposes the complete existing lifecycle:

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

For the initial persistent runtime, `session.start`, `resources.discover`, and `session.end` are runner-local runtime events. The remaining events are either proxied from the central agent loop or dispatched around runner-side tool execution.

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

### Host-executed control-plane tool lifecycle

Control-plane tools execute beside central conversation state, but runner-owned extensions must retain the opportunity to observe, block, or sanitize them when they subscribe to the relevant tool events.

For a control-plane tool, the control plane therefore proxies `tool.call` to the runner before execution, proxies transient `tool.update` values through the runner before client display, and proxies `tool.result` before inserting the authoritative result into provider history.

This adds network calls for central tools but preserves the current rule that workspace extension policy applies to every host-executed model tool. A later explicit policy may exempt trusted internal tools, but that is not the default behavior in this design.

### Provider-native tool lifecycle

Provider-native tools such as OpenAI web search execute inside the provider API rather than through Kodelet's host tool executor. Runner extensions can affect whether those tools are offered when the provider integration represents them in the `agent.init` tool list, but the control plane cannot promise pre-execution blocking or result mutation through `tool.call` and `tool.result`.

Provider integrations may forward native-tool activity as observational events when the provider exposes enough structured information, but those events must not be presented as an enforceable extension policy boundary.

### Extension UI

An extension can issue UI requests while handling a lifecycle event or runner tool:

```text
extension → runner → control plane → browser or TUI
extension ← runner ← control plane ← browser or TUI
```

The runner proxies the full supported extension UI surface, including input, confirm, select, notifications, passive widgets, transcript entries, and interactive surfaces. Surface input and resize events travel in the opposite direction to the owning extension. Extension identity, process generation, scope ID, frame sequence, and run ID must survive the proxy so stale frames cannot overwrite a newer extension generation.

The control plane routes interactive requests to the client attached to the run and returns the response. If no capable interactive client is attached, it returns the same structured unavailable or dismissed outcome expected by the local extension API. Client capabilities are opt-in: an omitted `clientCapabilities` field means interactive input and persistent surfaces are unavailable, which keeps unattended API clients from unexpectedly receiving blocking prompts. Cancellation must unblock pending extension requests and close run-scoped surfaces.

## Tool Placement

The control plane builds one model-facing tool catalog by merging central tool definitions with the pinned runner manifest.

### Initial host-executed control-plane tools

Tools that operate on central conversation or provider state execute in the control plane:

| Tool | Reason |
|---|---|
| `get_goal` | Reads central conversation metadata |
| `update_goal` | Mutates central conversation metadata |
| `read_conversation` | Reads the authoritative conversation store and invokes a utility model |

### Provider-native capabilities

Provider-native capabilities such as OpenAI web search are configured by the control plane and execute inside the provider API. They are not runner tools or host-executed control-plane tools, even when represented by a name in Kodelet's allowed-tool configuration.

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

Model profiles and runner environment profiles are separate namespaces:

- `profile` selects a control-plane model profile. The control plane resolves provider, model, reasoning policy, provider-native capabilities, and control-plane tool policy from its own configuration.
- `environmentProfile` selects a runner-local configuration profile. The runner resolves that name from its own global and workspace configuration before discovering the run manifest. Blank or `default` selects the runner's base configuration.
- Selecting a model profile never implicitly selects a same-named runner profile, and the runner never uses the model profile as a local configuration lookup key.
- After runner identity, readiness, capacity, and profile compatibility are validated, the control plane creates an in-memory pending affinity before reserving `run.open`. The binding becomes durable only after the conversation record exists. Failed reservation or a first turn that produces no conversation releases the pending binding, avoiding phantom durable rows; once persisted, a later request may omit the environment profile and reuse it but cannot select a different runner or environment profile.

Runner profiles are defined under the separate `environment_profiles` configuration namespace on the runner host:

```yaml
environment_profiles:
  workspace:
    allowed_tools: [bash, apply_patch, file_read]
    allowed_commands: ["go test *", "mise run *"]
    extensions:
      allow: [acp-subagent]
```

The runner applies the selected environment profile before context, tools, skills, recipes, and extensions are discovered. Extension runtimes are cached by canonical workspace plus environment-profile identity. Fingerprint changes publish a new generation for later callers without closing a generation still leased by an active run.

At `run.open`, the runner materializes a sanitized environment configuration projection into the manifest. It never sends secrets, environment variables, runner authentication credentials, or arbitrary runner-global provider credentials.

Runner-local system information is excluded from the protocol-v1 resource digest for compatibility and to avoid date-only manifest churn.

Workspace-derived prompt inputs that the central model requires, such as a custom system-prompt file, must be loaded by the runner and included as content in the manifest. The control plane must not interpret runner-local paths as server-local paths.

Runner `allowed_tools` policy filters runner-owned tools while the manifest is built. An explicit list is currently a strict allowlist: unknown names do not cause a fallback to the full default tool catalog. It does not suppress `get_goal`, `update_goal`, `read_conversation`, or other control-plane-owned tools, whose availability is resolved centrally. Runner extensions still receive the effective merged tool list during `agent.init` and retain the lifecycle visibility described above for host-executed control-plane tools.

Recipe-backed commands carry a digest of their raw content and metadata in the pinned manifest. If a recipe changes or disappears during an active run, command execution fails and asks the user to start a new run rather than executing content that was not part of the pinned snapshot.

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

Kodelet already depends on `gorilla/websocket`, so this transport does not require adding a second RPC stack or generated Protobuf runtime. The protocol will add application code and schemas, but the incremental library and binary-size cost should be small relative to adopting gRPC or Connect-Go.

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
    "displayName": "kodelet-gpu",
    "host": {
      "instanceId": "host_xyz",
      "hostname": "framework-desktop",
      "os": "linux",
      "arch": "amd64"
    },
    "workspace": {
      "path": "/home/user/src/kodelet",
      "name": "kodelet"
    },
    "kodeletVersion": "1.2.3",
    "manifestDigest": "sha256:4d7e..."
  }
}
```

`runnerId` is optional on first registration. When omitted, the control plane finds or creates the record identified by the authenticated owner, `host.instanceId`, and canonical `workspace.path`, then returns its stable opaque runner ID. A reconnect sends the previously assigned ID when it is available locally; losing that cached ID does not create a duplicate because the server-side workspace identity key still resolves the existing record.

The runner creates `host.instanceId` as a stable random identifier in the Kodelet user-state directory. It is not a credential or a hardware identity. `host.hostname`, operating system, architecture, workspace name, and optional `displayName` are mutable display and diagnostic metadata and never determine authorization. The runner supplies the workspace name because the control plane may run on an operating system with different path semantics.

The control-plane store enforces a uniqueness constraint equivalent to `(owner_id, host_instance_id, canonical_workspace_path)`. Registration is an upsert against that key. Supplying an existing `runnerId` with a different host instance or workspace path is rejected rather than rebinding the runner. Starting from a moved or renamed workspace path creates a new runner under the one-runner-one-workspace model.

`displayName` is optional and defaults in the UI to the workspace basename, qualified by hostname when needed. It is mutable and need not be unique. The project directory basename must never be used as `runnerId`, and ordinary startup does not expose a user-selected `--id` flag.

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

Only one live connection can serve a stable runner registration. A reconnect atomically advances the generation and invalidates the previous connection. The local workspace lock prevents a second cooperating runner process from presenting itself as the same workspace, while the server-side uniqueness constraint prevents duplicate persistent records and the generation fences requests and notifications from a stale socket.

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
    "agent": {
      "provider": "anthropic",
      "model": "claude-sonnet-4-6",
      "profile": "default",
      "environmentProfile": "workspace",
      "recipeName": "",
      "invokedBy": "webui"
    },
    "clientCapabilities": {
      "interactiveUI": true,
      "persistentSurfaces": true
    },
    "reservedToolNames": ["get_goal", "update_goal", "read_conversation"]
  }
}
```

The runner returns the full pinned manifest in the response. Returning the manifest as the `run.open` result makes successful environment initialization a prerequisite for starting the central model loop.

The runner receives provider and model identifiers because some environment tools and extension call contexts are model-sensitive, but it does not receive provider credentials. `agent.profile` remains model-context metadata; only `agent.environmentProfile` selects runner-local configuration. If command execution changes recipe metadata, subsequent lifecycle requests carry the effective call context for that run.

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
ui.widget.set
ui.widget.frame
ui.widget.remove
ui.transcript.append
ui.surface.open
ui.surface.frame
ui.surface.close
ui.surface.input
ui.surface.resize

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

Heartbeat state is hot-path in-memory state. Registration, disconnect, run-state, and manifest transitions are persisted, while every individual heartbeat is not written to SQLite under the registry mutex. A clean disconnect persists the latest in-memory snapshot; after a control-plane crash, restored runners are offline regardless of the exact last heartbeat timestamp.

The registry watches transport termination independently of handler draining. If a connection disappears or is replaced while a run is active, the generation-fenced run becomes lost and the control plane invokes the conversation cancellation hook immediately, stopping provider streaming rather than waiting for the next runner tool call. Every opened run also watches the owning control-plane context; if that context ends and normal `run.close` has not completed within the cleanup grace period, the watchdog sends best-effort cancel and close operations, then closes the connection if cleanup remains uncertain.

### Concurrency and backpressure

Each connection has exactly one WebSocket reader goroutine and one writer goroutine. Request handlers run independently so a long tool call or UI request does not block receipt of cancellation or responses.

Inbound normal requests, cancellation/close control requests, and notifications use separate bounded concurrency pools. Saturated normal or control request capacity returns a JSON-RPC busy error without starting the operation. `operation.cancel` is handled directly by the reader path so it can cancel an in-flight request even when handler pools are full. Notification overload closes the connection rather than allowing unbounded goroutine or memory growth.

All outbound messages pass through bounded control and replaceable-update queues. Responses, cancellation, close, and heartbeat traffic use the control path ahead of transient tool updates. A slow or unreachable peer eventually fails the relevant operation rather than causing unbounded memory growth. A transient failure to enqueue one WebSocket ping does not permanently disable the ping loop.

The connection enforces read and write deadlines, symmetric inbound and outbound message-size limits, ping and pong handling, and context cancellation for pending requests. An oversized request or notification fails locally; an oversized handler result returns a small operation-level JSON-RPC error so the connection and run remain usable. Transport termination is observable immediately, while `Peer.Done()` closes only after in-flight request and notification handlers have returned. Graceful shutdown applies a default drain deadline when the caller did not provide one, preventing an uncooperative handler from blocking shutdown forever. WebSocket compression remains disabled initially.

Every `tool.update` carries the exact JSON-RPC request ID assigned to its active `tool.execute` request in addition to `runId`, `toolCallId`, and a monotonically increasing sequence number. The control plane rejects updates whose connection generation, run lease, tool call, request ID, or sequence does not match the active operation.

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

The WebSocket protocol is intended for manifests, lifecycle payloads, schemas, tool input, text output, structured results, and UI messages. Initial implementations enforce a conservative 4 MiB default in each direction. An oversized `tool.execute` result becomes an in-band tool error that tells the model to request a smaller result, allowing the agent loop to recover; other oversized RPC results fail only that operation. Oversized results should ultimately be reduced or moved to a future artifact channel and do not intentionally tear down the runner connection.

Arbitrarily large files, images, logs, database dumps, and future build artifacts should use a separate upload or artifact protocol rather than unbounded JSON or base64 on the control socket. That protocol is outside the initial scope.

## Runner Identity, Registration Uniqueness, and Conversation Affinity

A runner has a stable, opaque, control-plane-assigned `runnerId`; a stable local `hostInstanceId`; mutable hostname and display metadata; and an ephemeral live connection ID and generation. These values serve different purposes and must not be conflated:

| Value | Purpose | Lifetime |
|---|---|---|
| `runnerId` | Authoritative control-plane identity and conversation affinity | Stable across restarts in the same host workspace |
| `hostInstanceId` | Distinguishes local Kodelet installations for workspace deduplication | Stable local user-state value |
| `displayName` | Optional human-friendly alias | Mutable and non-unique |
| `hostname` | User display and diagnostics | Mutable and non-unique |
| `connectionId` | Identifies one WebSocket connection | One connection |
| `generation` | Rejects stale connection traffic | Increases on reconnect |

Restarting `kodelet runner start` from the same canonical workspace, host instance, authenticated owner, and control plane reconnects as the same runner. Starting from another canonical path creates another runner, even if the basename or repository contents are identical. The same path string on a different host instance also creates another runner because paths are host-local.

The runner stores its host instance ID and optional cached runner registrations in the Kodelet user-state directory, keyed by normalized control-plane identity and canonical workspace. It never writes identity or lock files into the source repository. The server-side uniqueness key remains authoritative, so deleting the local runner-ID cache does not create a duplicate record.

Control-plane URLs are canonicalized before they are used as local registration-cache identity or endpoint bases. Equivalent scheme and host casing, trailing host dots, default ports, path escapes, and redundant path separators resolve to one identity; endpoint path segments are escaped independently. Plain HTTP is accepted only for loopback hosts, while connections across hosts require HTTPS/WSS.

Duplicate prevention has three independent layers:

1. The local OS file lock prevents two cooperating runner processes from serving the same canonical workspace.
2. The server-side uniqueness constraint prevents two persistent runner records for the same owner, host instance, and workspace path.
3. The live connection generation prevents two sockets from concurrently acting as the same runner after reconnect or replacement.

Hostname, display name, PID, workspace basename, and manifest digest are metadata rather than identity. Changing them updates the runner record but does not create or authorize a different runner. `runnerId` and `hostInstanceId` are identifiers rather than secrets; the runner credential remains the authentication mechanism.

A new remote conversation selects a runner. The binding is durable because the runner defines the workspace, context, available skills, extensions, and future environment source.

Subsequent runs for that conversation default to the same runner. The control plane must not silently move a conversation to another runner because two paths or display names appear similar. Moving work to another runner initially requires an explicit conversation fork or migration operation.

Runner affinity is stored in the dedicated `conversation_runner_affinity` table. Explicit runner selection creates a pending in-memory binding only after the selected runner is usable, then persists it after the first conversation record is saved. A definitive missing-conversation result releases the pending binding, while a transient conversation-store error retains it for retry. A failed first turn cannot leave a phantom durable row, while an existing durable conversation still cannot silently become local or target another runner. Local chat, `kodelet run --resume`, and ACP session loading reject runner-bound conversations before resolving or using their runner-local CWD; a server-backed TUI instead lists and loads control-plane conversations and resumes through stored affinity. Conversation deletion removes its affinity and runner-run history in the same database transaction and evicts the corresponding in-memory registry binding. Pending reservations currently have process lifetime if a client disappears after a successful run but before commit or release; adding an expiry policy remains a follow-up because the timeout and recovery UX need an explicit choice.

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

Client-stream disconnection does not automatically cancel a run. An explicit stop action cancels the central provider request and sends `run.cancel` or `operation.cancel` for active runner operations. Runner transport disconnection is different: it invalidates the environment contract, marks the run lost, and cancels the central provider loop immediately.

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

The registry restores every nonterminal run plus a bounded recent terminal history instead of loading an unbounded run table into memory. Terminal run rows whose conversation was already deleted are pruned, and ordinary conversation deletion removes its associated run rows transactionally.

### Cleanup timeout policy

The initial direct-workspace runner treats an unconfirmed environment or execution-instance cleanup as a process-health failure. It releases the active lease, reports `error` heartbeats, and rejects later `run.open` requests until the runner process restarts. This conservative latch intentionally avoids reusing a workspace after cleanup state became uncertain; automatic recovery after late resource drain is a separate policy choice rather than an implicit timeout behavior.

## Security Model

The initial design assumes the control plane and runners are mutually trusted and operated by the same user or trusted team. The runner core, extensions, tools, shell commands, workspace processes, and other processes running as the runner operating-system user belong to one runner-host trust domain. The direct-workspace runner does not attempt to isolate these processes from each other.

Connections across hosts require WSS and runner authentication during the WebSocket upgrade. The initial server uses a distinct runner-role token for this endpoint; after registration, the connection's stable runner ID and generation scope all assigned environment operations. Per-runner credentials and multi-owner authorization are outside the initial trusted deployment model.

Provider credentials remain in the control plane. The runner client uses its registration token for the control connection and does not deliberately add that in-memory value to tool contexts, manifests, extension initialization payloads, or logs. Runner-owned subprocesses otherwise inherit the ambient runner-host environment according to the existing local execution model; supplying a credential through an environment variable therefore makes it available within the same trusted host-user domain. The CLI must not capture credential environment variables as flag defaults because Cobra may render non-empty defaults in help and error output.

Binding a runner to one workspace prevents accidental path routing but is not a sandbox. Runner-side Bash, extensions, build systems, and package managers execute with the runner process's host permissions until ephemeral execution instances are introduced.

A future isolated execution instance introduces a new trust boundary. Its provider must construct an explicit per-instance environment and keep runner and control-plane credentials outside the container, virtual machine, or other sandbox rather than relying on process-global environment scrubbing.

## User Experience

### Starting the control plane

```bash
kodelet serve
```

`kodelet serve` continues to expose the Web UI, conversation APIs, and its existing server-local chat environment. That direct-local environment is not represented as an implicit runner registration; remote execution is selected explicitly by runner ID.

### Starting a workspace-bound runner

```bash
cd ~/src/kodelet
kodelet runner start --server https://kodelet.example
```

The runner binds to the canonical current directory. The initial command does not need a `--cwd` or `--id` flag; starting from another directory creates another runner. The default display label is the workspace basename, qualified by hostname when necessary. Users can optionally set a mutable alias without changing runner identity:

```bash
kodelet runner start --server https://kodelet.example --name kodelet-gpu
```

### Selecting a runner

The Web UI and TUI present connected runners when creating a remote conversation and display the selected runner on existing conversations.

```bash
kodelet chat --runner kodelet-gpu
kodelet run --runner runner_abc "implement the requested change"
```

A runner selector accepts an exact runner ID, an unambiguous ID prefix, or an unambiguous display name. Ambiguous names fail with the matching runner IDs, hostnames, and workspace paths rather than selecting one silently.

`kodelet run --runner` remains a target UX, but the existing one-shot command has options not represented by `ChatRequest`. Remote Web UI or TUI chat may land first; remote one-shot execution requires a request contract covering the supported CLI options and output modes.

Direct local commands without `--runner` retain their existing behavior and do not require a running control plane.

For the implemented remote TUI, `--profile` selects the control plane's model profile while `--runner-profile` independently selects the runner's environment profile:

```bash
kodelet chat --runner kodelet-gpu --profile anthropic --runner-profile workspace
```

The TUI loads profile and reasoning-effort choices from the control plane rather than from the client machine's configuration. While a remote run is active, steering is queued through the control plane, and `Esc` or `Ctrl+C` requests control-plane cancellation before the client stream is closed.

The Web UI exposes the same independent runner-profile value when a runner is selected. It is a free-form name because the profile namespace belongs to the remote runner and is not copied into control-plane configuration.

### Inspecting runners

```bash
kodelet runner list
kodelet runner inspect kodelet
```

Status includes runner ID, optional display name, hostname, connected or offline state, canonical workspace, Kodelet version, manifest digest, current run, connection generation, and last heartbeat. Detailed local inspection can also show the lock-file path, recorded PID, and start time.

### Removing runners

Offline runner registrations remain durable until explicitly removed:

```bash
kodelet runner remove kodelet-gpu
```

The control plane refuses to remove a connected runner. Every durable conversation affinity blocks ordinary removal, preserving the rule that a remote conversation never silently becomes local or migrates to another runner. Delete those conversations first, which removes their affinities transactionally, or use `--force` as the explicit destructive escape hatch to abandon all bindings and delete the registration plus its durable runner-run history. Forced removal deliberately leaves the conversation's `runner_id` metadata intact, so affected conversations fail closed and cannot be resumed until a future explicit migration or unbind operation is available. The CLI requires confirmation unless `--no-confirm` is supplied, requires `--no-confirm` for JSON output, and conditionally clears a matching local registration cache after successful removal. There is no automatic offline-runner TTL in the initial design.

### Initial remote Web UI limitations

Several existing Web UI features execute directly on the `kodelet serve` host. For remote conversations, the initial UI replaces CWD selection with runner selection and hides or disables the integrated terminal, server-local Git diff, server-local CWD suggestions, and server-side workspace command discovery until those features have runner-backed protocols.

Commands submitted in the conversation still resolve through the selected runner's command manifest and `command.execute` operation.

## Future Ephemeral Execution Instances

The long-term goal is one fresh ephemeral execution instance per top-level run. The runner remains bound to one source workspace but creates a dedicated instance before returning the run manifest.

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

The implementation includes a provider interface and a non-isolating direct-workspace provider. No isolated environment implementation, provisioning mechanism, state-transfer mechanism, port publication model, resource policy, or artifact model is selected by this document.

Ephemeral must not imply that useful workspace changes disappear. Before same-workspace concurrent runs are enabled, the environment provider needs an explicit durability and conflict model for source edits, generated artifacts, and services. Possible mechanisms include a durable mounted workspace, promoted snapshots, patches, or commits, but this document deliberately does not select one. Without such a mechanism, a later run cannot reliably observe changes made by an earlier run.

The runner-side implementation changes from direct workspace execution to environment-backed execution, but the control plane continues using the same manifest, lifecycle, tool, command, UI, and cancellation protocol.

## Integration with the Current Codebase

### `pkg/llm` and `pkg/llm/base`

Provider loops currently read `State.DiscoverContexts()` and call local extension and tool helpers directly. Refactor shared turn flow and tool execution to depend on `agentenv.Environment` and a placement-aware tool router.

The provider thread remains in the control plane and continues owning provider-native messages, compaction, usage, and persistence.

### `pkg/tools`

Keep existing tool implementations for `agentenv.LocalEnvironment`. Add serializable tool definitions and proxy tool implementations for runner manifests. Split central conversation tools from workspace tools without exposing placement to the model.

### `pkg/extensions`

Keep extension discovery, subprocess management, stdio JSON-RPC, event ordering, and extension SDK behavior on the runner. Introduce an adapter that exposes aggregate extension lifecycle behavior through the agent-environment protocol.

### `pkg/chat`

`ChatRunner` remains the client-facing persisted-run interface, but `DefaultChatRunner` must be refactored so central run setup can open either a local or remote `agentenv.Environment` before constructing the provider turn flow.

### `pkg/webui`

The Web server owns runner registration, run state, client event fan-out, cancellation, and extension UI routing. Current process-local active-run maps should become views over the central run service where necessary.

### `pkg/acp`

ACP continues to be a client-facing protocol. ACP sessions can use the same local or remote agent-environment abstraction without making the runner protocol itself ACP-specific.

### `cmd/kodelet`

Add runner start, list, inspect, and remove commands plus optional runner selection for interactive clients. Preserve direct-local defaults.

Potential package boundaries are:

```text
pkg/agentenv/          local and remote agent-environment contracts
pkg/runner/protocol/   leaf JSON-RPC envelope, registration, heartbeat, and run-lease types
pkg/runner/protocol/payload/ application payloads that depend on tools, extensions, commands, and model types
pkg/runner/client/     workspace-bound runner process
pkg/runner/registry/   control-plane connections and run-state coordination
  affinity.go          pending and durable conversation affinity
  tool_updates.go      run-scoped transient tool-update routing
```

The transport package remains a dependency leaf: `agentenv` does not import the registry or database graph, and the core protocol envelope does not import the extension or tool stack.

## Delivery Plan

### Phase 1: local agent-environment abstraction

- Extract the `agentenv.Environment` abstraction from provider turn flow and tool execution.
- Implement `agentenv.LocalEnvironment` with existing context, skills, tools, commands, and extensions.
- Split control-plane tools from environment tools.
- Snapshot context and tool definitions once per run.
- Preserve current direct-local behavior with focused provider and extension tests.

### Phase 2: WebSocket JSON-RPC protocol

- Add typed JSON-RPC envelopes and method payloads.
- Add the runner-initiated WebSocket endpoint, authentication, stable host and runner identity, registration upsert, generation fencing, heartbeat, and bounded connection queues.
- Add `run.open`, manifest snapshotting, lifecycle dispatch, runner tool execution, tool updates, cancellation, and run close.
- Add bidirectional extension UI requests.
- Enforce one active run per runner and one active run per conversation.

### Phase 3: external runner and client integration

- Add `kodelet runner start`, list, inspect, and remove.
- Add the canonical-workspace advisory lock, diagnostic PID metadata, and local identity cache outside source repositories.
- Add remote runner selection to the Web UI and TUI.
- Persist runner affinity and run records centrally.
- Expose offline, idle, busy, incompatible, and manifest-changed states.
- Disable or hide control-plane-local workspace features for remote conversations.
- Define remote one-shot CLI behavior before enabling `kodelet run --runner` broadly.

### Future phase: ephemeral execution instances

- Use the existing execution-instance provider interface to add an isolated backend; the initial direct-workspace provider only supplies lifecycle and cleanup symmetry.
- Create one fresh execution instance per top-level run.
- Discover the manifest and execute tools and extensions inside that instance.
- Destroy the instance when the run ends.
- Increase runner capacity only when independent execution instances make concurrent runs safe.

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

### Per-run ephemeral execution instances in the first release

This remains the long-term target but is deliberately deferred. The environment protocol and central agent-loop split should be validated before selecting and implementing an isolation backend. Once introduced, the execution-instance provider owns the explicit environment projection and excludes runner and control-plane credentials from the isolated instance.

### Sharing one workspace across concurrent initial runs

This is not selected because filesystem locks cannot coordinate Bash, Git, build systems, extension processes, or services binding ports. Initial capacity is one.

## Open Questions

- Should extension runtimes be runner-lived, run-lived, or configurable once ephemeral execution instances exist?
- How should edits and artifacts produced in an ephemeral execution instance become durable, and how should conflicting concurrent results be reconciled?
- Which auxiliary Web UI features should receive runner-backed protocols first: Git diff, terminal, command discovery, or file browsing?
- What subset of existing `kodelet run` flags should the first remote one-shot request support?
