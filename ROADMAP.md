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

## Phase 2 – Telemetry Breadth ✅ (v0.0.11-alpha)

### Objectives
- Capture logs, metrics, and traces at parity with common observability stacks.
- **KILLER FEATURE:** Local analytics with DuckDB for offline SQL queries

### Completed Workstreams ✅
1. **Log Intake**
   - ✅ Autodetect JSON/logfmt, journald support (Linux), docker stdout tailer
   - ✅ Regex-based redaction/scrubbing rules

2. **Host Metrics**
   - ✅ Linux collectors for CPU, memory, disk, network, process stats
   - ✅ Configurable sampling interval and metric filters

3. **Metrics API**
   - ✅ StatsD/dogstatsd listener with counters, gauges, sets, histograms
   - ✅ Aggregation window, flush cadence, and tagging support

4. **Local Analytics (DuckDB)**
   - ✅ Embedded columnar SQL database (`internal/analytics`)
   - ✅ Two modes: Cloud + Local (dual-write) vs Local-Only (100% offline)
   - ✅ Schema matching ClickHouse for query consistency
   - ✅ Automatic retention and size-based cleanup
   - ✅ Async non-blocking writes with prepared statements

### Future Work
- **Tracing:** OpenTelemetry receiver (OTLP/HTTP + gRPC), span sampling policies, propagation helpers (W3C, B3)

### Acceptance Criteria
- ✅ Agent publishes host metrics and StatsD events to YAAT backend with accurate tags
- ✅ Scrubbing rules configurable in TUI/CLI and applied before send
- ✅ Local analytics queryable with SQL, no cloud dependency required
- 🔄 Traces ingested via OTLP appear in backend with correct relationships (pending)

---

## Phase 3 – Platform & Packaging (Linux-Focused)

### Objectives
- Make deployment frictionless on Linux servers and containers (99% of production deployments)

### Workstreams
1. **Linux Packaging** ✅
   - ✅ systemd unit with hardening (NoNewPrivileges, ProtectSystem)
   - ✅ Automated installer (`install.sh`)
   - 🔄 Signed binaries for releases (pending)

2. **Containers & Kubernetes**
   - 🔄 Build & publish Docker image (multi-arch: amd64, arm64)
   - 🔄 Helm chart with autodiscovery (annotations/labels)
   - 🔄 Sidecar/instrumentation examples for popular stacks (Nginx, Django, Node)

3. **Autodiscovery Framework** ✅
   - ✅ Auto-detect services (Nginx, Apache, Django, containers)
   - ✅ Docker/Kubernetes stdout detection
   - 🔄 Live reload when config files change

4. **Remote Config & Fleet Telemetry**
   - 🔄 Backend endpoints for agent status, version, queue metrics
   - 🔄 Agent polls for remote config overrides (with signatures)

### Platform Notes
- **Primary target:** Linux servers (amd64 + arm64)
- **Development only:** macOS, Windows (build from source)
- **Why Linux-only?** 99% of production servers run Linux; focusing here reduces complexity and maintenance burden

### Acceptance Criteria
- ✅ One-command install for Linux with systemd integration
- 🔄 Helm chart installs agent with autodiscovery; metrics/logs flow within 5 minutes
- 🔄 Fleet dashboard in backend shows agent health

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
