# netscan - Complete Manual

This manual provides comprehensive documentation for netscan, a production-grade network monitoring service.

**Contents:**
* **Part I: Deployment Guide** - Complete deployment instructions for Docker and Native deployments
* **Part II: Reference Documentation** - Configuration reference, InfluxDB schema, and health check API

---


# Part I: Deployment Guide

## Overview

netscan is a production-grade Go network monitoring service that performs automated network device discovery and continuous uptime monitoring. The service operates through a multi-ticker event-driven architecture that concurrently executes six independent monitoring workflows:

1. **ICMP Discovery**: Periodic ICMP ping sweeps for device discovery with randomized scanning
2. **Pinger Reconciliation**: Automatic lifecycle management ensuring all devices have active ping monitoring
3. **SNMP Poller Reconciliation**: Automatic lifecycle management ensuring all devices have active SNMP polling
4. **State Pruning**: Removal of stale devices not seen in 24 hours
5. **Health Reporting**: Continuous metrics export to InfluxDB health bucket
6. **Background Operations**: SNMP enrichment for newly discovered devices

All discovered devices are stored in a central StateManager (the single source of truth), and all metrics are written to InfluxDB v2 using an optimized batching system. The service implements comprehensive concurrency safety through mutexes, context-based cancellation, WaitGroups, and panic recovery throughout all goroutines. Deployment is supported via Docker Compose with InfluxDB or native systemd installation with capability-based security.

**Deployment Options:**
- **Docker Deployment (Recommended)** - Easiest path with automatic orchestration
- **Native systemd Deployment (Alternative)** - Maximum security with capability-based isolation

---

## Section 1: Docker Deployment (Recommended)

Docker deployment provides the easiest path to get netscan running with automatic orchestration of the complete stack (netscan + InfluxDB).

### Prerequisites

* **Docker Engine** 20.10 or later
* **Docker Compose** V2 (comes with Docker Desktop or install separately)
* **Network access** to target devices for ICMP and SNMP
* **Host network access** (for ICMP raw sockets - see Architecture Notes below)

### Installation Steps

#### 1. Clone Repository

```bash
git clone https://github.com/kljama/netscan.git
cd netscan
```

#### 2. Create Configuration File

```bash
cp config.yml.example config.yml
```

**CRITICAL:** Edit `config.yml` and update the `networks` section with your actual network ranges:

```yaml
networks:
  - "192.168.1.0/24"    # YOUR actual network range
  - "10.0.50.0/24"      # Add additional ranges as needed
```

⚠️ **Important:** The example networks (192.168.0.0/24) are placeholders. If these don't match your network, netscan will find 0 devices. Use `ip addr` (Linux) or `ipconfig` (Windows) to determine your network range.

#### 3. Configure Credentials (Optional but Recommended for Production)

For production security, create a `.env` file to override default credentials:

```bash
cp .env.example .env
chmod 600 .env
```

Edit `.env` and set secure values:

```bash
# InfluxDB Token (generate with: openssl rand -base64 32)
INFLUXDB_TOKEN=<your-secure-token>
DOCKER_INFLUXDB_INIT_ADMIN_TOKEN=<same-as-INFLUXDB_TOKEN>

# InfluxDB Admin Password
DOCKER_INFLUXDB_INIT_PASSWORD=<strong-password>

# SNMP Community String (change from default 'public')
SNMP_COMMUNITY=<your-snmp-community>
```

The `.env` file is automatically loaded by Docker Compose. Variables are expanded in `config.yml` using syntax like `${INFLUXDB_TOKEN}`.

**Default credentials (for testing only):**
- InfluxDB Token: `netscan-token`
- InfluxDB Admin: `admin` / `admin123`
- SNMP Community: `public`

> **⚠️ CRITICAL: Changing Credentials After First Deployment**
>
> InfluxDB only reads the `DOCKER_INFLUXDB_INIT_*` environment variables during **initial setup** when the `influxdbv2-data` volume is empty. Once InfluxDB has been initialized, these variables are **ignored** on subsequent starts because the database already exists.
>
> **If you need to change InfluxDB credentials after the first deployment:**
> 1. **Stop and destroy the database volume** (this will **permanently delete all monitoring data**):
>    ```bash
>    docker compose down -v
>    ```
> 2. Update your `.env` file with new credentials
> 3. Start the stack again (InfluxDB will re-initialize with new credentials):
>    ```bash
>    docker compose up -d
>    ```
>
> **Alternative (preserving data):** Instead of destroying the volume, you can change credentials directly in the InfluxDB UI (https://localhost) and then update the `.env` file to match. However, this is more complex and error-prone. For testing environments, using `docker compose down -v` is simpler.

#### 4. Start the Stack

```bash
docker compose up -d
```

This command:
- Builds the netscan Docker image from the local Dockerfile (multi-stage build)
- Starts InfluxDB v2.7 container with automatic initialization
- Starts netscan container with health checks
- Creates persistent volume for InfluxDB data

#### 5. Verify Operation

```bash
# Check container status (both should be 'Up' and 'healthy')
docker compose ps

# View netscan logs in real-time
docker compose logs -f netscan

# Check health endpoint (requires jq for pretty JSON)
curl http://localhost:8080/health | jq

# Alternative: check without jq
curl http://localhost:8080/health
```

Expected output from health endpoint:
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "5m30s",
  "device_count": 15,
  "active_pingers": 15,
  "influxdb_ok": true,
  ...
}
```

#### 6. Access InfluxDB UI (Optional)

Navigate to **https://localhost** in your browser:

> **⚠️ Self-Signed Certificate Warning:** You will see a browser security warning because the SSL certificate is self-signed (for local development/testing). This is expected and safe for local use. 
>
> **To proceed:**
> - **Chrome/Edge:** Click "Advanced" → "Proceed to localhost (unsafe)"
> - **Firefox:** Click "Advanced" → "Accept the Risk and Continue"
> - **Safari:** Click "Show Details" → "visit this website"

- **Username:** `admin`
- **Password:** `admin123` (or your `.env` value)
- **Organization:** `test-org`
- **Primary Bucket:** `netscan` (ping results and device info)
- **Health Bucket:** `health` (application metrics)

### Service Management

```bash
# Stop services (keeps data volumes)
docker compose stop

# Start services again
docker compose start

# Restart services (useful after config changes)
docker compose restart netscan

# View logs for specific service
docker compose logs -f netscan
docker compose logs -f influxdb

# Stop and remove containers (keeps volumes)
docker compose down

# Stop and remove containers + volumes (DELETES ALL DATA)
docker compose down -v

# Rebuild and restart after code changes
docker compose up -d --build
```

### Docker Architecture Notes

#### Why `network_mode: host`?

The netscan service uses `network_mode: host` in `docker-compose.yml` to access the host's network stack directly. This is **required** for two reasons:

1. **ICMP Raw Sockets:** ICMP ping requires raw socket access, which needs direct access to the host network interfaces
2. **Network Discovery:** To discover devices on local subnets (192.168.x.x, 10.x.x.x), netscan needs to see the actual network topology

**Trade-off:** The container shares the host's network namespace, so port 8080 (health check) is exposed on the host. This is acceptable for a monitoring service but means you cannot run multiple netscan instances on the same host.

#### Why `cap_add: NET_RAW`?

The `NET_RAW` capability grants permission to create raw ICMP sockets. This is defined in `docker-compose.yml`:

```yaml
cap_add:
  - NET_RAW
```

The Dockerfile also sets this capability on the binary:
```dockerfile
RUN setcap cap_net_raw+ep /app/netscan
```

**Security Note:** Even with `CAP_NET_RAW` capability, the container runs as `root` user. This is a Linux kernel limitation - non-root users cannot create raw ICMP sockets in Docker containers despite capability grants. This is documented in the Dockerfile (lines 48-51) as an accepted security trade-off for ICMP functionality.

#### Log Rotation

Docker Compose configures automatic log rotation to prevent disk space exhaustion:

```yaml
logging:
  driver: json-file
  options:
    max-size: "10m"  # Maximum size of a single log file
    max-file: "3"    # Keep 3 most recent log files (~30MB total)
```

This ensures logs don't grow indefinitely while preserving recent history for debugging.

#### Health Checks

Both services have health checks configured:

**InfluxDB Health Check:**
```yaml
healthcheck:
  test: ["CMD", "influx", "ping"]
  interval: 10s
  timeout: 5s
  retries: 5
  start_period: 30s
```

**netscan Health Check:**
```yaml
healthcheck:
  test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/health/live"]
  interval: 30s
  timeout: 3s
  retries: 3
  start_period: 40s
```

The netscan container waits for InfluxDB to be healthy before starting:
```yaml
depends_on:
  influxdb:
    condition: service_healthy
```

### Troubleshooting

#### Issue: "0 devices found" in logs

**Cause:** Network ranges in `config.yml` don't match your actual network.

**Solution:**
1. Find your network range: `ip addr` (Linux) or `ipconfig` (Windows)
2. Update `networks` in `config.yml` with correct CIDR notation
3. Restart: `docker compose restart netscan`

**Example:** If your IP is `192.168.1.50` with subnet mask `255.255.255.0`, use `192.168.1.0/24`

#### Issue: "InfluxDB connection failed" on startup

**Cause:** InfluxDB not ready or credentials mismatch.

**Solution:**
1. Check InfluxDB is healthy: `docker compose ps` (should show "healthy")
2. Check InfluxDB logs: `docker compose logs influxdb`
3. Verify token in `.env` matches between `INFLUXDB_TOKEN` and `DOCKER_INFLUXDB_INIT_ADMIN_TOKEN`
4. **If credentials were changed after initial deployment:** You must destroy and recreate the database volume because InfluxDB only initializes credentials on first run. Run:
   ```bash
   docker compose down -v && docker compose up -d
   ```
   **Warning:** This will permanently delete all existing monitoring data.

#### Issue: Changed `.env` credentials but InfluxDB still uses old password

**Cause:** InfluxDB persistent volume already exists with old credentials. InfluxDB only runs its initialization script (using `DOCKER_INFLUXDB_INIT_*` variables) on the **first run** when the `influxdbv2-data` volume is empty.

**Solution:**
To apply new credentials from your `.env` file, you must destroy the existing database volume and force InfluxDB to re-initialize:

```bash
# Stop services and destroy database volume (DELETES ALL DATA)
docker compose down -v

# Start services with new credentials
docker compose up -d
```

**⚠️ Warning:** The `-v` flag permanently deletes the `influxdbv2-data` volume containing all historical monitoring data. Only use this if you need to reset credentials or start fresh.

**Alternative (preserving data):** Change credentials directly in the InfluxDB UI at https://localhost, then update your `.env` file to match the new credentials.

#### Issue: Health check endpoint returns 503 "NOT READY"

**Cause:** Service started but InfluxDB connectivity failing.

**Solution:**
1. Check `/health/ready` endpoint: `curl http://localhost:8080/health/ready`
2. Check `/health` for details: `curl http://localhost:8080/health | jq .influxdb_ok`
3. Verify InfluxDB is accessible via HTTPS proxy: `curl -k https://localhost/health`
4. Check Docker network connectivity: `docker exec netscan wget -qO- http://influxdb:8086/health`

#### Issue: Permission denied errors for ICMP

**Cause:** Container doesn't have NET_RAW capability or not running as root.

**Solution:**
1. Verify capability in docker-compose.yml: `cap_add: - NET_RAW`
2. Check container is running as root (this is required, not a bug)
3. Restart containers: `docker compose restart netscan`

#### Issue: High memory usage

**Cause:** Monitoring too many devices or rate limits too high.

**Solution:**
1. Check device count: `curl http://localhost:8080/health | jq .device_count`
2. Reduce network ranges in `config.yml`
3. Lower `ping_rate_limit` and `ping_burst_limit` in `config.yml`
4. Increase `memory_limit_mb` if devices are legitimate
5. Restart: `docker compose restart netscan`

#### Issue: Containers exit immediately

**Cause:** Configuration error or missing files.

**Solution:**
1. Check logs: `docker compose logs netscan`
2. Verify `config.yml` exists and is valid YAML
3. Ensure `.env` file has no syntax errors
4. Try starting in foreground: `docker compose up` (without `-d`)

### Cleaning Up

To completely remove netscan and all data:

```bash
# Stop and remove all containers and volumes
docker compose down -v

# Remove Docker images
docker rmi netscan:latest influxdb:2.7

# Remove any orphaned volumes
docker volume prune
```

---

## Section 2: Native systemd Deployment (Alternative)

Native systemd deployment provides maximum security through capability-based isolation and dedicated system users. This is the recommended deployment for security-conscious production environments.

### Prerequisites

* **Go** 1.25 or later
* **InfluxDB** v2.x running and accessible (local or remote)
* **systemd** (most modern Linux distributions)
* **libcap** package for setcap command
* **Root/sudo access** for installation

### Verifying Prerequisites

```bash
# Check Go version (should be 1.25+)
go version

# Check systemd
systemctl --version

# Check if setcap is available
which setcap

# Verify InfluxDB is running (if local)
curl http://localhost:8086/health
```

### Installation Using deploy.sh

The `deploy.sh` script automates the entire installation process with proper security hardening.

#### 1. Clone and Prepare

```bash
git clone https://github.com/kljama/netscan.git
cd netscan
```

#### 2. Configure Application

```bash
# Copy configuration template
cp config.yml.example config.yml

# Edit configuration with your network ranges and InfluxDB details
nano config.yml  # or vim, vi, etc.
```

**Required changes in `config.yml`:**
- `networks`: Your actual CIDR ranges
- `influxdb.url`: InfluxDB server URL (e.g., `http://localhost:8086`)
- `influxdb.token`: Use `${INFLUXDB_TOKEN}` for environment variable expansion
- `snmp.community`: Use `${SNMP_COMMUNITY}` for environment variable expansion

#### 3. Run Deployment Script

```bash
sudo deploy/deploy.sh
```

**What the script does:**

1. **Go Version Check:** Verifies Go 1.21+ is installed
2. **Binary Build:** Compiles netscan binary from source
3. **Service User Creation:** Creates dedicated `netscan` system user
   - System account (UID < 1000)
   - No shell access (`/bin/false`)
   - No home directory
   - Cannot login
4. **File Installation:**
   - Creates `/opt/netscan/` directory
   - Installs binary to `/opt/netscan/netscan`
   - Copies `config.yml` to `/opt/netscan/config.yml`
   - Creates `/opt/netscan/.env` with secure environment variables
5. **Permission Setting:**
   - Binary: `755` (executable)
   - .env file: `600` (owner read/write only)
   - Ownership: `netscan:netscan`
6. **Capability Grant:** Sets `cap_net_raw+ep` on binary for ICMP access
7. **systemd Service Creation:** Installs and enables service
8. **Service Start:** Starts netscan service immediately

**Expected output:**
```
[INFO] Go 1.25.1 found ✓
[INFO] Building netscan binary...
[INFO] Binary built successfully ✓
[INFO] Creating service user: netscan
[INFO] Service user created successfully ✓
[INFO] Installing files to /opt/netscan
[INFO] .env file created with secure placeholders ✓
[INFO] Files installed successfully ✓
[INFO] Setting ownership and permissions
[INFO] .env file permissions set to 600 ✓
[INFO] Permissions set successfully ✓
[INFO] Setting CAP_NET_RAW capability for ICMP access
[INFO] Capabilities set successfully ✓
[INFO] Creating systemd service
[INFO] Systemd service created ✓
[INFO] Enabling and starting systemd service
[INFO] Service enabled and started successfully ✓
[INFO] netscan deployed and running as a systemd service
```

#### 4. Configure Environment Variables

Edit `/opt/netscan/.env` with your actual credentials:

```bash
sudo nano /opt/netscan/.env
```

**Required values:**
```bash
# InfluxDB credentials
INFLUXDB_TOKEN=your-actual-influxdb-token
INFLUXDB_ORG=your-org-name

# SNMP community string
SNMP_COMMUNITY=your-snmp-community
```

After editing, restart the service:
```bash
sudo systemctl restart netscan
```

### Security Model

The native deployment provides significantly better security than Docker:

#### 1. Dedicated System User

```bash
# Created by deploy.sh
useradd -r -s /bin/false netscan
```

- `-r`: System account (non-interactive, UID < 1000)
- `-s /bin/false`: Prevents shell login
- No password set (cannot login)
- Principle of least privilege

#### 2. Capability-Based Security

Instead of running as root, the binary is granted only the specific capability it needs:

```bash
# Applied by deploy.sh
setcap cap_net_raw+ep /opt/netscan/netscan
```

- `cap_net_raw`: Allows raw ICMP socket creation
- `+ep`: Effective and Permitted flags
- Capability persists across executions
- Much safer than full root privileges

You can verify the capability:
```bash
getcap /opt/netscan/netscan
# Output: /opt/netscan/netscan = cap_net_raw+ep
```

#### 3. systemd Service Hardening

The generated systemd service (`/etc/systemd/system/netscan.service`) includes multiple security hardening directives:

```ini
[Service]
Type=simple
User=netscan
Group=netscan
ExecStart=/opt/netscan/netscan
WorkingDirectory=/opt/netscan

# Environment variables from secure file
EnvironmentFile=/opt/netscan/.env

# Security hardening
NoNewPrivileges=yes          # Prevents privilege escalation
PrivateTmp=yes               # Isolated /tmp directory
ProtectSystem=strict         # Read-only filesystem except /opt/netscan
AmbientCapabilities=CAP_NET_RAW  # Only grant needed capability
```

#### 4. Secure Credential Storage

The `.env` file is protected:
- Permissions: `600` (owner read/write only)
- Owner: `netscan:netscan`
- Contains sensitive tokens and credentials
- Automatically loaded by systemd via `EnvironmentFile` directive
- Not readable by other users

**Comparison with Docker:**

| Security Aspect | Native systemd | Docker |
|----------------|----------------|---------|
| User privileges | Dedicated non-root user | root (required) |
| Capability model | Single capability (CAP_NET_RAW) | Full CAP_NET_RAW |
| Filesystem | ProtectSystem=strict | Container isolation |
| Shell access | /bin/false (disabled) | N/A |
| Tmp isolation | PrivateTmp=yes | N/A |
| Privilege escalation | NoNewPrivileges=yes | N/A |

### Service Management

#### Start/Stop/Restart

```bash
# Start service
sudo systemctl start netscan

# Stop service
sudo systemctl stop netscan

# Restart service (after config changes)
sudo systemctl restart netscan

# Check if service is running
sudo systemctl is-active netscan
```

#### Enable/Disable Auto-Start

```bash
# Enable auto-start on boot (done by deploy.sh)
sudo systemctl enable netscan

# Disable auto-start
sudo systemctl disable netscan

# Check if enabled
sudo systemctl is-enabled netscan
```

#### View Status

```bash
# Detailed status with recent log entries
sudo systemctl status netscan

# Example output:
● netscan.service - netscan network monitoring service
     Loaded: loaded (/etc/systemd/system/netscan.service; enabled)
     Active: active (running) since Mon 2024-01-15 10:30:45 UTC; 2h ago
   Main PID: 1234 (netscan)
      Tasks: 25
     Memory: 45.2M
        CPU: 1min 30s
     CGroup: /system.slice/netscan.service
             └─1234 /opt/netscan/netscan
```

#### View Logs

```bash
# Follow logs in real-time (recommended)
sudo journalctl -u netscan -f

# View last 100 lines
sudo journalctl -u netscan -n 100

# View logs since last boot
sudo journalctl -u netscan -b

# View logs from specific time
sudo journalctl -u netscan --since "1 hour ago"
sudo journalctl -u netscan --since "2024-01-15 10:00:00"

# View logs with priority level (errors only)
sudo journalctl -u netscan -p err

# Export logs to file
sudo journalctl -u netscan > netscan.log
```

#### Configuration Changes

After modifying `/opt/netscan/config.yml` or `/opt/netscan/.env`:

```bash
# Restart to apply changes
sudo systemctl restart netscan

# Verify service restarted successfully
sudo systemctl status netscan

# Check logs for errors
sudo journalctl -u netscan -f
```

### Uninstallation Using undeploy.sh

The `undeploy.sh` script safely removes netscan and all associated files:

```bash
sudo deploy/undeploy.sh
```

**What the script does:**

1. **Stop Service:** Gracefully stops running service
2. **Disable Service:** Removes from auto-start
3. **Remove Service File:** Deletes `/etc/systemd/system/netscan.service`
4. **Reload systemd:** Updates systemd daemon
5. **Remove Capabilities:** Clears capabilities from binary
6. **Delete Installation Directory:** Removes `/opt/netscan/` and all contents
7. **Remove Service User:** Deletes `netscan` system user
8. **Verify Cleanup:** Confirms complete removal

**Expected output:**
```
[INFO] Stopping and disabling netscan service
[INFO] Service stopped ✓
[INFO] Service disabled ✓
[INFO] Removing systemd service file
[INFO] Service file removed ✓
[INFO] Systemd daemon reloaded ✓
[INFO] Removing capabilities from binary
[INFO] Capabilities removed ✓
[INFO] Removing installation directory: /opt/netscan
[INFO] Installation directory removed (45M) ✓
[INFO] Removing service user: netscan
[INFO] Service user removed ✓
[INFO] No additional artifacts found ✓
[INFO] Complete removal verified ✓
[INFO] netscan has been completely uninstalled
```

### Manual Installation (Advanced)

If you prefer manual installation or need to customize:

```bash
# 1. Build binary
go build -o netscan ./cmd/netscan

# 2. Create user
sudo useradd -r -s /bin/false netscan

# 3. Create installation directory
sudo mkdir -p /opt/netscan

# 4. Install files
sudo cp netscan /opt/netscan/
sudo cp config.yml /opt/netscan/
sudo cp .env.example /opt/netscan/.env

# 5. Set permissions
sudo chown -R netscan:netscan /opt/netscan
sudo chmod 755 /opt/netscan/netscan
sudo chmod 600 /opt/netscan/.env

# 6. Set capability
sudo setcap cap_net_raw+ep /opt/netscan/netscan

# 7. Create systemd service (see deploy.sh for template)
sudo nano /etc/systemd/system/netscan.service

# 8. Enable and start
sudo systemctl daemon-reload
sudo systemctl enable netscan
sudo systemctl start netscan
```

### Troubleshooting

#### Issue: "permission denied" when creating raw socket

**Cause:** Binary doesn't have CAP_NET_RAW capability.

**Solution:**
```bash
# Check current capabilities
getcap /opt/netscan/netscan

# If missing, set capability
sudo setcap cap_net_raw+ep /opt/netscan/netscan

# Restart service
sudo systemctl restart netscan
```

#### Issue: Service fails to start

**Cause:** Configuration error or permission issue.

**Solution:**
```bash
# Check service status
sudo systemctl status netscan

# View detailed logs
sudo journalctl -u netscan -n 50

# Common issues:
# - config.yml syntax error: Validate YAML
# - InfluxDB unreachable: Check URL and network
# - Permission issue: Verify ownership is netscan:netscan
```

#### Issue: "0 devices found"

**Cause:** Network ranges don't match actual network.

**Solution:**
```bash
# Edit config
sudo nano /opt/netscan/config.yml

# Update networks section
networks:
  - "your-actual-network/24"

# Restart
sudo systemctl restart netscan
```

#### Issue: InfluxDB connection failed

**Cause:** Wrong credentials or InfluxDB not accessible.

**Solution:**
```bash
# Check InfluxDB is running
curl http://localhost:8086/health

# Verify token in .env file
sudo cat /opt/netscan/.env

# Test connectivity
curl -H "Authorization: Token YOUR_TOKEN" \
  http://localhost:8086/api/v2/buckets

# Update .env if needed
sudo nano /opt/netscan/.env

# Restart
sudo systemctl restart netscan
```

#### Issue: High CPU or memory usage

**Cause:** Monitoring too many devices or aggressive intervals.

**Solution:**
```bash
# Check metrics
curl http://localhost:8080/health

# Adjust config.yml:
# - Increase ping_interval
# - Reduce networks scope
# - Lower icmp_workers/snmp_workers
# - Adjust ping_rate_limit

sudo nano /opt/netscan/config.yml
sudo systemctl restart netscan
```

### Maintenance

#### Updating netscan

```bash
# 1. Stop service
sudo systemctl stop netscan

# 2. Backup current binary and config
sudo cp /opt/netscan/netscan /opt/netscan/netscan.backup
sudo cp /opt/netscan/config.yml /opt/netscan/config.yml.backup

# 3. Pull latest code
cd /path/to/netscan
git pull origin main

# 4. Rebuild
go build -o netscan ./cmd/netscan

# 5. Install new binary
sudo cp netscan /opt/netscan/

# 6. Reset capability (lost during copy)
sudo setcap cap_net_raw+ep /opt/netscan/netscan

# 7. Check for config changes
diff config.yml.example /opt/netscan/config.yml

# 8. Update config if needed
sudo nano /opt/netscan/config.yml

# 9. Restart
sudo systemctl start netscan

# 10. Verify
sudo systemctl status netscan
sudo journalctl -u netscan -f
```

#### Log Rotation

systemd journal handles log rotation automatically, but you can configure retention:

```bash
# Check current journal size
sudo journalctl --disk-usage

# Configure retention in /etc/systemd/journald.conf:
# SystemMaxUse=1G
# SystemKeepFree=2G
# MaxRetentionSec=1month

# Manually clean old logs
sudo journalctl --vacuum-time=7d
sudo journalctl --vacuum-size=500M
```

---

**End of Part I: Deployment Guide**


# Part II: Reference Documentation

## 1. Configuration Reference

This section provides a complete reference for all configuration parameters in `config.yml`.

### Configuration File Format

netscan uses YAML format for configuration. The configuration file supports:
- Duration strings (e.g., `"5m"`, `"30s"`, `"1h30m"`)
- Environment variable expansion using `${VAR_NAME}` syntax
- Sensible defaults for most parameters

### Environment Variable Expansion

Configuration values can reference environment variables using the syntax `${VAR_NAME}` or `$VAR_NAME`. This is particularly useful for sensitive credentials that shouldn't be hardcoded.

**Supported in:**
- `influxdb.url`
- `influxdb.token`
- `influxdb.org`
- `influxdb.bucket`
- `influxdb.health_bucket`
- `snmp.community`

**Example:**
```yaml
influxdb:
  token: "${INFLUXDB_TOKEN}"  # Expanded from environment variable
  org: "${INFLUXDB_ORG}"
  
snmp:
  community: "${SNMP_COMMUNITY}"
```

**Setting environment variables:**

Docker Compose (via `.env` file):
```bash
INFLUXDB_TOKEN=my-secret-token
INFLUXDB_ORG=my-org
SNMP_COMMUNITY=private-community
```

Native systemd (via `/opt/netscan/.env`):
```bash
export INFLUXDB_TOKEN=my-secret-token
export INFLUXDB_ORG=my-org
export SNMP_COMMUNITY=private-community
```

### Complete Parameter Reference

#### Network Discovery Settings

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|
| `networks` | `[]string` | *(none)* | **Yes** | List of CIDR network ranges to scan for devices (e.g., `["192.168.1.0/24", "10.0.0.0/24"]`). **Critical:** Must match your actual network or netscan will find 0 devices. |
| `icmp_discovery_interval` | `duration` | *(none)* | **Yes** | How often to run ICMP discovery sweeps to find new devices (e.g., `"5m"` for 5 minutes). Minimum: 1 minute. **Note:** Scans only usable host IPs (excludes network and broadcast addresses for /30 and larger networks); IPs are scanned in randomized order to obscure the scanning pattern. |

#### Continuous SNMP Polling Settings

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|
| `snmp_interval` | `duration` | `"1h"` | No | How often to poll each device for SNMP metadata (hostname and sysDescr). Continuous per-device polling (not batch). Minimum: 1 minute. |
| `snmp_rate_limit` | `float64` | `10.0` | No | Global SNMP query rate limit in queries per second (token bucket rate). Controls sustained SNMP traffic across all devices. |
| `snmp_burst_limit` | `int` | `50` | No | SNMP query burst capacity (token bucket size). Allows short bursts above sustained rate. Should be >= `snmp_rate_limit`. |
| `snmp_max_consecutive_fails` | `int` | `5` | No | SNMP circuit breaker threshold. Number of consecutive SNMP failures before suspending SNMP polling for a device. |
| `snmp_backoff_duration` | `duration` | `"1h"` | No | SNMP circuit breaker suspension duration. How long to suspend SNMP polling after reaching failure threshold. Minimum: 1 minute. |

#### SNMP Connection Settings

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|
| `snmp.community` | `string` | *(none)* | **Yes** | SNMPv2c community string for device authentication. Supports environment variable expansion. Default in docker-compose: `"public"`. **Production:** Change to secure value. |
| `snmp.port` | `int` | *(none)* | **Yes** | SNMP port number. Standard: `161`. |
| `snmp.timeout` | `duration` | `"5s"` | No | Timeout for individual SNMP requests. |
| `snmp.retries` | `int` | *(none)* | **Yes** | Number of retry attempts for failed SNMP requests. Recommended: `1` to `3`. |

#### Monitoring Settings

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|
| `ping_interval` | `duration` | *(none)* | **Yes** | Time between continuous pings for each monitored device (e.g., `"2s"`). Minimum: 1 second. Lower values increase network traffic and CPU usage. |
| `ping_timeout` | `duration` | `"3s"` | No | Maximum time to wait for ICMP echo reply. Should be less than `ping_interval`. |
| `ping_rate_limit` | `float64` | `64.0` | No | Sustained ping rate in pings per second across all devices (token bucket rate). Controls global ping rate to prevent network flooding. |
| `ping_burst_limit` | `int` | `256` | No | Maximum burst ping capacity (token bucket size). Allows short bursts above sustained rate. |

#### Performance Tuning Settings

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|
| `icmp_workers` | `int` | `64` | No | Number of concurrent goroutines for ICMP discovery sweeps. **Tuning:** Small networks (<500 devices): 64; Medium (500-2000): 128; Large (2000+): 256. **Warning:** Values >256 may cause kernel socket buffer overflow. |
| `snmp_workers` | `int` | `32` | No | Number of concurrent goroutines for SNMP polling. **Recommended:** 25-50% of `icmp_workers` to avoid overwhelming SNMP agents. |

#### InfluxDB Settings

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|
| `influxdb.url` | `string` | *(none)* | **Yes** | InfluxDB server URL. Must use `http://` or `https://` scheme. Example: `"http://localhost:8086"`. Supports environment variable expansion. |
| `influxdb.token` | `string` | *(none)* | **Yes** | InfluxDB authentication token. **Security:** Use environment variable expansion: `"${INFLUXDB_TOKEN}"`. Never hardcode tokens. |
| `influxdb.org` | `string` | *(none)* | **Yes** | InfluxDB organization name. Supports environment variable expansion. |
| `influxdb.bucket` | `string` | *(none)* | **Yes** | Primary bucket for ping results and device info metrics. |
| `influxdb.health_bucket` | `string` | `"health"` | No | Bucket for application health metrics (device count, memory usage, etc.). |
| `influxdb.batch_size` | `int` | `5000` | No | Number of data points to accumulate before writing to InfluxDB. Higher values reduce write frequency but increase memory usage. Range: 100-10000. |
| `influxdb.flush_interval` | `duration` | `"5s"` | No | Maximum time to hold points before flushing to InfluxDB, even if batch not full. Ensures timely data delivery. |

#### Health Check Settings

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|
| `health_check_port` | `int` | `8080` | No | HTTP port for health check endpoints. Provides `/health`, `/health/ready`, and `/health/live` endpoints for monitoring and container orchestration. |
| `health_report_interval` | `duration` | `"10s"` | No | How often to write application health metrics to InfluxDB health bucket. |

#### Resource Protection Settings

These limits prevent resource exhaustion and DoS attacks.

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|
| `max_concurrent_pingers` | `int` | `20000` | No | Maximum number of concurrent pinger goroutines. Each monitored device has one pinger. Prevents goroutine exhaustion. |
| `max_concurrent_snmp_pollers` | `int` | `20000` | No | Maximum number of concurrent SNMP poller goroutines. Each monitored device has one SNMP poller. Prevents goroutine exhaustion. |
| `max_devices` | `int` | `20000` | No | Maximum devices managed by StateManager. When limit reached, oldest devices (by LastSeen) are evicted (LRU). |
| `min_scan_interval` | `duration` | `"1m"` | No | Minimum time between ICMP discovery scans. Prevents scan storms. |
| `memory_limit_mb` | `int` | `16384` | No | Memory usage warning threshold in MB. Logs warning when exceeded but doesn't stop operation. Used for monitoring and capacity planning. |

#### Legacy/Deprecated Parameters

| Parameter | Type | Default | Required | Description |
|-----------|------|---------|----------|-------------|

### Configuration Examples

#### Minimal Configuration (Development)

```yaml
networks:
  - "127.0.0.1/32"  # Localhost only

icmp_discovery_interval: "1m"
ping_interval: "5s"

snmp:
  community: "public"
  port: 161
  timeout: "5s"
  retries: 1

influxdb:
  url: "http://localhost:8086"
  token: "test-token"
  org: "test-org"
  bucket: "netscan"
```

#### Production Configuration (Small Network)

```yaml
networks:
  - "192.168.1.0/24"
  - "192.168.2.0/24"

icmp_discovery_interval: "5m"

# Continuous SNMP polling (replaces daily batch scan)
snmp_interval: "1h"
snmp_rate_limit: 10.0
snmp_burst_limit: 50
snmp_max_consecutive_fails: 5
snmp_backoff_duration: "1h"

snmp:
  community: "${SNMP_COMMUNITY}"  # From environment
  port: 161
  timeout: "5s"
  retries: 2

ping_interval: "2s"
ping_timeout: "3s"
ping_rate_limit: 100.0
ping_burst_limit: 500

icmp_workers: 64
snmp_workers: 32  # Used for initial SNMP scan on discovery

influxdb:
  url: "http://influxdb.internal:8086"
  token: "${INFLUXDB_TOKEN}"  # From environment
  org: "${INFLUXDB_ORG}"
  bucket: "netscan"
  health_bucket: "health"
  batch_size: 5000
  flush_interval: "5s"

health_check_port: 8080
health_report_interval: "10s"

max_concurrent_pingers: 5000
max_concurrent_snmp_pollers: 5000
max_devices: 5000
memory_limit_mb: 4096
```

#### Production Configuration (Large Network)

```yaml
networks:
  - "10.0.0.0/16"     # Large corporate network
  - "172.16.0.0/16"   # Data center

icmp_discovery_interval: "10m"  # Slower discovery for large network

# Continuous SNMP polling tuned for large scale
snmp_interval: "2h"  # Poll less frequently (many devices)
snmp_rate_limit: 50.0  # Higher rate for many devices
snmp_burst_limit: 200
snmp_max_consecutive_fails: 10  # More tolerant
snmp_backoff_duration: "2h"     # Longer backoff

snmp:
  community: "${SNMP_COMMUNITY}"
  port: 161
  timeout: "10s"  # Longer timeout for slow devices
  retries: 3

ping_interval: "5s"  # Longer interval to reduce load
ping_timeout: "4s"
ping_rate_limit: 500.0  # Higher rate for many devices
ping_burst_limit: 2000

icmp_workers: 256  # Maximum recommended
snmp_workers: 128  # Used for initial SNMP scan on discovery (50% of icmp_workers)

influxdb:
  url: "https://influxdb-cluster.internal:8086"
  token: "${INFLUXDB_TOKEN}"
  org: "${INFLUXDB_ORG}"
  bucket: "netscan-prod"
  health_bucket: "health-prod"
  batch_size: 10000  # Larger batches for efficiency
  flush_interval: "10s"

health_check_port: 8080
health_report_interval: "30s"  # Less frequent for large scale

max_concurrent_pingers: 100000      # Support many devices
max_concurrent_snmp_pollers: 100000 # Support many devices
max_devices: 100000
memory_limit_mb: 32768  # 32GB for large deployments
```

---

## 2. InfluxDB Schema Reference

netscan writes data to InfluxDB v2 using three distinct measurements. Understanding the schema is essential for creating custom queries and dashboards.

### Measurement: `ping`

Stores ICMP ping results for continuous uptime monitoring.

**Bucket:** Primary bucket (configured via `influxdb.bucket`)

**Frequency:** Written every `ping_interval` per device (e.g., every 2 seconds per device)

**Tags:**
| Tag | Type | Description | Example |
|-----|------|-------------|---------|
| `ip` | string | IPv4 address of the monitored device | `"192.168.1.100"` |

**Fields:**
| Field | Type | Unit | Description | Example |
|-------|------|------|-------------|---------|
| `rtt_ms` | float64 | milliseconds | Round-trip time for successful pings. `0.0` for failed pings. | `12.5` |
| `success` | bool | n/a | Ping success status. `true` if device responded, `false` if timeout. | `true` |

**Timestamp:** Time when ping was executed (not when response received)

**Example Data Points:**
```
# Normal successful ping
ping,ip=192.168.1.100 rtt_ms=12.5,success=true 1698765432000000000

# Failed ping (timeout)
ping,ip=192.168.1.100 rtt_ms=0.0,success=false 1698765433000000000
```

**Sample Flux Query (Last 24h ping success rate by device):**
```flux
from(bucket: "netscan")
  |> range(start: -24h)
  |> filter(fn: (r) => r._measurement == "ping")
  |> filter(fn: (r) => r._field == "success")
  |> group(columns: ["ip"])
  |> aggregateWindow(every: 1h, fn: mean, createEmpty: false)
```

### Measurement: `device_info`

Stores device metadata collected via SNMP.

**Bucket:** Primary bucket (configured via `influxdb.bucket`)

**Frequency:** 
- Written immediately when device first discovered (background SNMP enrichment)
- Re-written periodically by continuous SNMP polling (default: every 1 hour per device)
- Re-written when SNMP data changes

**Tags:**
| Tag | Type | Description | Example |
|-----|------|-------------|---------|
| `ip` | string | IPv4 address of the device | `"192.168.1.100"` |

**Fields:**
| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `hostname` | string | Device hostname from SNMP sysName (.1.3.6.1.2.1.1.5.0) or IP address if SNMP fails. Sanitized to max 500 chars, control characters removed. | `"switch-office-1"` |
| `snmp_description` | string | Device system description from SNMP sysDescr (.1.3.6.1.2.1.1.1.0). Sanitized to max 500 chars, control characters removed. | `"Cisco IOS Software, C2960 Software"` |

**Timestamp:** Time when SNMP scan completed

**Example Data Point:**
```
device_info,ip=192.168.1.100 hostname="switch-office-1",snmp_description="Cisco IOS Software" 1698765432000000000
```

**Sample Flux Query (Get latest device info for all devices):**
```flux
from(bucket: "netscan")
  |> range(start: -7d)
  |> filter(fn: (r) => r._measurement == "device_info")
  |> group(columns: ["ip"])
  |> last()
  |> pivot(rowKey: ["ip"], columnKey: ["_field"], valueColumn: "_value")
```

### Measurement: `health_metrics`

Stores application health and observability metrics.

**Bucket:** Health bucket (configured via `influxdb.health_bucket`, default: `"health"`)

**Frequency:** Written every `health_report_interval` (default: 10 seconds)

**Tags:** None (application-level metrics, not device-specific)

**Fields:**
| Field | Type | Unit | Description |
|-------|------|------|-------------|
| `device_count` | int | count | Total number of devices currently managed by StateManager |
| `active_pingers` | int | count | Number of pinger goroutines currently running (one per monitored device) |
| `snmp_suspended_devices` | int | count | Number of devices currently suspended by SNMP circuit breaker |
| `goroutines` | int | count | Total Go goroutines in the application (for debugging goroutine leaks) |
| `memory_mb` | int | MB | Go heap memory usage (runtime.MemStats.Alloc) |
| `rss_mb` | int | MB | OS-level resident set size (from `/proc/self/status` VmRSS on Linux) |
| `influxdb_ok` | bool | n/a | InfluxDB connectivity status (`true` if healthy, `false` if down) |
| `influxdb_successful_batches` | uint64 | count | Cumulative count of successful batch writes to InfluxDB since startup |
| `influxdb_failed_batches` | uint64 | count | Cumulative count of failed batch writes to InfluxDB since startup |
| `pings_sent_total` | uint64 | count | Total monitoring pings sent since application startup |

**Timestamp:** Time when metrics collected

**Example Data Point:**
```
health_metrics device_count=150i,active_pingers=150i,snmp_suspended_devices=5i,goroutines=325i,memory_mb=245i,rss_mb=512i,influxdb_ok=true,influxdb_successful_batches=1234u,influxdb_failed_batches=0u,pings_sent_total=456789u 1698765432000000000
```

**Sample Flux Query (Monitor application health over time):**
```flux
from(bucket: "health")
  |> range(start: -1h)
  |> filter(fn: (r) => r._measurement == "health_metrics")
  |> filter(fn: (r) => r._field == "device_count" or r._field == "active_pingers" or r._field == "memory_mb")
  |> aggregateWindow(every: 1m, fn: mean, createEmpty: false)
```

**Memory Metrics Explained:**

- **`memory_mb`** (Go Heap): Memory allocated by Go runtime for heap objects. Only includes Go-managed memory. Does not include stack memory, OS-level overhead, or memory-mapped files.

- **`rss_mb`** (Resident Set Size): Total physical memory used by the process from the OS perspective. Includes Go heap, stacks, memory-mapped files, shared libraries, and OS overhead. More accurate reflection of actual memory consumption. Linux-specific (reads `/proc/self/status`).

### Data Retention Recommendations

**Primary Bucket (ping + device_info):**
- **Short-term monitoring (7-30 days):** Raw data at full resolution
- **Long-term trends (6-12 months):** Downsampled to hourly or daily aggregates

**Health Bucket (health_metrics):**
- **Short-term (7-14 days):** Raw data at 10-second resolution
- **Long-term (90 days):** Downsampled to 1-minute or 5-minute resolution

**Example InfluxDB Retention Policies:**

```bash
# Primary bucket: 30 days retention
influx bucket create \
  -n netscan \
  -o my-org \
  -r 30d

# Health bucket: 14 days retention
influx bucket create \
  -n health \
  -o my-org \
  -r 14d
```

### Common Queries

**Get devices that are currently down:**
```flux
from(bucket: "netscan")
  |> range(start: -5m)
  |> filter(fn: (r) => r._measurement == "ping")
  |> filter(fn: (r) => r._field == "success")
  |> filter(fn: (r) => r._value == false)
  |> group(columns: ["ip"])
  |> last()
```

**Calculate average RTT per device over last hour:**
```flux
from(bucket: "netscan")
  |> range(start: -1h)
  |> filter(fn: (r) => r._measurement == "ping")
  |> filter(fn: (r) => r._field == "rtt_ms")
  |> filter(fn: (r) => r._value > 0.0)  // Only successful pings
  |> group(columns: ["ip"])
  |> mean()
```

**Monitor application resource usage:**
```flux
from(bucket: "health")
  |> range(start: -24h)
  |> filter(fn: (r) => r._measurement == "health_metrics")
  |> filter(fn: (r) => r._field == "memory_mb" or r._field == "rss_mb" or r._field == "goroutines")
  |> aggregateWindow(every: 5m, fn: mean, createEmpty: false)
```

---




---

## 3. Health Check Endpoint Reference

netscan exposes HTTP health check endpoints for monitoring, container orchestration, and operational visibility.

**Base URL:** `http://localhost:8080` (configurable via `health_check_port`)

### Endpoints

#### GET `/health`

**Purpose:** Comprehensive health status with detailed metrics (JSON)

**Response Type:** `application/json`

**HTTP Status Codes:**
- `200 OK` - Service responding (check `status` field in JSON for actual health)

**Response Body:**

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h15m30s",
  "device_count": 150,
  "snmp_suspended_devices": 5,
  "active_pingers": 145,
  "influxdb_ok": true,
  "influxdb_successful": 12345,
  "influxdb_failed": 0,
  "pings_sent_total": 456789,
  "goroutines": 325,
  "memory_mb": 245,
  "rss_mb": 512,
  "timestamp": "2024-01-15T10:30:45Z"
}
```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | Overall service health: `"healthy"` (all systems operational), `"degraded"` (InfluxDB unreachable but monitoring continues), or `"unhealthy"` (critical failure) |
| `version` | string | Application version string (currently hardcoded `"1.0.0"`, TODO: inject at build time) |
| `uptime` | string | Human-readable time since service started (e.g., `"2h15m30s"`) |
| `device_count` | int | Total number of devices currently managed by StateManager |
| `snmp_suspended_devices` | int | Number of devices currently suspended by SNMP circuit breaker |
| `active_pingers` | int | Number of pinger goroutines currently running (one per monitored device) |
| `influxdb_ok` | bool | InfluxDB connectivity status. `true` if InfluxDB health check passes, `false` if unreachable. |
| `influxdb_successful` | uint64 | Cumulative count of successful batch writes to InfluxDB since service startup |
| `influxdb_failed` | uint64 | Cumulative count of failed batch writes to InfluxDB since service startup |
| `pings_sent_total` | uint64 | Total monitoring pings sent across all devices since service startup |
| `goroutines` | int | Current number of Go goroutines in the application. Used for detecting goroutine leaks. Normal range: 100-500 depending on device count. |
| `memory_mb` | uint64 | Go heap memory usage in MB (from `runtime.MemStats.Alloc`). Only includes Go-managed memory. |
| `rss_mb` | uint64 | OS-level resident set size in MB (from `/proc/self/status` VmRSS on Linux). Total physical memory used by process. Returns `0` on non-Linux systems. |
| `timestamp` | string | ISO 8601 timestamp when metrics were collected |

**Usage Examples:**

```bash
# Check service health
curl http://localhost:8080/health | jq

# Extract specific field
curl -s http://localhost:8080/health | jq -r '.status'

# Check if InfluxDB is connected
curl -s http://localhost:8080/health | jq -r '.influxdb_ok'

# Monitor resource usage
watch -n 5 'curl -s http://localhost:8080/health | jq "{memory_mb, rss_mb, goroutines, device_count}"'
```

**Prometheus Scraping (Alternative):**

While netscan doesn't export Prometheus metrics directly, you can use a JSON exporter to scrape the `/health` endpoint.

#### GET `/health/ready`

**Purpose:** Kubernetes/Docker readiness probe (determines if service should receive traffic)

**Response Type:** `text/plain`

**HTTP Status Codes:**
- `200 OK` - Service is ready to accept traffic (InfluxDB accessible)
- `503 Service Unavailable` - Service not ready (InfluxDB unreachable)

**Response Body:**
- Success: `"READY"`
- Failure: `"NOT READY: InfluxDB unavailable"`

**Usage:**

```bash
# Check if service is ready
curl http://localhost:8080/health/ready
echo $?  # 0 if ready, non-zero if not

# Kubernetes readiness probe configuration
readinessProbe:
  httpGet:
    path: /health/ready
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 5
```

**Behavior:**
- Service is considered "ready" only when InfluxDB health check passes
- Returns 503 if InfluxDB is unreachable
- Monitoring continues even when not ready, but metrics cannot be stored

#### GET `/health/live`

**Purpose:** Kubernetes/Docker liveness probe (determines if service should be restarted)

**Response Type:** `text/plain`

**HTTP Status Codes:**
- `200 OK` - Service is alive (process responding)

**Response Body:** `"ALIVE"`

**Usage:**

```bash
# Check if service is alive
curl http://localhost:8080/health/live
echo $?  # 0 if alive

# Kubernetes liveness probe configuration
livenessProbe:
  httpGet:
    path: /health/live
    port: 8080
  initialDelaySeconds: 30
  periodSeconds: 10
  failureThreshold: 3
```

**Behavior:**
- If this endpoint returns successfully, the service process is alive
- Kubernetes will restart the pod if this check fails `failureThreshold` times
- This is a simple "is the HTTP server responding" check

### Docker Compose Health Check

The `docker-compose.yml` uses the `/health/live` endpoint:

```yaml
healthcheck:
  test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/health/live"]
  interval: 30s
  timeout: 3s
  retries: 3
  start_period: 40s
```

### Monitoring Best Practices

**1. Use `/health` for dashboards and alerting:**
```bash
# Example alert: InfluxDB down
if [ "$(curl -s http://localhost:8080/health | jq -r '.influxdb_ok')" != "true" ]; then
  echo "ALERT: InfluxDB unreachable"
fi

# Example alert: High SNMP suspended device count
suspended=$(curl -s http://localhost:8080/health | jq -r '.snmp_suspended_devices')
if [ $suspended -gt 10 ]; then
  echo "WARNING: $suspended devices suspended by SNMP circuit breaker"
fi
```

**2. Use `/health/ready` for load balancers:**
- Ensures traffic only sent to instances with working InfluxDB connection
- Prevents metric loss

**3. Use `/health/live` for container orchestration:**
- Detects hung processes
- Triggers automatic restart on failures

**4. Monitor memory growth:**
```bash
# Track memory usage over time
while true; do
  date=$(date -Iseconds)
  memory=$(curl -s http://localhost:8080/health | jq -r '.memory_mb')
  rss=$(curl -s http://localhost:8080/health | jq -r '.rss_mb')
  echo "$date,$memory,$rss" >> memory.csv
  sleep 60
done
```

---


---

**End of Part II: Reference Documentation**

---

## Conclusion

This manual provides comprehensive documentation for deploying and operating netscan. It covers:

- **Part I**: Complete deployment guide for both Docker and native systemd installations
- **Part II**: Reference documentation for configuration, InfluxDB schema, and health check endpoints

For questions, issues, or feature requests, please visit the GitHub repository at https://github.com/kljama/netscan.

**For Developers:** Development documentation, architecture details, code API reference, and performance benchmarks are maintained separately in the project's `.github/copilot-instructions.md` file (the Single Source of Truth for the codebase).

---

**End of MANUAL.md**

*Last updated: 2024-10-30*
*This is the user and operator manual. Developer documentation is in `.github/copilot-instructions.md`*
