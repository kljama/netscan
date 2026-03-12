#!/usr/bin/env bash
# netscan uninstallation and cleanup script
set -euo pipefail

# Variables (matching deploy.sh)
INSTALL_DIR=/opt/netscan
SERVICE_FILE=/etc/systemd/system/netscan.service
SERVICE_USER=netscan
BINARY_NAME=netscan

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

# Stop and disable systemd service
stop_service() {
    log_info "Stopping and disabling netscan service"

    # Check if service exists
    if ! systemctl list-units --all | grep -q "netscan.service"; then
        log_info "Service not found, skipping service cleanup"
        return 0
    fi

    # Stop service if running
    if systemctl is-active --quiet netscan 2>/dev/null; then
        if ! systemctl stop netscan; then
            log_warn "Failed to stop service, but continuing..."
        else
            log_info "Service stopped ✓"
        fi
    else
        log_info "Service already stopped"
    fi

    # Disable service
    if systemctl is-enabled --quiet netscan 2>/dev/null; then
        if ! systemctl disable netscan; then
            log_warn "Failed to disable service, but continuing..."
        else
            log_info "Service disabled ✓"
        fi
    else
        log_info "Service already disabled"
    fi
}

# Remove systemd service file
remove_service_file() {
    if [[ -f "$SERVICE_FILE" ]]; then
        log_info "Removing systemd service file"
        if ! rm -f "$SERVICE_FILE"; then
            log_error "Failed to remove service file: $SERVICE_FILE"
            return 1
        fi
        log_info "Service file removed ✓"
    else
        log_info "Service file not found, skipping"
    fi

    # Reload systemd daemon
    if ! systemctl daemon-reload; then
        log_warn "Failed to reload systemd daemon, but continuing..."
    else
        log_info "Systemd daemon reloaded ✓"
    fi
}

# Remove capabilities from binary
remove_capabilities() {
    local binary_path="$INSTALL_DIR/$BINARY_NAME"

    if [[ -f "$binary_path" ]]; then
        log_info "Removing capabilities from binary"

        # Check if capabilities are set
        if getcap "$binary_path" 2>/dev/null | grep -q "cap_net_raw"; then
            if ! setcap -r "$binary_path" 2>/dev/null; then
                log_warn "Failed to remove capabilities, but continuing..."
            else
                log_info "Capabilities removed ✓"
            fi
        else
            log_info "No capabilities found on binary"
        fi
    else
        log_info "Binary not found, skipping capability removal"
    fi
}

# Remove installation directory
remove_install_dir() {
    if [[ -d "$INSTALL_DIR" ]]; then
        log_info "Removing installation directory: $INSTALL_DIR"

        # Get directory size for reporting
        local dir_size
        dir_size=$(du -sh "$INSTALL_DIR" 2>/dev/null | cut -f1 || echo "unknown")

        if ! rm -rf "$INSTALL_DIR"; then
            error_exit "Failed to remove installation directory: $INSTALL_DIR"
        fi

        log_info "Installation directory removed ($dir_size) ✓"
    else
        log_info "Installation directory not found, skipping"
    fi
}

# Remove service user
remove_service_user() {
    if id "$SERVICE_USER" &>/dev/null; then
        log_info "Removing service user: $SERVICE_USER"

        # Check if user has any running processes
        if pgrep -u "$SERVICE_USER" >/dev/null 2>&1; then
            log_warn "User $SERVICE_USER has running processes, forcing removal"
            if ! userdel -rf "$SERVICE_USER" 2>/dev/null; then
                log_error "Failed to force remove user $SERVICE_USER"
                log_error "You may need to manually kill processes and remove user"
                return 1
            fi
        else
            if ! userdel "$SERVICE_USER" 2>/dev/null; then
                log_warn "Failed to remove user $SERVICE_USER, but continuing..."
            else
                log_info "Service user removed ✓"
            fi
        fi
    else
        log_info "Service user not found, skipping"
    fi
}

# Clean up any remaining artifacts
cleanup_artifacts() {
    log_info "Checking for remaining artifacts"

    local cleaned=false

    # Check for any netscan processes
    if pgrep -f "netscan" >/dev/null 2>&1; then
        log_warn "Found running netscan processes, please stop them manually"
        cleaned=true
    fi

    # Check for any netscan-related systemd units
    if systemctl list-units --all | grep -q "netscan"; then
        log_warn "Found systemd units containing 'netscan', please review manually"
        cleaned=true
    fi

    # Check for any remaining files in common locations
    local common_locations=("/usr/local/bin/netscan" "/usr/bin/netscan" "/bin/netscan")
    for location in "${common_locations[@]}"; do
        if [[ -f "$location" ]]; then
            log_warn "Found netscan binary at: $location (not managed by this script)"
            cleaned=true
        fi
    done

    if [[ "$cleaned" == "false" ]]; then
        log_info "No additional artifacts found ✓"
    fi
}

# Verify complete removal
verify_cleanup() {
    log_info "Verifying complete removal"

    local issues_found=false

    # Check if service still exists
    if systemctl list-units --all | grep -q "netscan.service"; then
        log_error "Service still exists"
        issues_found=true
    fi

    # Check if user still exists
    if id "$SERVICE_USER" &>/dev/null; then
        log_error "Service user still exists"
        issues_found=true
    fi

    # Check if install directory still exists
    if [[ -d "$INSTALL_DIR" ]]; then
        log_error "Installation directory still exists: $INSTALL_DIR"
        issues_found=true
    fi

    if [[ "$issues_found" == "true" ]]; then
        log_error "Some components were not removed successfully"
        log_error "Please check the errors above and clean up manually if needed"
        exit 1
    else
        log_info "Complete removal verified ✓"
    fi
}

# Main execution
main() {
    log_info "Starting netscan uninstallation..."

    stop_service
    remove_service_file
    remove_capabilities
    remove_install_dir
    remove_service_user
    cleanup_artifacts
    verify_cleanup

    log_info "netscan has been completely uninstalled from the system."
    log_info "All files, users, and services have been removed."
}

# Show usage if help requested
if [[ "${1:-}" == "--help" ]] || [[ "${1:-}" == "-h" ]]; then
    echo "netscan uninstallation script"
    echo ""
    echo "This script completely removes netscan from the system, including:"
    echo "  - Systemd service stop/disable/removal"
    echo "  - Service user removal"
    echo "  - Installation directory removal (/opt/netscan)"
    echo "  - Binary capability removal"
    echo "  - Systemd daemon reload"
    echo ""
    echo "Usage: sudo ./undeploy.sh"
    echo "       sudo ./undeploy.sh --help"
    exit 0
fi

# Run main function
main "$@"