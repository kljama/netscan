package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kljama/netscan/internal/influx"
	"github.com/kljama/netscan/internal/state"
	"github.com/kljama/netscan/internal/version"
	"github.com/rs/zerolog/log"
)

// HealthServer provides HTTP health check endpoint
type HealthServer struct {
	stateMgr          *state.Manager
	writer            *influx.Writer
	startTime         time.Time
	port              int
	getPingerCount    func() int
	getPingsSentCount func() uint64
	rssMB             atomic.Uint64
}

// HealthResponse represents the health check JSON response
type HealthResponse struct {
	Status             string    `json:"status"`                 // "healthy", "degraded", "unhealthy"
	Version            string    `json:"version"`                // Version string
	Uptime             string    `json:"uptime"`                 // Human readable uptime
	DeviceCount        int       `json:"device_count"`           // Number of monitored devices
	SNMPSuspendedDevices int     `json:"snmp_suspended_devices"` // Number of suspended devices (SNMP circuit breaker)
	ActivePingers      int       `json:"active_pingers"`         // Number of active pinger goroutines (accurate count)
	InfluxDBOK         bool      `json:"influxdb_ok"`            // InfluxDB connectivity status
	InfluxDBSuccessful uint64    `json:"influxdb_successful"` // Successful batch writes
	InfluxDBFailed     uint64    `json:"influxdb_failed"`     // Failed batch writes
	DroppedPoints      uint64    `json:"dropped_points"`      // Points dropped due to full buffer
	PingsSentTotal     uint64    `json:"pings_sent_total"`    // Total monitoring pings sent
	Goroutines         int       `json:"goroutines"`          // Current goroutine count
	MemoryMB           uint64    `json:"memory_mb"`           // Current memory usage in MB (Go heap Alloc)
	RSSMB              uint64    `json:"rss_mb"`              // OS-level resident set size in MB
	Timestamp          time.Time `json:"timestamp"`           // Current timestamp
}

// NewHealthServer creates a new health check server
func NewHealthServer(port int, stateMgr *state.Manager, writer *influx.Writer, getPingerCount func() int, getPingsSentCount func() uint64) *HealthServer {
	hs := &HealthServer{
		stateMgr:          stateMgr,
		writer:            writer,
		startTime:         time.Now(),
		port:              port,
		getPingerCount:    getPingerCount,
		getPingsSentCount: getPingsSentCount,
	}
	// Initial RSS reading
	hs.rssMB.Store(getRSSMB())
	return hs
}

// Start begins serving health checks (non-blocking)
func (hs *HealthServer) Start() error {
	http.HandleFunc("/health", hs.healthHandler)
	http.HandleFunc("/health/ready", hs.readinessHandler)
	http.HandleFunc("/health/live", hs.livenessHandler)

	addr := fmt.Sprintf(":%d", hs.port)
	go func() {
		// Panic recovery for health server goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("Health server panic recovered")
			}
		}()

		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Error().Err(err).Msg("Health server error")
		}
	}()

	// Background RSS updater (every 10 seconds)
	go func() {
		// Panic recovery for RSS updater goroutine
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("RSS updater panic recovered")
			}
		}()

		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			hs.rssMB.Store(getRSSMB())
		}
	}()

	log.Info().Str("address", addr).Msg("Health check endpoint started")
	return nil
}

// healthHandler provides detailed health information
func (hs *HealthServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := hs.GetHealthMetrics()

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// GetHealthMetrics gathers and returns current health metrics
func (hs *HealthServer) GetHealthMetrics() HealthResponse {
	// Get memory stats (Go runtime heap)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Get OS-level RSS (cached value)
	rssMB := hs.rssMB.Load()

	// Determine overall status
	influxOK := hs.writer.HealthCheck() == nil
	status := "healthy"
	if !influxOK {
		status = "degraded"
	}

	return HealthResponse{
		Status:               status,
		Version:              version.Version,
		Uptime:               time.Since(hs.startTime).String(),
		DeviceCount:          hs.stateMgr.Count(),
		SNMPSuspendedDevices: hs.stateMgr.GetSNMPSuspendedCount(),
		ActivePingers:        hs.getPingerCount(), // Accurate count from activePingers map
		InfluxDBOK:           influxOK,
		InfluxDBSuccessful:   hs.writer.GetSuccessfulBatches(),
		InfluxDBFailed:     hs.writer.GetFailedBatches(),
		DroppedPoints:      hs.writer.GetDroppedPoints(),
		PingsSentTotal:     hs.getPingsSentCount(), // Total pings sent counter
		Goroutines:         runtime.NumGoroutine(),
		MemoryMB:           m.Alloc / 1024 / 1024,
		RSSMB:              rssMB,
		Timestamp:          time.Now(),
	}
}

// readinessHandler indicates if service is ready to accept traffic
func (hs *HealthServer) readinessHandler(w http.ResponseWriter, r *http.Request) {
	// Service is ready if InfluxDB is accessible
	if err := hs.writer.HealthCheck(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("NOT READY: InfluxDB unavailable"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("READY"))
}

// livenessHandler indicates if service is alive
func (hs *HealthServer) livenessHandler(w http.ResponseWriter, r *http.Request) {
	// If we can respond, we're alive
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ALIVE"))
}

// getRSSMB attempts to read /proc/self/status and parse VmRSS (kB) to MB.
// This is Linux-specific. On failure it returns 0.
func getRSSMB() uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			// Expected format: "VmRSS:    3472 kB"
			if len(fields) >= 3 && fields[2] == "kB" {
				kb, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					return kb / 1024
				}
			}
		}
	}

	// Check for scanner errors
	if err := s.Err(); err != nil {
		return 0
	}

	return 0
}
