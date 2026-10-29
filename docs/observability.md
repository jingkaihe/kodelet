# Observability

Kodelet exports OpenTelemetry traces and opt-in metrics over OTLP/HTTP. A chat submission carries W3C trace context through the client, daemon, and runner so model calls, tool lifecycles, and remote execution appear in one trace. Metrics are recorded independently of trace sampling. Conversation history remains separate; this integration does not export OpenTelemetry logs.

## Enable push metrics

Set `metrics.enabled: true` in trusted process configuration (`~/.kodelet/config.yaml` or `KODELET_CONFIG_FILE`), or set `KODELET_METRICS_ENABLED=true`. Metrics default to disabled. `--metrics-enabled` overrides configuration for the receiving process only. Tracing can remain disabled.

```yaml
metrics:
  enabled: true
```

```bash
export KODELET_METRICS_ENABLED=true
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
# Optional authentication; never put credentials in repository configuration.
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer <collector-token>"
kodelet server restart
```

Configure and restart the daemon for both model and tool metrics. Runners report tool duration and outcome over their existing control-plane connection; they do not need metrics enabled, Collector credentials, or connectivity to the metrics backend. Embedded and standalone runners use the same reporting path. Enabling metrics only on `kodelet run` does not enable them in an existing daemon. Kodelet does not expose a Prometheus scrape endpoint, and metrics never start an extra HTTP listener.

The exporter sends HTTP Protobuf to `/v1/metrics` under `OTEL_EXPORTER_OTLP_ENDPOINT`. `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` overrides this with a complete URL; `OTEL_EXPORTER_OTLP_METRICS_HEADERS` overrides shared exporter headers. The default export interval is 60 seconds; set `OTEL_METRIC_EXPORT_INTERVAL` in milliseconds to change it. `OTEL_METRIC_EXPORT_TIMEOUT` controls the periodic reader's export timeout, also in milliseconds. Exports run in the background and normal process shutdown makes a final export within the shared five-second telemetry shutdown window. Delivery is best-effort: abrupt termination, collector outages, and expired shutdown deadlines can lose data. Metrics are not a billing ledger.

Metrics default to cumulative temporality, suitable for Prometheus. `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` can select `cumulative`, `delta`, or `lowmemory` when required by another backend; delta metrics require conversion before a conventional Prometheus scrape exporter.

### Exported metrics

| OTEL instrument | Kind / unit | Meaning |
| --- | --- | --- |
| `kodelet.model.calls` | Counter / calls | Logical model calls, including streaming and retries, by provider, model, and outcome |
| `kodelet.model.duration` | Histogram / seconds | Duration of those logical model calls |
| `kodelet.model.tokens` | Counter / tokens | Per-response usage increments by provider, model, and `token.type` |
| `kodelet.model.cost` | Counter / USD | Estimated cost increments using Kodelet's configured model pricing |
| `kodelet.tool.executions` | Counter / executions | Reported tool completions by tool name, outcome, and `kodelet.tool.nested` |
| `kodelet.tool.duration` | Histogram / seconds | Runner-measured duration of those tool lifecycles, excluding the control-plane round trip |

Token categories are disjoint: `input` excludes cache reads/writes, `output` includes reasoning tokens where billed as output, and `cache_read` / `cache_write` report cache usage. Usage is recorded where provider responses update accounting, not from cumulative conversation snapshots or parent aggregation of helper usage. Model call metrics count logical calls rather than individual HTTP retry attempts.

Tool metrics include ordinary calls and code-mode children. The control plane records completion summaries from tool responses (including reported execution errors) and code-mode child-end messages. A `code_execute` wrapper and its child calls are distinct executions, identified by `kodelet.tool.nested`; their durations overlap and must not be summed as elapsed run time. Runners, caller-side logical tool spans, and RPC spans do not increment these counters again. Outcomes are `success`, `error`, `cancelled`, `timeout`, and, for tools, `blocked`.

Completion reporting is best-effort. Duplicate child-end messages are counted once, and a child can report after parent cancellation while its run and runner connection remain valid. Reports lost after run teardown, RPC cancellation, or connection loss are not reconstructed as successful executions or estimated durations. Older runners that do not send completion summaries contribute no tool metrics; upgrade them to collect these measurements.

Metric attributes exclude prompts, content, file paths, conversation/tool-call IDs, and raw error text. Model/provider and tool names remain visible. Cardinality is capped at 2,000 attribute sets per instrument; excess combinations aggregate into the SDK's overflow series.

Model and tool metrics use the daemon's service resource and `host.name`, not the runner's identity. `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` can override resource attributes. For multiple daemons exporting the same metric series, configure a distinct `service.instance.id` per daemon (for example, `OTEL_RESOURCE_ATTRIBUTES=service.instance.id=build-host-daemon-1`) and preserve it through the Collector. Prometheus does not automatically use `host.name` as its `instance` label.

### Prometheus via a Collector

Use a Collector distribution containing the `prometheus` exporter, such as OpenTelemetry Collector Contrib. For processes on the same host, a minimal metrics-only Collector configuration is:

```yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: 127.0.0.1:4318
exporters:
  prometheus:
    endpoint: 127.0.0.1:8889
service:
  pipelines:
    metrics:
      receivers: [otlp]
      exporters: [prometheus]
```

Configure Prometheus to scrape the Collector, not Kodelet:

```yaml
scrape_configs:
  - job_name: kodelet
    honor_labels: true
    static_configs:
      - targets: ["127.0.0.1:8889"]
```

The Collector receives pushed OTLP metrics and exposes `/metrics` for scraping. Preserve the exported job/instance labels; metric names may be translated to Prometheus naming conventions. Adjust listener addresses and add TLS/authentication for remote daemons or container deployments. This example does not configure a traces pipeline.

### Directly to Prometheus

Alternatively, start a recent Prometheus with `--web.enable-otlp-receiver` and configure:

```bash
export KODELET_METRICS_ENABLED=true
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT="http://localhost:9090/api/v1/otlp/v1/metrics"
export OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=cumulative
```

Restart the daemon with these settings. This pushes directly to Prometheus without a Collector or scrape step. Use the metrics-specific endpoint so existing traces can continue to use their own destination.

## Enable tracing

Configure each participating process: the client, daemon, and any standalone runners. An embedded runner shares its daemon's tracing configuration. Enabling tracing only on `kodelet run` does not reconfigure an already-running daemon or standalone runner.

In the user configuration (`~/.kodelet/config.yaml`):

```yaml
tracing:
  enabled: true
  sampler: ratio
  ratio: 1.0
  capture_content: false
```

Or set environment variables before starting the processes:

```bash
export KODELET_TRACING_ENABLED=true
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
# If the collector requires authentication:
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer <collector-token>"

kodelet server restart
kodelet run "inspect this repo"
```

Restart the daemon and standalone runners after changing their configuration or environment. A local background server retains the environment it started with. A process flag such as `kodelet run --tracing-enabled "inspect this repo"` enables the client only; use configuration or environment variables for the other processes.

The exporter sends protobuf to the endpoint's `/v1/traces` path. Use `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` to specify the complete trace endpoint instead. Any compatible collector/backend can receive the data. Export is batched, and normal CLI shutdown drains queued spans before closing the exporter.

## Configuration

| Setting | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| `tracing.enabled` | `--tracing-enabled` | `KODELET_TRACING_ENABLED` | `false` |
| `tracing.sampler` | `--tracing-sampler` | `KODELET_TRACING_SAMPLER` | `ratio` |
| `tracing.ratio` | `--tracing-ratio` | `KODELET_TRACING_RATIO` | `1.0` |
| `tracing.capture_content` | `--tracing-capture-content` | `KODELET_TRACING_CAPTURE_CONTENT` | `false` |
| `tracing.internal_rpc_spans` | `--tracing-internal-rpc-spans` | `KODELET_TRACING_INTERNAL_RPC_SPANS` | `false` |

Samplers are `always`, `never`, or `ratio`. The ratio sampler uses parent-based sampling: downstream spans honor the incoming sampling decision, while new root traces use the configured ratio. Use it consistently across processes to avoid partial traces. A process with tracing disabled does not export its own spans.

## Trace structure

A one-shot invocation typically has this structure (initialization RPCs are omitted):

```text
cli.command
└─ POST /api/chat                         client
   └─ POST /api/chat                      daemon
      └─ invoke_agent kodelet
         ├─ chat <model>
         ├─ execute_tool file_read
         │  └─ runner.rpc tool.execute   client
         │     └─ runner.rpc tool.execute server
         │        └─ tools.run_tool.file_read
         └─ chat <model>
```

Interactive chat creates bounded traces per submission, not a single process-lifetime trace. Long-lived event subscriptions and WebSocket connections are not trace roots. Missing or invalid incoming context starts an independent server trace. Runner RPC context is carried on individual requests, including reverse calls, rather than on the shared connection; older peers can ignore the optional `traceContext` envelope field.

The daemon preserves trace parentage when it detaches execution from an HTTP observer's cancellation. The runner preserves request parentage while also honoring run cancellation. Logical tool spans include extension hooks and remote execution; lower-level runner spans show where the actual implementation ran.

Successful `lifecycle.dispatch` and `ui.extension.cleanup` RPCs do not create transport spans by default. They still execute normally and propagate trace context, but empty lifecycle hooks and background UI teardown no longer clutter the trace timeline or search results. Registered extension event handlers produce `extension <extension-id> <event>` spans only when invoked, with `kodelet.extension.id` and `kodelet.extension.event` attributes. Their model/helper calls inherit the handler span.

Failures remain visible: suppressed RPCs add a `runner.rpc.error` event to a recording caller span. When no recording span is available locally (for example, on the receiving process or during background cleanup), they emit a failure-only RPC span instead. Error classifications and RPC error codes are recorded by default; raw messages still require content capture. Handler failures are marked on their own spans without changing existing extension error-handling behavior.

To debug transport overhead, set `tracing.internal_rpc_spans: true` or `KODELET_TRACING_INTERNAL_RPC_SPANS=true` on the daemon and runner and restart them. This restores both client and server spans for lifecycle dispatch and UI cleanup. It is a process-local diagnostic setting, independent of content capture; other RPC spans, including tool execution, are unchanged.

Model calls use `chat <model>` client spans for Anthropic, OpenAI Chat Completions, and OpenAI Responses. Spans cover streaming and logical-call retries rather than individual tokens. Model calls finish before tool execution, making model and logical tool spans siblings under the agent invocation. Automatic provider-SDK retry attempts are not necessarily separate spans.

Common attributes include `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.response.id`, `gen_ai.conversation.id`, and per-call `gen_ai.usage.*`. Tool spans include `gen_ai.tool.name` and `gen_ai.tool.call.id`. HTTP submission spans carry `kodelet.turn.id` and, when available, `kodelet.run.id`. Existing invocation-level `tokens.*` and `cost.total` attributes are thread usage snapshots; use model-span `gen_ai.usage.*` for per-call aggregation rather than summing those snapshots.

## Content capture and privacy

By default, traces record operation metadata, usage, timing, and error classifications, not prompt bodies, message contents, tool arguments/results, or CLI flag values. Raw error/exception text is also excluded because provider and tool errors can echo content. Structured logs and saved conversations have separate policies; this setting is not a general log-redaction mechanism.

Set `tracing.capture_content: true` only when the destination is appropriate for sensitive data. This enables the existing Anthropic system/recent-message attributes, logical tool arguments/results, tool-specific attributes such as file contents and commands, CLI flag values other than the existing sensitive-name exclusions, and detailed model/tool/RPC exception text. It does not promise a complete transcript or add prompt/response capture to every provider.

Content capture is process-local policy: an incoming trace header or runner request cannot enable it. Enable it separately on the daemon for model/logical-tool payloads and on the runner for implementation-specific tool attributes. Keep it off in production unless access controls, retention, and payload size limits have been reviewed.

## Profiling

Go pprof endpoints are opt-in on a dedicated listener, separate from the server port, so they stay reachable when request handling is stuck and are never published through the server's proxy or ingress. Pass `--debug-addr 127.0.0.1:6060` to `kodelet serve` or `kodelet runner start`, or set `serve.debug_addr` in trusted configuration for the server. The listener is unauthenticated and accepts only loopback addresses unless `--debug-allow-remote` (`serve.debug_allow_remote`) is set. CPU, heap, allocation, goroutine, and thread profiles, execution traces, and symbol lookup are served; `cmdline` is disabled because flags can contain tokens. See [Health checks and profiling](MANUAL.md#health-checks-and-profiling) for usage.

Pull-based profilers such as Grafana Alloy's `pyroscope.scrape` can read these endpoints, which requires a non-loopback address restricted to the scraper. Block and mutex profiles stay empty because Kodelet does not enable their sampling. eBPF profilers such as `pyroscope.ebpf` need no listener, but sample on-CPU (and optionally off-CPU) time per OS thread, not memory, goroutines, or contention.
