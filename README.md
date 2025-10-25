# YAAT Sidecar

[![Latest Release](https://img.shields.io/github/v/release/yaat-app/sidecar)](https://github.com/yaat-app/sidecar/releases)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](https://opensource.org/licenses/GPL-3.0)

Backend monitoring agent for the YAAT analytics platform. Monitor your production applications with zero code changes.

## Features

- **HTTP Traffic Monitoring**: Reverse proxy that captures request/response metrics, latencies, and status codes
- **Log File Tailing**: Real-time log parsing and forwarding with intelligent pattern matching
- **Multiple Format Support**: Django, Nginx, and JSON logs out of the box
- **Zero Code Changes**: Deploy as a sidecar alongside your application
- **Buffered Delivery**: Efficient batching with automatic retry and backoff
- **Multi-Platform**: Linux, macOS, and Windows support

## Installation

### Quick Install (Recommended)

One-line installation for Linux and macOS:

```bash
curl -sSL https://raw.githubusercontent.com/yaat-app/sidecar/main/install.sh | bash
```

This will:
- Auto-detect your OS and architecture
- Download the latest release binary
- Install to `/usr/local/bin/yaat-sidecar`
- Verify the installation

### Manual Installation

1. **Download the binary for your platform** from the [latest release](https://github.com/yaat-app/sidecar/releases/latest):

   - **Linux (amd64)**: `yaat-sidecar-linux-amd64.tar.gz`
   - **Linux (arm64)**: `yaat-sidecar-linux-arm64.tar.gz`
   - **macOS (Intel)**: `yaat-sidecar-darwin-amd64.tar.gz`
   - **macOS (Apple Silicon)**: `yaat-sidecar-darwin-arm64.tar.gz`
   - **Windows**: `yaat-sidecar-windows-amd64.exe.zip`

2. **Extract and install**:

   ```bash
   # Extract (Linux/macOS)
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

### 1. Get Your API Key

1. Log in to your [YAAT dashboard](https://yaat.io/welcomwe)
2. Navigate to **Settings** → **API Keys**
3. Click **Create API Key**
4. Copy the generated key (shown only once!)

### 2. Create Configuration File

Download the example configuration:

```bash
curl -sSL https://raw.githubusercontent.com/yaat-app/sidecar/main/yaat.yaml.example -o yaat.yaml
```

Or create it manually:

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

# YAAT API endpoint
api_endpoint: "https://yaat.io/v1/ingest"
```

### 3. Start the Sidecar

```bash
yaat-sidecar --config yaat.yaml
```

### 4. Route Traffic (for HTTP monitoring)

If using proxy mode, update your load balancer or nginx to point to the sidecar:

```nginx
upstream app {
    server 127.0.0.1:19000;  # Sidecar port
}
```

### 5. Verify in Dashboard

Visit your YAAT dashboard at [yaat.io](https://yaat.io) → **Services** to see your service and incoming events!

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

## Supported Log Formats

### Django

```
[2024-10-26 10:30:15,123] ERROR [django.request] Message here
```

### Nginx

```
IP - - [timestamp] "METHOD /path HTTP/1.1" status size
```

### JSON

Any JSON-formatted log line.

## Troubleshooting

### Events not appearing in dashboard

1. **Check API key**: Ensure your API key is correct and active in the YAAT dashboard
2. **Verify connectivity**: Check that the sidecar can reach the API endpoint
   ```bash
   curl -H "Authorization: Bearer YOUR_API_KEY" https://yaat.io/v1/ingest
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