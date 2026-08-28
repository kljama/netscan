# netscan

`netscan` is an automated network discovery and monitoring daemon written in Go. It discovers active hosts across configured subnets using ICMP sweeps, enriches device metadata via SNMPv2c, continuously monitors latency and packet loss, and writes operational and health metrics to InfluxDB v2.

## Prerequisites

* **Docker Deployment**: Docker Engine 20.10+ and Docker Compose v2. Linux host networking is required for raw ICMP socket access.
* **Native Deployment**:
  * Go 1.26 or later (for compiling from source)
  * Linux OS with `libcap` / `setcap` utilities
  * InfluxDB 2.x instance reachable over HTTP/HTTPS

## Installation & Setup

### 1. Clone the Repository

```bash
git clone https://github.com/kljama/netscan.git
cd netscan
```

### 2. Configure Settings

Create local configuration and environment files from templates:

```bash
cp config.yml.example config.yml
cp .env.example .env
```

Edit `config.yml` to specify the target subnets in CIDR notation:

```yaml
networks:
  - "192.168.1.0/24"
```

### 3. Start the Application

#### Option A: Docker Compose (Recommended)

Run the full stack (netscan, InfluxDB v2, and Nginx reverse proxy):

```bash
docker compose up -d
```

#### Option B: Native Binary

Build the binary, grant `CAP_NET_RAW` permissions for ICMP socket operations, and run:

```bash
go build -o netscan ./cmd/netscan
sudo setcap cap_net_raw+ep ./netscan
./netscan -config config.yml
```

Alternatively, build using the provided helper script:

```bash
./scripts/build.sh
sudo setcap cap_net_raw+ep ./netscan
./netscan -config config.yml
```

## Usage / Quickstart

### Verify Service Health

The application provides an HTTP health server on port `8080` (configurable via `health_check_port`):

```bash
# Detailed health metrics (JSON)
curl -s http://localhost:8080/health

# Liveness check (200 OK)
curl -i http://localhost:8080/health/live

# Readiness check (200 OK)
curl -i http://localhost:8080/health/ready
```

### View Logs

When running under Docker Compose:

```bash
docker compose logs -f netscan
```

### Access InfluxDB UI

When using Docker Compose, access the InfluxDB Web UI via Nginx at `https://localhost` (or direct InfluxDB endpoint at `http://127.0.0.1:8086`). Default credentials defined in `.env.example`:
* **Username**: `admin`
* **Password**: `admin123`

### Command-Line Flags

```
./netscan [flags]

Flags:
  -config string
        Path to configuration file (default "config.yml")
  -version
        Print version and exit
```

## Configuration


 Configuration File (`config.yml`)


## Architecture / Project Structure

```
netscan/
├── cmd/
│   └── netscan/          # Entrypoint (main.go) and HTTP health server (health.go)
├── internal/
│   ├── config/           # YAML parsing, validation, and environment variable expansion
│   ├── discovery/        # CIDR IP enumeration and ICMP subnet sweeps
│   ├── influx/           # InfluxDB v2 async batch writer and connection health checks
│   ├── logger/           # Structured logging initialization (zerolog)
│   ├── monitoring/       # Continuous per-device ICMP pinger and SNMP poller routines
│   ├── snmp/             # SNMP client helpers and OID parsing utilities
│   ├── state/            # Thread-safe in-memory device state manager
│   └── version/          # Build version metadata
├── deploy/               # Systemd deployment scripts (deploy.sh, undeploy.sh)
├── influxdb/             # InfluxDB configuration and dashboard templates
├── nginx/                # Reverse proxy configuration for SSL termination to InfluxDB UI
├── scripts/              # Build script (build.sh) and InfluxDB setup script
├── Dockerfile            # Multi-stage build definition
└── docker-compose.yml    # Multi-container orchestration (netscan, InfluxDB, Nginx)
```
