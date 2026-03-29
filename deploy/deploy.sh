#!/usr/bin/env bash
# netscan installation and deployment script
set -euo pipefail

# Variables
BINARY=netscan
CONFIG=config.yml
INSTALL_DIR=/opt/netscan
SERVICE_FILE=/etc/systemd/system/netscan.service
SERVICE_USER=netscan

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

error_exit() {
    log_error "$1"
    exit 1
}

# Check if running as root or with sudo
if [[ $EUID -ne 0 ]]; then
    error_exit "This script must be run as root or with sudo privileges"
fi

# Check if Go is installed
check_go() {
    if ! command -v go &> /dev/null; then
        error_exit "Go is not installed. Please install Go 1.21+ first."
    fi

    local go_version
    go_version=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | sed 's/go//')
    local major_version
    major_version=$(echo "$go_version" | cut -d. -f1)
    local minor_version
    minor_version=$(echo "$go_version" | cut -d. -f2)

    if [[ $major_version -lt 1 ]] || [[ $major_version -eq 1 && $minor_version -lt 25 ]]; then
        error_exit "Go version 1.25+ required. Found: $go_version"
    fi

    log_info "Go $go_version found ✓"
}

# Build the binary
build_binary() {
    log_info "Building netscan binary..."
    
    # Get version info
    local version
    version=$(git describe --tags --always --dirty 2>/dev/null || echo "1.0.0")
    log_info "Building version: $version"

    if ! go build -ldflags "-X github.com/kljama/netscan/internal/version.Version=$version" -o "$BINARY" ./cmd/netscan; then
        error_exit "Failed to build netscan binary"
    fi

    if [[ ! -f "$BINARY" ]]; then
        error_exit "Build completed but binary $BINARY not found"
    fi

    log_info "Binary built successfully ✓"
}

# Create dedicated service user
create_service_user() {
    if id "$SERVICE_USER" &>/dev/null; then
        log_info "Service user $SERVICE_USER already exists ✓"
        return 0
    fi

    log_info "Creating service user: $SERVICE_USER"
    if ! useradd -r -s /bin/false "$SERVICE_USER" 2>/dev/null; then
        log_error "Failed to create user $SERVICE_USER"
        log_error "This might be due to insufficient permissions or user already exists"
        log_error "Try running: sudo useradd -r -s /bin/false $SERVICE_USER"
        exit 1
    fi

    log_info "Service user created successfully ✓"
}

# Create secure .env file with sensitive environment variables
create_env_file() {
    log_info "Creating secure .env file with environment variables"

    local env_content
    env_content=$(cat <<'EOF'
# Secure environment variables for netscan
# This file contains sensitive configuration values
# DO NOT commit this file to version control

# InfluxDB credentials (defaults from docker-compose.yml for testing)
INFLUXDB_TOKEN=netscan-token
INFLUXDB_ORG=test-org

# SNMP credentials
SNMP_COMMUNITY=public

# Instructions:
# 1. For testing: Use the default values above (matches docker-compose.yml)
# 2. For production: Replace with your actual secure credentials
# 3. Ensure this file has restrictive permissions (600)
# 4. The service will automatically load these variables
EOF
)

    echo "$env_content" | tee "$INSTALL_DIR/.env" > /dev/null
    if [[ $? -ne 0 ]]; then
        error_exit "Failed to create .env file"
    fi

    log_info ".env file created with secure placeholders ✓"
}

# Create install directory and copy files
install_files() {
    log_info "Installing files to $INSTALL_DIR"

    # Create install directory
    if ! mkdir -p "$INSTALL_DIR"; then
        error_exit "Failed to create directory $INSTALL_DIR"
    fi

    # Copy binary
    if [[ ! -f "$BINARY" ]]; then
        error_exit "Binary $BINARY not found. Please build it first."
    fi

    if ! cp "$BINARY" "$INSTALL_DIR/"; then
        error_exit "Failed to copy binary to $INSTALL_DIR"
    fi

    # Copy config template as config.yml (without sensitive values)
    if [[ -f "config.yml.example" ]]; then
        if ! cp "config.yml.example" "$INSTALL_DIR/config.yml"; then
            log_warn "Failed to copy config template, but continuing..."
        else
            log_info "Config template copied as config.yml"
        fi
    else
        log_warn "config.yml.example not found. Please ensure config template exists."
    fi

    # Create .env file with secure environment variables
    create_env_file

    log_info "Files installed successfully ✓"
}

# Set ownership and permissions
set_permissions() {
    log_info "Setting ownership and permissions"

    if ! chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR"; then
        error_exit "Failed to set ownership on $INSTALL_DIR"
    fi

    if ! chmod 755 "$INSTALL_DIR/$BINARY"; then
        error_exit "Failed to set permissions on binary"
    fi

    # Set restrictive permissions on .env file
    if [[ -f "$INSTALL_DIR/.env" ]]; then
        if ! chmod 600 "$INSTALL_DIR/.env"; then
            error_exit "Failed to set restrictive permissions on .env file"
        fi
        log_info ".env file permissions set to 600 ✓"
    fi

    log_info "Permissions set successfully ✓"
}

# Set capabilities for ICMP access
set_capabilities() {
    log_info "Setting CAP_NET_RAW capability for ICMP access"

    if ! command -v setcap &> /dev/null; then
        error_exit "setcap command not found. Please install libcap2-bin package."
    fi

    if ! setcap cap_net_raw+ep "$INSTALL_DIR/$BINARY"; then
        error_exit "Failed to set capabilities on binary. This might require root privileges."
    fi

    # Verify capability was set
    if ! getcap "$INSTALL_DIR/$BINARY" | grep -q "cap_net_raw+ep"; then
        log_warn "Capability verification failed, but continuing..."
    else
        log_info "Capabilities set successfully ✓"
    fi
}

# Create systemd service
create_service() {
    log_info "Creating systemd service"

    local service_content
    service_content=$(cat <<EOF
[Unit]
Description=netscan network monitoring service
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/$BINARY
WorkingDirectory=$INSTALL_DIR
Restart=always
User=$SERVICE_USER
Group=$SERVICE_USER

# Load environment variables from .env file
EnvironmentFile=$INSTALL_DIR/.env

# Security hardening
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
AmbientCapabilities=CAP_NET_RAW

[Install]
WantedBy=multi-user.target
EOF
)

    echo "$service_content" | tee "$SERVICE_FILE" > /dev/null
    if [[ $? -ne 0 ]]; then
        error_exit "Failed to create systemd service file"
    fi

    log_info "Systemd service created ✓"
}

# Enable and start service
enable_service() {
    log_info "Enabling and starting systemd service"

    if ! systemctl daemon-reload; then
        error_exit "Failed to reload systemd daemon"
    fi

    if ! systemctl enable netscan; then
        error_exit "Failed to enable netscan service"
    fi

    if ! systemctl start netscan; then
        error_exit "Failed to start netscan service"
    fi

    # Verify service is running
    sleep 2
    if ! systemctl is-active --quiet netscan; then
        log_error "Service failed to start. Check logs with: journalctl -u netscan -f"
        exit 1
    fi

    log_info "Service enabled and started successfully ✓"
}

# Main execution
main() {
    log_info "Starting netscan deployment..."

    # Get the directory of the script and project root
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
    
    # Change to project root for build
    cd "$PROJECT_ROOT"

    check_go
    build_binary
    create_service_user
    install_files
    set_permissions
    set_capabilities
    create_service
    enable_service

    log_info "netscan deployed and running as a systemd service with dedicated user and capabilities."
    log_info "Monitor with: sudo systemctl status netscan"
    log_info "View logs with: sudo journalctl -u netscan -f"
}

# Run main function
main "$@"
