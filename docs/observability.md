# Observability

Kodelet exports OpenTelemetry traces over OTLP/HTTP. A chat submission carries W3C trace context through the client, daemon, and runner so model calls, tool lifecycles, and remote execution appear in one trace. Conversation history remains separate from tracing; this integration does not export OpenTelemetry logs or metrics.

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

Samplers are `always`, `never`, or `ratio`. The ratio sampler uses parent-based sampling: downstream spans honor the incoming sampling decision, while new root traces use the configured ratio. Use it consistently across processes to avoid partial traces. A process with tracing disabled does not export its own spans.

## Trace structure

A one-shot invocation typically has this structure (initialization and lifecycle RPCs are omitted):

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

Model calls use `chat <model>` client spans for Anthropic, OpenAI Chat Completions, and OpenAI Responses. Spans cover streaming and logical-call retries rather than individual tokens. Model calls finish before tool execution, making model and logical tool spans siblings under the agent invocation. Automatic provider-SDK retry attempts are not necessarily separate spans.

Common attributes include `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.response.id`, `gen_ai.conversation.id`, and per-call `gen_ai.usage.*`. Tool spans include `gen_ai.tool.name` and `gen_ai.tool.call.id`. HTTP submission spans carry `kodelet.turn.id` and, when available, `kodelet.run.id`. Existing invocation-level `tokens.*` and `cost.total` attributes are thread usage snapshots; use model-span `gen_ai.usage.*` for per-call aggregation rather than summing those snapshots.

## Content capture and privacy

By default, traces record operation metadata, usage, timing, and error classifications, not prompt bodies, message contents, tool arguments/results, or CLI flag values. Raw error/exception text is also excluded because provider and tool errors can echo content. Structured logs and saved conversations have separate policies; this setting is not a general log-redaction mechanism.

Set `tracing.capture_content: true` only when the destination is appropriate for sensitive data. This enables the existing Anthropic system/recent-message attributes, logical tool arguments/results, tool-specific attributes such as file contents and commands, CLI flag values other than the existing sensitive-name exclusions, and detailed model/tool/RPC exception text. It does not promise a complete transcript or add prompt/response capture to every provider.

Content capture is process-local policy: an incoming trace header or runner request cannot enable it. Enable it separately on the daemon for model/logical-tool payloads and on the runner for implementation-specific tool attributes. Keep it off in production unless access controls, retention, and payload size limits have been reviewed.
