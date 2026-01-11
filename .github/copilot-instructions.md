# netscan: Project Bible & AI Development Guide

**Welcome!** This document serves two purposes:
1.  **It is the Project's Single Source of Truth (SSOT).** It contains the complete architecture, deployment model, and component details, all derived directly from the source code.
2.  **It is the Instruction Manual for the AI Agent.** It contains the strict rules, mandates, and non-goals for all future development.

All developers (human and AI) must adhere to the principles and mandates in this guide.

---

## Project Overview

`netscan` is a production-grade Go network monitoring service that performs automated network device discovery and continuous uptime monitoring. The service operates through a multi-ticker event-driven architecture that concurrently executes six independent monitoring workflows:

1. **ICMP Discovery**: Periodic ICMP ping sweeps for device discovery with randomized scanning
2. **Pinger Reconciliation**: Automatic lifecycle management ensuring all devices have active ping monitoring
3. **SNMP Poller Reconciliation**: Automatic lifecycle management ensuring all devices have active SNMP polling
4. **State Pruning**: Removal of stale devices not seen in 24 hours
5. **Health Reporting**: Continuous metrics export to InfluxDB health bucket
6. **Background Operations**: SNMP enrichment for newly discovered devices

All discovered devices are stored in a central StateManager (the single source of truth), and all metrics are written to InfluxDB v2 using an optimized batching system. The service implements comprehensive concurrency safety through mutexes, context-based cancellation, WaitGroups, and panic recovery throughout all goroutines. Deployment is supported via Docker Compose with InfluxDB or native systemd installation with capability-based security.

---

## Core Architecture

### Multi-Ticker Event-Driven Design

The application uses six independent, concurrent tickers orchestrated in `cmd/netscan/main.go`, each implementing a specific monitoring workflow. All tickers run in a single select statement within the main event loop and are controlled by a shared context for graceful shutdown.

#### 1. ICMP Discovery Ticker

- **Interval:** Configurable via `cfg.IcmpDiscoveryInterval`
- **Purpose:** Periodically scans configured network ranges to discover responsive devices
- **Scanning Pattern:** IP addresses are scanned in randomized order to obscure sequential scanning
- **Operation:**
  - Calls `discovery.RunICMPSweep()` with context, networks, worker count (default 64), and rate limiter
  - RunICMPSweep buffers all IPs from all networks using `ipsFromCIDR()`
  - Shuffles IPs using `math/rand.Shuffle()` to randomize scan order across all subnets
  - Returns list of IPs that responded to ICMP echo requests
  - For each responsive IP, calls `stateMgr.AddDevice(ip)` to add to state
  - If device is new (`isNew == true`), launches background goroutine for immediate SNMP scan
  - SNMP results written to StateManager via `stateMgr.UpdateDeviceSNMP()` and InfluxDB via `writer.WriteDeviceInfo()`
- **Concurrency:** SNMP scans run in background goroutines with panic recovery
- **Memory Check:** Calls `checkMemoryUsage()` before each scan (warns if memory exceeds configured limit)
- **Memory Trade-off:** All IPs buffered in memory before shuffling (acceptable: /16 limit, 16GB default memory limit)

#### 2. Pinger Reconciliation Ticker

- **Interval:** Fixed 5 seconds
- **Purpose:** Ensures every device in StateManager has an active continuous pinger goroutine
- **Operation:**
  - Acquires `pingersMu` lock for thread-safe access to `activePingers` and `stoppingPingers` maps
  - Retrieves current IPs from StateManager via `stateMgr.GetAllIPs()`
  - Builds map with pre-allocated capacity for fast lookup (performance optimization)
  - **Start New Pingers:** For each IP in StateManager:
    - Checks if IP not in `activePingers` AND not in `stoppingPingers` (prevents race)
    - Respects `cfg.MaxConcurrentPingers` limit (default 20000)
    - Creates child context with `context.WithCancel(mainCtx)`
    - Stores cancel function in `activePingers[ip]`
    - Increments `pingerWg` before starting goroutine
    - Launches wrapper goroutine calling `monitoring.StartPinger()` and notifying `pingerExitChan` on completion
  - **Stop Removed Pingers:** For each IP in `activePingers`:
    - If IP not in current StateManager IPs, device was removed (pruned)
    - Moves IP to `stoppingPingers[ip] = true` before calling cancel function
    - Removes IP from `activePingers` map
    - Calls `cancelFunc()` to signal pinger stop (asynchronous, doesn't wait for exit)
  - Releases `pingersMu` lock
- **Race Prevention:** `stoppingPingers` map prevents race where device is pruned and quickly re-discovered before old pinger exits
- **Concurrency Safety:** All map access protected by `pingersMu` mutex

#### 3. SNMP Poller Reconciliation Ticker

- **Interval:** Fixed 10 seconds
- **Purpose:** Ensures every device in StateManager has an active continuous SNMP poller goroutine
- **Operation:**
  - Acquires `snmpPollersMu` lock for thread-safe access to `activeSNMPPollers` and `stoppingSNMPPollers` maps
  - Retrieves current IPs from StateManager via `stateMgr.GetAllIPs()`
  - Builds map with pre-allocated capacity for fast lookup
  - **Start New SNMP Pollers:** For each IP in StateManager:
    - Checks if IP not in `activeSNMPPollers` AND not in `stoppingSNMPPollers`
    - Respects `cfg.MaxConcurrentSNMPPollers` limit (default 20000)
    - Creates child context with `context.WithCancel(mainCtx)`
    - Stores cancel function in `activeSNMPPollers[ip]`
    - Increments `snmpPollerWg` before starting goroutine
    - Launches wrapper goroutine calling `monitoring.StartSNMPPoller()` and notifying `snmpPollerExitChan` on completion
  - **Stop Removed SNMP Pollers:** For each IP in `activeSNMPPollers`:
    - If IP not in current StateManager IPs, device was removed
    - Moves IP to `stoppingSNMPPollers[ip] = true` before calling cancel function
    - Removes IP from `activeSNMPPollers` map
    - Calls `cancelFunc()` to signal SNMP poller stop
  - Releases `snmpPollersMu` lock
- **Race Prevention:** `stoppingSNMPPollers` map prevents race condition similar to pingers
- **Concurrency Safety:** All map access protected by `snmpPollersMu` mutex

#### 4. State Pruning Ticker

- **Interval:** Fixed 1 hour
- **Purpose:** Removes devices that haven't been seen (successful ping) in the last 24 hours
- **Operation:**
  - Calls `stateMgr.PruneStale(24 * time.Hour)`
  - Returns list of pruned devices
  - Logs each pruned device at debug level with IP and hostname
  - Logs summary at info level if any devices were pruned
- **Integration:** Reconciliation tickers automatically detect removed devices and stop their pingers/pollers within 5-10 seconds

#### 5. Health Report Ticker

- **Interval:** Configurable via `cfg.HealthReportInterval` (default: 10s)
- **Purpose:** Writes application health and observability metrics to InfluxDB health bucket
- **Operation:**
  - Calls `healthServer.GetHealthMetrics()` to collect current metrics
  - Loads `totalPingsSent` atomic counter value
  - Calls `writer.WriteHealthMetrics()` with device count, active pingers, goroutines, memory (heap), RSS memory, suspended devices, InfluxDB status, batch success/failure counts, and total pings sent
- **Metrics Written:** Device count, active pinger count (from `currentInFlightPings.Load()`), total goroutines, heap memory MB, RSS memory MB, suspended device count, InfluxDB connectivity status, successful batch count, failed batch count, total pings sent

### Concurrency Model

The application uses a comprehensive concurrency model to ensure thread-safety and graceful shutdown across all components:

- **Context-Based Cancellation:**
  - Main context created with `context.WithCancel(context.Background())`
  - All child operations (discovery, SNMP scans, pingers, SNMP pollers) receive contexts derived from main context
  - Signal handler (SIGINT, SIGTERM) calls `stop()` function which cancels main context
  - Context cancellation propagates to all active goroutines, triggering coordinated shutdown

- **WaitGroup Tracking:**
  - `pingerWg`: Tracks all pinger goroutines for graceful shutdown
  - `snmpPollerWg`: Tracks all SNMP poller goroutines for graceful shutdown
  - `pingerWg.Add(1)` called before starting each pinger wrapper goroutine
  - `defer pingerWg.Done()` in `monitoring.StartPinger()` ensures count decremented when pinger exits
  - Shutdown sequence calls `pingerWg.Wait()` and `snmpPollerWg.Wait()` to block until all confirm exit

- **Mutex Protection:**
  - `pingersMu`: `sync.Mutex` protecting `activePingers` and `stoppingPingers` maps
  - `snmpPollersMu`: `sync.Mutex` protecting `activeSNMPPollers` and `stoppingSNMPPollers` maps
  - Locked during reconciliation loops when starting/stopping pingers/pollers
  - Locked when removing IPs from stopping maps via exit notification handlers

- **Atomic Counters:**
  - `currentInFlightPings` (`atomic.Int64`): Tracks active pinger count for health metrics
  - `totalPingsSent` (`atomic.Uint64`): Tracks cumulative pings sent for observability
  - `currentInFlightSNMPQueries` (`atomic.Int64`): Tracks active SNMP query count
  - `totalSNMPQueries` (`atomic.Uint64`): Tracks cumulative SNMP queries sent
  - Lock-free atomic operations for high-frequency updates without contention

- **Panic Recovery:**
  - All long-running goroutines wrapped with `defer func() { recover() }` pattern
  - Includes: discovery workers, SNMP scan workers, pingers, SNMP pollers, shutdown handler, pinger/SNMP poller exit notification handlers
  - Panic recovery logs error with context (IP, operation type) and continues operation
  - Prevents single goroutine panic from crashing entire service

- **Non-Blocking Operations:**
  - SNMP scans for newly discovered devices run in background goroutines
  - Pinger exit notifications use buffered channel (`pingerExitChan`, capacity 100)
  - SNMP poller exit notifications use buffered channel (`snmpPollerExitChan`, capacity 100)
  - Rate limiters use `golang.org/x/time/rate` package for non-blocking control

### Initialization Sequence

The application follows this strict initialization sequence in `main()`:

1. Parse `-config` CLI flag (default: "config.yml")
2. Initialize zerolog structured logging via `logger.Setup(false)` (debug mode disabled)
3. Load configuration from YAML file via `config.LoadConfig(*configPath)`
4. Validate configuration with `config.ValidateConfig(cfg)` (security and sanity checks, logs warnings)
5. Create StateManager with LRU eviction: `state.NewManager(cfg.MaxDevices)`
6. Create InfluxDB writer with batching: `influx.NewWriter()` with URL, token, org, bucket, health bucket, batch size, flush interval
7. Perform InfluxDB health check via `writer.HealthCheck()` (fail-fast with `log.Fatal()` on error)
8. Initialize global ping rate limiter: `rate.NewLimiter(rate.Limit(cfg.PingRateLimit), cfg.PingBurstLimit)`
9. Initialize global SNMP rate limiter: `rate.NewLimiter(rate.Limit(cfg.SNMPRateLimit), cfg.SNMPBurstLimit)`
10. Initialize atomic counters: `currentInFlightPings`, `totalPingsSent`, `currentInFlightSNMPQueries`, `totalSNMPQueries`
11. Initialize concurrency primitives:
    - `activePingers` map (IP → cancel function)
    - `stoppingPingers` map (IP → bool)
    - `pingersMu` mutex
    - `pingerWg` WaitGroup
    - `pingerExitChan` buffered channel (capacity 100)
    - `activeSNMPPollers` map (IP → cancel function)
    - `stoppingSNMPPollers` map (IP → bool)
    - `snmpPollersMu` mutex
    - `snmpPollerWg` WaitGroup
    - `snmpPollerExitChan` buffered channel (capacity 100)
12. Start health check HTTP server with callbacks for dynamic pinger count and total pings sent
13. Setup signal handling: `signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)`
14. Create main context with cancel function: `mainCtx, stop := context.WithCancel(context.Background())`
15. Create all six tickers (ICMP discovery, pinger reconciliation, SNMP poller reconciliation, pruning, health report)
16. Run initial ICMP discovery scan before entering main event loop
17. Start shutdown handler goroutine (listens for signals)
18. Start pinger exit notification handler goroutine (removes IPs from `stoppingPingers`)
19. Start SNMP poller exit notification handler goroutine (removes IPs from `stoppingSNMPPollers`)
20. Enter main event loop (select statement with all ticker cases)

### Graceful Shutdown Sequence

When shutdown signal (SIGINT or SIGTERM) is received, the following sequence executes:

1. Signal received on `sigChan` in shutdown handler goroutine
2. Shutdown handler calls `stop()` function, canceling main context (`mainCtx`)
3. Main event loop receives `<-mainCtx.Done()` in select case, enters shutdown block
4. Stop all tickers explicitly via `.Stop()` calls:
   - `icmpDiscoveryTicker.Stop()`
   - `reconciliationTicker.Stop()`
   - `snmpReconciliationTicker.Stop()`
   - `pruningTicker.Stop()`
   - (Health report ticker stopped implicitly)
5. Acquire `pingersMu` lock for exclusive access
6. Iterate `activePingers` map and call all cancel functions:
   - `for ip, cancel := range activePingers { cancel() }`
   - Each `cancel()` triggers context cancellation in corresponding pinger
7. Release `pingersMu` lock
8. Acquire `snmpPollersMu` lock for exclusive access
9. Iterate `activeSNMPPollers` map and call all cancel functions
10. Release `snmpPollersMu` lock
11. Call `pingerWg.Wait()` to block until all pinger goroutines exit
12. Call `snmpPollerWg.Wait()` to block until all SNMP poller goroutines exit
13. Call `writer.Close()` to flush remaining batched points:
    - Cancels batch flusher context
    - Drains points from batch channel
    - Flushes remaining points to both WriteAPIs (primary and health buckets)
    - Closes InfluxDB client
14. Log "Shutdown complete" and return from `main()`

---

## Technology Stack

**Language:** Go 1.25.1
- Module: `github.com/kljama/netscan` (as specified in `go.mod`)
- Go version requirement: `go 1.25.1` (minimum version)

**Primary Dependencies (from `go.mod`):**

- **`gopkg.in/yaml.v3 v3.0.1`** - YAML configuration file parsing
  - Used in `config.LoadConfig()` to parse `config.yml`
  - Supports struct tags for mapping YAML fields to Go structs

- **`github.com/gosnmp/gosnmp v1.42.1`** - SNMPv2c protocol implementation
  - Used in `discovery.RunSNMPScan()` and `monitoring.StartSNMPPoller()` for querying device metadata
  - Supports Get, GetNext, and Walk operations
  - Queries sysName (hostname) and sysDescr (system description) OIDs

- **`github.com/prometheus-community/pro-bing v0.7.0`** - ICMP ping implementation with raw socket support
  - Used in `discovery.RunICMPSweep()` for device discovery
  - Used in `monitoring.StartPinger()` for continuous uptime monitoring
  - Requires CAP_NET_RAW capability or root privileges for raw ICMP sockets

- **`github.com/influxdata/influxdb-client-go/v2 v2.14.0`** - InfluxDB v2 client with WriteAPI
  - Used in `influx.NewWriter()` for batched time-series writes
  - Supports dual-bucket writes (primary metrics + health metrics)
  - Provides non-blocking WriteAPI with background flushing

- **`github.com/rs/zerolog v1.34.0`** - Zero-allocation structured logging (JSON and console)
  - Configured in `logger.Setup()` with service name, timestamp, and caller info
  - Supports debug/info/warn/error/fatal levels
  - Console output when `ENVIRONMENT=development` environment variable set
  - Debug level enabled via `debugMode` parameter or `DEBUG=true` environment variable
  - Adds caller information (file:line) to all log entries for debugging

- **`golang.org/x/time v0.14.0`** - Rate limiting utilities
  - Used to create `rate.NewLimiter()` for global ping and SNMP rate limiting
  - Controls sustained ICMP ping rate and SNMP query rate across all devices
  - Prevents network flooding and resource exhaustion

**Standard Library Usage:**

- **`sync`** - Concurrency primitives
  - `sync.Mutex` / `sync.RWMutex` - Protects shared maps (activePingers, stoppingPingers, activeSNMPPollers, stoppingSNMPPollers, StateManager devices)
  - `sync.WaitGroup` - Tracks pinger and SNMP poller goroutine lifecycle
  - `sync/atomic` - Lock-free atomic counters for metrics

- **`context`** - Cancellation propagation and timeout control
  - Main context with `context.WithCancel()` for graceful shutdown
  - Child contexts for pingers, SNMP pollers, discovery, and SNMP scans
  - Timeout contexts for individual operations

- **`time`** - Time-based operations
  - `time.NewTicker()` - Six independent ticker loops
  - `time.Duration` - Interval configuration
  - `time.NewTimer()` - Used in pinger and SNMP poller for interval-based scheduling

- **`flag`** - CLI argument parsing
  - Single `-config` flag for configuration file path (default: "config.yml")

- **`net`** - IP address validation and parsing
  - Used in device validation and network operations
  - IP format validation and address type checking

- **`os/signal`** - Signal handling for graceful shutdown
  - `signal.Notify()` - Listens for SIGINT and SIGTERM
  - Triggers context cancellation on signal receipt

- **`container/heap`** - Min-heap implementation for O(log n) LRU eviction
  - Used in `state.Manager` for efficient device eviction
  - `heap.Init()`, `heap.Push()`, `heap.Pop()`, `heap.Fix()` operations

---

## Deployment Models

### Docker Deployment (Primary - Recommended)

**Multi-Stage Dockerfile:**

- **Builder Stage:** Uses `golang:1.25-alpine` as build environment
  - Installs build dependencies: `git`, `ca-certificates`
  - Compiles binary with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`
  - Binary stripping via `-ldflags="-w -s"` to minimize size and remove debug symbols

- **Runtime Stage:** Uses `alpine:latest` for minimal production image
  - Creates non-root user `netscan` with dedicated group
  - Copies only the compiled binary from builder stage
  - Includes config template (`config.yml.example`)

**Runtime Stage Packages:**

- `ca-certificates` - TLS/SSL certificate validation for HTTPS connections
- `libcap` - Linux capability management utilities (provides `setcap`)
- `wget` - HTTP client for health check endpoint testing

**Capabilities:**

- **Dockerfile:** Sets `cap_net_raw+ep` capability on binary via `setcap cap_net_raw+ep /app/netscan`
  - `cap_net_raw` - Allows raw ICMP socket creation for ping operations
  - `+ep` flags - Effective and Permitted capability sets
- **docker-compose.yml:** Adds `NET_RAW` capability to container via `cap_add: - NET_RAW`
  - Grants container permission to create raw sockets at runtime
  - Required for ICMP echo request/reply functionality

**Security Note:**

- **Runtime User:** Container runs as `root` (non-root user commented out in Dockerfile)
- **Reason:** Linux kernel security model in containerized environments requires root privileges for raw ICMP socket access, even with `CAP_NET_RAW` capability set
- **Limitation:** Non-root users cannot create raw ICMP sockets in Docker containers despite capability grants
- **Comment in Dockerfile:** Lines 48-51 explain this security constraint

**Docker Compose Stack:**

- **Services:**
  - `netscan` - Network monitoring application (builds from Dockerfile)
  - `influxdb` - InfluxDB v2.7 time-series database for metrics storage
  - `nginx` - HTTPS reverse proxy for secure InfluxDB UI access
- **Service Dependencies:** `netscan` depends on `influxdb` with `condition: service_healthy`

**Network Mode:**

- **Configuration:** `network_mode: host` on netscan service
- **Purpose:** Provides direct access to host network stack for ICMP and SNMP operations
- **Impact:** Container shares host's network namespace, enabling network device discovery on local subnets

**Configuration:**

- **Config Mount:** `./config.yml:/app/config.yml:ro` (read-only)
- **Location:** Config file must exist in same directory as `docker-compose.yml`
- **Preparation:** Copy from template with `cp config.yml.example config.yml`
- **Environment Variables:** Loaded from `.env` file for credential management:
  - `INFLUXDB_TOKEN` (default: `netscan-token`)
  - `INFLUXDB_ORG` (default: `test-org`)
  - `SNMP_COMMUNITY` (default: `public`)
  - `DEBUG` (default: `false`)
  - `ENVIRONMENT` (default: `development`)

**Health Checks:**

- **Dockerfile HEALTHCHECK:**
  - Command: `wget --no-verbose --tries=1 --spider http://localhost:8080/health/live || exit 1`
  - Interval: 30 seconds
  - Timeout: 3 seconds
  - Start period: 40 seconds (grace period for startup)
  - Retries: 3 consecutive failures before marking unhealthy

- **docker-compose.yml healthcheck:**
  - Command: `["CMD", "wget", "--spider", "-q", "http://localhost:8080/health/live"]`
  - Same timing parameters as Dockerfile
  - Tests HTTP endpoint at `/health/live` on port 8080

**Log Rotation:**

- Driver: `json-file`
- Max size: `10m` per log file
- Max files: `3` retained files (~30MB total storage)

### Native systemd Deployment (Alternative - Maximum Security)

**Security Model:**

- **Dedicated System User:**
  - Creates `netscan` system user via `useradd -r -s /bin/false netscan`
  - `-r` flag: Creates system account (UID < 1000)
  - `-s /bin/false`: Disables shell login for security

- **Capability-Based Security:**
  - Command: `setcap cap_net_raw+ep /opt/netscan/netscan`
  - Grants only `CAP_NET_RAW` capability to binary (principle of least privilege)
  - Eliminates need for full root privileges
  - Capability persists across executions

- **Environment File Security:**
  - Location: `/opt/netscan/.env`
  - Permissions: `600` (owner read/write only)
  - Owner: `netscan:netscan` system user
  - Contains sensitive credentials (InfluxDB token, SNMP community string)
  - Automatically loaded by systemd service via `EnvironmentFile` directive

**Installation Location:**

- Base directory: `/opt/netscan/`
- Binary: `/opt/netscan/netscan`
- Configuration: `/opt/netscan/config.yml`
- Environment: `/opt/netscan/.env`
- Systemd service: `/etc/systemd/system/netscan.service`

**Systemd Service Hardening:**

The `deploy.sh` script generates a systemd service file with the following security features:

- **`NoNewPrivileges=yes`** - Prevents privilege escalation via setuid/setgid binaries
- **`PrivateTmp=yes`** - Provides isolated `/tmp` directory (not shared with host)
- **`ProtectSystem=strict`** - Makes entire filesystem read-only except specific writable paths
- **`AmbientCapabilities=CAP_NET_RAW`** - Grants only raw socket capability to process
- **`User=$SERVICE_USER` / `Group=$SERVICE_USER`** - Runs as dedicated non-root `netscan` user
- **`Restart=always`** - Automatic restart on failure for high availability
- **`EnvironmentFile=/opt/netscan/.env`** - Securely loads credentials from protected file

**Service Management:**

- Enable: `systemctl enable netscan`
- Start: `systemctl start netscan`
- Status: `systemctl status netscan`
- Logs: `journalctl -u netscan -f`

---

## Core Components

### Configuration System (`internal/config/config.go`)

**YAML Configuration Loading:**

- Configuration loaded from `config.yml` file via `LoadConfig(path string)` function
- Uses `gopkg.in/yaml.v3` decoder for parsing YAML to Go structs
- **Environment Variable Expansion:** Applies `os.ExpandEnv()` to sensitive fields after YAML parsing:
  - `influxdb.url`, `influxdb.token`, `influxdb.org`, `influxdb.bucket`, `influxdb.health_bucket`
  - `snmp.community`
- Supports `${VAR}` and `$VAR` syntax for environment variable substitution
- Duration parsing from string format (e.g., "5m", "1h") to `time.Duration` type

**Validation:**

- `ValidateConfig(cfg *Config)` performs security and sanity checks
- Returns `(warning string, error)` tuple - warnings for security concerns, errors for validation failures
- **Security Validations:**
  - CIDR network range validation (rejects loopback, multicast, link-local, overly broad ranges)
  - SNMP community string validation (character restrictions, weak password detection)
  - InfluxDB URL validation (http/https scheme enforcement, URL format checks)
  - IP address validation for network ranges
- **Sanity Checks:**
  - Worker count limits (ICMP: 1-2000, SNMP: 1-1000)
  - Interval minimums (discovery: 1min, ping: 1s, SNMP: 1min)
  - Resource protection limits (max devices: 1-100000, max pingers: 1-100000, max SNMP pollers: 1-100000, memory: 64-16384 MB)
  - Rate limiting validation (ping and SNMP rate/burst limits must be positive, burst >= rate)
  - Circuit breaker validation (max consecutive fails > 0, backoff >= 1 minute)

**Configuration Parameters with Defaults:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| **Main Config** | | | |
| `icmp_discovery_interval` | `time.Duration` | (required) | Interval for ICMP network discovery sweeps |
| `icmp_workers` | `int` | `64` | Number of concurrent ICMP discovery workers |
| `snmp_workers` | `int` | `32` | Number of concurrent SNMP scan workers |
| `networks` | `[]string` | (required) | List of CIDR network ranges to scan |
| `ping_interval` | `time.Duration` | (required) | Interval between continuous pings per device |
| `ping_timeout` | `time.Duration` | `3s` | Timeout for individual ping operations |
| `ping_rate_limit` | `float64` | `64.0` | Sustained ping rate in pings per second (token bucket rate) |
| `ping_burst_limit` | `int` | `256` | Maximum burst ping capacity (token bucket size) |
| `ping_max_consecutive_fails` | `int` | `10` | Circuit breaker: consecutive failures before suspension |
| `ping_backoff_duration` | `time.Duration` | `5m` | Circuit breaker: suspension duration after threshold |
| `snmp_interval` | `time.Duration` | `1h` | Interval for continuous SNMP polling per device |
| `snmp_rate_limit` | `float64` | `10.0` | Sustained SNMP query rate in queries per second |
| `snmp_burst_limit` | `int` | `50` | Maximum burst SNMP capacity (token bucket size) |
| `snmp_max_consecutive_fails` | `int` | `5` | Circuit breaker: consecutive SNMP failures before suspension |
| `snmp_backoff_duration` | `time.Duration` | `1h` | Circuit breaker: SNMP suspension duration after threshold |
| `health_check_port` | `int` | `8080` | HTTP port for health check endpoint |
| `health_report_interval` | `time.Duration` | `10s` | Interval for writing health metrics to InfluxDB |
| `max_concurrent_pingers` | `int` | `20000` | Maximum number of concurrent pinger goroutines |
| `max_concurrent_snmp_pollers` | `int` | `20000` | Maximum number of concurrent SNMP poller goroutines |
| `max_devices` | `int` | `20000` | Maximum devices managed by StateManager (LRU eviction) |
| `min_scan_interval` | `time.Duration` | `1m` | Minimum time between discovery scans |
| `memory_limit_mb` | `int` | `16384` | Memory limit in MB (warning threshold) |
| **SNMP Config** | | | |
| `snmp.community` | `string` | (required) | SNMPv2c community string |
| `snmp.port` | `int` | (required) | SNMP port (typically 161) |
| `snmp.timeout` | `time.Duration` | `5s` | SNMP request timeout |
| `snmp.retries` | `int` | (required) | Number of SNMP retry attempts |
| **InfluxDB Config** | | | |
| `influxdb.url` | `string` | (required) | InfluxDB server URL (http:// or https://) |
| `influxdb.token` | `string` | (required) | InfluxDB authentication token |
| `influxdb.org` | `string` | (required) | InfluxDB organization name |
| `influxdb.bucket` | `string` | (required) | Primary bucket for ping/device metrics |
| `influxdb.health_bucket` | `string` | `"health"` | Bucket for application health metrics |
| `influxdb.batch_size` | `int` | `5000` | Number of points to batch before writing |
| `influxdb.flush_interval` | `time.Duration` | `5s` | Maximum time to hold points before flushing |

### State Management (`internal/state/manager.go`)

**Thread-Safe Device Registry:**

- `Manager` struct provides centralized device state storage
- **Concurrency Control:** `sync.RWMutex` (`mu` field) protects all map operations
  - Read operations use `RLock()`/`RUnlock()` for concurrent read access
  - Write operations use `Lock()`/`Unlock()` for exclusive write access
- **Device Storage:** `devices map[string]*Device` - maps IP addresses to device pointers
- **Capacity Management:** `maxDevices int` - enforces device count limit with O(log n) LRU eviction using min-heap

**Device Struct:**

- `IP string` - IPv4 address of the device (map key)
- `Hostname string` - Device hostname from SNMP or IP address as fallback
- `SysDescr string` - SNMP sysDescr MIB-II value (system description)
- `LastSeen time.Time` - Timestamp of last successful ping or discovery
- `ConsecutiveFails int` - Circuit breaker: counter for consecutive ping failures
- `SuspendedUntil time.Time` - Circuit breaker: timestamp when ping suspension expires
- `SNMPConsecutiveFails int` - Circuit breaker: counter for consecutive SNMP failures
- `SNMPSuspendedUntil time.Time` - Circuit breaker: timestamp when SNMP suspension expires
- `heapIndex int` - Index in the min-heap for O(log n) eviction (internal use only)

**Min-Heap for O(log n) LRU Eviction:**

- `deviceHeap` type implements `heap.Interface` for min-heap ordered by `LastSeen` timestamp
- Oldest devices (smallest `LastSeen`) at top of heap for efficient eviction
- Heap operations: `Push()`, `Pop()`, `Fix()`, `Swap()`, `Less()`, `Len()`
- `heap.Fix()` called when `LastSeen` changes to maintain heap ordering (O(log n))
- Replaces O(n) iteration with O(log n) heap operations for eviction

**Public Methods:**

- **`NewManager(maxDevices int) *Manager`**
  - Constructor: Creates new state manager with device capacity limit
  - Default: 10000 devices if maxDevices <= 0
  - Initializes heap with `heap.Init()`

- **`Add(device Device)`**
  - Adds or updates complete device struct
  - Idempotent: updates existing device if IP already exists
  - O(log n) eviction: removes oldest device (via heap.Pop) when maxDevices reached
  - Updates heap position with `heap.Fix()` if LastSeen changed

- **`AddDevice(ip string) bool`**
  - Adds device by IP only, minimal initialization
  - Returns `true` if new device, `false` if already exists
  - Sets Hostname to IP, LastSeen to current time
  - O(log n) eviction: same as Add() method

- **`Get(ip string) (*Device, bool)`**
  - Retrieves device by IP address
  - Returns `(device, true)` if found, `(nil, false)` if not found

- **`GetAll() []Device`**
  - Returns copy of all devices as slice (value copies, not pointers)
  - Safe for iteration without holding lock

- **`GetAllIPs() []string`**
  - Returns slice of all device IP addresses
  - Used by reconciliation loops and daily SNMP scan

- **`UpdateLastSeen(ip string)`**
  - Updates LastSeen timestamp to current time
  - Called on successful ping to mark device as alive
  - Updates heap position with `heap.Fix()` (O(log n))

- **`UpdateDeviceSNMP(ip, hostname, sysDescr string)`**
  - Enriches device with SNMP metadata
  - Updates Hostname, SysDescr, and LastSeen fields
  - Updates heap position with `heap.Fix()` (O(log n))

- **`PruneStale(olderThan time.Duration) []Device`**
  - Removes devices not seen within duration (e.g., 24 hours)
  - Returns slice of removed devices for logging
  - Rebuilds heap after bulk removals (more efficient than individual removals)
  - Alias: `Prune()` - same functionality

- **`Count() int`**
  - Returns current number of managed devices

- **`ReportPingSuccess(ip string)`**
  - Circuit breaker: resets ping failure counter and clears suspension
  - Sets ConsecutiveFails to 0, SuspendedUntil to zero time
  - Updates suspended device count atomic counter

- **`ReportPingFail(ip string, maxFails int, backoff time.Duration) bool`**
  - Circuit breaker: increments ping failure counter
  - Returns `true` if device was suspended (threshold reached)
  - On suspension: resets counter, sets SuspendedUntil to now + backoff
  - Updates suspended device count atomic counter

- **`IsSuspended(ip string) bool`**
  - Checks if device ping is currently suspended by circuit breaker
  - Returns `true` if SuspendedUntil is set and in the future

- **`GetSuspendedCount() int`**
  - Returns count of currently ping-suspended devices
  - Uses cached atomic counter for O(1) performance (optimized for frequent calls)
  - Note: Count may be slightly stale if suspensions expired but not cleared

- **`GetSuspendedCountAccurate() int`**
  - Returns accurate count by iterating all devices (O(n))
  - Provides most up-to-date count including expired suspensions
  - Only use when accuracy is critical (e.g., debugging)

- **`ReportSNMPSuccess(ip string)`**
  - Circuit breaker: resets SNMP failure counter and clears SNMP suspension
  - Sets SNMPConsecutiveFails to 0, SNMPSuspendedUntil to zero time
  - Updates SNMP suspended device count atomic counter

- **`ReportSNMPFail(ip string, maxFails int, backoff time.Duration) bool`**
  - Circuit breaker: increments SNMP failure counter
  - Returns `true` if SNMP polling was suspended (threshold reached)
  - On suspension: resets counter, sets SNMPSuspendedUntil to now + backoff
  - Updates SNMP suspended device count atomic counter

- **`IsSNMPSuspended(ip string) bool`**
  - Checks if SNMP polling is currently suspended by circuit breaker
  - Returns `true` if SNMPSuspendedUntil is set and in the future

- **`GetSNMPSuspendedCount() int`**
  - Returns count of devices with SNMP polling currently suspended
  - Uses cached atomic counter for O(1) performance

**LRU Eviction:**

- Triggered in `Add()` and `AddDevice()` when `len(devices) >= maxDevices`
- **Eviction Algorithm:**
  1. Call `heap.Pop(&m.evictionHeap)` to get oldest device (O(log n))
  2. Delete device from map
  3. Add new device to freed slot
  4. Call `heap.Push(&m.evictionHeap, devicePtr)` to add new device (O(log n))
- **Guarantees:** Device count never exceeds maxDevices limit
- **Performance:** O(log n) eviction time (improved from O(n) iteration)

### InfluxDB Writer (`internal/influx/writer.go`)

**High-Performance Batch System:**

- **Architecture:** Channel-based lock-free design with background flusher goroutine
- **Components:**
  - `batchChan chan *write.Point` - Buffered channel (capacity: 2x batch size)
  - `backgroundFlusher()` - Goroutine that accumulates and flushes points
  - `flushTicker *time.Ticker` - Triggers time-based flushes at `flushInterval`
- **Batching Logic:**
  - Accumulates points in local slice until batch size reached OR flush interval elapsed
  - Size-based flush: immediately when batch reaches `batchSize` points
  - Time-based flush: every `flushInterval` even if batch incomplete
  - Non-blocking writes: drops points if channel full (logs warning)

**Dual-Bucket Architecture:**

- **Primary WriteAPI** (`writeAPI`): Writes ping results and device info to main bucket
- **Health WriteAPI** (`healthWriteAPI`): Writes application health metrics to separate health bucket
- **Rationale:** Separates operational metrics from application monitoring data
- **Error Monitoring:** Each WriteAPI has dedicated error channel monitored by `monitorWriteErrors()` goroutine

**Constructor:**

- **`NewWriter(url, token, org, bucket, healthBucket string, batchSize int, flushInterval time.Duration) *Writer`**
  - Creates InfluxDB client with dual WriteAPI instances
  - Initializes buffered batch channel (capacity: `batchSize * 2`)
  - Starts background flusher goroutine immediately
  - Obtains error channels once during initialization (stored for reuse)
  - Returns Writer with context-based cancellation support

**HealthCheck():**

- Verifies InfluxDB connectivity using client health API
- 5-second timeout via context
- Returns error if health status is not "pass"
- Called during application startup (fail-fast if InfluxDB unavailable)

**Batching Architecture Details:**

- **`batchChan chan *write.Point`**
  - Buffered channel for lock-free point submission
  - Capacity: 2x batch size to prevent blocking during normal operation
  - Writers use non-blocking send with default case (drops on full)

- **`batchSize int`**
  - Number of points to accumulate before flushing
  - Default: 5000 points (configurable via InfluxDB config)
  - Triggers immediate flush when reached

- **`flushInterval time.Duration`**
  - Maximum time to hold points before flushing
  - Default: 5 seconds (configurable via InfluxDB config)
  - Ensures timely data delivery even with low write rates

- **Background Flusher:**
  - Single goroutine with panic recovery
  - Select loop handles: context cancellation, flush timer, new points
  - Accumulates points in local slice (no mutex needed)
  - Flushes to InfluxDB via `flushWithRetry()` with exponential backoff

**Graceful Shutdown:**

- **`Close()` Method:**
  1. Cancels context - signals background flusher to stop
  2. Stops flush ticker
  3. Waits 100ms for background flusher to finish
  4. Background flusher calls `drainAndFlush()` - empties channel and flushes remaining points
  5. Flushes both WriteAPI buffers (primary and health)
  6. Closes InfluxDB client connection
- **Guarantees:** No data loss on graceful shutdown - all queued points flushed

**Write Methods:**

- **`WritePingResult(ip string, rtt time.Duration, successful bool, suspended bool) error`**
  - **Measurement:** `"ping"`
  - **Tags:** `ip` (device IP address)
  - **Fields:**
    - `rtt_ms` (float64): Round-trip time in milliseconds
    - `success` (bool): Ping success/failure status
    - `suspended` (bool): Circuit breaker suspension status
  - **Validation:** IP address format, RTT range (0 to 1 minute)
  - **Batching:** Adds to batch channel via `addToBatch()`

- **`WriteDeviceInfo(ip, hostname, sysDescr string) error`**
  - **Measurement:** `"device_info"`
  - **Tags:** `ip` (device IP address)
  - **Fields:**
    - `hostname` (string): Device hostname from SNMP
    - `snmp_description` (string): SNMP sysDescr value
  - **Validation:** IP address format
  - **Sanitization:** Applies `sanitizeInfluxString()` to hostname and sysDescr
  - **Batching:** Adds to batch channel via `addToBatch()`

- **`WriteHealthMetrics(deviceCount, pingerCount, goroutines, memMB, rssMB, suspendedCount int, influxOK bool, influxSuccess, influxFailed, pingsSentTotal uint64)`**
  - **Measurement:** `"health_metrics"`
  - **Tags:** None (application-level metrics)
  - **Fields:**
    - `device_count` (int): Total devices in StateManager
    - `active_pingers` (int): Currently running pinger goroutines
    - `suspended_devices` (int): Devices suspended by circuit breaker
    - `goroutines` (int): Total Go goroutines
    - `memory_mb` (int): Heap memory usage in MB
    - `rss_mb` (int): OS-level resident set size in MB
    - `influxdb_ok` (bool): InfluxDB connectivity status
    - `influxdb_successful_batches` (uint64): Cumulative successful batch writes
    - `influxdb_failed_batches` (uint64): Cumulative failed batch writes
    - `pings_sent_total` (uint64): Total pings sent since startup
  - **Write Path:** Bypasses batch channel, writes directly to `healthWriteAPI`
  - **Rationale:** Health metrics written on fixed interval, no need for batching

**Data Sanitization:**

- **`sanitizeInfluxString(s, fieldName string) string`**
  - **Purpose:** Prevents database corruption and injection attacks
  - **Operations:**
    - Length limiting: truncates to 500 characters, appends "..."
    - Control character removal: strips characters < 32 (except tab and newline)
    - Whitespace trimming: removes leading/trailing spaces
  - **Applied to:** hostname and sysDescr fields in WriteDeviceInfo()

**Metrics Tracking:**

- `successfulBatches atomic.Uint64` - Counter for successful batch writes
- `failedBatches atomic.Uint64` - Counter for failed batch writes
- Atomic operations ensure thread-safe updates from background flusher
- Exposed via `GetSuccessfulBatches()` and `GetFailedBatches()` for health reporting

**Error Handling:**

- `monitorWriteErrors()` goroutine continuously monitors error channels
- Logs errors with bucket context (primary or health)
- Retry logic in `flushWithRetry()`:
  - Up to 3 retry attempts with exponential backoff (1s, 2s, 4s)
  - Increments failed batch counter on final failure
  - Increments successful batch counter on success

### ICMP Discovery (`internal/discovery/scanner.go`)

**Function Signature:**
```go
func RunICMPSweep(ctx context.Context, networks []string, workers int, limiter *rate.Limiter) []string
```

**Worker Pool Pattern:**

- Creates `jobs` channel (buffered: 256) for IP addresses to ping
- Creates `results` channel (buffered: 256) for responsive IPs
- Launches `workers` goroutines (default: 64) that consume from `jobs` channel
- Each worker:
  - Acquires token from rate limiter via `limiter.Wait(ctx)` before pinging
  - Creates pinger with `probing.NewPinger(ip)`
  - Sends single ICMP echo request (1 second timeout)
  - Sends responsive IP to `results` channel if `stats.PacketsRecv > 0`
- Producer goroutine buffers all IPs from all networks, shuffles them using `math/rand.Shuffle`, then sends to `jobs` channel in randomized order
- Wait goroutine waits for all workers via `WaitGroup`, then closes `results` channel
- Main function collects all responsive IPs from `results` channel and returns slice

**Randomization:**

- All IPs from all configured networks first collected into master slice using `ipsFromCIDR()`
- Master slice shuffled using `rand.Shuffle()` to randomize scan order across all subnets
- Obscures sequential scanning pattern (e.g., 192.168.1.1, 192.168.1.2, 192.168.1.3...)
- Memory trade-off: All IPs buffered in memory (acceptable: /16 limit, 16GB default memory limit)

**Implementation Details:**

- **`ipsFromCIDR(network string) []string`**:
  - Expands CIDR notation into individual IP addresses as slice
  - Used by producer to build master IP list for shuffling
  - Parses CIDR with `net.ParseCIDR()`
  - Iterates through subnet using `incIP()` helper function
  - Safety limit: max 65,536 IPs per network (networks larger than /16 return empty slice)
  - Skips network address (first IP) and broadcast address (last IP) for /30 and larger networks
  - For /31 and /32 networks, includes all IPs (RFC 3021 - no network/broadcast addresses)

- **`streamIPsFromCIDR(network string, ipChan chan<- string)`**:
  - Legacy function that streams IPs sequentially to channel (used by other discovery functions)
  - Not used by RunICMPSweep (which uses ipsFromCIDR for randomization)
  - Avoids allocating memory for all IPs at once (memory-efficient for large networks)
  - Logs warning for networks larger than /16 (65K hosts)

- **`SetPrivileged(true)`**:
  - Configures pinger to use raw ICMP sockets
  - Requires CAP_NET_RAW capability or root privileges
  - Necessary for sending/receiving ICMP echo request/reply packets

### SNMP Scanning (`internal/discovery/scanner.go`)

**Function Signature:**
```go
func RunSNMPScan(ips []string, snmpConfig *config.SNMPConfig, workers int) []state.Device
```

**Worker Pool Pattern:**

- Creates `jobs` channel (buffered: 256) for IP addresses to scan
- Creates `results` channel (buffered: 256) for discovered devices
- Launches `workers` goroutines (default: 32) that consume from `jobs` channel
- Each worker:
  - Configures `gosnmp.GoSNMP` with target IP, port, community, version, timeout, retries
  - Connects via `params.Connect()`
  - Queries standard MIB-II OIDs using `snmp.GetWithFallback()`
  - Validates and sanitizes SNMP responses via `snmp.ValidateString()`
  - Sends `state.Device` with IP, Hostname, SysDescr, LastSeen to `results` channel
- Producer goroutine enqueues all IPs to `jobs` channel, then closes it
- Wait goroutine waits for all workers via `WaitGroup`, then closes `results` channel
- Main function collects all discovered devices from `results` channel and returns slice

**SNMP Robustness Features:**

- **`snmp.GetWithFallback(params *gosnmp.GoSNMP, oids []string) (*gosnmp.SnmpPacket, error)`**:
  - **Primary Strategy:** Attempts `params.Get(oids)` first (most efficient for .0 instances)
  - **Fallback Strategy:** If Get returns `NoSuchInstance`/`NoSuchObject`, tries `params.GetNext()` for each OID
  - **Rationale:** Some devices don't support .0 instance OIDs, GetNext retrieves next OID in tree
  - **Validation:** Verifies returned OID has the requested base OID as prefix
  - **Error Handling:** Returns error if no valid SNMP data retrieved from either method

- **`snmp.ValidateString(value interface{}, oidName string) (string, error)`**:
  - **Type Handling:** Accepts both `string` and `[]byte` types (SNMP OctetString values)
  - **Conversion:** Converts `[]byte` to string via `string(v)`
  - **Security Checks:**
    - Rejects strings containing null bytes (`\x00`)
    - Limits length to 1024 characters (truncates to prevent memory exhaustion)
  - **Sanitization:**
    - Replaces newlines/tabs (`\n`, `\r`, `\t`) with spaces
    - Removes non-printable ASCII characters (< 32 or > 126)
    - Trims whitespace
  - **Validation:** Returns error if string is empty after sanitization

**Queried OIDs:**

- **`1.3.6.1.2.1.1.5.0`** - `sysName` (device hostname)
- **`1.3.6.1.2.1.1.1.0`** - `sysDescr` (system description from MIB-II)

### Continuous Ping Monitoring (`internal/monitoring/pinger.go`)

**Function Signature:**
```go
func StartPinger(ctx context.Context, wg *sync.WaitGroup, device state.Device, interval time.Duration, timeout time.Duration, writer PingWriter, stateMgr StateManager, limiter *rate.Limiter, inFlightCounter *atomic.Int64, totalPingsSent *atomic.Uint64, maxConsecutiveFails int, backoffDuration time.Duration)
```

**Lifecycle:**

- Runs as dedicated goroutine per device (one pinger per monitored device)
- Uses `time.NewTimer()` for scheduling pings at configured intervals
- Defers `wg.Done()` to signal completion when goroutine exits
- Includes panic recovery with `defer func() { recover() }` pattern
- Continues until context is cancelled via `<-ctx.Done()`

**Operation:**

1. **Circuit Breaker Check:**
   - Calls `stateMgr.IsSuspended(device.IP)` before acquiring rate limiter token
   - Skips ping if device is suspended (circuit breaker tripped)
   - Writes suspension status to InfluxDB with `suspended=true` flag
   - Logs debug message and resets timer for next interval

2. **Rate Limiting:**
   - Acquires token from global rate limiter via `limiter.Wait(ctx)`
   - Blocks until token available or context cancelled
   - Ensures compliance with global ping rate limit across all devices

3. **Ping Execution:**
   - Calls `performPingWithCircuitBreaker()` with device, timeout, writer, state manager, counters, circuit breaker params
   - Increments `inFlightCounter` (atomic) at start, decrements on completion
   - Increments `totalPingsSent` (atomic) for observability

4. **IP Validation** (`validateIPAddress(ipStr string) error`):
   - Checks IP is not empty
   - Parses IP with `net.ParseIP()`
   - **Security Checks:**
     - Rejects loopback addresses (`ip.IsLoopback()`)
     - Rejects multicast addresses (`ip.IsMulticast()`)
     - Rejects link-local addresses (`ip.IsLinkLocalUnicast()`)
     - Rejects unspecified addresses (`ip.IsUnspecified()`)
   - Returns error if any check fails

5. **Success Criteria:**
   - Determines success by checking `len(stats.Rtts) > 0 && stats.AvgRtt > 0`
   - More reliable than just `stats.PacketsRecv` as RTT measurements prove response received
   - `stats.Rtts` is slice of individual round-trip times
   - `stats.AvgRtt` is average RTT across all attempts

6. **State Updates on Success:**
   - Calls `stateMgr.ReportPingSuccess(ip)` to reset circuit breaker failure counter
   - Calls `stateMgr.UpdateLastSeen(ip)` to update LastSeen timestamp

7. **State Updates on Failure:**
   - Calls `stateMgr.ReportPingFail(ip, maxConsecutiveFails, backoffDuration)` to increment failure counter
   - Returns `true` if device was suspended (threshold reached)
   - Logs warning when circuit breaker trips

8. **Metrics Writing:**
   - Calls `writer.WritePingResult(ip, rtt, success, suspended)` with device IP, RTT, success boolean, and suspension status
   - RTT is `stats.AvgRtt` for successful pings, `0` for failures
   - Suspended flag indicates circuit breaker status
   - Logs error if write fails (non-fatal, continues monitoring)

**Interface Design:**

```go
// PingWriter interface for writing ping results to external storage
type PingWriter interface {
    WritePingResult(ip string, rtt time.Duration, successful bool, suspended bool) error
    WriteDeviceInfo(ip, hostname, sysDescr string) error
}

// StateManager interface for updating device state and circuit breaker
type StateManager interface {
    UpdateLastSeen(ip string)
    ReportPingSuccess(ip string)
    ReportPingFail(ip string, maxFails int, backoff time.Duration) bool
    IsSuspended(ip string) bool
}
```

### Continuous SNMP Polling (`internal/monitoring/snmppoller.go`)

**Function Signature:**
```go
func StartSNMPPoller(ctx context.Context, wg *sync.WaitGroup, device state.Device, interval time.Duration, snmpConfig *config.SNMPConfig, writer SNMPWriter, stateMgr SNMPStateManager, limiter *rate.Limiter, inFlightCounter *atomic.Int64, totalSNMPQueries *atomic.Uint64, maxConsecutiveFails int, backoffDuration time.Duration)
```

**Lifecycle:**

- Runs as dedicated goroutine per device (one SNMP poller per monitored device)
- Uses `time.NewTimer()` for scheduling SNMP queries at configured intervals
- Defers `wg.Done()` to signal completion when goroutine exits
- Includes panic recovery with `defer func() { recover() }` pattern
- Continues until context is cancelled via `<-ctx.Done()`

**Operation:**

1. **Circuit Breaker Check:**
   - Calls `stateMgr.IsSNMPSuspended(device.IP)` before acquiring rate limiter token
   - Skips SNMP query if SNMP polling is suspended (circuit breaker tripped)
   - Logs debug message and resets timer for next interval

2. **Rate Limiting:**
   - Acquires token from global SNMP rate limiter via `limiter.Wait(ctx)`
   - Blocks until token available or context cancelled
   - Ensures compliance with global SNMP query rate limit across all devices

3. **SNMP Query Execution:**
   - Calls `performSNMPQueryWithCircuitBreaker()` with device, SNMP config, writer, state manager, counters, circuit breaker params
   - Increments `inFlightCounter` (atomic) at start, decrements on completion
   - Increments `totalSNMPQueries` (atomic) for observability

4. **SNMP Query Process:**
   - Configures `gosnmp.GoSNMP` with target IP, port, community, version, timeout, retries
   - Connects via `params.Connect()`
   - Queries standard MIB-II OIDs using `snmp.GetWithFallback()`
   - Validates and sanitizes SNMP responses via `snmp.ValidateString()`
   - Reports success or failure to circuit breaker

5. **State Updates on Success:**
   - Calls `stateMgr.ReportSNMPSuccess(ip)` to reset SNMP circuit breaker failure counter
   - Calls `stateMgr.UpdateDeviceSNMP(ip, hostname, sysDescr)` to update device metadata
   - Calls `writer.WriteDeviceInfo(ip, hostname, sysDescr)` to persist to InfluxDB

6. **State Updates on Failure:**
   - Calls `stateMgr.ReportSNMPFail(ip, maxConsecutiveFails, backoffDuration)` to increment SNMP failure counter
   - Returns `true` if SNMP polling was suspended (threshold reached)
   - Logs warning when SNMP circuit breaker trips

**Interface Design:**

```go
// SNMPStateManager interface for updating device SNMP data and circuit breaker state
type SNMPStateManager interface {
    UpdateDeviceSNMP(ip, hostname, sysDescr string)
    ReportSNMPSuccess(ip string)
    ReportSNMPFail(ip string, maxFails int, backoff time.Duration) bool
    IsSNMPSuspended(ip string) bool
}

// SNMPWriter interface for writing device info to external storage
type SNMPWriter interface {
    WriteDeviceInfo(ip, hostname, sysDescr string) error
}
```

**Local Helper Functions:**

- `snmp.GetWithFallback()` - Shared utility function
- `snmp.ValidateString()` - Shared utility function
- Same logic and security checks as discovery versions

### Health Check Server (`cmd/netscan/health.go`)

**HTTP Server:**

- Server started in `Start()` method via `http.ListenAndServe()`
- Runs in background goroutine with panic recovery
- Binds to port specified by `health_check_port` config (default: 8080)
- Non-blocking: returns immediately after starting goroutine
- Logs startup with `log.Info().Str("address", addr).Msg("Health check endpoint started")`

**Three Endpoints:**

1. **`/health`** - Detailed health information
   - Returns comprehensive `HealthResponse` JSON with all metrics
   - Always returns HTTP 200 (provides status in JSON field)
   - Calls `GetHealthMetrics()` to gather current metrics

2. **`/health/ready`** - Readiness probe
   - Checks if service is ready to accept traffic
   - Tests InfluxDB connectivity via `writer.HealthCheck()`
   - Returns HTTP 200 with "READY" if InfluxDB accessible
   - Returns HTTP 503 with "NOT READY: InfluxDB unavailable" if InfluxDB down
   - Used by orchestrators to determine when to send traffic

3. **`/health/live`** - Liveness probe
   - Indicates if service process is alive
   - Always returns HTTP 200 with "ALIVE" if handler responds
   - Used by orchestrators to determine if container should be restarted
   - Simple check: if we can respond, we're alive

**Health Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `status` | `string` | Overall status: "healthy" (InfluxDB OK) or "degraded" (InfluxDB down) |
| `version` | `string` | Version string (hardcoded "1.0.0", TODO: build-time variable) |
| `uptime` | `string` | Human-readable uptime since service start |
| `device_count` | `int` | Number of devices in StateManager |
| `suspended_devices` | `int` | Number of devices suspended by circuit breaker |
| `active_pingers` | `int` | Accurate count of active pinger goroutines (from callback) |
| `influxdb_ok` | `bool` | InfluxDB connectivity status (true if healthy) |
| `influxdb_successful` | `uint64` | Cumulative successful InfluxDB batch writes |
| `influxdb_failed` | `uint64` | Cumulative failed InfluxDB batch writes |
| `pings_sent_total` | `uint64` | Total monitoring pings sent since startup |
| `goroutines` | `int` | Current Go goroutine count via `runtime.NumGoroutine()` |
| `memory_mb` | `uint64` | Go heap memory usage in MB (from `runtime.MemStats.Alloc`) |
| `rss_mb` | `uint64` | OS-level resident set size in MB (from `/proc/self/status`) |
| `timestamp` | `time.Time` | Current timestamp when metrics collected |

**Memory Metrics Implementation:**

- **`memory_mb`** - Go heap allocation
  - Obtained from `runtime.MemStats.Alloc`
  - Represents memory allocated by Go runtime for heap objects
  - Only includes Go-managed memory (heap allocations)
  - Does not include stack memory, OS-level overhead, or memory-mapped files

- **`rss_mb`** - OS-level resident set size
  - Obtained via `getRSSMB()` function which reads `/proc/self/status`
  - Parses `VmRSS` field (in kB) and converts to MB
  - Represents total physical memory used by process (Linux-specific)
  - Includes: Go heap, stacks, memory-mapped files, shared libraries, OS overhead
  - More accurate reflection of actual memory consumption from OS perspective
  - Returns 0 on failure (non-Linux systems, permission issues, parse errors)
  - **Implementation:** Opens `/proc/self/status`, scans for line starting with "VmRSS:", parses value in kB, converts to MB

---

## Inferred Mandates & Principles

Based on recurring patterns in the source code, these are the foundational rules of the project:

### Concurrency Safety Mandates

- **Mandate: All shared state MUST be protected by mutexes**
  - `StateManager.devices` protected by `sync.RWMutex`
  - `activePingers` and `stoppingPingers` protected by `pingersMu`
  - `activeSNMPPollers` and `stoppingSNMPPollers` protected by `snmpPollersMu`
  - Read operations use `RLock()`, write operations use `Lock()`

- **Mandate: All long-running goroutines MUST have panic recovery**
  - Pattern: `defer func() { if r := recover(); r != nil { log.Error()... } }()`
  - Applied to: discovery workers, SNMP scan workers, pingers, SNMP pollers, shutdown handler, exit notification handlers
  - Logs panic with context (IP, operation type) for debugging
  - Prevents single goroutine panic from crashing entire service

- **Mandate: All goroutines MUST be tracked for graceful shutdown**
  - Use `sync.WaitGroup` for lifecycle tracking
  - Call `wg.Add(1)` before starting goroutine
  - Call `defer wg.Done()` at start of goroutine
  - Call `wg.Wait()` during shutdown to ensure all goroutines exit

- **Mandate: All goroutines MUST respect context cancellation**
  - Check `<-ctx.Done()` in select statements or loops
  - Return immediately when context is cancelled
  - Propagate context to all blocking operations (rate limiter, timeouts)

- **Mandate: Use atomic counters for high-frequency metrics**
  - Pattern: `atomic.Int64` or `atomic.Uint64` for counters
  - Methods: `.Add()`, `.Load()`, `.Store()`
  - Use cases: in-flight ping count, total pings sent, suspended device count
  - Provides lock-free thread-safe updates for frequently accessed counters

### Interface-Based Design Mandates

- **Mandate: Use interfaces for testability and dependency injection**
  - `PingWriter` interface: abstracts InfluxDB writer for testing
  - `StateManager` interface: abstracts state management for testing
  - `SNMPWriter` interface: abstracts SNMP result persistence
  - `SNMPStateManager` interface: abstracts SNMP state management
  - Enables mocking in unit tests without coupling to concrete implementations

- **Mandate: Keep interfaces minimal and focused**
  - Interfaces should have only the methods needed for specific use case
  - Example: `PingWriter` has only 2 methods (WritePingResult, WriteDeviceInfo)
  - Example: `StateManager` interface in pinger.go has only 4 methods (not entire Manager API)

### Error Handling Mandates

- **Mandate: Log all errors with context, then continue**
  - Never panic for operational errors (network failures, timeouts, SNMP errors)
  - Log with structured logging (`log.Error().Str("ip", ip).Err(err).Msg(...)`)
  - Include context: IP address, operation type, relevant parameters
  - Continue operation after error (graceful degradation)

- **Mandate: Use fail-fast for configuration errors**
  - Call `log.Fatal()` for: invalid config, InfluxDB unreachable at startup, missing required fields
  - Rationale: Service cannot operate correctly without valid configuration and dependencies
  - Fail-fast prevents service from running in degraded state

- **Mandate: Validate all external inputs**
  - IP addresses: use `validateIPAddress()` to reject dangerous addresses
  - SNMP strings: use `snmp.ValidateString()` to sanitize and validate
  - Network ranges: use `validateCIDR()` to reject dangerous networks
  - URLs: use `validateURL()` to ensure proper scheme and format

### Security Mandates

- **Mandate: Sanitize all external data before storage**
  - `sanitizeInfluxString()` for InfluxDB field values
  - `snmp.ValidateString()` for SNMP response data
  - Remove null bytes, control characters, limit length
  - Prevents injection attacks and database corruption

- **Mandate: Validate IP addresses before operations**
  - Reject loopback, multicast, link-local, unspecified addresses
  - Prevents scanning or monitoring dangerous address ranges
  - Applied in both ping and InfluxDB write operations

- **Mandate: Environment variable expansion only for credentials**
  - Apply `os.ExpandEnv()` to: InfluxDB URL/token/org/bucket, SNMP community
  - Supports `${VAR}` and `$VAR` syntax
  - Enables secure credential management via environment variables or `.env` files

### Performance Mandates

- **Mandate: Use buffered channels for producer-consumer patterns**
  - Discovery: `jobs` and `results` channels buffered at 256
  - InfluxDB: `batchChan` buffered at 2x batch size
  - Pinger exit notifications: `pingerExitChan` buffered at 100
  - Prevents blocking on channel operations during normal operation

- **Mandate: Use worker pools for concurrent operations**
  - ICMP discovery: configurable worker count (default: 64)
  - SNMP scanning: configurable worker count (default: 32)
  - Pattern: create jobs channel, spawn workers, wait for completion
  - Limits concurrency to prevent resource exhaustion

- **Mandate: Use rate limiting for network operations**
  - Global ping rate limiter: `rate.NewLimiter()` controls sustained ping rate
  - Global SNMP rate limiter: separate limiter for SNMP queries
  - Token bucket algorithm: rate (tokens/sec) and burst (capacity)
  - All pingers and SNMP pollers share single rate limiter per operation type

- **Mandate: Use batching for InfluxDB writes**
  - Channel-based lock-free batching with background flusher
  - Size-based flush: when batch reaches configured size (default: 5000)
  - Time-based flush: every configured interval (default: 5s)
  - Reduces InfluxDB API calls and improves write throughput

- **Mandate: Use min-heap for O(log n) LRU eviction**
  - `container/heap` package for efficient eviction
  - Operations: `heap.Push()`, `heap.Pop()`, `heap.Fix()`
  - Maintains devices ordered by LastSeen timestamp
  - O(log n) eviction instead of O(n) iteration

### Circuit Breaker Mandates

- **Mandate: Implement circuit breaker for all unreliable operations**
  - Ping circuit breaker: suspends device after N consecutive ping failures
  - SNMP circuit breaker: suspends SNMP polling after N consecutive SNMP failures
  - Independent circuit breakers for ping and SNMP (different failure modes)
  - Configuration: max consecutive fails, backoff duration

- **Mandate: Check circuit breaker before acquiring resources**
  - Check `IsSuspended()` BEFORE calling `limiter.Wait()`
  - Prevents wasting rate limiter tokens on known-failed devices
  - Skip operation entirely if suspended, write suspension status to InfluxDB

- **Mandate: Reset circuit breaker on first success**
  - Call `ReportPingSuccess()` or `ReportSNMPSuccess()` on successful operation
  - Resets failure counter to 0, clears suspension timestamp
  - Allows device to resume normal monitoring

### Code Organization Mandates

- **Mandate: Follow Go package structure conventions**
  - `cmd/netscan/` - Main application entry point
  - `internal/` - Private application packages
  - `internal/config/` - Configuration loading and validation
  - `internal/state/` - Device state management
  - `internal/influx/` - InfluxDB client and batching
  - `internal/discovery/` - ICMP and SNMP discovery
  - `internal/monitoring/` - Continuous ping and SNMP monitoring
  - `internal/logger/` - Structured logging setup

- **Mandate: Keep files focused and single-purpose**
  - `scanner.go` - Discovery functions (ICMP sweep, SNMP scan)
  - `pinger.go` - Continuous ping monitoring
  - `snmppoller.go` - Continuous SNMP polling
  - `manager.go` - State management
  - `writer.go` - InfluxDB batching and writes
  - `health.go` - Health check HTTP server

- **Mandate: Use clear, descriptive names**
  - Functions: `RunICMPSweep`, `StartPinger`, `UpdateLastSeen`
  - Variables: `activePingers`, `stoppingPingers`, `currentInFlightPings`
  - Constants: `MaxRetries`, `DefaultTimeout`
  - Avoid abbreviations except for well-known terms (IP, HTTP, SNMP, ICMP)

### Logging Mandates

- **Mandate: Use structured logging exclusively**
  - Package: `github.com/rs/zerolog`
  - Pattern: `log.Info().Str("key", value).Msg("message")`
  - Levels: Debug, Info, Warn, Error, Fatal
  - Include context: IP address, operation type, error details

- **Mandate: Log at appropriate levels**
  - **Debug:** Detailed operation info (individual pings, SNMP queries, state updates)
  - **Info:** Major operations (service startup, discovery scans, health checks)
  - **Warn:** Recoverable issues (circuit breaker trips, rate limit warnings, memory warnings)
  - **Error:** Operation failures (network errors, InfluxDB writes, SNMP failures)
  - **Fatal:** Unrecoverable errors (config validation, InfluxDB unreachable at startup)

- **Mandate: Include relevant context in all log messages**
  - Always include IP address for device-specific operations
  - Include operation type for errors (ICMP discovery, SNMP scan, ping, etc.)
  - Include error details via `.Err(err)` when logging errors
  - Include relevant parameters (timeout, retry count, etc.)

### Testing Mandates

- **Mandate: All packages MUST have unit tests**
  - Test file naming: `*_test.go`
  - Test function naming: `TestFunctionName`
  - Use table-driven tests for multiple scenarios
  - Current test coverage: All internal packages have test files

- **Mandate: Use interfaces for mocking external dependencies**
  - Mock InfluxDB writer via `PingWriter` interface
  - Mock state manager via `StateManager` interface
  - Enables unit testing without real InfluxDB or state

- **Mandate: Test concurrency and race conditions**
  - Use `-race` flag when running tests: `go test -race ./...`
  - Test concurrent access to shared state
  - Test context cancellation behavior
  - Test graceful shutdown sequences

---

## Common Implementation Patterns

### Adding a New Ticker

When adding a new periodic operation:

1. **Add configuration parameter** in `internal/config/config.go`:
```go
type Config struct {
    NewScannerInterval time.Duration `yaml:"new_scanner_interval"`
}
```

2. **Create ticker in main.go**:
```go
newScannerTicker := time.NewTicker(cfg.NewScannerInterval)
defer newScannerTicker.Stop()
```

3. **Add to main event loop**:
```go
case <-newScannerTicker.C:
    // Your scan logic here
    log.Info().Msg("Starting new scanner...")
```

4. **Add to shutdown sequence**:
```go
newScannerTicker.Stop()
```

### Adding a New Metric

When adding a new InfluxDB metric:

1. **Add field to WriteHealthMetrics** in `internal/influx/writer.go`:
```go
func (w *Writer) WriteHealthMetrics(..., newMetric int) {
    p := influxdb2.NewPoint(
        "health_metrics",
        map[string]string{},
        map[string]interface{}{
            "new_metric": newMetric,
        },
        time.Now(),
    )
    w.healthWriteAPI.WritePoint(p)
}
```

2. **Add to health response** in `cmd/netscan/health.go`:
```go
type HealthResponse struct {
    NewMetric int `json:"new_metric"`
}
```

3. **Update GetHealthMetrics**:
```go
func (hs *HealthServer) GetHealthMetrics() HealthResponse {
    return HealthResponse{
        NewMetric: calculateNewMetric(),
    }
}
```

### Adding a New State Field

When adding a new device state field:

1. **Update Device struct** in `internal/state/manager.go`:
```go
type Device struct {
    IP       string
    NewField string  // Add your field
    LastSeen time.Time
}
```

2. **Add update method**:
```go
func (m *Manager) UpdateNewField(ip, value string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if dev, exists := m.devices[ip]; exists {
        dev.NewField = value
    }
}
```

3. **Update tests** in `internal/state/manager_test.go`:
```go
func TestUpdateNewField(t *testing.T) {
    // Test the new method
}
```

---

## Architecture Boundaries & Non-Goals

To keep the project focused, we explicitly **do not** do the following:

### Explicitly Out of Scope

- **No Web UI:** `netscan` is a headless backend service. A UI is out of scope.
- **No Additional Databases:** Data persistence is **exclusively for InfluxDB**. Do not add support for Prometheus, MySQL, PostgreSQL, etc.
- **No Network Modification:** This is a *read-only* monitoring tool. It must never perform active network changes (e.g., blocking IPs, modifying device configs).
- **No Alternative State Stores:** The `StateManager` is the **single source of truth** for device state. Do not create parallel device lists or caches.
- **No Root for Non-ICMP:** The Docker `root` user is *only* for ICMP raw sockets. All other operations should be possible as a non-root user.

### Design Constraints

- **Single InfluxDB Instance:** The service connects to a single InfluxDB instance (no failover, no clustering)
- **IPv4 Only:** The service currently supports only IPv4 addresses (IPv6 is future work)
- **SNMPv2c Only:** The service currently supports only SNMPv2c (SNMPv3 is future work)
- **Single Process:** The service runs as a single process (no distributed mode)
- **In-Memory State:** Device state is stored in memory (no persistence across restarts)

---

## Future Work / Deferred Features

The following features are intentionally deferred to keep the current implementation focused:

### Advanced Features (Future)

- **State Persistence:** Periodic state snapshots to disk for faster restart recovery
- **IPv6 Support:** Dual-stack IPv4/IPv6 discovery and monitoring
- **SNMPv3 Support:** Authentication and encryption for SNMP queries
- **Multi-Architecture Builds:** ARM64, ARM/v7 support for Raspberry Pi and ARM servers
- **Webhook Alerting:** Configurable webhook endpoints for device down alerts
- **Prometheus Metrics:** `/metrics` endpoint for Prometheus scraping
- **Device Grouping:** Tags and groups for organizing devices in InfluxDB

### Performance Enhancements (Future)

- **Connection Pooling:** SNMP connection reuse for devices with frequent queries
- **Distributed Tracing:** OpenTelemetry integration for request tracing across goroutines
- **Advanced Rate Limiting:** Per-device circuit breakers with exponential backoff

### Operational Features (Future)

- **Build-Time Version Injection:** Inject Git commit, version tag, and build date into binary
- **Configuration Hot-Reload:** Reload configuration without restart (via SIGHUP)
- **Grafana Dashboards:** Pre-built Grafana dashboards for InfluxDB visualization

**Note on Deferral:** These features are deferred, not rejected. They may be implemented when project scale or requirements change. Current implementation prioritizes simplicity, reliability, and performance for typical deployments (100-1000 devices).

---

## Development Workflow

### Building the Project

```bash
# Build binary
go build -o netscan ./cmd/netscan

# Build with version info (future)
go build -ldflags="-X main.Version=1.0.0 -X main.GitCommit=$(git rev-parse --short HEAD)" -o netscan ./cmd/netscan

# Build Docker image
docker build -t netscan:latest .
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with race detection
go test -race ./...

# Run tests with coverage
go test -cover ./...

# Run specific package tests
go test ./internal/state/...
```

### Local Development

```bash
# Copy config template
cp config.yml.example config.yml

# Edit config.yml with your settings
# - Set networks to scan
# - Set SNMP community string
# - Configure InfluxDB connection

# Run service
./netscan -config config.yml

# Run with Docker Compose
cp .env.example .env
docker-compose up -d

# View logs
docker-compose logs -f netscan
```

### Deployment

**Docker Deployment:**
```bash
cp config.yml.example config.yml
cp .env.example .env
# Edit .env with production credentials
docker-compose up -d
```

**Native Deployment:**
```bash
sudo deploy/deploy.sh
sudo systemctl status netscan
sudo journalctl -u netscan -f
```

**Undeployment:**
```bash
sudo deploy/undeploy.sh
```

---

## Maintenance and Operations

### Common Maintenance Procedures

- **Fresh deployment with clean InfluxDB data:**
  ```bash
  docker-compose down -v  # Removes influxdb-data volume
  docker-compose up -d --build
  ```

- **Rebuild and run latest code:**
  ```bash
  docker-compose up -d --build
  ```

- **Reclaim Docker disk space:**
  ```bash
  docker system prune
  ```

### Monitoring the Service

- **Health Check Endpoint:** `http://localhost:8080/health`
  - Returns JSON with device count, active pingers, memory usage, InfluxDB status
  
- **Readiness Probe:** `http://localhost:8080/health/ready`
  - Returns HTTP 200 if service is ready, HTTP 503 if not
  
- **Liveness Probe:** `http://localhost:8080/health/live`
  - Returns HTTP 200 if service is alive

- **Docker Logs:**
  ```bash
  docker-compose logs -f netscan
  ```

- **Systemd Logs:**
  ```bash
  sudo journalctl -u netscan -f
  ```

### Troubleshooting

**Service won't start:**
1. Check InfluxDB is running: `docker-compose ps`
2. Check configuration: `cat config.yml`
3. Check logs: `docker-compose logs netscan`
4. Verify InfluxDB credentials match between `config.yml` and `.env`

**No devices discovered:**
1. Check network ranges in `config.yml` are correct
2. Verify container has NET_RAW capability: `docker-compose config`
3. Check ICMP discovery logs for errors
4. Verify firewall allows ICMP traffic

**SNMP enrichment fails:**
1. Check SNMP community string in `.env` matches device configuration
2. Verify SNMP port 161 is accessible from container
3. Check SNMP device configuration (v2c enabled, community string correct)
4. Review SNMP scan logs for specific errors

**High memory usage:**
1. Check device count: `curl http://localhost:8080/health | jq '.device_count'`
2. Reduce `max_devices` in `config.yml` if too high
3. Reduce network ranges if scanning too many devices
4. Check for memory leaks with `memory_mb` and `rss_mb` metrics

**InfluxDB write failures:**
1. Check InfluxDB health: `curl http://localhost:8086/health`
2. Verify InfluxDB token and permissions
3. Check InfluxDB bucket exists
4. Review `influxdb_failed_batches` metric

---

## Conclusion

This document represents the complete architectural blueprint of the `netscan` project, derived entirely from the source code implementation. It serves as both the technical specification and the development guidelines for all future work.

All changes to the codebase must align with the architecture, mandates, and principles documented here. Any deviation from these guidelines must be justified and documented.

**Remember:** This is a living document. As the code evolves, this document should be updated to reflect the current state of the implementation.
