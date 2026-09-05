# Kodelet Daemon-First, Runner-Only Execution Design

## Status

Proposed. Source review date: September 5, 2026. This document describes a target architecture and migration plan, not behavior already available in the CLI. Names of new flags, configuration keys, and API fields must be settled during implementation; existing commands mentioned here retain their current behavior until their migration lands.

This design builds on the [runner design](runner-design.md) and [extension design](extension-design.md). It proposes replacing the runner design's preservation of direct-local CLI execution with one daemon-owned execution path. It retains the existing runner protocol, control-plane ownership of the agent loop, and direct-host execution constraints. The [manual](MANUAL.md) remains the reference for currently shipped behavior.

## Summary

`kodelet serve` becomes the required control-plane daemon. `kodelet chat`, `kodelet run`, `kodelet conversation`, ACP, and the SDK become clients of that daemon rather than alternative hosts for the agent loop. The daemon owns provider connections, provider credentials, conversations, execution state, goals, steering, and event distribution. Runners own workspace context, tools, skills, extensions, and workspace processes.

For single-machine operation, `serve` can host an embedded runner. That runner uses the same WebSocket and JSON-RPC connection, registration, manifests, and run lifecycle as a separately launched `kodelet runner start`. Embedding changes process placement and lifecycle ownership, not execution semantics. A control-plane-only deployment can disable embedded execution and accept external runners instead.

The migration removes the direct-local execution path from clients and the control plane. It does not remove the local tool execution implementation used inside a runner. It also does not require every operation to open a runner lease: conversation-store operations and tool-free model helpers can execute centrally without accessing a workspace.

## Motivation

The current architecture supports both direct-local execution and runner-backed execution. Core runner behavior is already extensive, but the two entry paths differ in configuration handling, CLI coverage, extension UI, lifecycle expectations, and nested model calls. Maintaining both requires fixes and features to account for both ownership models.

A daemon-first architecture gives all clients one execution and persistence authority, while preserving local repository workflows and separately deployed runners. The intended simplification is one runtime contract, not a requirement for one process or one physical machine.

## Goals

- Make all supported agent execution originate in the control plane, including nested model helpers and subagents.
- Make workspace operations use the runner boundary, whether the runner is embedded, separately managed on the same host, or on another host.
- Keep CLI clients responsible for interaction and presentation, not provider threads or direct database access.
- Preserve the supported scripting, interactive, extension, and SDK workflows, with explicit decisions for incompatible behavior.
- Reuse existing APIs, runner messages, stores, and lifecycle mechanisms rather than building a parallel orchestration system.
- Allow incremental delivery and verification against external runners before switching defaults or deleting local paths.

## Non-goals

- An in-memory replacement for the WebSocket transport.
- Automatic daemon startup on the first CLI invocation in the initial release.
- High availability, transparent continuation across daemon restarts, or automatic replay of tools with uncertain side effects.
- Sandboxing, worktrees, containers, micro-VMs, process isolation, or network isolation.
- General-purpose file synchronization, artifact transfer, port forwarding, or public preview URLs.
- Identical UI capabilities across the native TUI, browser, ACP, and unattended clients.
- Preserving every existing flag by silently forwarding arbitrary client configuration or secrets.

## Current implementation and gaps

The following source anchors identify existing seams and migration work. They are not a claim that the proposed daemon-first behavior is implemented.

| Area | Current implementation | Consequence for this design |
|---|---|---|
| Runner hosting | [Runner](../pkg/runner/client/runner.go) exposes construction, connection, and cleanup independently of its CLI command. [Remote ACP](../cmd/kodelet/acp_remote.go) already hosts a runner alongside another service. | Reuse the runner component when composing `serve`; do not add another tool executor. |
| Local environment | [Runner service](../pkg/runner/client/service.go) constructs `LocalEnvironment` internally. [Chat](../pkg/chat/chat.go), [one-shot execution](../cmd/kodelet/run.go), and [provider environment setup](../pkg/llm/base/environment.go) also have local paths. | Retain runner-side execution while removing client and control-plane bypasses. |
| Execution request | [ChatRequest](../pkg/chat/chat.go) carries content, conversation, runner, profiles, reasoning effort, and CWD, but not the complete local one-shot contract. | Define retained execution options before converting `run`. |
| Conversation API | [Control-plane routes](../pkg/controlplane/server.go) already expose list, get, delete, fork, stream, steer, stop, and tool-result operations. | Reuse these APIs and extend only missing CLI semantics. |
| Configuration | [Runner configuration loading](../pkg/runner/client/service.go) uses the process's Viper settings through [LLM configuration helpers](../pkg/llm/config.go). `ServiceOptions` offers a configuration-loader seam. | An embedded runner needs explicit runner configuration ownership, especially across different conversation directories. |
| Nested model calls | [Web fetch extraction](../pkg/tools/web_fetch.go) shells out to local `kodelet run`. [SDK session launching](../sdk/src/agent.ts) has local configuration and ACP assumptions. | Remove hidden local-provider execution and define nested-call authorization. |
| Extension lifetime | [Background leases](../pkg/runner/client/background.go) retain run-isolated resources; acquisition requires a ready run and currently has a runner-wide limit. | Define state and background lifetime explicitly rather than promising local process persistence. |
| Tool metadata | The conversation-fork adapter in [runner service](../pkg/runner/client/service.go) forwards forking but returns no generic live thread metadata and ignores metadata writes. | Add only the concrete control-plane context operations required by supported tools. |
| Persistent UI | [Remote chat transport](../pkg/chat/controlplane.go) advertises interactive prompts but not the native TUI's persistent UI capabilities. [Runner UI routing](../pkg/controlplane/runner.go) does not complete surface or transcript routing. | Complete client-aware UI routing rather than just enabling capability bits. |
| Discovery and workspace panels | [ChatPage](../pkg/webui/frontend/src/pages/ChatPage.tsx) disables remote slash-command discovery and CWD suggestions. [Workspace targeting](../pkg/controlplane/workspace_runner.go) rejects custom CWD for runner-wide operations. | Add runner-backed discovery and distinguish conversation directory targets from runner-wide targets. |
| ACP directory behavior | [Remote ACP sessions](../pkg/acp/remote.go) constrain session CWD to the startup workspace. | Move directory selection and validation to the selected runner consistently. |

Core shell/file tools, skills, extension tools, ordinary agent/tool lifecycle hooks, slash-command execution, images, goals, steering, and background leases are not wholesale missing from the runner architecture. The work is to close the specific gaps and remove alternative ownership paths.

## Proposed architecture

```diagram
╭────────────────────────────────────────────────────╮
│ Thin clients: chat / run / conversation / ACP / SDK│
│ Web UI                                             │
╰────────────────────────┬───────────────────────────╯
                         │ Client API and event streams
                         ▼
╭──────────────────── kodelet serve ─────────────────╮
│ ╭──────────────────────╮   ╭────────────────────╮  │
│ │ Control plane        │◀─▶│ Embedded runner    │  │
│ │ Models, runs, store  │   │ Workspace execution│  │
│ ╰──────────┬───────────╯   ╰────────────────────╯  │
│            │        WebSocket + JSON-RPC over      │
│            │        loopback for embedded execution│
╰────────────┼───────────────────────────────────────╯
             │ Same runner protocol
             ▼
╭────────────────────────────────────────────────────╮
│ Standalone runners: same host or another host      │
╰────────────────────────────────────────────────────╯
```

### Ownership

| Component | Owns | Must not do |
|---|---|---|
| Thin client | Argument parsing, stdin, attachment preparation, terminal rendering, interactive responses, request submission, output formatting, exit codes | Construct provider threads, execute an alternative agent loop, or read/write the conversation database directly |
| Control plane | Provider configuration and credentials, model requests, execution state, conversation persistence, goals, steering, usage, client event distribution, runner assignment | Interpret runner-local paths as server-local files or execute workspace tools through a direct-local fallback |
| Runner | Canonical execution CWD, context and resource discovery, tools, extensions, environment policy, workspace services, execution-instance lifecycle | Own the central provider loop or require provider credentials for supported Kodelet-managed helpers |
| Service host | Listener startup, embedded runner composition, process shutdown, operational status | Change execution behavior based on whether a runner shares its process |

Provider-native tools remain provider-owned. Control-plane tools such as goals and conversation reading remain central; their placement is not a workspace-execution bypass. Extension subprocesses retain their existing runner-local protocol.

### Invariants

1. The control plane is the sole authority for supported Kodelet provider calls and conversation state.
2. Every workspace-capable run resolves a registered runner and uses the same environment protocol.
3. An unavailable daemon or runner produces an explicit error, never a silent local fallback.
4. Embedded and external runners share registration, capability negotiation, manifests, run fencing, cancellation, and cleanup semantics.
5. Clients cannot override control-plane model policy or runner environment policy by supplying unchecked configuration.
6. Every execution pins its effective configuration; workspace-backed runs additionally pin their runner environment manifest. Discovery changes apply to later runs.
7. Concurrent conversations cannot mutate process-global CWD, environment, or configuration to select their execution context.
8. One active top-level run per conversation remains enforced; background leases and child executions have explicit ownership.
9. Client disconnection, explicit cancellation, runner loss, and daemon shutdown are distinct events.
10. Removing direct-local execution does not remove local workspace access or the runner's local tool implementation.

## Daemon and embedded runner lifecycle

### Deployment model

The proposed single-machine default is one user-owned `serve` daemon with one configured embedded runner. Its canonical startup workspace supplies stable identity and defaults; it may accept other accessible execution directories using the same per-conversation CWD mechanism as an external runner. The initial release does not require creating a new runner registration for every repository.

For a new single-machine CLI conversation targeting the known same-host daemon's embedded runner, the client submits its invoking working directory explicitly unless the user selects another directory. Starting the daemon elsewhere must not change which repository a later `kodelet run` operates on. For external targets, an omitted CWD uses the runner default and explicit paths use runner-host semantics; the client must not assume that its own filesystem paths exist remotely. Resuming a conversation uses its validated stored affinity rather than replacing it with the current shell directory.

A central deployment can opt out of embedded execution and use standalone runners only. An explicitly selected runner always takes precedence over the default for a new conversation; an existing conversation retains its validated runner, environment profile, and CWD affinity. A default runner becoming unavailable must not silently move a conversation to another host or directory.

The exact configuration spelling is an implementation decision. This proposal does not reinterpret the existing control-plane-workspace flag as an implemented embedded-runner switch.

### Startup

1. Load trusted daemon configuration and initialize authentication and persistence.
2. Bind the control-plane listener and determine the actual local endpoint for the embedded runner. Do not dial a wildcard listen address.
3. Construct the embedded runner with explicit workspace configuration and daemon-managed registration credentials appropriate to the configured authentication mode.
4. Acquire its normal workspace lock, connect over loopback WebSocket, and register through the ordinary runner path.
5. Expose local execution as ready only after successful registration and environment readiness. Distinguish API availability from default-runner availability in status reporting.

The embedded connection must not disable authentication globally or bypass runner identity validation. If the workspace lock is held by a standalone runner, either explicitly reuse a compatible registered runner or report an actionable conflict; never steal the lock or create a duplicate owner. Final reuse policy is a workstream 2 decision.

### Shutdown and failure

Shutdown stops admission of new executions, cancels or boundedly drains active work according to the shutdown policy, closes runner resources while the control-plane connection is still usable, and then closes transport and persistence resources. Normal interactive foreground commands remain subject to their explicit cancellation behavior.

An embedded runner shares the daemon's process lifetime. A daemon crash can therefore lose both the agent loop and local runner. A standalone runner connection loss retains the existing lost-run handling. Neither case permits automatic replay of tools with uncertain side effects. Restart reconciles stale active state and reports interrupted or lost execution rather than pretending it completed or resumed transparently.

The initial release requires an explicitly started daemon, in the foreground or under a supported service manager. Client-side auto-start and a comprehensive cross-platform service installer are separate follow-up work; a clear connection error and documented start/stop/status workflow are required for cutover.

## Execution, configuration, and persistence contract

### Extend the existing API deliberately

Start from the existing chat execution and event-stream APIs. Add the fields and operations required by retained CLI and SDK behavior rather than introducing a parallel one-shot agent loop. A route rename or generalized execution endpoint is not a prerequisite; exact wire shapes belong to workstream 1.

| Concern | Target contract |
|---|---|
| Model and reasoning options | Explicit control-plane-validated request overrides or named model profiles, subject to server policy |
| Turn limits and weak-model selection | Control-plane execution options, including defined behavior for nested helpers |
| Tool and extension restrictions | Applied before environment discovery/execution; restrictions cannot widen server or runner policy |
| Tool-free execution | No model-callable host, extension, or provider-native tools; required internal lifecycle/administrative operations are not exposed to the model |
| Recipes and system prompt files | Resolve runner-owned resources on the selected runner; preserve recipe arguments and pinned-resource checks |
| Attachments | Read client files into supported attachment payloads; identify runner-local references explicitly and never reinterpret them on the server filesystem |
| Output formatting | Client-owned, including final-result-only output and structured formats retained by the CLI |
| Completion | Explicit terminal outcome and error information from which clients derive meaningful exit status |
| Follow/resume | Resolve against the daemon's conversation store, with explicit workspace/runner scoping rather than a client-local database |

Every existing execution flag and SDK option must receive a disposition: supported remotely, replaced by a named configuration mechanism, or explicitly removed/deprecated with a useful error. Silently accepting but ignoring an option is not acceptable.

### Configuration ownership and shell environment

The control plane resolves model profiles from trusted daemon configuration. The runner resolves environment policy, resources, and workspace configuration for the effective execution CWD. A repository cannot change the daemon endpoint, inject provider credentials into daemon configuration, or rewrite daemon authentication policy. Model profiles and runner environment profiles remain separate namespaces.

For embedding, inject runner-specific configuration rather than relying on the control plane's process-global Viper instance. Resolve each run against an immutable settings snapshot and define precedence among runner defaults, workspace settings, the selected environment profile, and permitted request restrictions. Existing conversations retain their documented persisted configuration behavior; reload must not silently replace pinned settings during a run.

A daemon or runner does not inherit changes from each invoking shell. Initial behavior uses the runner's configured process environment. Any supported per-run environment overrides must be explicit and applied to the affected execution resources, not through `os.Setenv` or `os.Chdir` on the shared daemon. Activating a virtual environment before invoking a thin client must not be assumed to modify an already-running runner. This behavior must be documented.

### Conversation persistence

Remove `--no-save` rather than porting it to the daemon. All user-facing conversations, including one-shot `run`, use daemon-owned persistence. Streaming and `--result-only` affect output, not saving. Do not introduce an equivalent transient CLI, SDK, or API mode, or silently accept the removed flag.

Internal utility model calls, such as extraction and compaction, remain separate implementation details; they need not create standalone conversations or append their temporary prompts to user history. This does not expose a replacement no-save execution mode to clients.

### Client attachment versus execution lifetime

A network disconnect detaches a client; it does not by itself cancel daemon-owned execution. Reattachment can recover persisted conversation state and follow live events, without promising replay of every transient UI update. Explicit stop requests cancel the targeted execution. For a foreground `run`, Ctrl+C attempts cancellation and exits with appropriate status; if cancellation cannot be acknowledged, the client must not claim that work stopped.

Interactive requests need deterministic ownership and bounded unavailable/dismissed behavior when their client disappears. Repeated cancellation should be safe, but a client must not blindly retry an execution submission whose acceptance is uncertain. Workstream 1 must define how the existing request/turn identity is used to reconcile that uncertainty.

## Runtime parity decisions

### Nested model helpers and subagents

Tool-free model extraction and workspace-capable subagents are different operations. The former needs central model execution and temporary input, not necessarily another workspace lease. The latter needs an explicitly authorized child execution with its own identity, runner context, and cancellation relationship.

An ordinary `--no-tools` request is not automatically a runnerless helper: it may still require runner-owned context, recipe resolution, or extension lifecycle processing. Only requests whose complete inputs are already available centrally and which require no workspace resources can omit the runner environment.

Replace local-provider helper paths such as `web_fetch` extraction with a control-plane operation. Where a runner requests a helper through its symmetric connection, validate the active run/tool ownership and permitted operation; possession of a runner registration credential must not imply unrestricted user API access. Standalone SDK clients use client authentication, while delegated child execution needs an explicit scoped authorization contract. Provider credentials remain centrally managed.

Do not solve this by giving the runner provider credentials or allowing nested helpers to fall back to local `kodelet run`. Avoid reentrancy deadlocks: child/helper execution must not wait for the parent to release the same conversation slot or a shared runner snapshot lock while the parent is waiting for its result.

SDK inline profiles must become permitted remote options rather than local configuration files that accidentally change endpoint selection. Inline extensions need a documented disposition: runner-deployable resources or an explicitly supported bridge with clear execution location and lifetime. Arbitrary client callbacks or local bridge paths cannot be assumed to exist on a remote runner. Unsupported SDK shapes must be identified and deprecated explicitly before cutover, not silently dropped.

### Extension lifetime and context

Use the existing run-isolated environment and background lease model as the starting point. Do not retain every extension indefinitely merely to mimic a local TUI process. Run state, background work, persistent conversation data, and client UI attachment must have separate ownership.

Supported extensions must either use explicit persistence for data needed by later submissions or receive a deliberately specified conversation-scoped lifetime. Workstream 4 must settle initialization-time background acquisition, lease limits, reattachment, and final cleanup. No extension may rely on client attachment alone to keep its runner resources alive.

Preserve agent/tool lifecycle ordering, tool policy, updates, and follow-up messages. Audit consumers of live thread metadata and expose only the concrete context reads or mutations they need; do not proxy an unrestricted thread object. Existing centrally routed conversation forking should remain a supported operation.

### Client UI and workspace services

Complete persistent UI routing end to end, not by advertising unsupported capabilities. Native TUI surfaces require input and resize paths as well as output frames; transcript entries, passive widgets, and shortcuts require discovery, ownership, and cleanup. The browser, ACP, and unattended clients may expose smaller capability sets, provided those sets behave consistently with embedded and external runners.

When multiple clients attach, only the selected capable client should own a blocking prompt or interactive surface. Passive observers can continue receiving conversation events. Disconnect, owner changes, background lease release, and run termination must not leave hanging requests or stale interactive resources. Persistent extension UI does not imply durability across daemon restarts.

Add runner-backed command discovery and CWD suggestions without starting a provider turn. Discovery must be keyed by the relevant runner, CWD, and environment profile; any run-time execution still validates against its pinned manifest. Extend workspace operations to distinguish the runner's startup workspace from a conversation's effective CWD. Terminal and Git diff should follow the selected conversation when requested, while preserving explicit runner-wide operations. Validate all paths and conversation affinity on the runner/control-plane boundary rather than the client filesystem.

## Delivery workstreams

Each workstream should land in small, independently testable changes. The table below identifies ownership and dependency, not a requirement to create seven large pull requests.

| Workstream | Main ownership | Dependencies | Exit criterion |
|---|---|---|---|
| 1. Execution/configuration contract | `pkg/chat`, `pkg/controlplane`, shared types | None | Ordinary, tool-free, and restricted requests have tested semantics; retained flags and SDK options have explicit dispositions. |
| 2. Embedded runner hosting | `cmd/kodelet/serve.go`, `pkg/controlplane`, `pkg/runner/client` | Configuration decisions from 1 | Embedded and standalone runners pass the same execution tests, including startup, concurrent CWDs, cancellation, and shutdown. |
| 3. Nested execution and SDK | `pkg/tools/web_fetch.go`, `pkg/controlplane`, `pkg/acp`, `sdk/src/agent.ts` | 1 | Supported extraction and subagent examples run without provider credentials on clients or standalone runners; authorization and cancellation are tested. |
| 4. Extension lifetime/context | `pkg/extensions`, `pkg/runner/client`, SDK extension APIs | Lifecycle/context decisions from 1 | Representative extensions work across submissions, concurrent conversations, background work, and cleanup with one documented lifetime contract. |
| 5. Client UI/discovery/workspace parity | Control-plane routes, runner protocol/service, TUI, ACP, Web UI | Relevant contracts from 1 and 4 | Supported features within each client behave the same with embedded and external runners. |
| 6. Thin-client conversion | `cmd/kodelet`, `pkg/chat/controlplane.go`, `pkg/acp`, SDK | 1; specific features from 3–5 | Supported commands use the daemon for execution and persistence, with no client-local provider or database dependency. |
| 7. Migration and cutover | CLI/server composition, persistence migration, docs, packaging | Agreed release criteria from 1–6 | Existing data is preserved, daemon absence is explicit, and direct-local execution bypasses are removed. |

### Workstream 1: contract before convenience

Implement the retained execution fields, validation, configuration snapshots, result/error semantics, and cancellation contract on the existing remote path. Keep the existing persisted-conversation model rather than adding transient executions. Add tests that distinguish omitted values from explicit restrictions. Reject unsupported combinations before provider or tool side effects begin. This workstream should not wait for embedding or a daemon installer.

### Workstream 2: compose existing runner components

Add optional embedded hosting, readiness reporting, authentication provisioning, workspace-lock handling, default selection, and bounded shutdown to `serve`. Extract/inject the necessary runner settings without broadly refactoring unrelated configuration. Test simultaneous requests for different CWDs and configuration profiles. Standalone runners must remain valid peers throughout.

### Workstream 3: remove hidden local model paths

First migrate tool-free extraction, replacing its local `kodelet run --no-save` subprocess with the internal control-plane helper operation before removing the CLI flag. Then migrate SDK/subagent launch and supported per-session options, including explicit authentication and parent/child relationships. Test with provider credentials available only to a separate control-plane process; an embedded-only test cannot establish that runner code is independent of shared provider credentials.

### Workstream 4: extension compatibility

Create a small set of representative extensions covering lifecycle hooks, background tasks, persistent data, follow-up messages, and conversation forking. Resolve lifetime/context gaps using those examples rather than inventing hypothetical generic state APIs. Record intentional compatibility changes in extension documentation and update affected examples.

### Workstream 5: two parallel deliverables

1. Complete native TUI extension UI and shortcut routing, including client capability and ownership handling.
2. Add runner-backed command/CWD discovery and conversation-directory terminal/Git diff targeting, then align ACP session directory behavior.

Do not make every client implement every renderer. Define the capability matrix and test both supported and unavailable outcomes.

### Workstream 6: convert clients in slices

| Slice | Scope |
|---|---|
| A. One-shot execution | `run`: stdin, images/attachments, recipes and arguments, streaming, final-only output, conversation persistence, exit status, and Ctrl+C |
| B. Conversation operations | List/show/delete/fork and retained export/filter operations, with daemon-owned follow/resume selection |
| C. Interactive clients | Make chat and ACP daemon clients; move embedded runner lifetime out of ACP/client processes and into `serve` |
| D. Remaining entry points | `commit`, `pr`, standalone steering, and provider/account commands whose current ownership assumes client-local state |

Share the existing control-plane client transport and add missing operations rather than building a new client framework. Conversation-store commands must work without any runner online. Git helper workspace reads and mutations must run on the selected runner; local CLI confirmation and output remain client responsibilities. SDK protocol bridging and explicitly supported inline-extension behavior retain their declared boundaries rather than becoming an accidental second runtime.

### Workstream 7: migration and removal

Establish endpoint discovery, client credentials, service operation instructions, and an actionable daemon-unavailable error. Preserve existing conversation IDs, content, metadata, and usage. For a same-machine upgrade, reuse the existing database where appropriate rather than requiring export/import; prevent incompatible old clients from continuing to mutate it directly after cutover.

Legacy conversations without runner affinity need an explicit adoption path that validates CWD and environment compatibility before binding. Do not infer that a matching path string on another host means the same workspace. Histories that cannot be safely adopted remain readable with guidance for selecting a compatible environment or starting a new conversation.

After release criteria pass, remove direct-local CLI/provider setup, local ACP execution ownership, control-plane-local workspace services, and implicit local environment fallbacks. Keep the provider implementations centrally and the tool/environment implementation within runners. Rename internal types only if it makes the boundary clearer; deleting `LocalEnvironment` is not itself a success criterion.

Remove the `--no-save` flag and its CLI configuration plumbing, migrate internal callers, and update tests, documentation, skills, and examples. Document that one-shot runs now always save conversations; scripts using the removed flag must fail explicitly rather than unknowingly saving content.

## Sequencing and first milestone

```diagram
╭───────────────────────────────╮
│ 1. Execution/config contract  │
╰───────────────┬───────────────╯
                │
       ╭────────┼─────────┬──────────┬─────────╮
       ▼        ▼         ▼          ▼         ▼
   2. Embed  3. Nested  4. Extension 5. UI /  6. Thin
   runner    model/SDK   lifetime   discovery clients
       │        │         │          │         │
       ╰────────┴─────────┴──────────┴─────────╯
                          ▼
              ╭──────────────────────╮
              │ 7. Migration/cutover │
              ╰──────────────────────╯
```

The diagram shows coarse parallelism, not the absence of feature-level dependencies. UI lifetime depends on extension ownership decisions, and individual client slices depend on the corresponding API support. Tests and documentation land with each slice, not only in workstream 7.

The first useful milestone is a daemon-backed `kodelet run` against an existing standalone runner, covering ordinary execution, conversation persistence, `--no-tools`, stdin, streaming/final output, and cancellation. Exact runner-selection syntax must be introduced and documented with that change; it is not available merely because this proposal mentions it. This milestone proves the one-shot contract before local convenience hosting. Embedded hosting can progress in parallel once configuration ownership is agreed.

During development, keep the new behavior opt-in until the release gates pass. Temporary local compatibility code must have a removal owner and gate; it must not become a permanent fallback when the daemon is unavailable. Remaining UI/discovery gaps may be deferred only as explicit product decisions with documented limitations, not mislabeled as full parity.

## Verification and release gates

Use the same focused acceptance scenarios with an embedded runner and with a standalone runner connected through the real WebSocket/JSON-RPC transport. Add separate client/runner processes with no provider credentials to expose dependencies that same-process embedding can hide. Existing protocol and provider tests remain useful and should be extended rather than replaced.

| Scenario | Required assertion |
|---|---|
| Ordinary CLI run | Tools operate on the selected runner; output, usage, persistence, and exit status are correct. |
| Removed `--no-save` flag | Rejected before execution; streaming and final-only runs still persist their conversations. |
| Tool-free/restricted run | Disallowed built-in, extension, and provider-native tools cannot execute; unrelated requests keep their own policy. |
| Multiple clients | Chat, CLI, and browser observe the same conversation; prompts and surfaces have one valid owner. |
| Client disconnect | Execution is not canceled merely by stream loss; reattachment restores the documented view. |
| Explicit cancellation | The intended run and its required child work stop boundedly; unrelated conversations continue. |
| Runner disconnect or uncertain submission | Outcome is reported/reconciled without blindly replaying side effects. |
| Concurrent CWDs/configurations | Context, resources, environment policy, and subprocess directories do not leak between conversations or into daemon configuration. |
| Nested helper/subagent | Supported nested execution succeeds with provider credentials only on the control plane and cannot exceed delegated authority. |
| Extension lifecycle | Two submissions, background work, failure cleanup, and unavailable UI follow the same contract on both runner placements. |
| Workspace discovery/panels | Commands and suggestions come from the selected runner; conversation-scoped terminal and diff use the correct CWD. |
| Daemon restart | Existing history remains valid; interrupted work is identified without claiming transparent execution recovery. |
| Legacy conversation adoption | IDs and history survive; affinity is bound only after validating the target workspace. |
| Conversation-only operation | Listing, reading, and deleting history require no runner and no client-side database access. |
| No daemon | Clients fail clearly and never create a local provider thread or execute workspace tools as fallback. |

For implementation changes, run focused Go tests for affected control-plane, runner, chat, ACP, and tool packages; SDK tests for session/extension changes; and frontend tests for changed browser flows. Run the repository's Go/frontend lint tasks as applicable and broaden integration coverage for cross-boundary changes. A source audit of provider-thread construction and local fallback sites complements behavioral tests but does not replace them.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Daemon startup configuration leaks into every repository | Explicit runner configuration loader, immutable per-run snapshots, concurrent multi-CWD tests |
| Loopback embedding hides a dependency on provider credentials or server-local files | Separate-process acceptance tests with different filesystems/environment configuration and no runner provider credentials |
| Retaining CLI flags creates a policy bypass | Typed overrides, explicit ownership, validation, and negative restriction tests |
| Disconnect behavior surprises scripts or leaves interactive work hanging | Explicit detach/cancel semantics, acknowledged outcomes, bounded prompt unavailability, documented exit behavior |
| Extension compatibility expands into indefinite runtime retention | Run-isolated default, explicit leases/persistence, representative examples, documented compatibility decisions |
| SDK inline execution quietly preserves a second runtime | Explicit disposition for inline profiles/extensions and no fallback when remote configuration is unsupported |
| Embedded runner failure takes down local execution with the daemon | Document shared lifetime; retain standalone deployment without changing client semantics |
| Migration binds history to the wrong host directory | Validated affinity adoption and read-only access when compatibility cannot be established |

The embedded runner is not a security boundary from the control plane: it shares the process, and the direct-host runner remains within the trusted host execution model. This design does not introduce token environment/argv scrubbing or claim that selectively hiding process credentials would isolate tools from their host.

## Decisions required before implementation

The architecture above establishes the ownership boundary, but it does not settle every compatibility and product choice. The questions below explain what remains open, why it affects implementation, and a recommended starting point. Recommendations remain proposals unless explicitly marked as accepted; acceptance records the design direction, not an implemented interface. Any illustrative configuration names or limit values are likewise provisional.

Not all nine questions must be resolved before any code can land. Resolve the execution, configuration, and authentication contracts before building features on them; resolve the remaining questions before the corresponding compatibility or cutover gate.

### 1. Which flags and SDK options remain usable?

**Question:** Which local flags and SDK options are preserved as request overrides, replaced by profiles, or explicitly deprecated?

**What is being decided:** Whether the daemon is simply a new execution location for familiar requests or also a substantial reduction in per-invocation configurability. For example, a user might reasonably expect to keep selecting a model or turn limit for one `kodelet run` without editing daemon configuration first. Conversely, a client-local configuration file may contain provider credentials, extension paths, and other values that cannot be copied wholesale to another machine.

There are three approaches: make all execution settings profile-only, accept a typed subset of per-request overrides, or forward the existing configuration object. Profile-only is simpler but breaks useful scripting and SDK patterns. Forwarding the full object preserves superficial compatibility while leaving ownership, path interpretation, and policy ambiguous. A typed subset requires an explicit inventory but preserves useful flexibility without those ambiguities.

**Recommended starting point:** Keep the useful existing controls, but classify them by owner and reject unsupported combinations before execution.

| Kind | Examples | Proposed treatment |
|---|---|---|
| Presentation | `--result-only`, retained output-format options | Keep entirely in the client. |
| Model/execution | Model, reasoning effort, turn/token limits, weak-model selection | Typed request fields validated against daemon policy; selecting a profile supplies defaults, not a prohibition on all overrides. |
| Per-run restrictions | `--no-tools`, `--no-extensions`, allowed-tool/command restrictions | Apply before discovery/execution and only within effective host policy. If a requested disablement conflicts with mandatory policy, reject it rather than silently ignoring it. |
| Workspace inputs | CWD, recipe name/arguments, system prompt reference | Resolve against the selected runner, with explicit client-content upload only where supported. |
| Persistence | `--no-save` | Remove the option; user-facing conversations always use daemon-owned persistence. |
| Credentials and installation | Provider account credentials, installed extensions, runner-local executable paths | Configure at the owning daemon/runner; a request may select an authorized named account or resource, not implicitly import the client's installation. |

Also distinguish request-only settings from conversation configuration. A turn limit need not become the next submission's default. Model/profile changes on a resumed conversation must follow an explicit supported transition, not silently rewrite the stored snapshot. Unsupported resume overrides should fail clearly; changing that contract is a separate compatibility decision.

**Owner and completion:** Workstream 1, consumed by 3 and 6. Produce a flag/SDK-option matrix with wire representation, owner, default, persistence behavior, policy checks, and error behavior for each retained option.

### 2. Which configuration wins, and when do changes take effect?

**Accepted direction:** Keep existing global plus conversation-CWD extension discovery, including plugins, with isolated runtimes and explicit background leases. Do not add runner-startup or parent-directory extension inheritance.

**Configuration work:** The runner currently uses startup-loaded settings plus an environment profile; per-CWD YAML loading is still new work. Resolve runner defaults → execution-CWD environment settings → selected environment profile → permitted request overrides, subject to host policy. Model credentials and model profiles remain daemon-owned. Finalize explicit override-file and isolated-config semantics during implementation.

Pin effective settings at `run.open`; workspace changes affect later runs, respecting persisted conversation settings. Initially require explicit reload or restart for daemon configuration, without file watchers. Scope resource caches by CWD, environment profile, and effective settings.

**Owner and completion:** Workstreams 1 and 2. Test concurrent directories, configuration changes between submissions, and unchanged extension-discovery roots.

### 3. How does the CLI find the daemon, and how is local execution configured?

**Question:** What embedded-runner configuration and endpoint discovery syntax should ship, and should a compatible existing runner be reused automatically or require explicit selection?

**What is being decided:** The normal single-machine experience should not require users to pass a server URL, runner ID, and workspace on every invocation. At the same time, an explicitly selected remote server must not be replaced with a local daemon when it is unavailable. The daemon also needs to know whether to host a runner, which startup workspace identifies it, and how to handle a workspace already served by another process.

Automatic reuse of a same-workspace runner is convenient, but that runner may have different startup configuration or environment policy. Always starting another one is not an option because of the workspace ownership lock. Requiring explicit reuse is less convenient but avoids silently substituting a differently configured execution environment.

**Recommended starting point:** Keep endpoint selection simple: explicit client server option, then the supported server environment variable, then user-level server configuration, then the documented local default. Repository configuration must not select the server. Never try a different endpoint after an explicitly selected one fails. Local client authentication should use the daemon's documented user credential mechanism, not its runner registration token.

Use a dedicated embedded-runner configuration section, for example a proposed `serve.embedded_runner` section containing enablement and canonical startup workspace. Do not overload the model profile or the old direct-local workspace flag to mean embedded hosting. A new conversation on the known same-host default uses the invoking directory; external targets use runner-host path rules. Endpoint discovery must not treat a loopback address alone as proof of a shared filesystem, since users may connect through forwarding.

For the initial release, prefer explicit reuse when a workspace is already owned: report the compatible existing runner and how to select it or disable embedding. Keep the control-plane API's availability distinct from embedded-runner readiness. Automatic adoption can follow once compatibility checks cover server, authenticated owner, host identity, canonical workspace, and effective runner configuration.

**Owner and completion:** Workstreams 2 and 7. Settle the configuration/CLI spelling, local credential discovery, default-target rules, and lock-conflict behavior, and test both ordinary startup and a conflicting standalone runner without interactive prompts in the daemon.

### 4. What authority does an embedded runner or child execution receive?

**Question:** How are embedded registration and delegated child execution authorized across token and enrollment deployments without treating runner credentials as unrestricted user credentials?

**What is being decided:** There are three separate identities: a client allowed to submit work, a runner allowed to register and service assigned work, and an extension/helper asking to create additional work on behalf of an existing execution. They should not become one unrestricted credential merely because all components can run on the same machine.

For example, `web_fetch` extraction needs one central model request associated with its active tool call. It does not need permission to delete conversations or administer other runners. A background subagent may need to continue beyond the parent tool response, but that does not imply authority to start arbitrary unrelated conversations forever.

**Recommended starting point:** Let the daemon provision the embedded runner's credential through the configured authentication mechanism and use normal WebSocket authentication and registration. Token mode can provision the appropriate runner credential; enrollment mode needs a daemon-managed approved runner identity represented in the authentication store. Neither should depend on disabling authentication or manually entering an enrollment code on every daemon restart. The exact owner representation in enrollment deployments must be settled explicitly.

Prefer run-scoped helper/child requests on the already-authenticated runner connection, checking run ID, tool ownership where applicable, permitted operation, and the parent execution's policy. If SDK child creation must use a client-facing endpoint, use an explicitly delegated credential with limited operations and lifetime rather than exposing a reusable administrative user credential. Child model/resource choices must stay within the authority delegated by the parent and the host policies.

Define expiry and cancellation with the actual child lifetime: ordinary child work belongs to its parent execution, while work intentionally retained by a background lease needs authority for that declared lifetime. A parent tool response returning is not automatically the end of all authorized background work. These are API authorization boundaries, not a claim that same-user host processes are sandboxed from each other.

**Owner and completion:** Workstreams 2 and 3. Specify registration provisioning and delegated-request validation, then test unauthorized run IDs, expired/stale authority, child cancellation, and attempts to exceed the parent's permitted operation set.

### 5. How long do extensions and their state live?

**Question:** Which extension state requires persistence or conversation-scoped lifetime, and how should initialization-time background work acquire that lifetime?

**What is being decided:** These are different requirements: caching data for one run, retaining a live worker between submissions, remembering user choices next week, and restoring work after a daemon crash. Keeping an extension process alive can address some of them but cannot provide crash persistence.

| Model | Benefit | Cost or limitation |
|---|---|---|
| Runner/daemon-lifetime extension process | Convenient shared caches and connections | Cross-conversation state contamination and implicit resource retention; still not durable across restart. |
| Conversation-lifetime process | In-memory state follows a conversation | Needs an idle/eviction policy and clear behavior when configuration changes or many conversations accumulate. |
| Run-isolated process plus explicit leases and persistent storage | Matches explicit work ownership and bounded cleanup | Extensions must declare background work and persist data needed after resource release. |

**Recommended starting point:** Retain run isolation plus explicit background leases. Use extension-owned persistent storage for data that must survive cleanup or restart; use leases for live work such as indexing or polling. Do not introduce a permanently resident process for every saved conversation. Add a conversation-scoped lifetime only if representative supported extensions demonstrate a need that leases and persistence cannot reasonably satisfy.

Initialization needs a specific rule. An extension may request background work during `session.start`, before `run.open` is complete. A possible compatible rule is to allow provisional leases only for an identified opening execution: activate them on successful open and release/cancel their resources if opening fails. Discovery-only manifest probes must not create indefinitely retained workers. The implementation must distinguish those contexts rather than unconditionally allowing background acquisition during every initialization.

Cleanup must cover normal completion, explicit cancellation, failed initialization, extension crashes, and daemon shutdown. Persisted extension data has a separate lifecycle from process cleanup.

**Owner and completion:** Workstream 4. Choose the lifetime model and initialization rule using a small extension compatibility suite, and specify lease limits, configuration-change behavior, cleanup, and storage expectations.

### 6. What happens to SDK inline extensions?

**Question:** Which SDK inline-extension forms are supportable without violating the declared execution boundary?

**What is being decided:** An installed extension module and a JavaScript callback closing over variables in a client's process are not interchangeable. An inline callback might read an application object, use a client-local database connection, or call another function that does not exist on the runner. Sending its function text would not reproduce that environment.

| Approach | What it preserves | Architectural consequence |
|---|---|---|
| Named or runner-local extension module | Runner-owned tools and lifecycle handlers | Requires the extension and dependencies to be available on the selected runner. |
| Client callback bridge | Arbitrary live client closures | The client becomes an execution dependency; disconnect and background lifetime require explicit rules, and client-hosted tools become a separate declared execution placement. |
| Transfer a packaged module | Some self-contained extension code | Introduces code/dependency transfer and provisioning work; it still does not serialize arbitrary closures. |

**Recommended starting point:** Support runner-installed modules and keep UI callbacks, such as responding to a selection prompt, in the client where they belong. Do not treat an arbitrary inline model-tool callback as a UI callback or silently execute it on the client while describing it as runner-owned. Port the supported examples to runner-hosted extensions and explicitly reject/deprecate unsupported inline tool forms during the migration.

If arbitrary callbacks are essential to the SDK's product contract, make that a conscious amendment to the ownership model: name the client execution placement, require the client to remain attached, and define callback cancellation and unavailable-client behavior. That is more than swapping a Unix socket for a network connection. Avoid a same-host-only exception that accidentally passes embedded tests but fails with external runners.

**Owner and completion:** Workstreams 3 and 6. Inventory documented inline extension use cases, decide the supported subset, and provide migration examples or a separately specified callback placement before removing local SDK execution.

### 7. Which parity gaps block cutover, and who owns interactive UI?

**Question:** Which UI/discovery gaps are release blockers, and what deterministic client ownership rule governs interactive extension requests?

**What is being decided:** There are two related choices: the compatibility promise made when local mode disappears, and which attached client receives interactive requests. A missing autocomplete list can have a manual workaround; a missing approval prompt or a terminal pointed at the wrong repository can prevent correct execution. A browser and a TUI viewing the same conversation must not both race to answer one prompt.

**Recommended starting point:** Treat execution correctness, prompt/approval handling, cancellation, and the existing documented native persistent extension UI as cutover blockers. Keep command execution available even if discovery is not complete. Command/CWD autocomplete and extension shortcuts may be deferred only with an explicit documented compatibility decision; do not advertise full local-feature parity in that release. Browser features that never existed locally in the browser need not be invented for this migration. Until a workspace panel can target the correct conversation CWD, report it as unavailable rather than showing another directory's terminal or diff as if it were the requested one.

For interactive ownership, prefer the capable client that submitted the run. Other attached clients are observers unless ownership is explicitly transferred. For API/background submissions with no capable owner, return the documented unavailable/dismissed outcome instead of silently making an arbitrary viewer responsible.

A takeover must be explicit and invalidate input from the previous owner. On disconnect, bound the wait for a pending prompt; after dismissal or expiration, a late reply must not revive it. A new client may take ownership of subsequent interactions, but reissuing an already dismissed prompt should require an explicit new request. Passive widgets may be mirrored to multiple capable clients without granting all viewers control of an interactive surface.

**Owner and completion:** Workstream 5, with release scope agreed in 7. Publish a per-client capability/release matrix and test TUI-plus-browser attachment, explicit takeover, stale responses, and loss of the owning client.

### 8. Remove `--no-save`

**Accepted decision:** Remove the option completely. User-facing conversations always use daemon-owned persistence; no transient execution API, result-retention cache, or expiration contract is required. Internal utility calls remain separate from user conversations.

**Owner and completion:** Workstreams 1, 3, 6, and 7. Migrate internal callers before removing the flag, update user-facing references, and test explicit rejection of the removed option.

### 9. How do existing local conversations become runner-backed?

**Question:** What explicit adoption workflow binds legacy local conversations to a compatible runner while preserving history?

**What is being decided:** Reusing the database preserves history but does not identify the machine and execution environment that should handle the next turn. A stored CWD such as `/home/user/project` is not proof that the same path on another host contains the same repository. A historical tool-output path is likewise not a portable artifact.

Automatic binding to the default runner is convenient but can continue a conversation in the wrong workspace. Refusing to continue every legacy conversation avoids that risk but makes the migration unnecessarily disruptive. Explicit adoption offers a middle ground and can support batch confirmation for a known same-machine upgrade.

**Recommended starting point:** Preserve conversation IDs, messages, metadata, configuration snapshots, and usage. Keep history readable independently of adoption. For the first continuation of an unbound legacy conversation:

1. Propose a target runner and environment profile using available same-host provenance, but do not treat matching path strings alone as sufficient proof.
2. Ask the selected runner to canonicalize and validate the intended CWD and environment-profile compatibility. Separately, have the control plane validate the stored model configuration against its available providers/accounts and daemon policy. Both checks must succeed; do not start a provider turn or editing tool to perform adoption.
3. Show the resolved runner, host, CWD, and any configuration incompatibilities for explicit confirmation. Offer a batch preview/confirmation path for a known same-machine migration rather than forcing every history to be exported and re-imported.
4. Persist the binding transactionally only after validation and confirmation. A failed adoption leaves history unchanged and readable.

If compatibility cannot be established, keep the history readable and offer a new or explicitly forked conversation in the chosen environment rather than silently rewriting the original configuration or affinity. Existing remote conversations retain their established bindings; adoption is not a general mechanism to bypass affinity checks.

Coordinate the database cutover with old-client compatibility: document backups and a minimum supported client/schema version, and do not support old direct-write clients concurrently mutating the store after migration. This is an operational compatibility rule, not an isolation boundary against processes running as the same user.

**Owner and completion:** Workstream 7, using the targeting/configuration contracts from 1 and 2. Test same-machine adoption, a same-path/different-host mismatch, unavailable directories/profiles, failed confirmation, and unchanged history on every failure path.

The most consequential choices are preserving typed per-request configuration, keeping runner configuration independent of daemon startup state, and deciding whether arbitrary SDK client callbacks remain part of the execution model. The remaining choices establish predictable lifecycle, compatibility, and migration behavior around that boundary. None requires a new provider loop or a second direct-local runtime.
