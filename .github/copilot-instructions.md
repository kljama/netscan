# netscan: Project Bible & AI Development Guide

> **Single Source of Truth (SSOT)**: This document contains the complete architecture, deployment model, and component details.
> **AI Mandate**: All developers (human and AI) must adhere to the principles and mandates in this guide.

## Table of Contents
1.  [Core Mandates & Principles](#core-mandates--principles)
2.  [Project Overview](#project-overview)
3.  [Core Architecture](#core-architecture)
4.  [Deployment Models](#deployment-models)
5.  [Core Components](#core-components)
6.  [Development Workflow](#development-workflow)
7.  [Maintenance & Operations](#maintenance--operations)
8.  [Architecture Boundaries](#architecture-boundaries--non-goals)

---

## Core Mandates & Principles

These are the foundational rules of the project. **Strict adherence is required.**

### 1. Concurrency & Safety
*   **Protect Shared State**: All shared maps and slices MUST be protected by mutexes (`sync.RWMutex`). Use `RLock()` for reads and `Lock()` for writes.
*   **Panic Recovery**: All long-running goroutines (workers, pingers, pollers) MUST have `defer recover()` blocks to prevent service crashes.
*   **Graceful Shutdown**: All goroutines MUST be tracked via `sync.WaitGroup` and respect `context.Context` cancellation.
*   **Atomic Metrics**: Use `sync/atomic` for high-frequency counters (e.g., in-flight requests) to avoid lock contention.

### 2. Error Handling
*   **Log & Continue**: For operational errors (network, SNMP), log with context (`ip`, `err`) and continue. Do NOT panic.
*   **Fail Fast**: For configuration or startup errors (invalid config, DB unreachable), call `log.Fatal()`.
*   **Validate Inputs**: Always validate IP addresses, CIDR ranges, and SNMP strings before processing.

### 3. Architecture & Design
*   **Interface-Driven**: Use interfaces (`PingWriter`, `StateManager`) to decouple components and enable testing.
*   **Worker Pools**: Use buffered channels and fixed-size worker pools for all batch operations (discovery, scanning).
*   **Rate Limiting**: Respect global rate limits (`x/time/rate`) for all network egress (ICMP and SNMP).
*   **Circuit Breakers**: Check circuit breaker status *before* attempting network operations. suspend specific devices upon repeated failures.

### 4. Security
*   **Sanitization**: Sanitize all external data (SNMP strings, hostnames) before writing to InfluxDB.
*   **Least Privilege**: The Docker container must run entirely as `root` for `CAP_NET_RAW` (ICMP) access, due to restrictions on raw sockets for non-root users in container environments.
*   **Credential Safety**: Never log secrets. Use environment variable expansion (`${VAR}`) for configuration values.

---

## Project Overview

`netscan` is a production-grade, event-driven network monitoring service written in Go 1.25+. It performs:
1.  **Automated Discovery**: Randomized ICMP sweeps associated with SNMP enrichment.
2.  **Continuous Monitoring**: Real-time ICMP ping and SNMP polling for thousands of devices.
3.  **Metrics Export**: Batched, non-blocking writes to InfluxDB v2.

**Key Stats**:
*   **Capacity**: 20,000+ concurrent devices.
*   **Architecture**: Multi-ticker event loop with 5 independent workflows, plus an autonomous InfluxDB batching workflow.
*   **Storage**: In-memory state (StateManager) + InfluxDB (Metrics).

---

## Core Architecture

The application runs a single event loop in `main.go` managing five concurrent tickers, while the InfluxDB `Writer` manages its own internal background flushing ticker.

### 1. ICMP Discovery Workflow
*   **Role**: Finds new devices.
*   **Mechanism**: Scans configured subnets (`RunICMPSweep`).
*   **Randomization**: Shuffles target IPs to avoid sequential scanning patterns.
*   **Integration**: New devices are added to `StateManager` and immediately queued for SNMP enrichment.

### 2. Reconciliation Workflows (Ping & SNMP)
*   **Role**: Ensures every managed device has active monitoring goroutines.
*   **Mechanism**:
    *   **Diff**: Compares `StateManager` devices vs. `activePingers`/`activeSNMPPollers`.
    *   **Start**: Launches goroutines for new devices.
    *   **Stop**: Cancels contexts for removed devices.
*   **Safety**: Uses specific "stopping" maps to prevent race conditions during rapid churn.

### 3. State Pruning
*   **Role**: Removes stale devices.
*   **Policy**: Evicts devices not seen (no successful ping) in 24 hours.
*   **Effect**: Reconciliation loops automatically stop monitoring for pruned devices.

### 4. Health Reporting
*   **Role**: Self-monitoring.
*   **Output**: Writes internal metrics (goroutines, memory, active pingers) to the InfluxDB `health` bucket every 10s.

### Concurrency Model
*   **Context Tree**: `mainCtx` -> `tickerCtx` -> `workerCtx`. Cancellation propagates down.
*   **WaitGroups**: `pingerWg` and `snmpPollerWg` track thousands of monitoring goroutines.
*   **Mutexes**: Granular locking on `StateManager` and Pinger/Poller maps.

---

## Deployment Models

### 1. Docker (Recommended)
*   **Image**: Multi-stage build (Alpine).
*   **Privileges**: Runs enabling `cap_net_raw` for ICMP.
*   **Network**: `network_mode: host` required for correct ARP/Discovery operations.
*   **Orchestration**: Docker Compose manages `netscan`, `influxdb`, and `nginx`.

### 2. Native Systemd (Hardened)
*   **User**: Dedicated `netscan` system user (no shell).
*   **Capabilities**: `setcap cap_net_raw+ep` on binary.
*   **Isolation**: `PrivateTmp=yes`, `ProtectSystem=strict`.
*   **Config**: Environment variables loaded from secure `/opt/netscan/.env` (mode 600).

---

## Core Components

### Configuration (`internal/config`)
*   **Format**: YAML with environment substitution (`${INFLUX_TOKEN}`).
*   **Validation**: Strict checks for CIDR validity, URL schemes, and logical limits (e.g., burst >= rate).

### State Manager (`internal/state`)
*   **Structure**: In-memory map protected by RWMutex.
*   **Eviction**: Min-Heap implementation for O(log n) LRU eviction when capacity is reached.
*   **Circuit Breakers**: Tracks `ConsecutiveFails` and expiration times for Ping/SNMP suspensions.

### InfluxDB Writer (`internal/influx`)
*   **Dual-Path**: Separate APIs for Business Metrics (`ping`, `device_info`) vs Health Metrics.
*   **Batching**: Lock-free channel (`batchChan`) with background flusher.
*   **Logic**: Updates flush on `BatchSize` (5000) OR `FlushInterval` (5s).

### Discovery (`internal/discovery`)
*   **Scanner**: Worker pool pattern. Rate-limited.
*   **SNMP**: Fallback logic (`Get` -> `GetNext`) to handle varied device support.

### Monitoring (`internal/monitoring`)
*   **Pinger**: Dedicated goroutine per device. Check Circuit Breaker -> Wait Rate Limit -> Ping -> Update State -> Write Metric.
*   **SNMP Poller**: Dedicated goroutine per device. Similar flow to Pinger.

---

## Development Workflow

### Build & Run
```bash
# Build
go build -ldflags="-X main.Version=$(git describe --tags)" -o netscan ./cmd/netscan

# Test (Race detection is mandatory)
go test -race ./...

# Run Local
./netscan -config config.yml
```

### Adding Features
1.  **New Metric**: Add to `HealthResponse` struct, update `GetHealthMetrics`, update `WriteHealthMetrics`.
2.  **New Ticker**: Define interval in Config, create Ticker in `main`, add to `select` loop, add to Shutdown sequence.

---

## Maintenance & Operations

### Health Checks (`/health`)
*   **`/health`**: Full JSON status (Mem, Goroutines, Influx Status).
*   **`/health/ready`**: InfluxDB connectivity check (Kubernetes Readiness).
*   **`/health/live`**: Process existence check (Kubernetes Liveness).

### Troubleshooting
*   **"0 devices found"**: Check `network_mode: host` and CIDR ranges.
*   **"Permission denied"**: Check `CAP_NET_RAW` or root privileges.
*   **High Memory**: Reduce `max_devices` or discovery frequency.

---

## Architecture Boundaries & Non-Goals
*   **No UI**: This is a headless service.
*   **No Active Actions**: Read-only monitoring. No config changes on devices.
*   **InfluxDB Only**: No support for other TSDBs.
*   **IPv4 Only**: IPv6 is future work.
