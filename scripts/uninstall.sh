#!/bin/bash
set -e

# YAAT Sidecar Uninstall Script

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')

print_info "Uninstalling YAAT Sidecar..."

# Stop running sidecar
if command -v yaat-sidecar &> /dev/null; then
    print_info "Stopping sidecar..."

    case "$OS" in
        linux*)
            if systemctl is-active --quiet yaat-sidecar 2>/dev/null; then
                sudo systemctl stop yaat-sidecar
                sudo systemctl disable yaat-sidecar
                print_info "Stopped systemd service"
            fi
            ;;
        darwin*)
            PLIST="$HOME/Library/LaunchAgents/io.yaat.sidecar.plist"
            if [ -f "$PLIST" ]; then
                launchctl unload "$PLIST" 2>/dev/null || true
                print_info "Stopped launchd service"
            fi
            ;;
    esac

    # Kill any running processes
    pkill -f yaat-sidecar || true
fi

# Remove binary
if [ -f "/usr/local/bin/yaat-sidecar" ]; then
    print_info "Removing binary..."
    sudo rm -f /usr/local/bin/yaat-sidecar
fi

# Remove systemd service (Linux)
if [ "$OS" = "linux" ] && [ -f "/etc/systemd/system/yaat-sidecar.service" ]; then
    print_info "Removing systemd service..."
    sudo rm -f /etc/systemd/system/yaat-sidecar.service
    sudo systemctl daemon-reload
fi

# Remove launchd plist (macOS)
if [ "$OS" = "darwin" ]; then
    PLIST="$HOME/Library/LaunchAgents/io.yaat.sidecar.plist"
    if [ -f "$PLIST" ]; then
        print_info "Removing launchd plist..."
        rm -f "$PLIST"
    fi
fi

# Remove configuration
print_warning "Do you want to remove configuration files? [y/N]"
read -r response
if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
    print_info "Removing configuration..."
    sudo rm -rf /etc/yaat /usr/local/etc/yaat
    rm -rf "$HOME/.yaat"
fi

# Remove logs
print_warning "Do you want to remove log files? [y/N]"
read -r response
if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]; then
    print_info "Removing logs..."
    sudo rm -rf /var/log/yaat /usr/local/var/log/yaat
fi

# Remove data directory
sudo rm -rf /var/lib/yaat /usr/local/var/lib/yaat

print_info "✓ YAAT Sidecar uninstalled successfully"
echo ""
echo "Thank you for using YAAT! We'd love your feedback:"
echo "https://github.com/yaat-app/sidecar/issues"
