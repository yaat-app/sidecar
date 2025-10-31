# YAAT Sidecar

[![Latest Release](https://img.shields.io/github/v/release/yaat-app/sidecar)](https://github.com/yaat-app/sidecar/releases)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](https://opensource.org/licenses/GPL-3.0)

Backend monitoring agent for the YAAT analytics platform. Monitor your production applications with zero code changes.

## Features

- **🎨 Beautiful TUI Dashboard**: Interactive terminal UI for monitoring service status, events, and logs in real-time
- **📝 In-TUI Config Editing**: Update API keys, batching, metrics, scrubbing, and log sources directly from the dashboard
- **📊 Log File Tailing**: Real-time log parsing and forwarding with intelligent pattern matching
- **🔧 Multiple Format Support**: Django, Nginx, Apache, and JSON logs with automatic field extraction
- **📦 Stack Trace Capture**: Multi-line traceback support for Django/Python applications
- **🐳 Container Aware**: Autodetects Docker/Kubernetes stdout files and parses Docker JSON envelopes out of the box
- **🚀 Zero Code Changes**: Deploy as a sidecar alongside your application - pure observation mode
- **🔄 Buffered Delivery**: Efficient batching with automatic retry and exponential backoff
- **🐧 Linux-First**: Optimized for production Linux servers (amd64 + arm64)
- **✨ Auto-Detection**: Automatically discovers services, log files, and optimal configurations
- **🔌 Plug & Play**: Interactive setup wizard gets you running in seconds
- **🛡 Sensitive Data Scrubbing**: Regex-driven redaction and drop rules prevent secrets from leaving your host

## Installation

### Quick Install (Recommended)

One-line installation for Linux servers:

```bash
curl -sSL https://raw.githubusercontent.com/yaat-app/sidecar/main/install.sh | bash
```

The installer:
- Detects your OS/architecture and downloads the latest release
- Installs the binary to `/usr/local/bin/yaat-sidecar`
- Creates Linux directories for config/state/logs
- Offers to install a systemd unit so the agent can run as a service

After the installer finishes, run the setup wizard as the service user:

```bash
sudo -u yaat yaat-sidecar --setup --config /etc/yaat/yaat.yaml
```

Once the setup wizard finishes, start the background service:

```bash
sudo systemctl start yaat-sidecar
```

### Manual Installation

1. **Download the binary for your platform** from the [latest release](https://github.com/yaat-app/sidecar/releases/latest):

   - **Linux (amd64/x86_64)**: `yaat-sidecar-linux-amd64.tar.gz`
   - **Linux (arm64/aarch64)**: `yaat-sidecar-linux-arm64.tar.gz`

   > **Note**: For development/testing on macOS or Windows, build from source with `go build ./cmd`

2. **Extract and install**:

   ```bash
   # Extract
   tar -xzf yaat-sidecar-*.tar.gz

   # Make executable and move to PATH
   chmod +x yaat-sidecar-*
   sudo mv yaat-sidecar-* /usr/local/bin/yaat-sidecar
   ```

3. **Verify installation**:

   ```bash
   yaat-sidecar --version
   ```

### Build from Source

If you prefer to build from source:

```bash
# Clone repository
git clone https://github.com/yaat-app/sidecar.git
cd sidecar

# Build binary
CGO_ENABLED=0 go build -o yaat-sidecar ./cmd

# Binary will be at ./yaat-sidecar
```

## Quick Start

### 1. Launch the setup wizard

```bash
yaat-sidecar --setup
```

The wizard will guide you through:

- Configuring your API key (get it from Dashboard → Settings → API Keys)
- Auto-detecting services (Nginx, Apache, Django, Node.js) and container stdout streams
- Discovering and selecting log files (local + Docker/Kubernetes) to monitor
- Choosing log formats (Django, Nginx, Apache, JSON, Docker envelopes)
- Enabling recommended scrubbing rules before events leave the box
- Testing API connectivity
- Optionally starting the sidecar in the background

### 2. View the interactive dashboard (NEW!)

```bash
yaat-sidecar --dashboard
```

The TUI dashboard shows:
- ✅ Service status (running/stopped) with uptime
- 📊 Real-time event metrics (events sent, failed, API status)
- 📁 Files being tailed with event counts
- ⌨️  Interactive controls: setup, config editor, event viewer

**Keyboard shortcuts:**
- `s` - Launch setup wizard
- `c` - Open configuration view (press `Enter` to edit and save)
- `e` - View real-time event feed
- `t` - Test configuration
- `q` - Quit

### 3. Manage the sidecar

- `yaat-sidecar --status` – Check daemon status
- `yaat-sidecar --stop` – Stop the background service
- `yaat-sidecar --restart` – Restart with latest config
- `yaat-sidecar --test` – Validate configuration and API connectivity
- `yaat-sidecar --update` – Self-update to newest release
- `yaat-sidecar --uninstall` – Complete removal (with helpful feedback)

### 4. Verify in YAAT dashboard

Visit your YAAT dashboard at [yaat.io](https://yaat.io) → **Services** to see events flowing in real-time.

## Service Locations & Files

| Config Path | Logs | State | Service |
|-------------|------|-------|---------|
| `/etc/yaat/yaat.yaml` | `/var/log/yaat/sidecar.log` | `/var/lib/yaat` | `systemctl enable yaat-sidecar` |

Use the TUI config editor (`c` → `Enter`) to update credentials, batching, metrics, and log sources at any time. The wizard and editor automatically apply secure permissions to sensitive files.

## Django Integration Checklist

1. **Emit structured logs**: update Django’s logging config (e.g. `LOGGING['handlers']['yaat_file'] = {'class': 'logging.handlers.WatchedFileHandler', 'filename': '/var/log/myapp/django.log'}`) and point your app logger at it.
2. **Run the setup wizard** (as shown above) and select the Django log path when prompted. The wizard auto-detects `manage.py`, gunicorn, and container stdout so you can accept defaults.
3. **Verify connectivity**: `yaat-sidecar --test` sends sample log/span/metric events to ensure the Django service appears in YAAT immediately.
4. **Optional metrics**: enable host metrics or StatsD in the config editor if your Django stack exposes application metrics.
5. **Deploy**: start/enable the service; the agent runs as the dedicated `yaat` system user with least-privilege defaults.

## Manual configuration (optional)

Prefer to manage the YAML yourself? Create `yaat.yaml` (for example in `~/.yaat/yaat.yaml`) with:
You can also open the dashboard, press `c`, and hit `Enter` to edit these fields interactively.

The inline editor lets you add/remove log sources and adjust formats without leaving the terminal.

```yaml
# Your YAAT API key
api_key: "yaat_your_api_key_here"

# Service identifier
service_name: "my-api-server"

# Environment (production, staging, development)
environment: "production"

# HTTP Proxy Configuration
proxy:
  enabled: true
  listen_port: 19000
  upstream_url: "http://127.0.0.1:8000"

# Log Files to Monitor
logs:
  - path: "/var/log/myapp/app.log"
    format: "django"
  - path: "/var/lib/docker/containers/<id>/<id>-json.log"
    format: "docker"

# Scrubbing rules (mask sensitive values before shipping events)
scrubbing:
  enabled: true
  rules:
    - name: "Mask Authorization bearer tokens"
      pattern: "(?i)(authorization:?\\s*bearer\\s+)[A-Za-z0-9._~-]+"
      replacement: "$1[REDACTED]"
      fields: ["message", "stacktrace", "tags.authorization"]

# YAAT API endpoint
api_endpoint: "https://yaat.io/api/v1/ingest"
```

Then run:

```bash
yaat-sidecar --config yaat.yaml
```

**⚠️ Note about Proxy Mode:**

The built-in HTTP proxy is optional and should be used carefully. For most use cases, **log-only monitoring** (passive observation) is recommended:

✅ **Recommended: Log-Only Mode**
- Zero risk to production traffic
- Works with any architecture (multiple load balancers, complex setups)
- No latency overhead
- Simply tail nginx/apache access logs

⚠️ **Use Proxy Mode Only If:**
- You have a simple single-server setup
- You need request/response body inspection
- You understand it adds a hop in your request path

To enable proxy mode, update your load balancer or nginx to point to the sidecar:

```nginx
upstream app {
    server 127.0.0.1:19000;  # Sidecar port
}
```

## Configuration

See `yaat.yaml.example` for a complete configuration example.

### Required Fields

- `api_key`: Your YAAT organization API key
- `service_name`: Name of your service
- `api_endpoint`: YAAT API endpoint

### Optional Fields

- `environment`: Environment name (default: "production")
- `buffer_size`: Number of events to buffer (default: 1000)
- `flush_interval`: How often to send events (default: "10s")
- `scrubbing.enabled`: Enable/disable regex-based scrubbing (default: true in setup wizard)
- `scrubbing.rules`: List of masking/drop rules (pattern, replacement, fields, drop)
- `delivery.batch_size`: Max events per HTTP request (default: 500)
- `delivery.compress`: Enable gzip compression for payloads
- `delivery.max_batch_bytes`: Optional soft cap for request payload size (0 disables)
- `delivery.queue_retention`: How long to keep persisted batches before cleanup (default: 24h)
- `delivery.dead_letter_retention`: Retention window for dead-letter batches (default: 168h)
- `metrics.enabled`: Enable host metrics emission (default: false)
- `metrics.interval`: Sampling cadence for host metrics (default: "30s")
- `metrics.tags`: Optional map of static tags applied to host metrics
- `logs.format: journald`: Stream from systemd journal (set `path` to match a `_SYSTEMD_UNIT`, or leave blank for all entries)

## Host Metrics

When `metrics.enabled` is true, the sidecar samples host-level telemetry at the configured interval and emits metric events alongside application telemetry. Current metrics include:

- `host.cpu.usage_percent`
- `host.memory.used_bytes` / `host.memory.total_bytes`
- `host.disk.usage_percent`
- `host.net.rx_bytes_per_sec` and `host.net.tx_bytes_per_sec`

Each metric inherits tags defined in `metrics.tags` (plus automatic `unit` annotations) and flows through the same buffer/queue pipeline, so delivery guarantees and diagnostics apply uniformly.

> **Note:** Host metrics are currently implemented for Linux only. Other platforms log a warning and skip sampling.

### StatsD / DogStatsD Listener

When `metrics.statsd.enabled` is true, the sidecar exposes a UDP listener (default `:8125`) compatible with StatsD / DogStatsD. Incoming metrics are normalised into YAAT metric events using the configured namespace and tags. Example payload:

```
echo "api.requests:1|c|#component:api" | nc -u -w0 localhost 8125
```

Generates an event with metric name `namespace.api.requests` (if namespace is set) and tags combining global `metrics.tags` + the packet tags.

## Supported Log Formats

### Django

```
[2024-10-26 10:30:15,123] ERROR [django.request] Message here
Traceback (most recent call last):
  File "/path/to/file.py", line 123, in function
    some_code()
ValueError: Something went wrong
```

**Captures:**
- Timestamp, log level, logger name, message
- Multi-line stack traces (automatically attached to error events)
- All Django log levels (DEBUG, INFO, WARNING, ERROR, CRITICAL)

### Nginx

```
192.168.1.1 - - [26/Oct/2025:09:15:23 +0000] "GET /api/users HTTP/1.1" 200 1234 "https://example.com" "Mozilla/5.0"
```

**Captures:**
- Client IP, timestamp, HTTP method, path, status code, response size
- Referer and User-Agent (if present in Combined format)
- Automatically generates trace/span IDs for distributed tracing

### Apache

```
192.168.1.1 - - [26/Oct/2025:09:15:23 +0000] "POST /api/orders HTTP/1.1" 201 567 "https://example.com" "curl/7.64.1"
```

**Captures:**
- Common and Combined formats supported
- Same fields as Nginx (IP, method, path, status, size, referer, user-agent)
- Proper timestamp parsing

### JSON

```json
{"level":"error","message":"Database connection failed","timestamp":"2025-10-26T09:15:23Z","user_id":123,"stacktrace":"..."}
```

**Intelligent parsing:**
- Extracts `level`/`severity`/`log_level` → standardized severity
- Extracts `message`/`msg`/`text` → event message
- Extracts `timestamp`/`time`/@timestamp` → proper timestamp
- Extracts `stacktrace`/`stack_trace` → stack trace field
- All remaining fields → preserved as tags
- Supports multiple timestamp formats (RFC3339, ISO8601, custom)

### Generic

Any unrecognized format is treated as a plain text log with `info` level.

### Journald

When `format: "journald"` is configured, the sidecar reads entries from systemd-journald (Linux+cgo only). Use the `path` field to filter by `_SYSTEMD_UNIT` (e.g., `nginx.service`), or leave empty to capture all entries. Journald fields are exposed as tags (unit, priority, identifier, hostname, etc.).

## Troubleshooting

### Events not appearing in dashboard

1. **Check API key**: Ensure your API key is correct and active in the YAAT dashboard
2. **Verify connectivity**: Check that the sidecar can reach the API endpoint
   ```bash
   curl -H "Authorization: Bearer YOUR_API_KEY" https://yaat.io/api/v1/ingest
   ```
3. **Check logs**: Run the sidecar with verbose logging to see what's happening
4. **Firewall**: Ensure outbound HTTPS (port 443) is allowed

### Proxy not forwarding traffic

1. **Check upstream URL**: Verify your application is running at the configured `upstream_url`
2. **Port conflicts**: Ensure the `listen_port` isn't already in use
3. **Test direct connection**: Try curl the sidecar port directly:
   ```bash
   curl http://localhost:19000/health
   ```

The same listener exposes Prometheus-compatible metrics at `/metrics` (queue depth, throughput, totals, last error):

```
curl http://localhost:19000/metrics
```

### Log files not being tailed

1. **File permissions**: Ensure the sidecar process has read access to log files
2. **File path**: Verify the log file path is correct and exists
3. **Format**: Ensure the log format matches one of: `django`, `nginx`, or `json`

### High memory usage

- Reduce `buffer_size` in your configuration
- Decrease `flush_interval` to send events more frequently

### Getting help

- 📚 [Documentation](https://github.com/yaat-app/sidecar)
- 🐛 [Report issues](https://github.com/yaat-app/sidecar/issues)
- 💬 [Community support](https://yaat.io/community)

## Development

### Run Tests

```bash
go test ./...
```

### Build Static Binary

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o yaat-sidecar ./cmd
```

## Architecture

```
┌─────────────┐
│   Your App  │
│  (port 8000)│
└──────▲──────┘
       │
       │ Forwarded traffic
       │
┌──────┴──────────────┐
│  YAAT Sidecar       │
│  (port 19000)       │
│                     │
│  ┌────────────┐    │
│  │   Proxy    │    │
│  └────────────┘    │
│                    │
│  ┌────────────┐    │
│  │ Log Tailer │    │
│  └────────────┘    │
│                    │
│  ┌────────────┐    │
│  │  Forwarder │────┼──► YAAT API
│  └────────────┘    │
└─────────────────────┘
```

## License

GPL-3.0 License © 2025 YAAT Team
