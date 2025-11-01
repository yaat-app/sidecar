# YAAT Sidecar – Deep-Dive Overview

This document explains how the YAAT Sidecar agent works end-to-end: process discovery, configuration, data capture, transformation, buffering, delivery, health checks, background management, and uninstall safety. It also details the schema mapping the agent produces for the YAAT ingest API so you can reason about backend integration issues.

---

## 1. Runtime Modes & Entry Points

`cmd/main.go` drives every code path via flags:

| Flag(s) | Purpose |
|---------|---------|
| *(no flag)*, `--dashboard`, `--ui` | Launch Bubble Tea TUI (`internal/tui`) – default behaviour when run interactively with no arguments. |
| `--setup` | Interactive CLI wizard (`internal/setup`) that writes configuration, runs test events, and optionally daemonises. |
| `--daemon`, `-d`, `--start` | Spawns a detached background process via `internal/daemon.Start`. |
| `--stop`, `--restart`, `--status` | Manage existing daemon instance. |
| `--test` | Sends synthetic events to the ingest API using `forwarder.Test`, records results through `internal/state`. |
| `--validate`, `--init` | Config-only helpers. |
| `--update`, `--uninstall` | Self-update and cleanup flows. |

The CLI is intentionally structured so that **all user journeys begin with the TUI** (dashboard or setup), keeping the UX consistent across shells and operating systems.

---

## 2. Core Configuration Lifecycle

### 2.1 File Locations

`internal/config` defines loading order:

1. `YAAT_CONFIG_PATH` env var (explicit override)  
2. `yaat.yaml` in the current directory  
3. `~/.yaat/yaat.yaml`  
4. `~/.config/yaat/yaat.yaml`  
5. `/etc/yaat/yaat.yaml`

Files are saved with `0600` permission. Example scaffold: `yaat.yaml.example`.

### 2.2 Config Schema (`internal/config.Config`)

```yaml
api_key:        (string, optional - required for cloud mode)
organization_id:(string, optional - required for cloud mode)
service_name:   (string, required)
environment:    (string, defaults to production)
buffer_size:    (int, default 1000 events)
flush_interval: (duration string, default 10s)
api_endpoint:   (string, defaults to https://yaat.io/api/v1/ingest)
analytics:
  enabled:          (bool, default true)
  database_path:    (string, default /var/lib/yaat/analytics.db)
  retention_days:   (int, default 14)
  max_size_gb:      (float, default 2.0)
  batch_size:       (int, default 100)
  write_timeout:    (duration string, default 5s)
delivery:
  batch_size:     (int, default 500)
  compress:       (bool, default false unless explicitly enabled)
  max_batch_bytes:(int, optional soft limit, bytes)
metrics:
  enabled:        (bool, default false)
  interval:       (duration string, default 30s)
  tags:           (map, optional static metadata)
proxy:
  enabled:      (bool)
  listen_port:  (int, default 19000)
  upstream_url: (string)
logs:           (list of {path, format})
scrubbing:
  enabled:     (bool, defaults to true when rules selected)
  rules:       (list of {name, pattern, replacement, fields, drop})
```

`Config.LoadConfig` applies defaults, parses `flush_interval`, and surfaces helpful errors if required fields are missing.

### 2.3 Interactive Setup (`internal/setup`)

1. Loads existing config if available (values become defaults).  
2. Prompts for API key, service name, environment.  
3. Uses `internal/detection.DetectEnvironment` to list candidate log sources – classic files plus Docker/Kubernetes stdout streams and journald units (readable entries are pre-selected so the user can simply press Enter).  
4. Presents recommended scrubbing rules (regex-based redaction) so operators can toggle protections before persisting.  
5. Persists the file via `config.SaveConfig`, marks the location in `internal/state.RecordConfig`.  
6. Offers to send a connectivity test: generates a log, span, and metric event and attempts delivery through `forwarder.Test`. Results are saved in `state.RecordTestOutcome`.  
7. Optionally daemonises via `internal/daemon.Start`.

The TUI setup wizard (`internal/tui/setup.go`) mirrors the same behaviour with keyboard navigation and inline validation.

---

## 3. Process Management

### 3.1 Daemonisation (`internal/daemon`)

- **Start:** re-executes the sidecar binary with `--log-file` option; detaches via `Setsid` and writes PID files (`/var/run/yaat-sidecar.pid` or `~/.yaat/sidecar.pid`). Logs default to `/var/log/yaat/sidecar.log` with a user home fallback.
- **Stop:** reads PID file, sends SIGTERM, removes PID file.
- **IsRunning:** checks PID file existence and verifies the process is alive with signal `0`.
- **GetLogPath / GetExpectedLogPath:** utility functions for TUI status messages.
- **Uninstall:** comprehensive cleanup – stops processes, removes systemd units (Linux-only), deletes PID/log/config files, schedules binary removal if required, and reports warnings requiring sudo.

### 3.2 Uninstall Safety Improvements

The new uninstall flow:

- Calls `Stop` plus `pkill` as backup.
- Handles systemd units (user + system scope) on Linux.
- Cleans PID/log/config directories (`/var/lib/yaat`, `/var/log/yaat`, `/etc/yaat`).
- Warns when sudo is necessary (binary owned by root).
- Leaves a clear summary of any warnings so CI or operators can react.

---

## 4. Event Capture Pipeline

```
           ┌─────────────┐
           │  Log Files  │
           └──────┬──────┘
                  │ tail
                  ▼
            ┌──────────┐
            │ Tailers  │  (internal/logs/tailer.go)
            └────┬─────┘
                 │ parse
                 ▼
        ┌──────────────────┐
        │ Event Parsers     │ (internal/logs/parsers.go)
        └─────────┬────────┘
                  │ enqueue
                  ▼
           ┌────────────┐
           │ Buffer     │ (internal/buffer)
           └────┬───────┘
                │ flush timer / capacity
                ▼
         ┌──────────────────┐
         │  Dual-Write      │
         │                  │
         │  ┌────────────┐  │
         │  │ Analytics  │──┼──► DuckDB (local storage)
         │  │  Writer    │  │
         │  └────────────┘  │
         │                  │
         │  ┌────────────┐  │
         │  │ Forwarder  │──┼──► YAAT API (cloud, optional)
         │  └────────────┘  │
         └──────────────────┘
```

### 4.1 Tailers (`internal/logs/tailer.go`)

- Uses `github.com/hpcloud/tail` configured with `Follow`, `ReOpen`, `Poll`, and `Location` at EOF.  
- Maintains multi-line traceback capture for Django log format (tracks the most recent error event and appends stacktrace lines).  
- Issues parsed events to the shared in-memory buffer.

### 4.2 Parsers (`internal/logs/parsers.go`)

Per format:

| Format | Strategy | Output Highlights |
|--------|----------|-------------------|
| `django` | Regex `[timestamp] LEVEL [logger] message`; normalises log levels, attaches logger tag, captures stack traces. |
| `nginx` / `apache` | Regex for access logs; creates **span** events with synthetic `trace_id`/`span_id`, HTTP metadata in tags, and `status_code`. |
| `json` | Parses arbitrary JSON; extracts common fields (timestamp, level, message, stacktrace) and preserves all other keys as tags. |
| `docker` | Parses Docker/Kubernetes log envelopes (`{"log":..., "stream":...}`), trims newline, maps `stream` → level, and re-parses nested JSON messages so application tags survive. |
| default | Falls back to a generic INFO log event. |

All events are created as `buffer.Event` (`map[string]interface{}`) and include `service_name`, `environment`, `timestamp`.

### 4.3 Buffer (`internal/buffer/buffer.go`)

- Fixed-size queue set by `buffer_size`.  
- `Add` returns `true` when the buffer is full, signalling immediate flush.  
- `Flush` atomically returns all events and resets storage.

### 4.4 Periodic Flushing

`cmd/main.go` launches `periodicFlusher` goroutine with a ticker using `flush_interval`. Every tick:

1. Drains the disk-backed queue (`internal/queue`) first, attempting delivery for each persisted batch. Failed batches are parked in a dead-letter directory for manual replay.
2. Calls `buf.Flush()` and performs **dual-write**:
   - **Analytics Write (async, non-blocking):** Sends events to DuckDB via `internal/analytics.Writer`. Failures are logged but don't block cloud delivery.
   - **Cloud Forwarding (conditional):** If `api_key` is set, sends to YAAT API. If empty, runs in **local-only mode** (100% offline).
3. Runs retention cleanup using `delivery.queue_retention` and `delivery.dead_letter_retention`.
4. Logs successful/failed attempts and updates diagnostics.

On shutdown (SIGINT/SIGTERM) the process flushes any remaining events synchronously, enqueuing to disk if delivery fails.

### 4.5 DuckDB Local Analytics (`internal/analytics`)

**KILLER FEATURE:** Embedded columnar SQL database for local event storage and querying.

#### 4.5.1 Operational Modes

1. **Cloud + Local Mode (api_key set):**
   - Events written to both DuckDB and YAAT API
   - Query locally with SQL while cloud dashboard shows same data
   - Best of both worlds: offline analysis + cloud platform features

2. **Local-Only Mode (api_key empty):**
   - 100% offline operation, zero cloud dependency
   - Perfect for air-gapped environments, compliance requirements, or trial usage
   - Organization ID automatically set to "local"

#### 4.5.2 Architecture (`internal/analytics/`)

- **`schema.go`:** DuckDB table schema matching ClickHouse exactly (tags stored as JSON VARCHAR)
- **`writer.go`:** Async batch writer with buffered channel (queue size 1000)
  - Single-writer pattern (SetMaxOpenConns=1)
  - Non-blocking writes prevent analytics failures from impacting cloud delivery
  - Prepared statements for efficient batch inserts
- **`retention.go`:** Automatic cleanup enforcing `retention_days` and `max_size_gb`
  - Runs daily via ticker
  - Aggressive oldest-first deletion when size limits exceeded
  - Dead-letter batches for failed writes

#### 4.5.3 Schema Alignment

DuckDB schema replicates backend ClickHouse for query consistency:

| Field | Type | Notes |
|-------|------|-------|
| `organization_id` | VARCHAR | "local" in offline mode |
| `service_name`, `environment` | VARCHAR | From config |
| `event_id` | VARCHAR (PK) | UUID |
| `timestamp`, `received_at` | TIMESTAMP WITH TIME ZONE | RFC3339 |
| `event_type` | VARCHAR | log, span, metric |
| `level` | VARCHAR | debug → critical |
| `message`, `stacktrace`, `operation` | VARCHAR | Nullable |
| `trace_id`, `span_id`, `parent_span_id` | VARCHAR | Nullable |
| `duration_ms`, `metric_value` | DOUBLE | Nullable |
| `status_code` | USMALLINT | Nullable |
| `metric_name` | VARCHAR | Nullable |
| `tags` | VARCHAR (JSON) | map[string]string serialized |

**Why JSON VARCHAR for tags?** DuckDB Go driver cannot bind map[string]string to MAP columns in prepared statements. JSON serialization provides same query flexibility.

### 4.6 Host Metrics Collector (`internal/metrics`)

### 4.7 Host Metrics Collector (`internal/metrics`)

- Optional subsystem enabled via `metrics.enabled` that samples host-level telemetry (CPU, memory, disk, network) at `metrics.interval` (default 30s).
- On Linux the collector reads `/proc/stat`, `/proc/meminfo`, `statfs`, and `/proc/net/dev`; other platforms log a graceful warning and skip sampling.
- Raw counters are normalised into metric events (`event_type: metric`) like `host.cpu.usage_percent`, `host.memory.used_bytes`, `host.disk.usage_percent`, `host.net.rx_bytes_per_sec`, using the existing buffering/queue pipeline.
- Static tags from `metrics.tags` are appended to every metric event.
- The collector runs in its own goroutine, stops during shutdown, and shares the same diagnostics instrumentation (throughput, queue depth).
- `metrics.statsd.*` can reuse the same tag set, letting host + StatsD metrics share metadata.

### 4.8 StatsD/DogStatsD Listener (`internal/statsd`)

- Controlled by `metrics.statsd.*`; when enabled the agent listens on UDP (default `:8125`) and parses standard StatsD + DogStatsD tags.
- Metrics are normalised into metric events (respecting namespace, tags, sample rates) and inherit `metrics.tags` plus packet-level tags.
- Compatible with apps emitting StatsD counters, gauges, timers, histograms, and sets; counters honour sample rates by adjusting values before enqueue.
- Diagnostics/TUI show queue impact; `/metrics` reflects the combined throughput.

### 4.9 Journald Streaming (`internal/logs/journald`)

- Linux + cgo builds can set `format: "journald"` on a log entry to stream from systemd-journald; the optional `path` acts as `_SYSTEMD_UNIT` filter.
- Entries are mapped to YAAT log events with priority → level mapping and useful metadata exposed as tags (unit, transport, hostname, identifier, PID, etc.).
- Stubbed on other platforms (no-op with warning) so configuration remains portable.

### 4.10 Sensitive Data Scrubbing (`internal/scrubber`)

- Runs before buffering to ensure redaction policies are applied consistently across logs, metrics, spans, journald, StatsD, and proxy events.
- Rules support top-level fields (`message`, `stacktrace`, `operation`, etc.) and tag selectors (`tags.api_key`, `tags.*`).
- `replacement` defaults to `[REDACTED]` when unspecified; capture groups can preserve safe prefixes (e.g., keep `Bearer `, mask the token).
- `drop: true` rules short-circuit delivery, ideal for suppressing `/healthz` noise or accidental debug dumps.
- `config.RecommendedScrubRules()` seeds deployments with sensible defaults (Bearer tokens, emails, UUIDs) and the setup wizard toggles them interactively.

---

## 5. Event Delivery (`internal/forwarder`)

`Forwarder.Send` ensures minimum schema compliance and tunable delivery characteristics:

- Injects `event_id`, `timestamp`, `received_at`, and `event_type` (default `log`) if missing.  
- Splits large submissions into batches (`delivery.batch_size`) and enforces optional payload ceilings (`delivery.max_batch_bytes`).  
- Optionally gzips payloads (`delivery.compress`) before posting to `api_endpoint` with `Authorization: Bearer <api_key>`.  
- Implements exponential backoff (1s, 2s, 4s) for retryable errors (`429`, `5xx`, network issues); stops immediately on auth or client errors.

### 5.1 Test Mode

`Forwarder.Test(service, environment)`:

1. Builds one log, one span, and one metric event with distinctive tags (`yaat.sidecar=true`, `yaat.test=true`).  
2. Calls `Send`, measuring round-trip latency.  
3. Returns a `TestReport` (`Endpoint`, `Events`, `Latency`).  
4. Callers record the data into persisted state (`internal/state`).

### 5.2 Diagnostics & Metrics

- Diagnostics state (`internal/diag`) now tracks queue sizes, dead-letter volume, total successes/failures, and a rolling throughput (events/min over the last 60 seconds). The ticker and forwarder update these counters.
- The health server exposes `/metrics` alongside `/health`, returning Prometheus-format gauges:
  - `yaat_sidecar_queue_*` (in-memory, persisted, deadletter)
  - `yaat_sidecar_events_*_total`
  - `yaat_sidecar_throughput_per_min`
  - `yaat_sidecar_last_error{message="..."}` when applicable.
- The TUI delivery panel mirrors the same information, showing throughput and backlog, making local diagnostics available without scraping metrics.

### 5.3 Backend Schema Alignment

Each event is crafted to match the backend table you provided:

| Backend Field | Population Source |
|---------------|-------------------|
| `organization_id` | Resolved by backend using API key (not set by sidecar). |
| `service_name`, `environment` | From config. |
| `event_id` | `uuid.NewString()` (guaranteed). |
| `timestamp` | Original log timestamp or current time (UTC RFC3339). |
| `received_at` | Injected by `Send` with current time. |
| `event_type` | `"log"`, `"span"`, or `"metric"`. |
| `level` | Normalised (`debug` → `critical`). |
| `message`, `stacktrace` | From parsers. |
| `trace_id`, `span_id`, `parent_span_id`, `operation`, `duration_ms`, `status_code` | Set for Nginx/Apache span events and synthetic connectivity tests. |
| `metric_name`, `metric_value` | For JSON metrics or synthetic metric event. |
| `tags` | `map[string]string` of remaining metadata (logger, HTTP method, referer, user_agent, custom JSON keys, etc.). |

Backend processing should type-coerce map values accordingly; all non-string values are converted to strings when persisted in `state` for display.

---

## 6. Persistent UI State (`internal/state`)

Stores user-facing telemetry in `~/.yaat/state.json`:

```json
{
  "config_path": "/home/user/.yaat/yaat.yaml",
  "last_setup_at": "2025-03-27T09:12:34Z",
  "last_test": {
    "ran_at": "2025-03-27T09:13:05Z",
    "success": true,
    "endpoint": "https://yaat.io/api/v1/ingest",
    "latency_ms": 180,
    "events": [
      {"event_type": "log", "message": "YAAT Sidecar connectivity test", ...},
      {"event_type": "span", "operation": "GET /yaat/healthz", ...},
      {"event_type": "metric", "metric_name": "yaat.sidecar.test.latency_ms", ...}
    ]
  }
}
```

`RecordConfig` and `RecordTestOutcome` updates are triggered by setup wizards, CLI tests, and TUI tests. The dashboard renders this information to give immediate feedback about the most recent delivery attempt.

---

## 7. User Interfaces

### 7.1 Terminal UI (`internal/tui`)

Built with Bubble Tea + Lipgloss:

- **Dashboard view:** shows service/daemon status, uptime, config location, last connectivity test (success/failure, latency, event count), and log source health.  
- **Config view (`c`):** redacts API key, shows proxy summary, log file count; pressing `Enter` opens the inline config editor (API key, batching, metrics, scrubbing, log sources) without leaving the TUI.  
- **Events view (`e`):** renders the last recorded test events with timestamps, levels, tags, truncated stack traces.  
- **Test view (`t`):** runs `runTests()` → config presence, API key, log readability, live connectivity check. Results saved to state.  
- **Setup view (`s`):** embedded wizard that writes config and records state identical to CLI setup.  
- **Shortcuts:** displayed at the bottom (`s`, `c`, `e`, `t`, `q`).

### 7.2 CLI Wizard (`internal/setup`)

Text-based wizard for minimal environments; uses `bufio.Reader` prompts, deduplicates log file entries, ensures values are valid, and mirrors state recording of the TUI setup.

---

## 8. HTTP Proxy (`internal/proxy`)

Not deeply changed in this iteration, but for completeness:

- Optional reverse proxy capturing HTTP traffic.  
- Injects spans for each proxied request with timing and status code.  
- Emits events to the buffer identical to log tailers.  
- Provides a `health` endpoint when enabled via `--health-port`.

---

## 9. Observability & Health

- **Health server:** `internal/health.New(port, version, serviceName)` – simple HTTP endpoint reporting status.  
- **Logging:** uses Go’s stdlib logger; verbosity controlled by `--verbose`. Logging destination can be redirected via `--log-file`.  
- **Stateful history:** TUI surfaces last known config/test details even if API is unreachable at runtime.

---

## 10. Installers & Service Packaging

- **`install.sh`** – Linux-only bootstrapper that installs the binary, sets up config/log/state directories, and optionally registers systemd service. Can run non-interactively via `YAAT_NONINTERACTIVE=1`.
- **Linux (systemd):** creates a `yaat` system user, provisions `/etc/yaat`, `/var/lib/yaat`, `/var/log/yaat`, installs `/etc/systemd/system/yaat-sidecar.service`, and offers to enable it. Operators run `sudo -u yaat yaat-sidecar --setup --config /etc/yaat/yaat.yaml` before starting the service.
- **Service hardening:** systemd unit enables `NoNewPrivileges`, `ProtectSystem=strict`, and binds writable paths to the minimal directories the agent needs.
- **Platform focus:** Production deployments target Linux servers (amd64 + arm64). For development/testing on other platforms, build from source with `go build ./cmd`.

## 11. Testing & Local Execution Checklist

1. `go mod tidy` – ensures dependencies are downloaded (configure `GOPROXY` if needed).  
2. `go build -o yaat-sidecar ./cmd` – rebuilds binary.  
3. `go test ./...` – runs buffer/log parser tests.  
4. `./yaat-sidecar --setup` or `--dashboard` – produce config and verify state.  
5. `./yaat-sidecar --test` – confirm events reach the backend; inspect logs or TUI for results.  
6. `./yaat-sidecar --start` / `--status` – background operation.  
7. `./yaat-sidecar --uninstall` – validate cleanup; may require sudo if installed system-wide.

---

## 12. Known Integration Touchpoints

- **API key scope:** backend associates `organization_id` via auth; make sure each test key is mapped correctly.  
- **Enum alignment:** ensure backend enum values match strings emitted by parsers (`log`, `span`, `metric`, `debug`, `info`, `warning`, `error`, `critical`).  
- **Tag persistence:** backend must flatten `tags` (map) to your storage (`Map(String, String)` in ClickHouse). Every parser guarantees string values.  
- **Timestamp precision:** events use RFC3339/RFC3339Nano; ClickHouse `DateTime64(3)` will truncate to milliseconds.  
- **Proxy metrics:** requests produce span events even without log tailing configs, so backend should accept span-type events under the same table.

---

## 13. Quick Reference to Key Packages

| Package | Responsibility |
|---------|----------------|
| `cmd/main.go` | CLI entry, flag parsing, lifecycle wiring, panic recovery. |
| `internal/config` | Config file parsing, defaults, validation, sample config generation. |
| `internal/setup` | CLI wizard for initial setup & connectivity test. |
| `internal/tui` | Bubble Tea dashboard + setup/test sub-views. |
| `internal/logs` | Tailers, format detectors, parsers, stacktrace handling. |
| `internal/buffer` | Concurrent event buffering. |
| `internal/forwarder` | HTTP client with retries, synthetic test events. |
| `internal/proxy` | Optional HTTP proxy instrumentation. |
| `internal/health` | Health check HTTP server. |
| `internal/daemon` | Background service control + uninstall logic. |
| `internal/selfupdate` | Release checks and binary replacement (not modified here). |
| `internal/state` | Persistent metadata for TUI/CLI insight. |
| `internal/analytics` | DuckDB local storage, schema, writer, retention cleanup. |

---

**Tip:** If backend ingestion still fails, start by running `./yaat-sidecar --test --verbose` and inspect the log output (`~/.yaat/sidecar.log` or `/var/log/yaat/sidecar.log`). Compare the emitted JSON payloads with the ClickHouse schema to spot mismatches quickly. The TUI “Events” view mirrors the same data for fast copy/paste during debugging.
