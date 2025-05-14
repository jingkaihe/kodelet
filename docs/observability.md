# Observability

Kodelet includes OpenTelemetry tracing to help with monitoring, debugging, and performance optimization.

## Enabling Tracing

Tracing can be enabled in three ways:

1. **Command Line Flag**:
   ```bash
   kodelet run --tracing-enabled=true "your query"
   ```

2. **Environment Variable**:
   ```bash
   export KODELET_TRACING_ENABLED=true
   ```

3. **Configuration File** (`config.yaml`):
   ```yaml
   tracing:
     enabled: true
     sampler: "ratio"
     ratio: 0.1
   ```

## Tracing Configuration Options

- `tracing.enabled` (boolean): Enable or disable tracing
- `tracing.sampler` (string): Sampling strategy: "always", "never", or "ratio"
- `tracing.ratio` (float): Sampling ratio when using "ratio" sampler (0.0-1.0)

## Tracing Backend

Tracing data is exported using the OTLP exporter, which can send data to any compatible backend such as Grafana Cloud, Jaeger, or OpenTelemetry Collector. Configure the backend using standard OpenTelemetry environment variables:

```bash
# Example for Grafana Cloud
export OTEL_EXPORTER_OTLP_ENDPOINT="https://otlp-gateway-<stack-name>.grafana.net/otlp"
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic <base64-encoded-username:apikey>"
```

## Traced Operations

Kodelet traces the following operations:

- CLI command executions
- LLM interactions
- Tool executions (bash, file operations, etc.)
- Rendering operations

This provides comprehensive visibility into Kodelet's performance and behavior.