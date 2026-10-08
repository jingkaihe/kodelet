# Tool calls as code

## Status and decision

Implemented as an opt-in feature: runner-side QuickJS/WASM execution, async catalog discovery, shared direct/nested tool execution, machine results, parent-only renderers, explicit image/artifact emission, and `off`/`on`/`only` advertisement. Saved scripts and non-image artifacts remain deferred. This design combines short JavaScript orchestration snippets with an in-memory tool catalog exposed through `catalog.list`, `catalog.search`, and `catalog.describe`. It does not introduce a second implementation of any tool or a generated filesystem catalog.

Add an opt-in `code_execute` tool. Run a fresh JavaScript VM on the runner for each invocation. Execute every child through shared runner execution machinery, on the same pinned run as a direct tool call. Intermediate results are processed runner-locally; selected output, bounded progress, and final UI-only child details return to the control plane. In `on` mode keep ordinary tools directly available alongside code execution; in `only` mode advertise just `code_execute`, with core, extension, and MCP tools discovered through the authorized catalog.

The model writes ordinary JavaScript, passes plain objects, receives predictable JSON results, and explicitly chooses what to return. No imports, generated client classes, package installation, or persistent interpreter state are required.

Execution is runner-side; there is no temporary control-plane VM. Nested-call ownership uses acknowledged `tool.child.begin`/`tool.child.end` reverse RPCs, and the runner shares execution context setup between ordinary requests and script children. Both peers negotiate support before advertising the parent tool. The implementation and tests are in `pkg/codemode`, `pkg/tools/code_execute.go`, and `pkg/runner/{client,registry}/code_execution.go`.

## 1. Model-facing interface

The only new execution tool is:

```ts
code_execute({ code: string }): CodeExecutionResult
```

`code` is the body of an async JavaScript function, not a complete script or module. Kodelet supplies the function wrapper, so `await` and `return` work in the submitted body without relying on nonstandard top-level-return syntax. For example, the submitted body `return await catalog.list();` executes as the equivalent of:

```js
(async function () {
  return await catalog.list();
})()
```

The harness waits for this function's returned promise and emits its resolved value, or reports its rejection. Preserve submitted-source line numbers in errors by accounting for the wrapper. TypeScript declarations describe the API, but executable input is JavaScript, not TypeScript.

The runtime provides:

```ts
interface ToolReply<T = unknown> {
  data: T | null;
  text: string;
  attachments: ArtifactRef[];
  truncated: boolean;
}

declare const tools: {
  [registeredName: string]: (input: unknown) => Promise<ToolReply>;
};

interface CatalogPageOptions {
  group?: string;
  limit?: number;
  cursor?: string;
}

interface ToolPage {
  tools: ToolSummary[];
  nextCursor?: string;
}

declare const catalog: {
  list(options?: CatalogPageOptions): Promise<ToolPage>;
  search(query: string, options?: CatalogPageOptions): Promise<ToolPage>;
  describe(registeredName: string): Promise<ToolDescription>;
};

declare function emit(value: unknown): void;
declare namespace emit {
  function image(ref: string | ArtifactRef, options?: { detail?: "original" }): void;
  function artifact(ref: string | ArtifactRef): void;
}
```

Only `code_execute` is a new model-facing tool. `catalog.*` and `tools[name]` are host-backed APIs exposed inside its VM, not additional provider tool declarations or an `operation` variant of the execution tool. Kodelet can reuse the underlying catalog service for other interfaces without running JavaScript.

Names are the exact registered tool names, not lossy conversions to JavaScript identifiers. `tools.bash(...)` works for identifier-safe names; `tools["a-tool-name"](...)` always works. A catalog entry can group a tool by its recorded extension or MCP server without changing its identity. The runtime must not infer server identity by splitting a flattened tool name.

Example, assuming discovery has advertised a tool named `search_issues` with an `items` result:

```js
const replies = await Promise.all(
  ["repo-a", "repo-b"].map(repo =>
    tools.search_issues({ repo, state: "open" })
  )
);

return replies.flatMap(reply => {
  if (reply.data === null) throw new Error("Expected structured issue data");
  return reply.data.items.map(issue => ({
    id: issue.id,
    title: issue.title,
  }));
});
```

The model sees the selected IDs and titles, not both complete search results. `emit` appends an explicitly selected JSON output; a non-undefined return value appends the final JSON output. `console.log` is an alias for text emission, not a hidden diagnostic channel. Selected strings reach the model as plain text; other values reach it as compact JSON. All three share an output budget. Output is serialized immediately, rather than retaining mutable VM objects. Await values before emitting them: `emit(await catalog.list())`, not `emit(catalog.list())`. The serializer rejects unresolved promises rather than silently emitting `{}`; returning a promise is supported because the async wrapper waits for it before final serialization. `emit.image` and `emit.artifact` are distinct synchronous host-backed operations: arbitrary returned JSON is never interpreted as a media instruction.

A script with no explicit output still returns a short execution summary. This avoids an apparently empty tool response after successful side effects. Arbitrary expressions and intermediate tool replies are never automatically printed.

There is no implicit retry. A script is orchestration, not a transaction: completed writes remain completed if a later step fails.

## 2. Schemas and discovery

Separate three concepts:

1. **Registered:** present in the pinned environment manifest.
2. **Callable:** authorized for this invocation after execution options, active command restrictions, and `agent.init` filtering.
3. **Advertised:** included in the model's direct tool declarations.

Code mode changes advertisement, never authorization. Its callable catalog is the authorized set minus `code_execute` itself and any explicitly unsupported tools. Tools without a host-executable implementation, including provider-owned capabilities, are not automatically callable through JavaScript.

At invocation start, the control plane snapshots the effective callable set, including its thread-owned `agent.init` restriction, and supplies it as host-only execution metadata bound to the parent call and pinned manifest. The runner intersects that set with its own current policy; it must never broaden it. Discovery only lists the resulting set, and dispatch checks membership before every child call while the environment continues to enforce its own policies. An empty allowlist remains empty, rather than meaning unrestricted. Missing required authorization metadata is an error, not a fallback to the full manifest. Neither script input nor tool-input hooks can edit this host-owned authorization context.

Add optional output schemas to tool definitions, extension registration, SDK types, and runner manifests. The schema describes `ToolReply.data`, not display metadata or the entire envelope. Missing output schemas produce `unknown`, never fabricated return types. Declarations always retain the possibility of `data: null`, including policy-modified results.

Generate declarations from the same manifest records used by execution. Unsupported JSON Schema constructs degrade conservatively to `unknown`; the original JSON Schema remains available in `catalog.describe`. Descriptions are tool data, not trusted instructions.

The bootstrap description of `code_execute` includes the runtime API, output/error rules, limits, and a discovery example. Recorded tool groups are returned by `catalog.list` and `catalog.search` rather than injected as a second catalog into the bootstrap description. Typical discovery calls are:

```js
return await catalog.list({ limit: 20 });
```

```js
return await catalog.search("open pull requests", { group: "mcp/github" });
```

```js
return await catalog.describe("the_exact_name_returned_by_search");
```

The three discovery operations have distinct roles:

| Operation | Purpose | Result |
| --- | --- | --- |
| `list` | Browse without knowing names or search terms | A page of exact tool names, short descriptions, and recorded groups |
| `search` | Find tools by intent or name | A ranked page of the same compact summaries |
| `describe` | Learn how to call one tool | Exact name, full description, input schema, optional output schema, generated declaration, and any author-supplied examples or behavioral notes |

All catalog methods return promises so scripts can submit independent discovery requests together:

```js
const [pullRequests, checks] = await Promise.all([
  catalog.search("open pull requests", { group: "mcp/github", limit: 5 }),
  catalog.search("failed checks", { group: "mcp/github", limit: 5 }),
]);
return { pullRequests, checks };
```

Search and description can also share one execution without a separate batch API:

```js
const matches = await catalog.search("open pull requests", { limit: 3 });
return await Promise.all(matches.tools.map(tool => catalog.describe(tool.name)));
```

The promise contract supports concurrent outstanding requests; it does not promise parallel CPU execution or a latency improvement for short in-memory lookups. Initially process catalog work in small bounded batches on the host without dedicating a goroutine to every lookup. The same API permits a bounded worker implementation later if measurements justify it. Catalog requests do not consume child-tool execution slots, but they do consume bounded host-request capacity and the invocation's time/output budgets.

Use an optional exact `group` filter for both listing and search, based on recorded provenance. Listing uses stable name order, and search uses stable tie-breaking. Both are paginated with host-capped page sizes and explicit continuation cursors, rather than dumping every schema into context. Cursors are bound to the effective catalog, operation, normalized query, filters, and ranking version, not a VM instance, so the model can continue browsing in a subsequent `code_execute` invocation while that catalog remains unchanged. Reject stale cursors after catalog changes.

Implement search as deterministic, runner-local weighted BM25 over pre-tokenized authorized tool records. Index names/titles, recorded groups, descriptions, and bounded input-property names/descriptions; do not index serialized schema boilerplate or large enum lists. Normalize case and split identifier punctuation, underscores, and camel-case boundaries while retaining exact registered names. Give exact name matches first priority, then start with field weights of 5 for name/title, 2 for group, 2 for description, and 1 for input-property text, with `k1 = 1.2` and `b = 0.75`. These are proposed starting parameters to evaluate, not correctness requirements.

Precompute token/document statistics per effective catalog and initially score its matching records directly in Go; add an inverted index only if measured latency warrants it. Allow partial term matches, filter before returning candidates, and break score ties by registered name. Return only matching candidates, not unrelated padding, and do not present lexical scores as confidence probabilities. Start without embeddings, fuzzy matching, an external search service, or a hidden model call. Evaluate natural-language and synonym-heavy queries against actual tools; improve supplied metadata or add semantic retrieval only when demonstrated misses justify it. `list` remains the complete fallback when search cannot find a capability.

All three operations run locally against the same authorized manifest snapshot used by the child dispatcher. They do not require filesystem permissions, execute tools, register child calls, or contact the control plane. The model must explicitly return or emit resolved discovery results to see them; they are not automatically injected into context. Looking up an unauthorized name must not reveal its schema. The model learns returned schemas only after the execution response; calling `describe` does not teach it how to write the remainder of an already-submitted script.

Do not generate catalog directories, declaration files, or importable wrappers. `describe` renders declarations on demand, so files would duplicate the discovery API while adding paths, cache invalidation, and filesystem-access concerns. An export for human editor integration can be considered if a concrete need arises; it is not part of the planned agent workflow. Saved executable scripts are a separate reuse feature, not a discovery requirement.

Keep the initial advertisement configuration small:

| Mode | Direct model declarations | Script-callable tools |
| --- | --- | --- |
| `off` | Existing behavior | None |
| `on` | Existing tools plus `code_execute` | Authorized host tools |
| `only` | Only `code_execute` | Same authorized host tools, including core tools |

Keep `off` as the default. Any other value is rejected with a configuration error rather than silently changing semantics. Do not implement `only` by changing `AllowedTools`: the full permitted manifest remains the source for discovery, child authorization, and `agent.init` policy, while only the provider-facing declarations are reduced.

`only` means exactly one advertised tool when permitted, with no fallback to direct tools if `code_execute` is denied. Explicit no-tools requests still advertise no tools. An incompatible daemon/runner must produce a clear compatibility error rather than downgrading `only` to ordinary calls; `on` may retain its ordinary tools with an older daemon. Provider-native web search is suppressed in `only` because it is not callable through the runner catalog; use `on` when native search is needed. Both provider adapters and state-based advertisement must follow these rules.

## 3. Machine results and errors

Use one success envelope rather than returning an object for one tool, text for another, and a generated class for a third. The extra `.data` access is a small cost for predictability.

Add an optional canonical JSON `Data` field to `StructuredToolResult`, carried through its custom serialization and runner-side extension result hooks. Keep it runner-local: code-mode children receive it in memory, top-level results clear it before crossing the runner link, and conversation history never retains it. Keep presentation metadata separate. Expose this to extension authors as an optional `structuredContent` result alongside their existing text and presentation data; preserve MCP `structuredContent` and `outputSchema` through that contract. Structured content whose JSON exceeds 1 MiB, or that is not valid JSON, is omitted at ingress with a visible text notice and a truncated flag, so text plus data still fit a 2 MiB child reply. Do not rename the existing extension `data` field, which already carries presentation and other metadata.

Initially provide documented machine results for `bash`, file reading, and MCP tools that supply structured content. Other tools remain usable with `data: null`, their effective text, and attachments. Do not silently JSON-parse arbitrary text or parse CLI renderings to manufacture a result. A script can explicitly parse text when a tool's documented contract warrants it.

Construct `ToolReply` only from the effective post-policy result. In particular, never read the original `ToolExecution.Result` to recover data that a hook removed or replaced. For compatibility, v1 clears machine `Data` whenever a legacy result hook modifies a child result, before producing the final normalized result. This is deliberately conservative: a hook written to redact display output must not accidentally leave a new machine-readable copy accessible. Explicit machine-data replacement by upgraded hooks can be introduced later with an unambiguous protocol contract.

Code-mode parents and children fail closed when a `tool.call` or `tool.result` hook fails: a failed call hook prevents execution, and a failed result hook withholds the output without implying rollback. Ordinary direct calls retain their existing hook-failure behavior. Execution failure provenance is host-owned, separate from hook-editable metadata, so an RPC timeout cannot be relabeled as a completed operation by a display replacement.

Tool failures reject with a serializable `ToolError`:

```ts
interface ToolError extends Error {
  kind: "blocked" | "invalid_input" | "tool_error" |
        "transport" | "cancelled" | "limit" | "invalid_output";
  tool: string;
  callId: string;
  outcome: "not_started" | "completed" | "unknown";
  result?: ToolReply;
  toJSON(): object;
}
```

Only report `not_started` when the host knows dispatch did not begin. A returned tool error means the invocation completed, not that it had no side effects. A lost connection, timeout, or cancellation after dispatch can have an unknown outcome. Preserve useful sanitized error details, including in `Promise.allSettled` output, rather than serializing errors to `{}`.

A caught child error does not automatically fail the script, but it remains visible in the final child-call summary. An uncaught exception or unhandled rejection fails the parent; when several rejections are unhandled, the earliest is reported. Before formatting a failure, the bridge stops accepting host requests and output, because formatting can run guest code. The diagnostic is bounded and contains only the message, kind, tool, call ID, outcome, and a trimmed stack, never the child's reply, which remains available to scripts that catch the error. Invalid or oversized child output is an output error after execution; never rerun the tool to obtain a smaller result.

Failure output follows the same explicit-selection rule as successful output. For diagnostics, catch the error and emit `e.result?.text ?? e.message`, then rethrow if the parent should fail. The bootstrap description must advertise this optional effective reply so the model does not rerun a failed command merely to recover its output.

## 4. Execution ownership

```diagram
╭────────────────────────────────────────────────────────╮
│ Control plane                                          │
│ Agent loop, parent authorization, child registrations  │
│ Central services: model helpers, artifacts, forks      │
╰─────────────────────┬──────────────────────────────────╯
                      │ script + host-owned callable set
                      │ ↕ child ownership / service requests
                      ▼
╭────────────────────────────────────────────────────────╮
│ Runner                                                 │
│ code_execute → fresh JS VM                             │
│                     │ tools[name](input)               │
│                     ▼                                  │
│ Shared per-call context and execution lifecycle        │
│   → tool.call → validation / policy → actual tool      │
│   → tool.result → artifacts → effective reply          │
│                     │                                  │
│                     ▼                                  │
│ JS filters / joins / summarizes results locally        │
╰─────────────────────┬──────────────────────────────────╯
                      │ selected output + bounded summaries
                      ▼
             One parent result in the transcript
```

### Parent dispatch and shared runner execution

Register `code_execute` as a runner/environment tool in the pinned manifest. The control plane invokes it through the ordinary `base.ExecuteEnvironmentTool` path, augmented with the host-owned callable set. The parent definition participates in policy selection before `agent.init`; synthesizing it afterward would bypass that filtering. No VM or script child loop runs in the control plane.

Extract the context setup, run-operation tracking, effective-result normalization, and artifact handling around `Service.executeTool` into shared runner execution machinery. Both incoming direct tool requests and locally initiated script children use it. Keep `LocalEnvironment.ExecuteTool` as the owner of validation, environment policy, and extension hooks. The transport entry point serializes direct results for the wire; the nested entry point returns an effective local result to the VM without sending that body to the control plane.

Supply the code tool with a run-scoped host callback into that shared machinery. Do not expose the callback as script-configurable transport, call tool implementations directly, or introduce a separate MCP client. A child does not start another agent turn, reopen the run, or invoke the provider message/history loop.

The parent and every child each pass through `tool.call` and `tool.result` once. Preserve the surrounding responsibilities as well: extension context, browser leases, model-helper requests, artifact authority and ingestion, and conversation-fork attribution. Merely recursing into `LocalEnvironment.ExecuteTool` would preserve hooks but miss some of that setup.

### Child-call ownership protocol

The current control-plane registry establishes capabilities around a centrally dispatched tool request. Add a small reverse-RPC lifecycle so runner-initiated children can establish the same ownership without sending their execution or result through the control plane. Proposed method names and payloads are:

```ts
// Runner -> control plane; acknowledged before child execution begins.
tool.child.begin({ runId, parentToolCallId, toolCallId, name })

// Runner -> control plane; acknowledged after child work and cleanup finish.
// Contains ownership metadata only, never the child result body.
tool.child.end({ runId, parentToolCallId, toolCallId, name })
```

For the first version, register every dispatched child before running its hooks or implementation. The control plane validates the authenticated runner connection generation, live run, active `code_execute` parent, unique child ID, allowed tool name, and parent call budget. It retains the parent's resolved authorization and central service context for that invocation; these are not supplied by the script or trusted from a child registration request. Reject recursive code execution and children of children.

On accepted registration, reuse the existing per-tool capability rules for the actual child name. For example, a `web_fetch` child can receive its normal one-use model-helper grant even though its `code_execute` parent is not itself a `web_fetch` call. Artifact access and conversation-fork attribution are bound to the child's own ID and name. Do not grant a generic parent capability that permits arbitrary helper calls. Central service handles remain in the control plane; no provider credentials are sent to the VM.

The runner waits for registration acknowledgement before dispatch. It then installs the child's helper, artifact, fork, and browser context through the shared execution machinery. Final effective results stay local, while any required artifact uploads or central service requests use the existing authenticated channels. Send `tool.child.end` after those operations finish. Cleanup releases active call authority, not already persisted artifacts.

Registration is not a durable job or a replay mechanism. A failed or uncertain begin acknowledgement prevents local execution; any orphan registration is bounded by the parent lifetime. End cleanup is safe to repeat for the same owner, and parent cancellation/completion, run closure, or connection-generation loss revokes all remaining child registrations. Registry locks are never held while awaiting a reverse RPC or tool completion.

Track the parent-child relationship for ownership, summaries, and diagnostics, but do not conflate it with the child tool-call ID or an extension JSON-RPC `parentId`. The latter continues to identify its actual pending extension request.

### Data locality and trade-offs

Ordinary child arguments and results do not cross the runner connection. The first version still pays a small acknowledged registration exchange per child plus an end exchange; it is not a zero-round-trip design. This deliberately favors capability parity and simple cleanup over optimizing metadata traffic. Batched or lazy registration is not required for the initial implementation.

Model-helper requests, artifact transfers, conversation forks, and interactive UI operations still cross the connection when needed. Only the final selected output and bounded parent progress enter the normal tool-result stream. Existing wire-size limits continue to apply to those messages, but runner-local intermediate results are governed by their own host/VM budgets rather than the tool-result transport limit.

## 5. Runtime, limits, and lifecycle

The implementation uses QuickJS compiled to WebAssembly, embedded in the Go runner and hosted with wazero. The package pin, digest, provenance, and upstream licenses are recorded in `pkg/codemode/runtime_wasm_provenance.md`. Code generation installs the locked npm package and checksum-verifies its WASM before embedding it; the generated binary is gitignored. Node/npm are build-time generation dependencies, as for the frontend, but neither Node nor a C toolchain is required at runtime. The adapter provides async host calls, pending-job execution, hard context interruption, bounded memory, and no ambient filesystem/network/process access. It mounts no working directory and supplies no inherited environment or host streams to WASI. It does supply the host wall clock, monotonic clock, and a cryptographic random source, so `Date`, `performance.now()`, and `Math.random()` behave normally rather than returning wazero's fixed defaults.

A fresh VM is created per invocation; immutable compiled runtime code may be reused. One goroutine owns the VM. Child tool workers return JSON completions over a channel; they never enter the VM concurrently. No Node, Python, imports, `fetch`, environment access, timers, or persistent globals are exposed. This constrains the orchestration runtime, not the tools: an authorized `bash` call retains its existing host powers.

### In-process host bridge

JavaScript communicates with the Go runner through host functions exposed by the QuickJS/WASM adapter. This is an in-process bridge, not HTTP, stdin/stdout, a Node server, or direct MCP transport. Keep it distinct from the authenticated runner/control-plane protocol used for child ownership and central services.

The adapter provides an asynchronous request/completion mechanism for both `tools[name](input)` and `catalog.*`. The following illustrates the boundary; the implementation uses QuickJS host-function value marshalling and a private prelude settlement closure, rather than exposing these literal functions to user code:

```text
WASM → Go: submit(request_id, json_pointer, json_length) → admission_status
Go → WASM: settle(request_id, success, json_pointer, json_length)
```

A request identifies an operation and JSON arguments, for example:

```json
{"operation":"tool.call","name":"bash","input":{"command":"git status --short"}}
```

```json
{"operation":"catalog.search","query":"open pull requests","options":{"limit":5}}
```

The VM prelude allocates an invocation-local request ID, stores its promise resolvers, and submits the request. Request IDs correlate replies only; the host independently allocates actual child tool-call IDs and owns run identity, parent identity, permissions, and central capability context. Treat bridge input as untrusted: validate the operation, JSON shape, sizes, and request uniqueness. Direct access to a low-level bridge function must not bypass the same host checks.

Exchange JSON bytes through bounded WASM memory buffers using offsets and lengths, not shared Go/JavaScript objects or host pointers. The host copies submitted bytes before returning from the import so asynchronous work never retains borrowed guest memory. Only the VM owner allocates/writes guest response buffers, invokes settlement, and releases adapter allocations. Define buffer ownership and cleanup explicitly in the binding; do not pass VM values or memory views to tool workers.

### Request and completion lifecycle

1. A JavaScript API wrapper creates and returns a promise. Its host submission only copies, validates, and admits bounded work; it never waits for a tool, control-plane acknowledgement, or a full queue while inside the WASM import. Admission failure rejects the promise without leaking a pending resolver.
2. The host routes tool requests through authorization, the child-ownership protocol, and the shared runner executor. Catalog requests use the local catalog service without child registration. Both inherit the invocation deadline and cancellation.
3. Tool workers send owned, serialized completions through a bounded Go channel. Catalog work queues the same completion form. Neither path re-enters QuickJS from a worker or while the submit import is still on the VM stack.
4. The VM owner settles the matching promise exactly once. Tools resolve with the post-policy `ToolReply` or reject with `ToolError`; catalog methods resolve directly with their page/description or reject with a serializable discovery error. A catalog error has no invented child-call identity.
5. The owner runs pending JavaScript jobs so `await` continuations resume. Completions may arrive out of submission order and are matched by request ID. `Promise.all` remains the script's way to combine independent requests, not a host-side batch tool.
6. The parent response is serialized only after the async wrapper and accepted host work have settled according to the completion/cancellation rules below. Late completions after cancellation are discarded without accessing a closed VM; their host-side cleanup still runs.

The owner loop alternates bounded batches of JavaScript jobs, catalog work, and host completions, checking cancellation between batches. When no JavaScript is runnable it waits for completions or cancellation rather than busy-spinning. Reserve completion capacity when admitting requests, bound total pending requests, and keep producers cancellation-aware so saturation cannot deadlock cleanup. A CPU loop inside one job must also be interruptible: checking only between jobs is insufficient. The runtime spike must prove interruption during WASM execution as well as an endless microtask chain.

### Limits and termination

Host-enforced limits:

| Resource | Initial bound |
| --- | --- |
| Script wall time, including awaited tools | 120 seconds, also bounded by parent cancellation |
| VM memory | 256 MiB of linear memory; the QuickJS heap is limited to 192 MiB (3/4), leaving headroom for marshalling and the WASM stack |
| Submitted async function body | 128 KiB |
| Child calls per invocation, including queued calls | 128 |
| Simultaneously dispatched child calls | 8 |
| Catalog requests per invocation | 256 |
| Outstanding bridge requests, including queued and completed-but-unsettled work | 256 |
| Selected JSON output and media descriptors | 32 KiB, excluding separately stored image bytes |
| Explicit media emissions | 8 items, including repeated IDs |
| JSON payload admitted to the VM per child | 2 MiB |
| Final UI-only child details per invocation | 512 KiB aggregate; oversized details explicitly omitted |

Selected output also has a 1,024-entry bound, and queued request/response payloads have separate 18 MiB retained-byte budgets. The response budget holds one 2 MiB response per tool worker plus one reserved for the catalog worker. Tool and catalog workers reserve from separate pools, so completed-but-unconsumed tool responses can never starve discovery. Overflow is an explicit error, not an automatic spill/retry. Existing tool-owned artifact handling still applies.

These are host policy, not script-controlled escape hatches. Also bound host-side result queues, aggregate retained payloads, and script input size; VM memory limits alone do not bound Go allocations. Use normal artifact storage for retained overflow when allowed, and report truncation/overflow explicitly rather than silently dropping selected output. Existing tool-level truncation remains visible through the reply contract.

`Promise.all` may submit independent work concurrently; the bridge queues calls beyond active worker capacity up to its admission limits, then rejects excess submissions explicitly. Catalog work shares the bounded bridge but not the child-tool semaphore. Do not hold a parent-occupied execution semaphore while waiting for child slots. There is no automatic serialization of conflicting writes: dependent effects must be explicitly awaited by the script.

After the script body settles, reject new host submissions and settle all already accepted catalog requests and children before finalizing, within the invocation deadline. On failure or cancellation, stop new work, cancel outstanding requests, interrupt the VM, and perform bounded cleanup. Release pending resolvers/buffers and never deliver late results into a closed VM. Report unresolved tool outcomes as unknown rather than claiming external work was rolled back or definitely stopped. There is no detached/background script execution and no recursive `code_execute`.

Bind the VM and every child operation to both the parent invocation context and the existing runner run lease. On parent cancellation, runner disconnect, or lease expiry, stop admission and cancel active local work; the control plane independently revokes the corresponding registrations. Runner-local execution does not authorize continuing offline, reconnecting to resume a script, or replaying side effects. Keep run-operation accounting active through child cleanup so environment shutdown cannot race a still-running child.

## 6. TUI, Web UI, and streamed tools

Use one normal top-level tool card. Do not emit top-level `tool_use`, `tool_update`, or `tool_result` chat events for children.

For children, invoke the shared runner executor with no output-update sink. This suppresses transient output forwarding where supported; extensions may still generate internal updates that their adapter discards. Keep all final results locally, along with hook decisions, attachments, failures, and cancellation handling. This is the precise sense in which streaming output can be discarded safely. Child ownership begin/end messages are internal protocol traffic, not chat events.

The runner-side controller can emit its own bounded, accumulated progress snapshots under the parent's tool-call ID through the parent's normal update hooks and transport, for example `3/8 completed · 2 running · 1 failed`. Limit updates to roughly ten per second and avoid raw child arguments/output in progress metadata. Interactive permission or extension UI requests are not output snapshots and must continue through their existing channels.

Persist `CodeExecutionMetadata` containing execution status, duration, selected outputs, and child summaries: call ID, tool name, status, duration, and error kind. The final snapshot also retains bounded effective child inputs and structured display results for nested UI rendering, never in progress updates or the model-facing reply. Exclude duplicate machine data and unselected attachments; omit inputs when result hooks modify output to avoid bypassing display redaction. The aggregate detail budget is 512 KiB; calls beyond the budget retain their summaries with an explicit omission flag. A caught failure is still shown even when the parent succeeds.

Persist ordered typed `items`: `{type: "json", value}`, `{type: "image", artifactId, detail?}`, or `{type: "artifact", artifactId}`. The old untyped `outputs` field remains readable for existing histories but never acquires media semantics. Selected descriptors are deduplicated in the parent's attachments while repeated emissions keep their positions. Normal conversation save, reload, and fork behavior preserves these parent-owned references without retaining every intermediate child attachment.

Example expanded card:

```text
▾ Code execution                               Done · 1.4s
    ▸ Code                                     JavaScript and selected output
    ▸ Command                                  completed
    ▸ Edit file: src/example.ts                (+3 -1)
```

The TUI and Web UI nest a Code foldout containing JavaScript and its selected output, followed by child tool foldouts reusing ordinary tool renderers. Adjacent commands are grouped, and patch file rows appear directly under the parent without a separate Apply patch wrapper. TUI fold choices are local presentation state on the parent tool call; mouse hit regions identify each nested group or file, and `Ctrl+O` expands or collapses all details. Older histories without retained child details remain readable but cannot reconstruct those results. No new nested event protocol is needed; live results and reloaded history use the same persisted metadata. Only the parent's explicit output, a concise execution summary, and uncaught errors enter the model-facing result.

### Explicit image and artifact output

`emit.image(ref, options?)` selects image pixels for the model; `emit.artifact(ref)` retains an image artifact for the user/history without sending pixels. Both accept an exact artifact ID string or an attachment descriptor containing `artifactId`, returned by an effective child reply in this invocation. Descriptor fields supplied by JavaScript cannot override the host's stored attachment. Plain `return`, `emit(value)`, and console output stay JSON/text, even if the value is an exact ID or an object shaped like an image. The current artifact store is image-only; PDF, ZIP, and other generic files remain a separate extension of this contract.

```javascript
const image = await tools.view_image({path: "/tmp/chart.png"});
emit({caption: "Quarterly revenue"});
emit.image(image.attachments[0]);
// Instead use emit.artifact(image.attachments[0]) to retain it without pixels.
```

An existing conversation artifact must first enter the invocation through an authorized child, for example `tools.view_image({artifactId})`. Image emission additionally requires `view_image` in the host-owned callable set and current runner restrictions. The optional `detail: "original"` belongs to the emission and is validated against the active model, just as for direct `view_image`; omission uses the normal resized behavior. Paths, URLs, and inline base64 are not image references. Emission performs local validation only, not a hidden tool call; the producing child and parent retain their ordinary call/result hooks. Policies that control which generated images may be exposed can redact the child's attachments or the parent's explicit items.

The host bridge, not a JSON field chosen by the script, determines an item's type. Before appending it, the VM owner validates structure, output budgets, and membership in the inventory of post-hook, successfully ingested child attachments. These checks are synchronous and do not perform I/O or re-enter the VM from a worker. Partial effective error replies can contribute attachments; rejected or redacted replies cannot. Eight media emissions include duplicates, while image bytes are excluded from the 32 KiB selected-output budget. Earlier valid emissions survive a later script failure; this does not imply rollback.

After parent result hooks, intersect media items with the original authorized selections and retained attachment IDs. Hooks can remove emissions but cannot manufacture references, upgrade retention to pixels, change image detail, or duplicate an authorized emission. Reconstruct model content from this effective snapshot, never the pre-hook result. Streamed snapshots do not carry media, and update hooks cannot inject it.

Binary data stays outside QuickJS. MCP image content is converted by the SDK to a transient inline image attachment, then decoded and uploaded through the ordinary bounded runner artifact channel after child result hooks. Local-path image attachments use that same channel. Raw bytes/base64 are stripped from the effective reply; JavaScript receives only persisted descriptors. The control plane rechecks conversation ownership and materializes only explicitly selected image items using the normal image sizing/provider path. If one selected image cannot be materialized, only that image is replaced by a notice; the summary, other outputs, and other images remain, because child side effects have already happened. Provider adapters likewise say when a named image could not be delivered rather than silently dropping it. Text and image selections preserve emission order. Artifact-only items keep IDs and authenticated links, never pixels. The parent card distinguishes viewed images from retained artifacts, using the same metadata for live results and history.

## 7. Reusable scripts without a persistent interpreter

Do not add `store`, `load`, automatic global persistence, package management, or module imports in v1. The exact snippet is already in the parent tool input and conversation history.

After the inline path is stable, add a mutually exclusive `script_path` input for executing a saved JavaScript body. Resolve and read it on the runner using the same file-access policy and hooks as an ordinary file read; execute those bytes in a fresh runner-local VM. Record the resolved path and content digest, and preserve the executed source or an immutable source artifact, so later edits cannot change what history claims ran. Saving a reusable script remains an ordinary file-write tool call.

Reusable source files are optional and independent of tool discovery, which remains entirely through `catalog.list/search/describe`. They do not require a generated filesystem catalog, runtime wrappers, or ambient filesystem access inside JavaScript.

## 8. Implementation sequence and acceptance criteria

### A. Runtime and dispatch spike

Prove an embedded runner-side VM can wrap an async function body, await fake tools and catalog methods through the host bridge, resolve out-of-order requests correctly, interrupt both a CPU loop and an endless microtask chain, enforce memory limits, serialize errors, and close with pending calls. Extract the smallest shared runner execution path and prove a real local child can register ownership, execute through normal hooks, and return data only to the VM. Exercise at least one central capability using the child's own identity, and cancel the parent during execution. The spike must validate this runner-side design, not substitute a control-plane VM that avoids its lifecycle questions.

Treat runtime interruption and child-authority cleanup as release gates. If either requires substantially more work than expected, report that concrete blocker and narrow the initial feature scope explicitly; do not silently relocate the VM or bypass the capability checks.

### B. Code execution alongside direct calls

Implement the runner tool registration, host-owned callable-set propagation and enforcement, child ownership lifecycle, `catalog.list/search/describe`, uniform result/error envelope, basic structured outputs including MCP output schemas/content, and parent-only renderers. Keep current direct tools unchanged and use an opt-in configuration. Add optional manifest/result fields through the full Go/SDK/wire serialization path. Capability-gate the feature on both ends, including the child-registration protocol; do not silently expose it with an incompatible runner or fall back to central execution.

### C. Code-only advertisement

Use `only` mode once the catalog API is sufficient to discover and write correct calls without already knowing tool names or having filesystem access. Verify bounded listing/search, complete per-tool descriptions, usable pagination across fresh VM invocations, and that both core and extension tools remain callable despite being hidden from direct declarations. Suppress provider-native tools, check policy denial without fallback, and report incompatible peers clearly. No filesystem catalog is needed for this phase.

### D. Reuse and selective multimodal output

Add saved-script execution and, separately, generalize the image-only artifact store if non-image files are needed. Explicit image/artifact emission is implemented in the inline path. Measure child registration overhead before considering batching or lazy registration; runner-local orchestration is already the baseline.

Relevant implementation areas:

| Area | Change |
| --- | --- |
| New focused `pkg/codemode` package | Runner-hosted VM, async host bridge/job loop, catalog search/rendering, limits, injected child callback; no provider loop or separate tool implementations |
| `pkg/llm/base/tool_execution.go` | Ordinary parent dispatch plus host-owned effective callable-set metadata; no VM or child execution loop |
| `pkg/llm/base/helpers.go`, `turn_flow.go` | Callable versus advertised sets; parent participates in policy selection |
| `pkg/runner/client/service.go` | Shared direct/nested execution setup, runner-local child results, parent/child cancellation and cleanup |
| `pkg/runner/registry`, runner protocol | Parent-scoped authorization, child begin/end methods, per-child capability grants and generation-fenced cleanup |
| `pkg/agentenv`, tool registration | Runner-owned parent tool, output schema propagation, effective in-memory catalog, existing hook lifecycle |
| `pkg/types/tools`, extensions, SDK MCP bridge | Canonical machine data, serialization, output schemas, metadata registration |
| TUI renderers, frontend tool renderers | One code-execution card with persisted child summaries |

Required verification includes direct/nested permission parity; preservation of `agent.init` restrictions across the runner boundary; rejection of missing or widened child authorization; hooks exactly once; post-hook redaction reaching JS; modified legacy results losing machine data; helper/artifact/fork authority under the correct child identity; stale, duplicate, and out-of-parent child registration rejection; lost registration acknowledgements without local execution; cancellation, lease expiry, and runner disconnects without replay; bounded concurrent calls; output and queue limits; no child chat cards; and identical live/reloaded parent summaries. Exercise at least two provider adapters so parent dispatch is genuinely shared.

Bridge tests must cover async-body wrapping and source locations, concurrent tool/catalog submissions, out-of-order and duplicate completions, admission rejection without leaked promises, copied guest inputs, single-owner VM access, unhandled rejections, and cancellation with queued work and late completions. Verify that passing an unresolved promise to `emit` fails clearly and returning a promise waits for its value. Catalog-only execution must emit no child-ownership messages or child chat events.

Discovery tests must cover concurrent searches with `Promise.all`, batch descriptions, group filtering, listing/search pagination across fresh VMs, stale cursor rejection, denied-tool schema exclusion, and discovery with file-reading and shell tools disabled. Evaluate top-five search recall with actual tool names, task descriptions, synonyms, competing tools, absent capabilities, and forbidden matches. The catalog and execution authorization must agree without generated files or access to the workspace.

The first end-to-end acceptance case is: discover two data-producing tools, run them in parallel on the runner, join their results, return five selected fields, and inspect one persistent TUI/Web UI card. Verify that child details cross the connection only inside the final parent snapshot, never in progress or ownership messages, and that model-facing output contains only selected values. Verify bounded detail retention and policy redaction. The corresponding failure case blocks one child, catches its error in JavaScript, and still displays that blocked call without recovering a withheld input or result.
