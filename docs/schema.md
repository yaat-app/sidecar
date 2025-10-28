# YAAT Backend Event Schema (Agent ↔ Ingest API)

This document describes the contract enforced between the YAAT Sidecar agent and the Django ingest endpoint (`/services/v1/ingest`). All events emitted by the agent **must** conform to this schema.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `organization_id` | `String` | server-filled | Added by backend using API key. Not supplied by agent. |
| `service_name` | `String` | ✔ | Identifies the emitting service. Derived from config or detection. Must be non-empty. |
| `environment` | `String` | ✔ | Defaults to `production` if unset. |
| `event_id` | `String` (UUID) | ✔ | Unique identifier for deduplication. Agent generates when absent. |
| `timestamp` | ISO8601 string | ✔ | UTC timestamp of the event occurrence. If missing, agent injects `time.Now().UTC()`. |
| `received_at` | ISO8601 string | ✔ | Injected by agent immediately before transmission. Backend overwrites if needed. |
| `event_type` | Enum (`log`, `span`, `metric`) | ✔ | Event category. |
| `level` | Enum (`debug`, `info`, `warning`, `error`, `critical`, ``) | depends | Only meaningful for `log` events. Agent normalises to lowercase. |
| `message` | `String` | depends | Primary log message. Optional for spans/metrics. |
| `stacktrace` | `String` | Optional | Multiline stack trace captured from log tailers. |
| `trace_id` | `String` | Optional | Trace correlation identifier. Required for distributed tracing. |
| `span_id` | `String` | Optional | Span identifier. |
| `parent_span_id` | `String` | Optional | Parent span identifier. |
| `operation` | `String` | Optional | Human-readable operation (e.g., HTTP method + path). |
| `duration_ms` | `Float64` | Optional | Span duration. Defaults to `0.0`. |
| `status_code` | `UInt16` | Optional | HTTP status code or domain-specific numeric code. |
| `metric_name` | `String` | Optional | Metric identifier for `metric` events. |
| `metric_value` | `Float64` | Optional | Numeric metric value. |
| `tags` | `Map(String, String)` | ✔ (can be empty) | Key/value metadata. Agent ensures string conversion. |
| `statsd_type` | String (tag) | optional | When emitted via StatsD listener, `tags["statsd_type"]` records the original metric type (`c`, `g`, `ms`, etc.). |

## Agent Validation

Before sending a batch, the sidecar validates:

1. `service_name` is non-empty and trimmed.
2. `environment` defaults to `production` when blank.
3. `event_type` falls back to `log` if unspecified.
4. `timestamp` is RFC3339 (milliseconds precision acceptable); if absent or invalid, the agent replaces it with `time.Now().UTC()`.
5. `tags` are converted to `map[string]string` (non-string values stringified).

Any event failing validation is rejected with a descriptive error and logged; the batch is **not** sent.

## Backend Normalisation

If the agent misses a field, the backend currently fills defaults (see `services.api.ingest_events`). However, the agent aims to meet the full contract to reduce backend load and improve observability.

Future fields must follow additive schema evolution. The agent sends a `schema_version` tag once versioning is introduced so backend can adapt parsing logic.
