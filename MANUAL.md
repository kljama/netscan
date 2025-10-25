# netscan - Complete Manual

This manual provides comprehensive documentation for netscan, a production-grade network monitoring service for linux-amd64.

**Contents:**
* Part I: Deployment Guide - Complete deployment instructions for Docker and Native deployments
* Part II: Development Guide - Architecture, development setup, building, testing, and contributing
* Part III: Reference Documentation - File structure, dependencies, and API reference

---

# Part I: Deployment Guide

## Overview

netscan is a production-grade network monitoring service written in Go (1.25+) for linux-amd64. Performs automated ICMP discovery, continuous ping monitoring, and SNMP metadata collection with time-series metrics storage in InfluxDB v2.

---

## Section 1: Docker Deployment (Recommended)

Docker deployment provides the easiest path to get netscan running with automatic orchestration of the complete stack (netscan + InfluxDB).

### Prerequisites

* Docker Engine 20.10+
* Docker Compose V2
* Network access to target devices

### Installation Steps

**1. Clone Repository**
```bash
git clone https://github.com/kljama/netscan.git
cd netscan
```

**2. Create Configuration File**
```bash
cp config.yml.example config.yml
```

Edit `config.yml` and update the `networks` section with your actual network ranges:
```yaml
networks:
  - "192.168.1.0/24"    # YOUR actual network range
  - "10.0.50.0/24"      # Add additional ranges as needed
```

**CRITICAL:** The example networks (192.168.0.0/24, etc.) are placeholders. If these don't match your network, netscan will find 0 devices. Use `ip addr` (Linux) or `ipconfig` (Windows) to determine your network range.

**3. (Optional) Configure Credentials**

For production security, create a `.env` file instead of using default credentials:
```bash
cp .env.example .env
chmod 600 .env
```

Edit `.env` and set secure values:
```bash
INFLUXDB_TOKEN=<generate-with-openssl-rand-base64-32>
DOCKER_INFLUXDB_INIT_ADMIN_TOKEN=<same-as-INFLUXDB_TOKEN>
DOCKER_INFLUXDB_INIT_PASSWORD=<strong-password>
SNMP_COMMUNITY=<your-snmp-community-not-public>
```

The `.env` file is automatically loaded by Docker Compose. Variables are expanded in `config.yml` (e.g., `${INFLUXDB_TOKEN}`).

**4. Start Stack**
```bash
docker compose up -d
```

This builds the netscan image from the local Dockerfile and starts both netscan and InfluxDB v2.7.

**5. Verify Operation**
```bash
# Check container status
docker compose ps

# View netscan logs
docker compose logs -f netscan

# Check health endpoint
curl http://localhost:8080/health | jq
```

**6. Access InfluxDB UI (Optional)**

Navigate to http://localhost:8086 in your browser:
* Username: `admin`
* Password: `admin123` (or your `.env` value)
* Organization: `test-org`
* Bucket: `netscan`

### Service Management

```bash
# Stop services (keeps data)
docker compose down

# Stop and remove all data
docker compose down -v

# Restart services
docker compose restart

# Rebuild after code changes
docker compose up -d --build

# View logs
docker compose logs -f netscan
docker compose logs -f influxdb
```

### Configuration Details

The `docker-compose.yml` configures:

* **netscan service:**
  * Builds from local Dockerfile (multi-stage, Go 1.25, ~15MB final image)
  * `network_mode: host` - Direct access to host network for ICMP/SNMP
  * `cap_add: NET_RAW` - Linux capability for raw ICMP sockets
  * Runs as root (required for ICMP in containers, see Security Notes)
  * Mounts `config.yml` as read-only volume at `/app/config.yml`
  * Environment variables from `.env` file or defaults
  * Log rotation configured (10MB max per file, 3 files retained, ~30MB total)
  * HEALTHCHECK on `/health/live` endpoint (30s interval, 3s timeout, 3 retries, 40s start period)
  * Auto-restart on failure

* **influxdb service:**
  * InfluxDB v2.7 official image
  * Exposed on port 8086
  * Environment variables from `.env` file or defaults
  * Persistent volume `influxdbv2-data` for data retention
  * Automatic creation of "netscan" and "health" buckets on initialization
  * Health check using `influx ping`

### Security Notes

**Docker Deployment:**
* Container runs as root user (non-negotiable requirement for ICMP raw socket access in Linux containers)
* CAP_NET_RAW capability provides raw socket access without full privileged mode
* Container remains isolated from host through Docker namespace isolation
* Minimal Alpine Linux base image (~15MB) reduces attack surface
* Config file mounted read-only (`:ro`)

**Security Trade-off:** Root access is required for ping functionality, but Docker containerization provides security boundary. For maximum security without root, use Native Deployment (Section 2).

### Troubleshooting

**Container finds 0 devices:**
```bash
# 1. Verify config.yml exists and is mounted
docker exec -it netscan cat /app/config.yml | grep -A 5 "networks:"

# 2. Check networks being scanned
docker compose logs netscan | grep "Scanning networks"

# 3. Verify host network mode
docker inspect netscan | grep NetworkMode
# Should show: "NetworkMode": "host"

# 4. Test ping manually
docker exec -it netscan ping -c 2 192.168.1.1
# Replace 192.168.1.1 with an IP from your network
```

**Container keeps restarting:**
```bash
# Check logs for errors
docker compose logs netscan

# Common causes:
# - Invalid config.yml syntax
# - InfluxDB credentials mismatch
# - Missing config.yml file
```

**InfluxDB connection failed:**
```bash
# Verify InfluxDB is healthy
docker compose ps influxdb

# Check InfluxDB health endpoint
curl http://localhost:8086/health

# Verify credentials in config.yml match docker-compose.yml
# token: netscan-token (default) or your .env value
# org: test-org (default) or your .env value
```

**Health bucket not found errors:**
```bash
# The init-influxdb.sh script should automatically create the health bucket
# If you see errors, verify the init script is mounted:
docker compose config | grep init-influxdb.sh

# Check InfluxDB logs for bucket creation
docker compose logs influxdb | grep -i "health bucket"

# Manually verify buckets exist
docker exec influxdbv2 influx bucket list --org test-org --token netscan-token
```

**Pings show success=false despite valid RTT values:**

**Symptom:** InfluxDB queries show `success=false` for ping records that have non-zero `rtt_ms` values (e.g., 12.34ms, 50.1ms). This indicates successful pings are being incorrectly classified as failures.

**Cause:** This was a bug in versions prior to the fix (commit fcbd411). The ping success detection logic relied solely on `stats.PacketsRecv > 0` from the pro-bing library, which had edge cases where the packet counter wasn't updated correctly even though valid RTT data existed.

**Fix:** The code was corrected to use RTT data directly for success detection:
```go
// New logic: RTT data proves we got a response
successful := len(stats.Rtts) > 0 && stats.AvgRtt > 0
```

**Solution:** Update to the latest version of netscan. The fix ensures that:
* Non-zero RTT values always result in `success=true`
* Zero RTT values always result in `success=false`
* Data consistency is maintained in InfluxDB

**Verification:** After updating, query InfluxDB to confirm all ping records are consistent:
```bash
# In InfluxDB UI or CLI, verify no records have non-zero rtt_ms with success=false
from(bucket: "netscan")
  |> range(start: -1h)
  |> filter(fn: (r) => r._measurement == "ping")
  |> filter(fn: (r) => r._field == "rtt_ms" or r._field == "success")
```

**Enhanced Logging:** The fix also added detailed packet statistics to debug logs. To view ping diagnostics:
```bash
# View debug logs with packet statistics
docker compose logs netscan | grep -E "(Ping successful|Ping failed)"

# Example output includes: ip, rtt, packets_recv, packets_sent
# {"level":"debug","ip":"192.168.1.1","rtt":12340000,"packets_recv":1,"packets_sent":1,"message":"Ping successful"}
```

### Log Management

**Docker Log Rotation:**

The netscan service is configured with automatic log rotation to prevent disk space exhaustion:

* **Max log file size:** 10MB per file
* **Files retained:** 3 most recent files
* **Total disk usage:** ~30MB maximum

Configuration in `docker-compose.yml`:
```yaml
logging:
  driver: json-file
  options:
    max-size: "10m"
    max-file: "3"
```

**Viewing logs:**
```bash
# View recent logs
docker compose logs netscan

# Follow logs in real-time
docker compose logs -f netscan

# View logs with timestamps
docker compose logs -t netscan

# View last 100 lines
docker compose logs --tail=100 netscan
```

**Log rotation behavior:**
* Logs are rotated automatically when a file reaches 10MB
* Oldest log files are deleted when more than 3 files exist
* No manual intervention required
* Prevents the common issue of unbounded log growth in long-running containers

### InfluxDB Bucket Initialization

**Automatic Bucket Creation:**

The InfluxDB service automatically creates two buckets on initialization:

1. **netscan bucket:** Stores device metrics (ping results, device_info)
2. **health bucket:** Stores application health metrics (device count, memory, goroutines)

The `init-influxdb.sh` script:
* Waits for InfluxDB to be ready
* Checks if the "health" bucket exists
* Creates it automatically if missing
* Uses the same retention period as the main bucket (default: 1 week)

**Manual bucket verification:**
```bash
# List all buckets
docker exec influxdbv2 influx bucket list --org test-org --token netscan-token

# Expected output should include both "netscan" and "health" buckets
```

**Bucket configuration:**
* Retention period: 1 week (default, configurable via `DOCKER_INFLUXDB_INIT_RETENTION`)
* Organization: test-org (default, configurable via `DOCKER_INFLUXDB_INIT_ORG`)
* Both buckets created automatically on first startup
* Subsequent restarts skip creation if buckets already exist

### Common Operations

#### Building and Running with the Latest Code

When you make changes to the Go source code, you must rebuild the Docker image to include them. The `--build` flag forces Docker Compose to re-run the build process.

```bash
docker-compose up -d --build
```

#### Starting a Fresh Deployment

If you need to start with a completely empty database, you can stop the services and remove the persistent InfluxDB data volume. The `-v` flag removes the named volumes defined in `docker-compose.yml`.

```bash
# This will permanently delete all stored monitoring data.
docker-compose down -v
```

After running this, you can start the services again with `docker-compose up -d`.

#### Reclaiming Disk Space

Docker can accumulate a lot of unused images, containers, and build cache over time. You can reclaim this space using Docker's built-in `prune` command.

```bash
# This command safely removes unused Docker objects.
docker system prune
```

---

## Section 2: Native systemd Deployment (Alternative)

Native deployment provides maximum security by running as a non-root service user with Linux capabilities. Recommended for production environments requiring strict security controls.

### Prerequisites

* Go 1.25+
* InfluxDB 2.x (separate installation)
* Linux with systemd
* Root privileges for installation (service runs as non-root)

### Installation

**1. Install Dependencies**

Ubuntu/Debian:
```bash
sudo apt update
sudo apt install golang-go
```

Arch/CachyOS:
```bash
sudo pacman -S go
```

**2. Clone and Build**
```bash
git clone https://github.com/kljama/netscan.git
cd netscan
go mod download
go build -o netscan ./cmd/netscan
```

**3. Deploy with Automated Script**
```bash
sudo ./deploy.sh
```

This creates:
* `/opt/netscan/` directory with binary, config, and `.env` file
* `netscan` system user (no shell, minimal privileges)
* CAP_NET_RAW capability on binary (via setcap)
* Systemd service unit with security restrictions
* Secure `.env` file (mode 600) for credentials

**4. Configure**

Edit `/opt/netscan/config.yml` with your network ranges:
```yaml
networks:
  - "192.168.1.0/24"    # YOUR actual network
```

Edit `/opt/netscan/.env` with your InfluxDB credentials:
```bash
INFLUXDB_URL=http://localhost:8086
INFLUXDB_TOKEN=<your-influxdb-token>
INFLUXDB_ORG=<your-org>
INFLUXDB_BUCKET=netscan
SNMP_COMMUNITY=<your-snmp-community>
```

**5. Start Service**
```bash
sudo systemctl start netscan
sudo systemctl enable netscan  # Start on boot
```

### Service Management

```bash
# Check status
sudo systemctl status netscan

# View logs
sudo journalctl -u netscan -f

# Restart
sudo systemctl restart netscan

# Stop
sudo systemctl stop netscan

# Disable auto-start
sudo systemctl disable netscan
```

### Manual Deployment (Alternative)

If you prefer not to use `deploy.sh`:

```bash
# Build binary
go build -o netscan ./cmd/netscan

# Create installation directory
sudo mkdir -p /opt/netscan
sudo cp netscan /opt/netscan/
sudo cp config.yml /opt/netscan/

# Set ICMP capability
sudo setcap cap_net_raw+ep /opt/netscan/netscan

# Create service user
sudo useradd -r -s /bin/false netscan
sudo chown -R netscan:netscan /opt/netscan

# Create systemd service
sudo tee /etc/systemd/system/netscan.service > /dev/null <<EOF
[Unit]
Description=netscan network monitoring
After=network.target

[Service]
Type=simple
ExecStart=/opt/netscan/netscan
WorkingDirectory=/opt/netscan
Restart=always
User=netscan
Group=netscan

# Load environment variables
EnvironmentFile=/opt/netscan/.env

# Security settings
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
AmbientCapabilities=CAP_NET_RAW

[Install]
WantedBy=multi-user.target
EOF

# Reload and start
sudo systemctl daemon-reload
sudo systemctl enable netscan
sudo systemctl start netscan
```

### Security Features

**Native Deployment Security:**
* Runs as dedicated `netscan` service user (not root)
* Service user has `/bin/false` shell (no interactive login)
* CAP_NET_RAW capability via setcap (no root required for ICMP)
* Systemd security restrictions: NoNewPrivileges, PrivateTmp, ProtectSystem=strict
* Credentials in `.env` file with mode 600 (readable only by service user)

**Comparison with Docker:**
* Native: Non-root execution, tighter security controls
* Docker: Root required for ICMP, container isolation provides security boundary

### Command Line Usage

```bash
# Standard mode (uses config.yml in working directory)
./netscan

# Custom config path
./netscan -config /path/to/config.yml

# Help
./netscan -help
```

### Health Check Endpoint

Once running, the service exposes an HTTP server on port 8080 (configurable):

```bash
# Detailed health status
curl http://localhost:8080/health | jq

# Readiness probe (for load balancers)
curl http://localhost:8080/health/ready

# Liveness probe (for process monitors)
curl http://localhost:8080/health/live
```

### Uninstallation

**Automated:**
```bash
sudo ./undeploy.sh
```

**Manual:**
```bash
sudo systemctl stop netscan
sudo systemctl disable netscan
sudo rm /etc/systemd/system/netscan.service
sudo systemctl daemon-reload
sudo rm -rf /opt/netscan
sudo userdel netscan
```

### Troubleshooting

**ICMP permission denied:**
```bash
# Check capability
getcap /opt/netscan/netscan
# Should show: cap_net_raw+ep

# Manually set capability
sudo setcap cap_net_raw+ep /opt/netscan/netscan
```

**InfluxDB connection issues:**
```bash
# Verify InfluxDB is running
systemctl status influxdb

# Test connectivity
curl http://localhost:8086/health

# Check credentials in /opt/netscan/.env
# Review logs
sudo journalctl -u netscan -n 50
```

**No devices discovered:**
```bash
# Verify network ranges in config.yml
# Check firewall rules for ICMP/SNMP
# Test manually
ping 192.168.1.1
# Check logs
sudo journalctl -u netscan | grep discovery
```

---

## Section 3: Configuration Reference

Complete documentation of all configuration parameters. All duration fields accept Go duration format (e.g., "5m", "2s", "1h30m").

### Configuration File Location

* **Docker:** `/app/config.yml` (mounted from host `./config.yml`)
* **Native:** `config.yml` in working directory or via `-config` flag

### Environment Variable Expansion

The configuration file supports `${VAR_NAME}` syntax for environment variable expansion. Variables are loaded from:
* **Docker:** docker-compose.yml environment section or `.env` file
* **Native:** `/opt/netscan/.env` file (loaded by systemd EnvironmentFile)

### Network Discovery Settings

```yaml
# Network ranges to scan (CIDR notation)
# REQUIRED: Update with YOUR actual network ranges
networks:
  - "192.168.1.0/24"    # Example: Home network
  - "10.0.50.0/24"      # Example: Server network
  # Add more ranges as needed
```

**Notes:**
* Use CIDR notation (e.g., `/24` for 254 hosts, `/16` for 65,534 hosts)
* Smaller ranges scan faster
* Maximum /16 recommended (security limit)

```yaml
# How often to run ICMP discovery to find new devices
# Default: "5m"
# Range: Minimum "1m" (enforced by min_scan_interval)
icmp_discovery_interval: "5m"
```

**Notes:**
* More frequent scans find new devices faster but increase CPU/network load
* 5 minutes is reasonable for most networks

```yaml
# Daily scheduled SNMP scan time (HH:MM format, 24-hour)
# Full SNMP scan of all known devices runs once per day at this time
# Default: "02:00" (2:00 AM)
# Set to empty string "" to disable scheduled SNMP scans
snmp_daily_schedule: "02:00"
```

**Notes:**
* Uses local system time
* Immediate SNMP scan still runs on device discovery
* Schedule useful for refreshing metadata overnight

### SNMP Settings

```yaml
snmp:
  # SNMPv2c community string (uses environment variable expansion)
  # Default: "public" (CHANGE THIS for production!)
  community: "${SNMP_COMMUNITY}"
  
  # SNMP port
  # Default: 161
  port: 161
  
  # Timeout for SNMP queries
  # Default: "5s"
  timeout: "5s"
  
  # Number of retries for failed SNMP queries
  # Default: 1
  retries: 1
```

**Notes:**
* SNMPv2c uses plain-text community strings (not secure)
* SNMPv3 support is deferred to future releases
* Increase timeout for slow/distant devices
* Increase retries for unreliable networks

### Monitoring Settings

```yaml
# Ping frequency per monitored device
# Default: "2s"
# Minimum time between ping attempts for each device (adaptive scheduling)
ping_interval: "2s"

# Timeout for individual ping operations
# Default: "3s"
# IMPORTANT: Should be > ping_interval to allow error margin
ping_timeout: "3s"

# Global ping rate limiting (token bucket algorithm)
# Controls the sustained rate of ICMP pings across all devices
# Default: 64.0 pings/sec, burst: 256
# Prevents network bursts, especially on startup when all pingers fire simultaneously
ping_rate_limit: 64.0    # Tokens per second (sustained ping rate)
ping_burst_limit: 256    # Token bucket capacity (max burst size)
```

**Notes:**
* ping_interval specifies the minimum time *between* ping operations (timer resets after each ping completes)
* This adaptive approach prevents "thundering herd" when rate limiter delays accumulate
* Lower intervals (e.g., "1s") provide more data points but increase CPU/network load
* ping_timeout should be > ping_interval to allow proper error margin (recommended: ping_interval + 1s minimum)
* ping_rate_limit controls global ping rate across all devices (64.0 = 64 pings/sec sustained)
* ping_burst_limit allows bursts (e.g., startup) up to this many concurrent pings
* Recommended: burst_limit >= rate_limit to avoid immediate throttling
* Example: With 100 devices pinging every 2s, peak load = 50 pings/sec (well under default 64/sec limit)

### Performance Tuning

```yaml
# Number of concurrent ICMP ping workers for discovery scans
# Default: 64 (safe for most deployments)
# Recommended: Start with 64 and increase if needed for large networks
# Range: 1-2000
# WARNING: Values >256 may cause kernel raw socket buffer overflow
icmp_workers: 64

# Number of concurrent SNMP polling workers
# Default: 32 (safe for most deployments)
# Recommended: 25-50% of icmp_workers to avoid overwhelming SNMP agents
# Range: 1-1000
snmp_workers: 32
```

**Worker Count Guidelines:**

| System Type       | Network Size | ICMP Workers | SNMP Workers | Max Devices | Max Pingers |
|-------------------|--------------|--------------|--------------|-------------|-------------|
| Raspberry Pi      | <100 devs    | 32           | 16           | 100         | 100         |
| Home Server       | <500 devs    | 64           | 32           | 500         | 500         |
| Workstation       | <2000 devs   | 128          | 64           | 2000        | 2000        |
| Small Server      | <5000 devs   | 256          | 128          | 5000        | 5000        |
| Large Server      | 5000+ devs   | 512          | 256          | 20000       | 20000       |

**Notes:**
* Start conservative with default 64/32 workers and increase based on network size and performance
* Values >256 for ICMP workers may cause kernel raw socket buffer saturation
* Monitor CPU usage and adjust accordingly - more workers ≠ always better
* Monitor with `htop` or `top`
* Network latency affects optimal worker count

### InfluxDB Settings

```yaml
influxdb:
  # InfluxDB v2 server URL
  # Docker default: "http://localhost:8086" (host network mode)
  # Native default: "http://localhost:8086" (assumes local InfluxDB)
  url: "http://localhost:8086"
  
  # API authentication token (uses environment variable expansion)
  # Docker: Set in docker-compose.yml or .env file
  # Native: Set in /opt/netscan/.env file
  token: "${INFLUXDB_TOKEN}"
  
  # Organization name (uses environment variable expansion)
  # Docker default: "test-org"
  # Native: Your organization name
  org: "${INFLUXDB_ORG}"
  
  # Bucket for metrics storage
  # Default: "netscan"
  bucket: "netscan"
  
  # Bucket for health metrics storage
  # Default: "health"
  health_bucket: "health"
  
  # Batch write settings for performance
  # Number of points to accumulate before writing
  # Default: 5000 (high-performance deployments)
  # Range: 10-10000
  batch_size: 5000
  
  # Maximum time to hold points before flushing
  # Default: "5s"
  # Range: "1s"-"60s"
  flush_interval: "5s"
```

**Batching Notes:**
* Batching reduces InfluxDB requests by ~99% for large deployments
* Larger batch_size reduces request frequency but increases memory usage
* Shorter flush_interval reduces data lag but increases request frequency
* Default (5000 points, 5s) optimized for high-performance servers with 10,000+ devices
* For small deployments (100-1000 devices), consider batch_size: 100

**InfluxDB Schema:**
* Measurement: `ping` (primary bucket)
  * Tags: `ip`, `hostname`
  * Fields: `rtt_ms` (float64), `success` (bool)
* Measurement: `device_info` (primary bucket)
  * Tags: `ip`
  * Fields: `hostname` (string), `snmp_description` (string)
* Measurement: `health_metrics` (health bucket)
  * Tags: none
  * Fields: `device_count` (int), `active_pingers` (int), `goroutines` (int), `memory_mb` (int), `rss_mb` (int), `influxdb_ok` (bool), `influxdb_successful_batches` (uint64), `influxdb_failed_batches` (uint64)
  * Note: `memory_mb` is Go heap allocation (runtime.MemStats.Alloc), `rss_mb` is OS-level resident set size (Linux /proc/self/status VmRSS)

### Health Check Endpoint

```yaml
# HTTP server port for health check endpoints
# Default: 8080
# Used by Docker HEALTHCHECK and Kubernetes probes
health_check_port: 8080

# Interval for writing health metrics to InfluxDB health bucket
# Default: "10s"
# Health metrics include device count, memory usage, goroutines, InfluxDB stats
health_report_interval: "10s"
```

**Endpoints:**
* `GET /health` - Detailed JSON status (device count, memory, goroutines, InfluxDB stats)
* `GET /health/ready` - Readiness probe (200 if InfluxDB OK, 503 if unavailable)
* `GET /health/live` - Liveness probe (200 if application running)

**Health Metrics Persistence:**
* Health metrics are automatically written to the health bucket at the configured interval
* Metrics include: device count, active pingers, goroutines, memory usage, InfluxDB status
* Separate bucket prevents health metrics from mixing with device monitoring data
* Useful for long-term application health tracking and alerting

**Docker Integration:**
* HEALTHCHECK directive in Dockerfile uses `/health/live`
* docker-compose.yml healthcheck uses wget on `/health/live`

**Kubernetes Integration:**
* Configure livenessProbe with `/health/live`
* Configure readinessProbe with `/health/ready`

### Resource Protection Settings

```yaml
# Maximum number of concurrent pinger goroutines
# Default: 20000 (high-performance servers)
# Prevents goroutine exhaustion
max_concurrent_pingers: 20000

# Maximum number of devices to monitor
# Default: 20000 (high-performance servers)
# When limit reached, oldest devices (by LastSeen) are evicted (LRU)
max_devices: 20000

# Minimum interval between discovery scans (rate limiting)
# Default: "1m"
# Prevents accidental tight loops
min_scan_interval: "1m"

# Memory usage warning threshold in MB
# Default: 16384 (16GB, high-performance servers)
# Logs warning when exceeded, does not stop service
memory_limit_mb: 16384
```

**Notes:**
* Resource limits prevent accidental DoS and resource exhaustion
* Memory baseline: ~50MB + ~1KB per device
* Adjust limits based on your hardware and network size
* High-performance defaults support up to 20,000 devices with 16GB RAM
* For small deployments, consider: max_devices: 1000, max_concurrent_pingers: 1000, memory_limit_mb: 512

### Complete Example

```yaml
# Network Discovery
networks:
  - "192.168.1.0/24"
  - "10.0.50.0/24"
icmp_discovery_interval: "5m"
snmp_daily_schedule: "02:00"

# SNMP
snmp:
  community: "${SNMP_COMMUNITY}"
  port: 161
  timeout: "5s"
  retries: 1

# Monitoring
ping_interval: "2s"
ping_timeout: "3s"

# Performance
icmp_workers: 64
snmp_workers: 32

# InfluxDB
influxdb:
  url: "http://localhost:8086"
  token: "${INFLUXDB_TOKEN}"
  org: "${INFLUXDB_ORG}"
  bucket: "netscan"
  batch_size: 5000
  flush_interval: "5s"

# Health Check
health_check_port: 8080

# Resource Limits
max_concurrent_pingers: 20000
max_devices: 20000
min_scan_interval: "1m"
memory_limit_mb: 16384
```

### Environment Variables Reference

These environment variables are used in config.yml via `${VAR_NAME}` syntax:

**Docker Deployment (via docker-compose.yml or .env file):**
```bash
INFLUXDB_TOKEN=netscan-token              # InfluxDB API token
INFLUXDB_ORG=test-org                     # InfluxDB organization
SNMP_COMMUNITY=public                     # SNMPv2c community string
DOCKER_INFLUXDB_INIT_USERNAME=admin       # InfluxDB admin username
DOCKER_INFLUXDB_INIT_PASSWORD=admin123    # InfluxDB admin password
DOCKER_INFLUXDB_INIT_ADMIN_TOKEN=netscan-token  # Same as INFLUXDB_TOKEN
```

**Native Deployment (via /opt/netscan/.env file):**
```bash
INFLUXDB_URL=http://localhost:8086        # InfluxDB server URL
INFLUXDB_TOKEN=<your-token>               # InfluxDB API token
INFLUXDB_ORG=<your-org>                   # InfluxDB organization
INFLUXDB_BUCKET=netscan                   # InfluxDB bucket
SNMP_COMMUNITY=<your-community>           # SNMPv2c community string
```

**Security Best Practices:**
* Never commit `.env` files to version control
* Use `chmod 600 .env` to restrict permissions
* Generate strong, unique tokens: `openssl rand -base64 32`
* Change SNMP community from default "public"
* Rotate credentials regularly

---

## License

MIT License - See LICENSE.md

---

# Part II: Development Guide

## Architecture Overview

netscan uses a decoupled, multi-ticker architecture with four independent concurrent event loops orchestrated in `cmd/netscan/main.go`:

### High-Level Design

```
┌─────────────────────────────────────────────────────────────┐
│                       Main Process                          │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐       │
│  │   Ticker 1  │  │   Ticker 2  │  │   Ticker 3  │       │
│  │    ICMP     │  │   Daily     │  │   Pinger    │       │
│  │  Discovery  │  │   SNMP      │  │Reconciliation│       │
│  │   (5 min)   │  │  (02:00)    │  │   (5 sec)   │       │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘       │
│         │                │                │               │
│         └────────────────┴────────────────┘               │
│                          │                                │
│                  ┌───────▼────────┐                       │
│                  │  StateManager  │ (Single Source)       │
│                  │  Thread-Safe   │                       │
│                  └───────┬────────┘                       │
│                          │                                │
│         ┌────────────────┼────────────────┐               │
│         │                │                │               │
│    ┌────▼────┐     ┌────▼────┐    ┌─────▼─────┐         │
│    │ Pinger  │     │ Pinger  │    │  Pinger   │   ...   │
│    │Device 1 │     │Device 2 │    │  Device N │         │
│    └────┬────┘     └────┬────┘    └─────┬─────┘         │
│         │                │                │               │
│         └────────────────┴────────────────┘               │
│                          │                                │
│                  ┌───────▼────────┐                       │
│                  │ InfluxDB Writer│ (Batched)             │
│                  │  Background    │                       │
│                  │   Flusher      │                       │
│                  └────────────────┘                       │
└─────────────────────────────────────────────────────────────┘
```

### Component Interaction

1. **ICMP Discovery Ticker (every 5m):**
   * Performs concurrent ICMP ping sweep across all configured networks
   * Returns list of responsive IPs
   * For each new device: Add to StateManager, trigger immediate SNMP scan
   * For existing devices: Updates are handled by continuous pingers

2. **Daily SNMP Scan Ticker (at 02:00):**
   * Retrieves all device IPs from StateManager
   * Performs concurrent SNMP queries for hostname and sysDescr
   * Updates StateManager with enriched metadata
   * Writes device_info measurements to InfluxDB

3. **Pinger Reconciliation Ticker (every 5s):**
   * Compares StateManager device list with activePingers map
   * Starts pinger goroutines for new devices
   * Stops pinger goroutines for removed devices
   * Ensures 1:1 mapping between devices and pingers

4. **State Pruning Ticker (every 1h):**
   * Removes devices not seen in last 24 hours from StateManager
   * Reconciliation ticker automatically stops their pingers

5. **Continuous Pingers (one per device):**
   * Dedicated goroutine pinging single device at configured interval (default: 2s)
   * Writes ping measurements to InfluxDB (batched)
   * Updates LastSeen timestamp in StateManager on successful ping
   * Runs until device removed or context cancelled

### Data Flow

**Discovery Flow:**
```
Network Range → ICMP Sweep → Responsive IPs → StateManager.AddDevice()
                                                      ↓
                                              Trigger SNMP Scan
                                                      ↓
                                    StateManager.UpdateDeviceSNMP(hostname, sysDescr)
                                                      ↓
                                         InfluxDB.WriteDeviceInfo()
```

**Monitoring Flow:**
```
Device → Pinger Goroutine → ICMP Ping (2s interval)
                                  ↓
                            Success/Failure
                                  ↓
                    ┌─────────────┴─────────────┐
                    ▼                           ▼
        StateManager.UpdateLastSeen()   InfluxDB.WritePingResult()
                                              (batched)
```

### Concurrency Model

* **Ticker Orchestration:** All tickers run in main select loop, non-blocking
* **Worker Pools:** ICMP discovery and SNMP scans use configurable worker pools
* **Pinger Goroutines:** One long-running goroutine per device
* **Mutex Protection:** StateManager and activePingers map protected with mutex
* **WaitGroup Tracking:** All pinger goroutines tracked for graceful shutdown
* **Context Cancellation:** Shutdown signal propagates via context to all goroutines

### Thread Safety

* **StateManager:** RWMutex protects all device map operations
* **activePingers map:** Mutex protects all pinger lifecycle operations (start/stop)
* **InfluxDB Writer:** Lock-free channel-based batching
* **No Shared State:** Pingers only access their own device struct and interfaces

### Performance Characteristics

* **Memory:** ~50MB baseline + ~1KB per device
* **Goroutines:** ~8 (framework) + N (pingers) + 2×workers (discovery/SNMP)
* **InfluxDB Requests:** Batched (100 points or 5s), ~99% reduction vs. unbatched
* **Network Load:** Configurable via worker counts and intervals
* **CPU Load:** Mostly idle, spikes during discovery/SNMP scans

---

## Development Setup

### Prerequisites

* **Go:** Version 1.25.1 or later
* **Docker:** For integration testing (optional)
* **InfluxDB:** v2.7+ for testing (optional, can use Docker)
* **Git:** For version control
* **Network Access:** To actual network devices for realistic testing

### Clone Repository

```bash
git clone https://github.com/kljama/netscan.git
cd netscan
```

### Install Dependencies

```bash
go mod download
```

This downloads all dependencies specified in go.mod:
* gopkg.in/yaml.v3 v3.0.1
* github.com/gosnmp/gosnmp v1.42.1
* github.com/prometheus-community/pro-bing v0.7.0
* github.com/influxdata/influxdb-client-go/v2 v2.14.0
* github.com/rs/zerolog v1.34.0

### IDE Setup

**VS Code (Recommended):**
1. Install Go extension
2. Configure gopls (Go language server) in settings.json:
   ```json
   {
     "go.useLanguageServer": true,
     "gopls": {
       "analyses": {
         "unusedparams": true,
         "shadow": true
       },
       "staticcheck": true
     }
   }
   ```

**GoLand/IntelliJ IDEA:**
1. Install Go plugin
2. Enable gofmt on save
3. Enable go vet on save

### Environment Setup for Development

Create `.env` file for testing:
```bash
cp .env.example .env
```

Edit values for your test environment. For local development with Docker InfluxDB:
```bash
INFLUXDB_TOKEN=test-token
INFLUXDB_ORG=test-org
INFLUXDB_BUCKET=netscan
SNMP_COMMUNITY=public
```

### Start Test InfluxDB (Optional)

```bash
# Start only InfluxDB from docker-compose
docker compose up -d influxdb

# Or use standalone InfluxDB container
docker run -d -p 8086:8086 \
  -e DOCKER_INFLUXDB_INIT_MODE=setup \
  -e DOCKER_INFLUXDB_INIT_USERNAME=admin \
  -e DOCKER_INFLUXDB_INIT_PASSWORD=admin123 \
  -e DOCKER_INFLUXDB_INIT_ORG=test-org \
  -e DOCKER_INFLUXDB_INIT_BUCKET=netscan \
  -e DOCKER_INFLUXDB_INIT_ADMIN_TOKEN=test-token \
  influxdb:2.7
```

---

## Building

### Standard Build

```bash
# Build for current platform
go build -o netscan ./cmd/netscan

# Run
./netscan -config config.yml
```

### Build with Optimizations (Production)

```bash
# Static binary with symbols stripped
CGO_ENABLED=0 go build \
  -ldflags="-w -s" \
  -o netscan \
  ./cmd/netscan

# Result: Smaller binary, no debug symbols
```

### Cross-Compilation

```bash
# For linux-amd64 (from any platform)
GOOS=linux GOARCH=amd64 go build -o netscan-linux-amd64 ./cmd/netscan

# Note: netscan officially supports linux-amd64 only
# ARM and other architectures are deferred to future releases
```

### Docker Build

```bash
# Build image
docker build -t netscan:local .

# Or use docker compose
docker compose build netscan
```

### Using build.sh Script

```bash
# Simple build script
./build.sh
```

This creates `netscan` binary in current directory.

---

## Testing

### Run All Tests

```bash
# Run all tests with verbose output
go test -v ./...

# Run with race detection (RECOMMENDED for development)
go test -race ./...

# Run with coverage
go test -cover ./...

# Generate coverage HTML report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

### Run Specific Package Tests

```bash
# Test configuration package
go test -v ./internal/config

# Test state manager
go test -v ./internal/state

# Test orchestration
go test -v ./cmd/netscan

# Test with race detection
go test -race ./internal/state
```

### Run Specific Test Functions

```bash
# Run single test
go test -v ./cmd/netscan -run TestPingerReconciliation

# Run tests matching pattern
go test -v ./cmd/netscan -run TestDaily

# Run benchmark
go test -v ./cmd/netscan -run ^$ -bench BenchmarkPingerReconciliation
```

### Test Suite Overview

**Unit Tests:**
* `internal/config/config_test.go` - Configuration parsing and validation
* `internal/state/manager_test.go` - Device state management
* `internal/state/manager_concurrent_test.go` - Concurrency and race conditions
* `internal/discovery/scanner_test.go` - ICMP and SNMP scanning
* `internal/influx/writer_test.go` - InfluxDB write operations
* `internal/influx/writer_validation_test.go` - Data sanitization
* `internal/monitoring/pinger_test.go` - Continuous ping monitoring

**Integration Tests:**
* `cmd/netscan/orchestration_test.go` - Multi-ticker orchestration (11 tests, 1 benchmark)

### Test Quality Requirements

All tests must:
* Pass without errors: `go test ./...`
* Pass race detection: `go test -race ./...`
* Execute in <2 seconds total
* Not depend on external services (use mocks/fakes)
* Not depend on specific timing (avoid time.Sleep() in assertions)

### Benchmarking

```bash
# Run all benchmarks
go test -bench=. ./...

# Run specific benchmark
go test -bench=BenchmarkPingerReconciliation ./cmd/netscan

# With memory allocation stats
go test -bench=. -benchmem ./...

# Multiple runs for statistical significance
go test -bench=. -benchtime=10s -count=5 ./...
```

---

## Code Quality

### Formatting

```bash
# Format all code (automatic)
go fmt ./...

# Check if formatting needed
gofmt -l .
```

### Static Analysis

```bash
# Run go vet
go vet ./...

# Install and run staticcheck (recommended)
go install honnef.co/go/tools/cmd/staticcheck@latest
staticcheck ./...
```

### Linting

```bash
# Install golangci-lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Run linter
golangci-lint run

# Run with auto-fix
golangci-lint run --fix
```

### Dependency Management

```bash
# Add new dependency
go get github.com/some/package

# Update dependency
go get -u github.com/some/package

# Update all dependencies
go get -u ./...

# Clean up unused dependencies
go mod tidy

# Verify dependencies
go mod verify

# Check for vulnerabilities
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

---

## Contribution Guidelines

### Development Workflow

1. **Fork Repository:** Fork kljama/netscan to your GitHub account
2. **Clone Fork:** `git clone https://github.com/YOUR-USERNAME/netscan.git`
3. **Create Branch:** `git checkout -b feature/your-feature-name`
4. **Make Changes:** Follow coding standards below
5. **Test Changes:** Run all tests with race detection
6. **Commit:** Use conventional commit messages
7. **Push:** `git push origin feature/your-feature-name`
8. **Pull Request:** Create PR against main branch

### Coding Standards

**General:**
* Follow Go official style guide
* Use `gofmt` for formatting
* Pass `go vet` and `staticcheck`
* Document exported functions/types with godoc comments
* Write tests for new code (target: >80% coverage)
* Run `go test -race` before committing

**Naming Conventions:**
* Packages: lowercase, single word (e.g., `discovery`, `state`)
* Files: lowercase, underscore-separated (e.g., `scanner.go`, `manager_test.go`)
* Types: PascalCase (e.g., `StateManager`, `Device`)
* Functions: camelCase for private, PascalCase for exported
* Constants: PascalCase for exported, camelCase for private
* Variables: camelCase (avoid single-letter except loop counters)

**Error Handling:**
* Always check errors (no `err` variable unused)
* Use structured logging for errors with context
* Return errors, don't panic (except for truly unrecoverable errors)
* Wrap errors with context: `fmt.Errorf("operation failed: %w", err)`

**Concurrency:**
* Always protect shared state with mutex
* Use channels for communication, not shared memory
* Add `defer recover()` to all goroutines
* Use WaitGroups to track goroutine lifecycle
* Document thread-safety guarantees in godoc

**Logging:**
* Use zerolog structured logging
* Include context fields (IP, device count, duration, etc.)
* Log levels: Fatal (startup failures), Error (actionable issues), Warn (degraded state), Info (major operations), Debug (verbose details)
* Never log in hot paths (inside tight loops)

### Commit Message Format

Use conventional commits:

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types:**
* `feat`: New feature
* `fix`: Bug fix
* `docs`: Documentation changes
* `style`: Code style changes (formatting, no logic change)
* `refactor`: Code refactoring
* `perf`: Performance improvements
* `test`: Adding/updating tests
* `chore`: Maintenance tasks (dependencies, build, etc.)

**Examples:**
```
feat(discovery): add IPv6 ICMP sweep support

Implements IPv6 address expansion and ping for dual-stack networks.
Adds configuration option ipv6_enabled (default: false).

Closes #42
```

```
fix(influx): prevent data loss on batch flush failure

Retry failed batches up to 3 times with exponential backoff
before discarding. Log all discarded points for manual recovery.

Fixes #156
```

### Pull Request Requirements

**Before Submitting:**
* [ ] All tests pass: `go test ./...`
* [ ] Race detection clean: `go test -race ./...`
* [ ] Code formatted: `go fmt ./...`
* [ ] No vet issues: `go vet ./...`
* [ ] Documentation updated (README, godoc comments)
* [ ] Commit messages follow conventional commits
* [ ] Branch is up-to-date with main

**PR Description Must Include:**
* Summary of changes
* Motivation and context
* Related issue numbers (if applicable)
* Testing performed
* Screenshots (if UI changes, though netscan has no UI)
* Breaking changes (if any)

**Review Process:**
* Maintainer reviews code
* CI/CD pipeline runs (tests, security scans, Docker build)
* At least one approval required
* Squash and merge to main

### Branching Strategy

* `main` - Stable, production-ready code
* `feature/*` - New features
* `fix/*` - Bug fixes
* `docs/*` - Documentation changes
* `release/*` - Release preparation

### Versioning

Project follows Semantic Versioning (SemVer):
* MAJOR: Breaking changes
* MINOR: New features (backward-compatible)
* PATCH: Bug fixes (backward-compatible)

---

## Project Dependencies

Comprehensive list of all dependencies with purpose explanations.

### Primary Dependencies (Direct)

**1. gopkg.in/yaml.v3 v3.0.1**
* **Purpose:** YAML configuration file parsing
* **Usage:** `config.LoadConfig()` in `internal/config/config.go`
* **License:** Apache-2.0 / MIT
* **Why Chosen:** Standard YAML library for Go, well-maintained

**2. github.com/gosnmp/gosnmp v1.42.1**
* **Purpose:** SNMPv2c protocol implementation
* **Usage:** SNMP queries in `internal/discovery/scanner.go`
* **License:** BSD-2-Clause
* **Why Chosen:** Native Go implementation, no CGO, good device compatibility

**3. github.com/prometheus-community/pro-bing v0.7.0**
* **Purpose:** ICMP ping implementation with raw socket support
* **Usage:** Ping operations in `internal/discovery/scanner.go` and `internal/monitoring/pinger.go`
* **License:** MIT
* **Why Chosen:** Fork of go-ping with better raw socket support, active maintenance

**4. github.com/influxdata/influxdb-client-go/v2 v2.14.0**
* **Purpose:** InfluxDB v2 client library
* **Usage:** Time-series data writes in `internal/influx/writer.go`
* **License:** MIT
* **Why Chosen:** Official InfluxDB client, non-blocking WriteAPI, batching support

**5. github.com/rs/zerolog v1.34.0**
* **Purpose:** Zero-allocation structured logging
* **Usage:** All logging throughout application via `internal/logger/logger.go`
* **License:** MIT
* **Why Chosen:** Fastest structured logger, JSON output, zero allocations

### Transitive Dependencies (Indirect)

**github.com/apapsch/go-jsonmerge/v2 v2.0.0**
* Required by: influxdb-client-go
* Purpose: JSON merging for InfluxDB API

**github.com/google/uuid v1.6.0**
* Required by: pro-bing
* Purpose: Generate unique identifiers for ICMP packets

**github.com/influxdata/line-protocol v0.0.0-20200327222509-2487e7298839**
* Required by: influxdb-client-go
* Purpose: InfluxDB line protocol encoding

**github.com/mattn/go-colorable v0.1.13**
* Required by: zerolog
* Purpose: Colored console output (Windows compatibility)

**github.com/mattn/go-isatty v0.0.19**
* Required by: zerolog
* Purpose: Detect terminal for colored output

**github.com/oapi-codegen/runtime v1.0.0**
* Required by: influxdb-client-go
* Purpose: OpenAPI runtime for InfluxDB API

**golang.org/x/net v0.38.0**
* Required by: pro-bing, influxdb-client-go
* Purpose: Network primitives (ICMP, HTTP/2)

**golang.org/x/sync v0.13.0**
* Required by: pro-bing
* Purpose: Extended synchronization primitives

**golang.org/x/sys v0.31.0**
* Required by: Multiple packages
* Purpose: System calls (raw sockets, capabilities)

### System Dependencies

**Linux Capabilities:**
* CAP_NET_RAW - Required for raw ICMP sockets
* Set via: `setcap cap_net_raw+ep /path/to/binary` (native) or container runs as root (Docker)

**Runtime Requirements:**
* Linux kernel 3.10+ (for capabilities support)
* glibc or musl (Alpine uses musl)

---


---

# Part III: Reference Documentation

## File & Directory Structure

Complete reference documenting every file and directory in the repository.

### Root Directory Files

**`.dockerignore`**
* Purpose: Specifies files/directories excluded from Docker build context
* Excludes: Documentation (*.md), tests (*_test.go), git metadata (.git/, .github/), build artifacts
* Why: Reduces Docker build context size, faster builds

**`.env.example`**
* Purpose: Template for environment variables used by Docker Compose
* Contains: Placeholders for InfluxDB credentials, SNMP community, org/bucket names
* Usage: `cp .env.example .env` then edit with actual values
* Security: .env file is in .gitignore, never commit credentials

**`.gitignore`**
* Purpose: Git ignore rules to prevent committing sensitive/generated files
* Excludes: config.yml (credentials), .env (credentials), netscan binary, dist/ (build artifacts), IDE files
* Why: Prevents accidental credential leaks and keeps repository clean

**`build.sh`**
* Purpose: Simple build script for netscan binary
* Usage: `./build.sh`
* Output: `netscan` binary in current directory
* Implementation: Calls `go build -o netscan ./cmd/netscan`

**`CHANGELOG.md`**
* Purpose: Project changelog tracking all releases and changes
* Format: Keep a Changelog format with semantic versioning
* Maintenance: Updated with each release

**`cliff.toml`**
* Purpose: Configuration for git-cliff changelog generator
* Usage: Automatically generate CHANGELOG.md from git commit history
* Command: `git cliff -o CHANGELOG.md`
* Format: Conventional commits parsing

**`config.yml.example`**
* Purpose: Template configuration file with all available parameters
* Contains: Network ranges (MUST be updated), SNMP settings, performance tuning, resource limits
* Usage: `cp config.yml.example config.yml` then edit networks section
* Security: Contains placeholders for environment variables (${VAR_NAME})

**`deploy.sh`**
* Purpose: Automated native deployment script for production
* Target: Systemd-based Linux systems
* Actions: Creates /opt/netscan/, copies binary/config, creates service user, sets capabilities, installs systemd unit
* Usage: `sudo ./deploy.sh`

**`docker-compose.yml`**
* Purpose: Docker Compose stack definition for netscan + InfluxDB
* Services: netscan (build from Dockerfile), influxdb (InfluxDB v2.7 image)
* Configuration: Host networking, CAP_NET_RAW capability, health checks, environment variable expansion
* Usage: `docker compose up -d`

**`Dockerfile`**
* Purpose: Multi-stage Docker build for netscan
* Stage 1: golang:1.25-alpine builder (compiles Go binary)
* Stage 2: alpine:latest runtime (minimal ~15MB image)
* Security: Creates non-root user but runs as root for ICMP (documented in comments)
* Features: CAP_NET_RAW capability, HEALTHCHECK directive, optimized binary (-ldflags="-w -s")

**`docker-verify.sh`**
* Purpose: CI/CD verification script for Docker deployment
* Actions: Creates test config.yml, starts Docker Compose, waits for health, verifies endpoints, cleanup
* Usage: Called by .github/workflows/ci-cd.yml
* Why: Validates complete stack deployment works end-to-end

**`go.mod` & `go.sum`**
* Purpose: Go module definition and dependency lock file
* Version: Go 1.25.1
* Dependencies: yaml.v3, gosnmp, pro-bing, influxdb-client-go, zerolog
* Maintenance: `go mod tidy` to clean up

**`LICENSE.md`**
* Purpose: MIT License file
* Terms: Free use, modification, distribution with attribution

**`undeploy.sh`**
* Purpose: Complete uninstallation script for native deployment
* Actions: Stops/disables systemd service, removes /opt/netscan/, deletes service user
* Usage: `sudo ./undeploy.sh`

### `.github/` Directory

**`.github/copilot-instructions.md`**
* Purpose: Comprehensive development guide for GitHub Copilot
* Content: Project architecture, implementation details, coding standards, mandates
* Audience: AI assistants and developers
* Maintenance: Updated with each major architectural change

**`.github/workflows/ci-cd.yml`**
* Purpose: Main CI/CD pipeline for GitHub Actions
* Triggers: Push to any branch, pull requests
* Jobs: Go build, unit tests with race detection, security scanning (govulncheck, Trivy), Docker verification
* Security: Uploads SARIF reports to GitHub Security tab, blocks on HIGH/CRITICAL vulnerabilities

### `cmd/netscan/` Directory

Main application entry point and orchestration logic.

**`cmd/netscan/main.go`** (224 lines)
* Purpose: Application entry point and multi-ticker orchestration
* Initialization: Config loading, logging setup, StateManager, InfluxDB writer, health server, signal handling
* Tickers: ICMP discovery (every 5m), daily SNMP (at 02:00), pinger reconciliation (every 5s), state pruning (every 1h)
* Shutdown: Graceful shutdown with context cancellation, WaitGroup, batch flush
* Exports: None (main package)

**`cmd/netscan/health.go`** (130 lines)
* Purpose: HTTP health check server with three endpoints
* Endpoints: /health (detailed JSON), /health/ready (readiness probe), /health/live (liveness probe)
* Exports: `HealthServer` struct, `NewHealthServer()` constructor, `Start()` method
* Integration: Docker HEALTHCHECK, Kubernetes probes

**`cmd/netscan/orchestration_test.go`** (527 lines)
* Purpose: Comprehensive integration tests for ticker orchestration
* Tests: 11 test functions covering daily SNMP scheduling, graceful shutdown, pinger reconciliation, resource limits
* Benchmark: BenchmarkPingerReconciliation with 1000 devices
* Coverage: Critical orchestration logic, edge cases, concurrent operation

### `internal/config/` Package

Configuration parsing, validation, and environment variable expansion.

**`internal/config/config.go`** (477 lines)
* Purpose: YAML configuration parsing with validation
* Structs: `Config`, `SNMPConfig`, `InfluxDBConfig`
* Functions: `LoadConfig(path)`, `ValidateConfig(cfg)`, validation helpers
* Features: Environment variable expansion, duration parsing, network range validation, security checks
* Exports: Config, SNMPConfig, InfluxDBConfig, LoadConfig, ValidateConfig

**`internal/config/config_test.go`**
* Purpose: Unit tests for configuration parsing and validation
* Coverage: Valid configs, invalid formats, environment expansion, validation rules

### `internal/discovery/` Package

ICMP discovery and SNMP scanning with concurrent worker pools.

**`internal/discovery/scanner.go`** (819 lines)
* Purpose: Network device discovery via ICMP and SNMP
* Functions:
  * `RunICMPSweep(networks, workers) []string` - Concurrent ICMP ping sweep
  * `RunSNMPScan(ips, snmpConfig, workers) []Device` - Concurrent SNMP queries
  * `snmpGetWithFallback()` - SNMP Get with GetNext fallback
  * `validateSNMPString()` - Handle string/[]byte OctetString types
* Exports: RunICMPSweep, RunSNMPScan, RunScanIPsOnly (for testing)

**`internal/discovery/scanner_test.go`**
* Purpose: Unit tests for ICMP and SNMP scanning
* Coverage: Worker pools, CIDR expansion, SNMP fallback logic, type handling

### `internal/influx/` Package

InfluxDB v2 client with high-performance batching system.

**`internal/influx/writer.go`** (376 lines)
* Purpose: InfluxDB time-series data writes with batching
* Struct: `Writer` with channel-based batching, background flusher
* Functions:
  * `NewWriter(url, token, org, bucket, batchSize, flushInterval) *Writer` - Constructor
  * `WritePingResult(ip, rtt, success)` - Queue ping measurement
  * `WriteDeviceInfo(ip, hostname, description)` - Queue device metadata
  * `HealthCheck()` - Test InfluxDB connectivity
  * `Close()` - Graceful shutdown with batch flush
* Performance: 99% reduction in HTTP requests via batching
* Exports: Writer, NewWriter

**`internal/influx/writer_test.go`**
* Purpose: Unit tests for InfluxDB operations
* Coverage: Batching logic, health checks, error handling, graceful shutdown

**`internal/influx/writer_validation_test.go`**
* Purpose: Data sanitization and validation tests
* Coverage: String sanitization, null byte removal, UTF-8 validation, length limits

### `internal/logger/` Package

Centralized structured logging with zerolog.

**`internal/logger/logger.go`** (43 lines)
* Purpose: Global logger initialization and configuration
* Functions:
  * `Setup(debugMode bool)` - Initialize global logger
  * `Get()` - Return logger with context
  * `With(key, value)` - Return logger with additional context
* Features: JSON logs (production), colored console (development), zero allocations
* Exports: Setup, Get, With

### `internal/monitoring/` Package

Continuous ICMP monitoring with dedicated pinger goroutines.

**`internal/monitoring/pinger.go`** (142 lines)
* Purpose: Continuous ping monitoring for single device
* Interfaces: `PingWriter`, `StateManager` (for testability)
* Functions:
  * `StartPinger(ctx, wg, device, interval, writer, stateMgr)` - Long-running pinger goroutine
  * `validateIPAddress(ip)` - Security validation (prevent loopback, multicast, etc.)
* Features: Context cancellation, WaitGroup tracking, panic recovery, IP validation
* Exports: PingWriter, StateManager, StartPinger

**`internal/monitoring/pinger_test.go`**
* Purpose: Unit tests for continuous monitoring
* Coverage: Pinger lifecycle, context cancellation, error handling, mock interfaces

### `internal/state/` Package

Thread-safe device state management (single source of truth).

**`internal/state/manager.go`** (180 lines)
* Purpose: Centralized, thread-safe device registry
* Struct: `Device` (IP, Hostname, SysDescr, LastSeen), `Manager` (devices map, RWMutex, maxDevices)
* Functions:
  * `NewManager(maxDevices) *Manager` - Constructor
  * `Add(device)` - Add device with LRU eviction
  * `AddDevice(ip) bool` - Add by IP only, returns true if new
  * `Get(ip) (*Device, bool)` - Retrieve device
  * `GetAll() []Device` - Return copy of all devices
  * `GetAllIPs() []string` - Return all IPs
  * `UpdateLastSeen(ip)` - Refresh timestamp
  * `UpdateDeviceSNMP(ip, hostname, sysDescr)` - Enrich with SNMP data
  * `Prune(olderThan) []Device` - Remove stale devices
  * `Count() int` - Current device count
* Thread Safety: All operations protected by RWMutex
* Exports: Device, Manager, NewManager, all methods

**`internal/state/manager_test.go`**
* Purpose: Unit tests for state management
* Coverage: CRUD operations, LRU eviction, timestamp updates, pruning

**`internal/state/manager_concurrent_test.go`**
* Purpose: Concurrency and race condition tests
* Coverage: Concurrent reads/writes, stress testing, race detector validation

---

## Code Reference (API Documentation)

Documentation of all exported functions, structs, and interfaces.

### Package: config

**Type: Config**
```go
type Config struct {
    DiscoveryInterval     time.Duration  // Deprecated, use IcmpDiscoveryInterval
    IcmpDiscoveryInterval time.Duration  // How often to scan for new devices
    IcmpWorkers           int            // Concurrent ICMP workers for discovery
    SnmpWorkers           int            // Concurrent SNMP workers
    Networks              []string       // CIDR ranges to scan
    SNMP                  SNMPConfig     // SNMP connection parameters
    PingInterval          time.Duration  // Ping frequency per device
    PingTimeout           time.Duration  // Timeout for single ping
    InfluxDB              InfluxDBConfig // InfluxDB connection parameters
    SNMPDailySchedule     string         // Daily SNMP scan time (HH:MM)
    HealthCheckPort       int            // HTTP health server port
    MaxConcurrentPingers  int            // Resource limit
    MaxDevices            int            // Resource limit with LRU eviction
    MinScanInterval       time.Duration  // Rate limiting
    MemoryLimitMB         int            // Memory warning threshold
}
```

**Function: LoadConfig**
```go
func LoadConfig(path string) (*Config, error)
```
Loads YAML configuration file and expands environment variables.
* Parameters: `path` - Path to config.yml file
* Returns: Parsed Config struct or error
* Environment Expansion: `${VAR_NAME}` syntax expanded via os.ExpandEnv()

**Function: ValidateConfig**
```go
func ValidateConfig(cfg *Config) (warning string, err error)
```
Validates configuration for security and sanity.
* Parameters: `cfg` - Config struct to validate
* Returns: Warning string (non-fatal issues), error (fatal issues)
* Validations: Network ranges, durations, resource limits, SNMPDailySchedule format

### Package: discovery

**Function: RunICMPSweep**
```go
func RunICMPSweep(networks []string, workers int) []string
```
Performs concurrent ICMP ping sweep across multiple networks.
* Parameters: `networks` - CIDR ranges, `workers` - Concurrent workers (default: 64)
* Returns: Slice of IP addresses that responded to ping
* Worker Pool: Producer-consumer pattern with buffered channels
* Timeout: 1 second per ping

**Function: RunSNMPScan**
```go
func RunSNMPScan(ips []string, snmpConfig *config.SNMPConfig, workers int) []state.Device
```
Performs concurrent SNMP queries for device metadata.
* Parameters: `ips` - IPs to query, `snmpConfig` - SNMP parameters, `workers` - Concurrent workers (default: 32)
* Returns: Slice of Device structs with SNMP data populated
* OIDs Queried: sysName (1.3.6.1.2.1.1.5.0), sysDescr (1.3.6.1.2.1.1.1.0)
* Fallback: Uses snmpGetWithFallback() for device compatibility
* Error Handling: Non-responsive devices logged, not returned

### Package: influx

**Type: Writer**
```go
type Writer struct {
    client   influxdb2.Client
    writeAPI api.WriteAPI
    org      string
    bucket   string
    // Batching fields (private)
}
```

**Function: NewWriter**
```go
func NewWriter(url, token, org, bucket string, batchSize int, flushInterval time.Duration) *Writer
```
Creates InfluxDB writer with batching.
* Parameters: Connection details, batch size (default: 100), flush interval (default: 5s)
* Returns: Writer instance (does not check health, call HealthCheck() separately)
* Background: Starts backgroundFlusher() and monitorWriteErrors() goroutines

**Method: WritePingResult**
```go
func (w *Writer) WritePingResult(ip string, rtt time.Duration, successful bool) error
```
Queues ping measurement to batch (non-blocking).
* Parameters: `ip` - Device IP, `rtt` - Round-trip time, `successful` - Ping succeeded
* Measurement: "ping"
* Tags: ip, hostname
* Fields: rtt_ms (float64), success (bool)

**Method: WriteDeviceInfo**
```go
func (w *Writer) WriteDeviceInfo(ip, hostname, description string) error
```
Queues device metadata to batch (non-blocking).
* Parameters: `ip`, `hostname`, `description` (all sanitized)
* Measurement: "device_info"
* Tags: ip
* Fields: hostname (string), snmp_description (string)

**Method: HealthCheck**
```go
func (w *Writer) HealthCheck() error
```
Tests InfluxDB connectivity.
* Returns: nil if connected, error otherwise
* Usage: Called on startup (fail-fast) and by health endpoint

**Method: Close**
```go
func (w *Writer) Close()
```
Graceful shutdown with batch flush.
* Actions: Cancels context, drains batch channel, performs final flush

### Package: logger

**Function: Setup**
```go
func Setup(debugMode bool)
```
Initializes global zerolog logger.
* Parameters: `debugMode` - Enable debug level logging
* Output: JSON (production) or colored console (ENVIRONMENT=development)
* Levels: Debug, Info, Warn, Error, Fatal
* Fields: Adds service="netscan" and timestamp to all logs

### Package: monitoring

**Interface: PingWriter**
```go
type PingWriter interface {
    WritePingResult(ip string, rtt time.Duration, successful bool) error
    WriteDeviceInfo(ip, hostname, sysDescr string) error
}
```
Implemented by: `influx.Writer`

**Interface: StateManager**
```go
type StateManager interface {
    UpdateLastSeen(ip string)
}
```
Implemented by: `state.Manager`

**Function: StartPinger**
```go
func StartPinger(ctx context.Context, wg *sync.WaitGroup, device state.Device, 
                 interval time.Duration, writer PingWriter, stateMgr StateManager)
```
Runs continuous ping monitoring for single device.
* Parameters: Context for cancellation, WaitGroup for tracking, device details, ping interval, writer interface, state manager interface
* Lifecycle: Runs until context cancelled, decrements WaitGroup on exit
* Behavior: Pings device at interval, writes results, updates LastSeen on success
* Error Handling: Logs errors, continues loop (does not exit on failure)
* Panic Recovery: Protected with defer recover()

### Package: state

**Type: Device**
```go
type Device struct {
    IP       string    // IPv4 address
    Hostname string    // From SNMP or defaults to IP
    SysDescr string    // SNMP sysDescr value
    LastSeen time.Time // Last successful ping timestamp
}
```

**Type: Manager**
```go
type Manager struct {
    devices    map[string]*Device // IP -> Device pointer
    mu         sync.RWMutex       // Thread-safety
    maxDevices int                // LRU eviction limit
}
```

**Function: NewManager**
```go
func NewManager(maxDevices int) *Manager
```
Creates new state manager.
* Parameters: `maxDevices` - Maximum devices (default: 10000 if <= 0)
* Returns: Initialized Manager instance

**Method: AddDevice**
```go
func (m *Manager) AddDevice(ip string) bool
```
Adds device by IP, returns true if new.
* Parameters: `ip` - Device IP address
* Returns: true if device is new, false if already exists
* LRU Eviction: Removes oldest device if at maxDevices limit
* Thread-Safe: Uses mutex lock

**Method: UpdateDeviceSNMP**
```go
func (m *Manager) UpdateDeviceSNMP(ip, hostname, sysDescr string)
```
Enriches device with SNMP metadata.
* Parameters: `ip` - Device IP, `hostname` - SNMP sysName, `sysDescr` - SNMP sysDescr
* Side Effects: Updates LastSeen timestamp
* Thread-Safe: Uses mutex lock

**Method: GetAllIPs**
```go
func (m *Manager) GetAllIPs() []string
```
Returns slice of all device IPs.
* Returns: Slice of IP addresses (copy, safe to modify)
* Thread-Safe: Uses RWMutex read lock

**Method: Prune**
```go
func (m *Manager) Prune(olderThan time.Duration) []Device
```
Removes devices not seen within duration.
* Parameters: `olderThan` - Duration threshold (e.g., 24 * time.Hour)
* Returns: Slice of removed devices
* Thread-Safe: Uses mutex lock

**Method: Count**
```go
func (m *Manager) Count() int
```
Returns current device count.
* Returns: Number of devices in state
* Thread-Safe: Uses RWMutex read lock
* Usage: Health endpoint, logging

---

## Glossary

**CIDR:** Classless Inter-Domain Routing notation for IP ranges (e.g., 192.168.1.0/24)

**Context:** Go pattern for cancellation signal propagation across goroutines

**Docker Compose:** Tool for defining and running multi-container Docker applications

**Goroutine:** Lightweight thread managed by Go runtime

**InfluxDB:** Time-series database for metrics storage

**LRU:** Least Recently Used eviction policy (oldest device removed first)

**Mutex:** Mutual exclusion lock for thread-safe access to shared data

**RWMutex:** Read-Write mutex allowing multiple readers or single writer

**SNMP:** Simple Network Management Protocol for device monitoring

**SNMPv2c:** SNMP version 2 with community strings (plain-text authentication)

**Ticker:** Go timer that fires at regular intervals

**WaitGroup:** Go synchronization primitive for waiting on goroutines

**Worker Pool:** Pattern with fixed number of workers processing jobs from queue

---

## End of Manual

For the latest updates, see:
* Repository: https://github.com/kljama/netscan
* Issues: https://github.com/kljama/netscan/issues
* Pull Requests: https://github.com/kljama/netscan/pulls

