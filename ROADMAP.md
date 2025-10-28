# YAAT Sidecar Modernization Roadmap

This roadmap converts the high-level vision into actionable engineering work. It is structured as sequential phases with clear deliverables, acceptance criteria, test strategy, and backend coordination tasks. The goal is to reach feature parity with commercial observability agents (Datadog-style) while keeping the TUI first-class.

---

## Phase 0 – Stabilize the Current Agent

### Objectives
- Eliminate silent data loss, tighten the ingest contract, and provide minimum operational visibility.

### Workstreams
1. **Ingestion Contract & Validation**
   - Document full schema contract (agent ↔ backend) in `docs/schema.md`.
   - Implement strict validation in `internal/forwarder` before send and in Django ingest API with descriptive errors.
   - Add integration test harness (Go) posting sample payloads to a local mock of `/v1/ingest`.

2. **Agent Diagnostics**
   - Extend `internal/health` to report queue length, last error, up-time.
   - Surface identical info in TUI dashboard and CLI command (`yaat-sidecar --diagnose`).
   - Add optional verbose logging toggle in TUI.

3. **Test Coverage**
   - Unit: `forwarder.Test`, `daemon` start/stop/uninstall, `state` persistence, `detection`.
   - Integration: run `go test ./integration/...` against mock ingest server.

### Acceptance Criteria
- CI (GitHub Actions) running `go test ./...` + integration suite.
- Health endpoint returns non-200 on delivery failures.
- Backend rejects malformed events with actionable 4xx response.

---

## Phase 1 – Reliable Data Pipeline

### Objectives
- Move from best-effort delivery to durable, observable batching.

### Workstreams
1. **Persistent Queue**
   - Introduce disk-backed queue (Badger/LevelDB or custom WAL) with configurable retention and max size.
   - Retry with jittered exponential backoff, configurable max attempts, DLQ directory for manual replays.

2. **Batching & Compression**
   - Configurable batch size, gzip compression toggle, request timeout tuning.
   - Track metrics: events queued, flushed, failed, throughput.

3. **Observability**
   - Emit self-metrics (Prometheus endpoint or internal stats).
   - TUI: histogram and sparkline for queue depth & throughput.

### Acceptance Criteria
- Cold reboot or crash does not lose queued events.
- TUI shows queue depth, failure counts, and last delivery status.
- Backend sees reasonable batch sizes (<5 MB compressed) with accurate `received_at`.

---

## Phase 2 – Telemetry Breadth

### Objectives
- Capture logs, metrics, and traces at parity with common observability stacks.

### Workstreams
1. **Log Intake**
   - Autodetect JSON/logfmt, journald support, Windows Event Log reader, docker stdout tailer.
   - Regex-based redaction/scrubbing rules.

2. **Host Metrics**
   - Cross-platform collectors for CPU, memory, disk, network, process stats.
   - Configurable sampling interval and metric filters.

3. **Metrics API**
   - StatsD/dogstatsd listener with counters, gauges, sets, histograms.
   - Aggregation window, flush cadence, and tagging support.

4. **Tracing**
   - OpenTelemetry receiver (OTLP/HTTP + gRPC).
   - Span sampling policies, propagation helpers (W3C, B3).

### Acceptance Criteria
- Agent publishes host metrics and StatsD events to YAAT backend with accurate tags.
- Traces ingested via OTLP appear in backend with correct relationships.
- Scrubbing rules configurable in TUI/CLI and applied before send.

---

## Phase 3 – Platform & Packaging

### Objectives
- Make deployment frictionless across OSes, containers, and clusters.

### Workstreams
1. **OS Packaging**
   - Windows service support, systemd unit, launchd plist automation.
   - Signed installers/binaries.

2. **Containers & Kubernetes**
   - Build & publish Docker image (multi-arch).
   - Helm chart with autodiscovery (annotations/labels).
   - Sidecar/instrumentation examples for popular stacks (Nginx, Django, Node).

3. **Autodiscovery Framework**
   - Config template loader keyed by service type.
   - Live reload when config files change.

4. **Remote Config & Fleet Telemetry**
   - Backend endpoints for agent status, version, queue metrics.
   - Agent polls for remote config overrides (with signatures).

### Acceptance Criteria
- One-command install for Linux/macOS, MSI for Windows.
- Helm chart installs agent with autodiscovery; metrics/logs flow within 5 minutes.
- Fleet dashboard in backend shows agent health.

---

## Phase 4 – Security & Compliance

### Objectives
- Enterprise-grade credential handling, encryption, and auditability.

### Workstreams
1. **Credential Security**
   - Use OS keychain or local encrypted vault for API keys.
   - Implement key rotation wizard and remote revocation flow.

2. **Transport Security**
   - Configurable TLS (CA bundle, pinning, mTLS).
   - Optional proxy support for outbound HTTP.

3. **Data Governance**
   - PII scrubbing DSL, field masks, sampling policies.
   - Audit log for agent actions (setup, config changes, remote commands).

4. **Signed Releases & Verification**
   - Sign binaries and updates; agent verifies signatures before self-update.

### Acceptance Criteria
- Secrets never stored in plaintext.
- Compliance checklist for SOC2-ready deployment.
- Audit log accessible via TUI/CLI and exported to backend.

---

## Phase 5 – UX & TUI-First Operations

### Objectives
- Ensure the entire lifecycle is manageable via TUI with CLI parity.

### Workstreams
1. **End-to-End TUI Control**
   - Setup, config editing, log source management, queue inspection, diagnostics, upgrade, uninstall all accessible in TUI.
   - Contextual help overlays, keyboard shortcuts, search.

2. **Guided Onboarding**
   - Fetch organization/service lists from backend during setup.
   - Suggest integrations and log format templates automatically.

3. **Fleet-aware TUI**
   - View all services and agent health as reported by backend.
   - Trigger remote diagnostics/commands from TUI (with confirmation).

4. **Headless Automation**
   - Preserve CLI for scripting with JSON outputs matching TUI data.
   - Provide Terraform/Ansible modules for automation.

### Acceptance Criteria
- A new user can install, configure, verify ingestion, and troubleshoot solely via TUI.
- CLI commands mirror TUI functionality for CI/CD purposes.

---

## Phase 6 – Backend Evolution (Services App)

### Objectives
- Enhance Django services API to support advanced agent capabilities.

### Workstreams
1. **Ingest Enhancements**
   - Rate limiting, quota management, fanned-out pipelines, 429 with retry hints.
   - Store raw payloads for replay/debugging (short retention).

2. **Agent Telemetry Endpoints**
   - `POST /agents/status` for queue metrics, versions, errors.
   - `GET /agents/config` for remote overrides (signed).

3. **Dashboard Expansion**
   - New widgets for host metrics, StatsD, OTEL spans.
   - Filters for agent version, deployment environment, scrubbed fields.

4. **API Key Lifecycle**
   - Rotation schedules, scoping to services/environments, usage analytics.

### Acceptance Criteria
- Backend can manage agent fleets (health view, remote config).
- Ingestion pipeline scales with new telemetry types and enforces quotas.

---

## Cross-Cutting Requirements

- **Documentation:** Maintain `docs/` with architecture, schema contracts, operational guides, compliance notes; keep `resume.md` updated per release.
- **Release Process:** Semantic versioning, changelog, upgrade notes, deprecation policy (minimum three minor versions supported).
- **Observability:** Self-monitoring: agent exports internal events to YAAT so backend surfaces fleet health automatically.
- **Security Reviews:** Each phase requires threat modeling and security sign-off.

---

## Execution Notes

- Treat phases as sequential but allow parallelization where safe (e.g., Phase 0 testing & Phase 1 design).
- Gate each phase with demo + documented acceptance checklist.
- Coordinate with backend team before altering ingest schemas; use feature flags/version headers.
- Maintain backwards compatibility by introducing new config fields with defaults and offering migration commands.

This plan should guide engineering from prototype to production-grade observability agent while keeping the TUI/CLI experience cohesive and approachable.
