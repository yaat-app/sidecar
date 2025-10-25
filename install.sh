#!/bin/bash
set -e

# YAAT Sidecar Installation Script
# Usage: curl -sSL https://raw.githubusercontent.com/yaat-app/sidecar/main/install.sh | bash

REPO="yaat-app/sidecar"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="yaat-sidecar"

# Colors for output
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

# Detect OS and architecture
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$OS" in
        linux*)
            OS="linux"
            ;;
        darwin*)
            OS="darwin"
            ;;
        msys*|mingw*|cygwin*|windows*)
            print_error "Windows is not supported. Please use WSL2 and run this script inside WSL."
            print_info "Install WSL2: https://learn.microsoft.com/en-us/windows/wsl/install"
            exit 1
            ;;
        *)
            print_error "Unsupported operating system: $OS"
            exit 1
            ;;
    esac

    case "$ARCH" in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            ;;
        *)
            print_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    print_info "Detected platform: ${OS}-${ARCH}"
}

# Get latest release version
get_latest_version() {
    print_info "Fetching latest release..."
    VERSION=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    if [ -z "$VERSION" ]; then
        print_error "Failed to fetch latest version"
        exit 1
    fi

    print_info "Latest version: $VERSION"
}

# Download and install binary
install_binary() {
    BINARY_FILE="${BINARY_NAME}-${OS}-${ARCH}"
    ARCHIVE_FILE="${BINARY_FILE}.tar.gz"

    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE_FILE}"

    print_info "Downloading from: $DOWNLOAD_URL"

    TMP_DIR=$(mktemp -d)
    cd "$TMP_DIR"

    if ! curl -sSL "$DOWNLOAD_URL" -o "$ARCHIVE_FILE"; then
        print_error "Failed to download binary"
        rm -rf "$TMP_DIR"
        exit 1
    fi

    print_info "Download complete. Extracting..."

    tar -xzf "$ARCHIVE_FILE"

    # Make binary executable
    chmod +x "$BINARY_FILE"

    # Install to system
    print_info "Installing to ${INSTALL_DIR}..."

    # Check if we need sudo
    if [ -w "$INSTALL_DIR" ]; then
        mv "$BINARY_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
    else
        print_warning "Need sudo permissions to install to ${INSTALL_DIR}"
        sudo mv "$BINARY_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
    fi

    # Cleanup
    cd - > /dev/null
    rm -rf "$TMP_DIR"

    print_info "Installation complete!"
}

# Verify installation
verify_installation() {
    if command -v "$BINARY_NAME" &> /dev/null; then
        VERSION_OUTPUT=$("$BINARY_NAME" --version 2>&1 || echo "unknown")
        print_info "Successfully installed: ${BINARY_NAME}"
        print_info "Version: ${VERSION_OUTPUT}"
    else
        print_error "Installation verification failed"
        exit 1
    fi
}

# Print next steps
print_next_steps() {
    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}  YAAT Sidecar installed successfully!${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    echo "Next steps:"
    echo ""
    echo "1. Get your API key from YAAT dashboard:"
    echo "   https://yaat.io/ → Settings → API Keys"
    echo ""
    echo "2. Create configuration file:"
    echo "   curl -sSL https://raw.githubusercontent.com/${REPO}/main/yaat.yaml.example -o yaat.yaml"
    echo ""
    echo "3. Edit yaat.yaml with your API key and settings"
    echo ""
    echo "4. Start the sidecar:"
    echo "   ${BINARY_NAME} --config yaat.yaml"
    echo ""
    echo "Documentation: https://github.com/${REPO}"
    echo ""
}

# Main installation flow
main() {
    print_info "Starting YAAT Sidecar installation..."

    detect_platform
    get_latest_version
    install_binary
    verify_installation
    print_next_steps
}

main
