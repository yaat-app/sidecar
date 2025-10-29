#!/usr/bin/env bash
set -euo pipefail

# YAAT Sidecar Installation Script
# Usage: curl -sSL https://raw.githubusercontent.com/yaat-app/sidecar/main/install.sh | bash

REPO="yaat-app/sidecar"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="yaat-sidecar"
NONINTERACTIVE="${YAAT_NONINTERACTIVE:-0}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

OS=""
ARCH=""
VERSION=""

print_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
print_error() { echo -e "${RED}[ERROR]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }

have_command() { command -v "$1" >/dev/null 2>&1; }

run_root() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    else
        sudo "$@"
    fi
}

prompt_yes_no() {
    local prompt="$1"
    local default="${2:-y}"
    local response

    if [ "$NONINTERACTIVE" = "1" ]; then
        case "$default" in
            y|Y|yes|YES) return 0 ;;
            *) return 1 ;;
        esac
    fi

    while true; do
        read -r -p "$prompt" response
        response="${response:-${default}}"
        case "$response" in
            [yY]|[yY][eE][sS]) return 0 ;;
            [nN]|[nN][oO]) return 1 ;;
            *) echo "Please answer yes or no." ;;
        esac
    done
}

detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$OS" in
        linux*) OS="linux" ;;
        darwin*) OS="darwin" ;;
        msys*|mingw*|cygwin*|windows*)
            print_error "Windows is not supported. Use WSL2 and run this script inside WSL."
            exit 1
            ;;
        *)
            print_error "Unsupported operating system: $OS"
            exit 1
            ;;
    esac

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)
            print_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    print_info "Detected platform: ${OS}-${ARCH}"
}

get_latest_version() {
    print_info "Fetching latest release..."
    VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$VERSION" ]; then
        print_error "Failed to determine latest release."
        exit 1
    fi
    print_info "Latest version: ${VERSION}"
}

install_binary() {
    local binary_file="${BINARY_NAME}-${OS}-${ARCH}"
    local archive_file="${binary_file}.tar.gz"
    local download_url="https://github.com/${REPO}/releases/download/${VERSION}/${archive_file}"

    print_info "Downloading ${download_url}"
    local tmp_dir
    tmp_dir=$(mktemp -d)
    trap 'rm -rf "${tmp_dir}"' EXIT

    (cd "$tmp_dir" && curl -fsSL "$download_url" -o "$archive_file")
    print_info "Extracting archive..."
    (cd "$tmp_dir" && tar -xzf "$archive_file")

    run_root install -m 0755 "${tmp_dir}/${binary_file}" "${INSTALL_DIR}/${BINARY_NAME}"
    print_info "Installed binary to ${INSTALL_DIR}/${BINARY_NAME}"
}

verify_installation() {
    if have_command "$BINARY_NAME"; then
        print_info "Installed: $($BINARY_NAME --version 2>/dev/null || echo unknown)"
    else
        print_error "Installation verification failed."
        exit 1
    fi
}

ensure_directory() {
    local dir="$1"
    run_root mkdir -p "$dir"
}

linux_prepare_user() {
    if id -u yaat >/dev/null 2>&1; then
        return
    fi
    print_info "Creating system user 'yaat'"
    run_root useradd --system --home /var/lib/yaat --shell /usr/sbin/nologin yaat
}

linux_setup_directories() {
    local config_dir="/etc/yaat"
    local state_dir="/var/lib/yaat"
    local log_dir="/var/log/yaat"

    ensure_directory "$config_dir"
    ensure_directory "$state_dir"
    ensure_directory "$log_dir"

    run_root chown yaat:yaat "$state_dir"
    run_root chown yaat:yaat "$log_dir"
    run_root chmod 0750 "$state_dir"
    run_root chmod 0750 "$log_dir"
}

linux_install_service() {
    if ! have_command systemctl; then
        print_warning "systemd not detected; skipping service installation."
        return
    fi

    if ! prompt_yes_no "Install systemd service for YAAT Sidecar? [Y/n] " "y"; then
        print_info "Skipping systemd service installation."
        return
    fi

    linux_prepare_user
    linux_setup_directories

    local unit_file="/etc/systemd/system/yaat-sidecar.service"
    local unit_contents="[Unit]
Description=YAAT Sidecar - Backend Monitoring Agent
Documentation=https://yaat.io/docs
After=network.target

[Service]
Type=simple
User=yaat
Group=yaat
WorkingDirectory=/var/lib/yaat
ExecStart=${INSTALL_DIR}/${BINARY_NAME} --config /etc/yaat/yaat.yaml --log-file /var/log/yaat/sidecar.log
ExecReload=/bin/kill -HUP \$MAINPID
Restart=on-failure
RestartSec=10s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/yaat /var/lib/yaat /etc/yaat
CapabilityBoundingSet=
LimitNOFILE=65536
MemoryMax=512M
CPUQuota=50%

[Install]
WantedBy=multi-user.target"

    print_info "Installing systemd unit: ${unit_file}"
    if [ "$(id -u)" -eq 0 ]; then
        printf '%s\n' "$unit_contents" > "$unit_file"
    else
        printf '%s\n' "$unit_contents" | sudo tee "$unit_file" >/dev/null
    fi

    run_root systemctl daemon-reload

    if prompt_yes_no "Enable YAAT Sidecar to start on boot? [Y/n] " "y"; then
        run_root systemctl enable yaat-sidecar
        print_info "Service enabled. Start it after configuring credentials:"
        echo "  sudo -u yaat ${BINARY_NAME} --setup --config /etc/yaat/yaat.yaml"
        echo "  sudo systemctl start yaat-sidecar"
    else
        print_info "Service installed but not enabled. Start manually once configured."
    fi
}

darwin_setup_directories() {
    local config_dir="/usr/local/etc/yaat"
    local state_dir="/usr/local/var/lib/yaat"
    local log_dir="/usr/local/var/log/yaat"

    ensure_directory "$config_dir"
    ensure_directory "$state_dir"
    ensure_directory "$log_dir"

    run_root chown "$(id -un)":staff "$state_dir" "$log_dir"
    run_root chmod 0750 "$state_dir" "$log_dir"
}

darwin_install_launchd() {
    if ! have_command launchctl; then
        print_warning "launchctl not available; skipping launchd installation."
        return
    fi

    if ! prompt_yes_no "Install launchd daemon for YAAT Sidecar? [Y/n] " "y"; then
        print_info "Skipping launchd installation."
        return
    fi

    darwin_setup_directories

    local plist_path="/Library/LaunchDaemons/io.yaat.sidecar.plist"
    local plist_contents="<?xml version=\"1.0\" encoding=\"UTF-8\"?>
<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">
<plist version=\"1.0\">
<dict>
    <key>Label</key>
    <string>io.yaat.sidecar</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/${BINARY_NAME}</string>
        <string>--config</string>
        <string>/usr/local/etc/yaat/yaat.yaml</string>
        <string>--log-file</string>
        <string>/usr/local/var/log/yaat/sidecar.log</string>
    </array>
    <key>WorkingDirectory</key>
    <string>/usr/local/var/lib/yaat</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>/usr/local/var/log/yaat/sidecar.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/usr/local/var/log/yaat/sidecar.stderr.log</string>
</dict>
</plist>"

    print_info "Installing launchd plist: ${plist_path}"
    if [ "$(id -u)" -eq 0 ]; then
        printf '%s\n' "$plist_contents" > "$plist_path"
    else
        printf '%s\n' "$plist_contents" | sudo tee "$plist_path" >/dev/null
    fi

    run_root chmod 0644 "$plist_path"

    if prompt_yes_no "Load YAAT Sidecar daemon with launchctl now? [Y/n] " "n"; then
        run_root launchctl load -w "$plist_path"
        print_info "Daemon loaded. Configure credentials via:"
        echo "  sudo ${BINARY_NAME} --setup --config /usr/local/etc/yaat/yaat.yaml"
        echo "  sudo launchctl kickstart -k system/io.yaat.sidecar"
    else
        print_info "Daemon installed but not loaded. Configure and load manually when ready."
    fi
}

post_install() {
    case "$OS" in
        linux) linux_install_service ;;
        darwin) darwin_install_launchd ;;
    esac
}

print_next_steps() {
    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}  YAAT Sidecar installation complete${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    case "$OS" in
        linux)
            echo "Configuration (run as the service user):"
            echo "  sudo -u yaat ${BINARY_NAME} --setup --config /etc/yaat/yaat.yaml"
            echo ""
            echo "Start service:"
            echo "  sudo systemctl start yaat-sidecar"
            ;;
        darwin)
            echo "Configuration:"
            echo "  sudo ${BINARY_NAME} --setup --config /usr/local/etc/yaat/yaat.yaml"
            echo ""
            echo "Start daemon:"
            echo "  sudo launchctl load -w /Library/LaunchDaemons/io.yaat.sidecar.plist"
            ;;
    esac
    echo ""
    echo "Dashboard & TUI:"
    echo "  ${BINARY_NAME} --dashboard"
    echo ""
    echo "Useful commands:"
    echo "  ${BINARY_NAME} --status"
    echo "  ${BINARY_NAME} --test"
    echo "  ${BINARY_NAME} --update"
    echo "  ${BINARY_NAME} --uninstall"
    echo ""
    echo "Documentation: https://github.com/${REPO}"
    echo ""
}

main() {
    print_info "Starting YAAT Sidecar installation..."
    detect_platform
    get_latest_version
    install_binary
    verify_installation
    post_install
    print_next_steps
}

main "$@"
