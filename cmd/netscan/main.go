package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kljama/netscan/internal/config"
	"github.com/kljama/netscan/internal/discovery"
	"github.com/kljama/netscan/internal/influx"
	"github.com/kljama/netscan/internal/logger"
	"github.com/kljama/netscan/internal/monitoring"
	"github.com/kljama/netscan/internal/state"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

func main() {
	configPath := flag.String("config", "config.yml", "Path to configuration file")
	flag.Parse()

	// Initialize structured logging
	logger.Setup(false) // Set to true for debug mode

	log.Info().Msg("netscan starting up...")
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Validate configuration for security and sanity
	warning, err := config.ValidateConfig(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}
	if warning != "" {
		log.Warn().Str("warning", warning).Msg("Configuration warning")
	}

	// Initialize state manager (single source of truth for devices)
	stateMgr := state.NewManager(cfg.MaxDevices)

	// Initialize InfluxDB writer with health check and batching
	writer := influx.NewWriter(
		cfg.InfluxDB.URL,
		cfg.InfluxDB.Token,
		cfg.InfluxDB.Org,
		cfg.InfluxDB.Bucket,
		cfg.InfluxDB.HealthBucket,
		cfg.InfluxDB.BatchSize,
		cfg.InfluxDB.BufferSize,
		cfg.InfluxDB.FlushInterval,
	)
	defer writer.Close()

	log.Info().Msg("Checking InfluxDB connectivity...")
	if err := writer.HealthCheck(); err != nil {
		log.Fatal().Err(err).Msg("InfluxDB connection failed")
	}
	log.Info().
		Int("batch_size", cfg.InfluxDB.BatchSize).
		Dur("flush_interval", cfg.InfluxDB.FlushInterval).
		Msg("InfluxDB connection successful ✓")

	// Initialize global rate limiter for ping operations
	// This controls the sustained rate of ICMP pings across all devices
	pingRateLimiter := rate.NewLimiter(rate.Limit(cfg.PingRateLimit), cfg.PingBurstLimit)
	log.Info().
		Float64("rate_limit", cfg.PingRateLimit).
		Int("burst_limit", cfg.PingBurstLimit).
		Msg("Ping rate limiter initialized")

	// Initialize global rate limiter for SNMP operations
	// This controls the sustained rate of SNMP queries across all devices
	snmpRateLimiter := rate.NewLimiter(rate.Limit(cfg.SNMPRateLimit), cfg.SNMPBurstLimit)
	log.Info().
		Float64("rate_limit", cfg.SNMPRateLimit).
		Int("burst_limit", cfg.SNMPBurstLimit).
		Msg("SNMP rate limiter initialized")

	// Initialize atomic counter for tracking in-flight pings
	var currentInFlightPings atomic.Int64

	// Initialize atomic counter for total pings sent (for observability/metrics)
	var totalPingsSent atomic.Uint64

	// Initialize atomic counter for tracking in-flight SNMP queries
	var currentInFlightSNMPQueries atomic.Int64

	// Initialize atomic counter for total SNMP queries sent (for observability/metrics)
	var totalSNMPQueries atomic.Uint64

	// Map IP addresses to their pinger cancellation functions
	// CRITICAL: Protected by mutex to prevent concurrent map access
	activePingers := make(map[string]context.CancelFunc)
	var pingersMu sync.Mutex

	// Map of IPs currently in the process of stopping
	// CRITICAL: Prevents starting a new pinger before old one fully exits
	// This fixes the race condition where a device is pruned and quickly re-discovered
	stoppingPingers := make(map[string]bool)

	// Channel for pingers to notify when they've fully exited
	// Buffer size allows multiple pingers to exit concurrently without blocking
	pingerExitChan := make(chan string, 100)

	// Map IP addresses to their SNMP poller cancellation functions
	// CRITICAL: Protected by mutex to prevent concurrent map access
	activeSNMPPollers := make(map[string]context.CancelFunc)
	var snmpPollersMu sync.Mutex

	// Map of IPs currently in the process of stopping SNMP pollers
	// CRITICAL: Prevents starting a new SNMP poller before old one fully exits
	stoppingSNMPPollers := make(map[string]bool)

	// Channel for SNMP pollers to notify when they've fully exited
	// Buffer size allows multiple SNMP pollers to exit concurrently without blocking
	snmpPollerExitChan := make(chan string, 100)

	// Start health check endpoint with accurate pinger count and total pings sent
	getPingerCount := func() int {
		return int(currentInFlightPings.Load())
	}
	getPingsSentCount := func() uint64 {
		return totalPingsSent.Load()
	}
	healthServer := NewHealthServer(cfg.HealthCheckPort, stateMgr, writer, getPingerCount, getPingsSentCount)
	if err := healthServer.Start(); err != nil {
		log.Warn().Err(err).Msg("Health check server failed to start")
	}

	// WaitGroup for tracking all pinger goroutines
	var pingerWg sync.WaitGroup

	// WaitGroup for tracking all SNMP poller goroutines
	var snmpPollerWg sync.WaitGroup

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	mainCtx, stop := context.WithCancel(context.Background())
	defer stop()

	// Memory monitoring function
	checkMemoryUsage := func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		memoryMB := m.Alloc / 1024 / 1024
		if memoryMB > uint64(cfg.MemoryLimitMB) {
			log.Warn().
				Uint64("memory_mb", memoryMB).
				Int("limit_mb", cfg.MemoryLimitMB).
				Msg("Memory usage exceeds limit")
		}
	}

	// Ticker 1: ICMP Discovery Loop - finds new devices
	icmpDiscoveryTicker := time.NewTicker(cfg.IcmpDiscoveryInterval)
	defer icmpDiscoveryTicker.Stop()

	// Ticker 2: Pinger Reconciliation Loop - ensures all devices have pingers
	reconciliationTicker := time.NewTicker(5 * time.Second)
	defer reconciliationTicker.Stop()

	// Ticker 3: SNMP Poller Reconciliation Loop - ensures all devices have SNMP pollers
	snmpReconciliationTicker := time.NewTicker(10 * time.Second)
	defer snmpReconciliationTicker.Stop()

	// Ticker 4: State Pruning Loop - removes stale devices
	pruningTicker := time.NewTicker(1 * time.Hour)
	defer pruningTicker.Stop()

	// Ticker 5: Health Report Loop - writes health metrics to InfluxDB
	healthReportTicker := time.NewTicker(cfg.HealthReportInterval)
	defer healthReportTicker.Stop()

	// Run initial ICMP discovery at startup
	log.Info().Msg("Starting ICMP discovery scan...")
	log.Info().Strs("networks", cfg.Networks).Msg("Scanning networks")
	responsiveIPs := discovery.RunICMPSweep(mainCtx, cfg.Networks, cfg.IcmpWorkers, pingRateLimiter)
	log.Info().Int("devices_found", len(responsiveIPs)).Msg("ICMP discovery completed")

	for _, ip := range responsiveIPs {
		isNew := stateMgr.AddDevice(ip)
		if isNew {
			log.Info().Str("ip", ip).Msg("New device found, performing initial SNMP scan")
			// Trigger immediate SNMP scan in background
			go func(newIP string) {
				// Panic recovery for SNMP scan goroutine
				defer func() {
					if r := recover(); r != nil {
						log.Error().
							Str("ip", newIP).
							Interface("panic", r).
							Msg("Initial SNMP scan panic recovered")
					}
				}()

				snmpDevices := discovery.RunSNMPScan([]string{newIP}, &cfg.SNMP, cfg.SnmpWorkers)
				if len(snmpDevices) > 0 {
					dev := snmpDevices[0]
					stateMgr.UpdateDeviceSNMP(dev.IP, dev.Hostname, dev.SysDescr)
					// Write device info to InfluxDB
					if err := writer.WriteDeviceInfo(dev.IP, dev.Hostname, dev.SysDescr); err != nil {
						log.Error().
							Str("ip", dev.IP).
							Err(err).
							Msg("Failed to write device info to InfluxDB")
					} else {
						log.Info().
							Str("ip", dev.IP).
							Str("hostname", dev.Hostname).
							Msg("Device enriched and written to InfluxDB")
					}
				} else {
					log.Debug().Str("ip", newIP).Msg("SNMP scan failed, will retry in next daily scan")
				}
			}(ip)
		}
	}

	// Shutdown handler
	go func() {
		// Panic recovery for shutdown handler
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("Shutdown handler panic recovered")
			}
		}()

		<-sigChan
		log.Info().Msg("Shutdown signal received, stopping all operations...")
		stop()
	}()

	// Pinger exit notification handler
	// Removes IPs from stoppingPingers when their goroutines fully exit
	go func() {
		// Panic recovery for exit notification handler
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("Pinger exit notification handler panic recovered")
			}
		}()

		for {
			select {
			case <-mainCtx.Done():
				return
			case ip := <-pingerExitChan:
				pingersMu.Lock()
				delete(stoppingPingers, ip)
				log.Debug().Str("ip", ip).Msg("Pinger fully exited, removed from stopping list")
				pingersMu.Unlock()
			}
		}
	}()

	// SNMP poller exit notification handler
	// Removes IPs from stoppingSNMPPollers when their goroutines fully exit
	go func() {
		// Panic recovery for SNMP exit notification handler
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Msg("SNMP poller exit notification handler panic recovered")
			}
		}()

		for {
			select {
			case <-mainCtx.Done():
				return
			case ip := <-snmpPollerExitChan:
				snmpPollersMu.Lock()
				delete(stoppingSNMPPollers, ip)
				log.Debug().Str("ip", ip).Msg("SNMP poller fully exited, removed from stopping list")
				snmpPollersMu.Unlock()
			}
		}
	}()

	log.Info().Msg("Starting monitoring loops...")
	log.Info().Dur("icmp_interval", cfg.IcmpDiscoveryInterval).Msg("ICMP Discovery interval")
	log.Info().Msg("Pinger Reconciliation: every 5s")
	log.Info().Msg("SNMP Poller Reconciliation: every 10s")
	log.Info().Msg("State Pruning: every 1h")
	log.Info().Dur("health_interval", cfg.HealthReportInterval).Msg("Health Report interval")

	// Main event loop with all tickers
	for {
		select {
		case <-mainCtx.Done():
			// Graceful shutdown
			log.Info().Msg("Shutting down all pingers and SNMP pollers...")

			// Stop all tickers
			icmpDiscoveryTicker.Stop()
			reconciliationTicker.Stop()
			snmpReconciliationTicker.Stop()
			pruningTicker.Stop()

			// Cancel all active pingers
			pingersMu.Lock()
			for ip, cancel := range activePingers {
				log.Debug().Str("ip", ip).Msg("Stopping pinger")
				cancel()
			}
			pingersMu.Unlock()

			// Cancel all active SNMP pollers
			snmpPollersMu.Lock()
			for ip, cancel := range activeSNMPPollers {
				log.Debug().Str("ip", ip).Msg("Stopping SNMP poller")
				cancel()
			}
			snmpPollersMu.Unlock()

			// Wait for all pingers to exit
			log.Info().Msg("Waiting for all pingers to stop...")
			pingerWg.Wait()

			// Wait for all SNMP pollers to exit
			log.Info().Msg("Waiting for all SNMP pollers to stop...")
			snmpPollerWg.Wait()

			log.Info().Msg("Shutdown complete")
			return

		case <-icmpDiscoveryTicker.C:
			// ICMP Discovery: Find new devices
			checkMemoryUsage()
			log.Info().Msg("Starting ICMP discovery scan...")
			log.Info().Strs("networks", cfg.Networks).Msg("Scanning networks")
			responsiveIPs := discovery.RunICMPSweep(mainCtx, cfg.Networks, cfg.IcmpWorkers, pingRateLimiter)
			log.Info().Int("devices_found", len(responsiveIPs)).Msg("ICMP discovery completed")

			for _, ip := range responsiveIPs {
				isNew := stateMgr.AddDevice(ip)
				if isNew {
					log.Info().Str("ip", ip).Msg("New device found, performing initial SNMP scan")
					// Trigger immediate SNMP scan in background
					go func(newIP string) {
						// Panic recovery for SNMP scan goroutine
						defer func() {
							if r := recover(); r != nil {
								log.Error().
									Str("ip", newIP).
									Interface("panic", r).
									Msg("Initial SNMP scan panic recovered")
							}
						}()

						snmpDevices := discovery.RunSNMPScan([]string{newIP}, &cfg.SNMP, cfg.SnmpWorkers)
						if len(snmpDevices) > 0 {
							dev := snmpDevices[0]
							stateMgr.UpdateDeviceSNMP(dev.IP, dev.Hostname, dev.SysDescr)
							// Write device info to InfluxDB
							if err := writer.WriteDeviceInfo(dev.IP, dev.Hostname, dev.SysDescr); err != nil {
								log.Error().
									Str("ip", dev.IP).
									Err(err).
									Msg("Failed to write device info to InfluxDB")
							} else {
								log.Info().
									Str("ip", dev.IP).
									Str("hostname", dev.Hostname).
									Msg("Device enriched and written to InfluxDB")
							}
						} else {
							log.Debug().Str("ip", newIP).Msg("SNMP scan failed, will retry via continuous SNMP poller")
						}
					}(ip)
				}
			}

		case <-reconciliationTicker.C:
			// Pinger Reconciliation: Ensure all devices have pingers
			pingersMu.Lock()

			// Get current state IPs
			currentIPs := stateMgr.GetAllIPs()
			// Pre-allocate map with exact capacity to avoid reallocation (performance optimization)
			currentIPMap := make(map[string]bool, len(currentIPs))
			for _, ip := range currentIPs {
				currentIPMap[ip] = true
			}

			// Start pingers for new devices
			// CRITICAL: Check both activePingers AND stoppingPingers to prevent race condition
			for ip := range currentIPMap {
				_, isActive := activePingers[ip]
				_, isStopping := stoppingPingers[ip]

				// Only start pinger if IP is not active AND not currently stopping
				if !isActive && !isStopping {
					if len(activePingers) >= cfg.MaxConcurrentPingers {
						log.Warn().
							Int("max_pingers", cfg.MaxConcurrentPingers).
							Str("ip", ip).
							Msg("Maximum concurrent pingers reached, skipping device")
						continue
					}
					log.Debug().Str("ip", ip).Msg("Starting continuous pinger")
					pingerCtx, pingerCancel := context.WithCancel(mainCtx)
					activePingers[ip] = pingerCancel

					// Get device info for logging
					dev, exists := stateMgr.Get(ip)
					if !exists {
						dev = &state.Device{IP: ip, Hostname: ip}
					}

					pingerWg.Add(1)
					// Create a wrapper goroutine to handle exit notification
					go func(d state.Device, ctx context.Context) {
						// Panic recovery for pinger wrapper
						defer func() {
							if r := recover(); r != nil {
								log.Error().
									Str("ip", d.IP).
									Interface("panic", r).
									Msg("Pinger wrapper panic recovered")
							}
						}()

						// Run the actual pinger
						monitoring.StartPinger(ctx, &pingerWg, d, cfg.PingInterval, cfg.PingTimeout, writer, stateMgr, pingRateLimiter, &currentInFlightPings, &totalPingsSent, nil)

						// Notify that this pinger has exited
						select {
						case pingerExitChan <- d.IP:
							// Successfully notified
						case <-mainCtx.Done():
							// Main context cancelled, don't block on notification
						}
					}(*dev, pingerCtx)
				} else if isStopping {
					log.Debug().
						Str("ip", ip).
						Msg("Pinger is stopping, will start new one after exit completes")
				}
			}

			// Stop pingers for removed devices
			// CRITICAL: Move to stoppingPingers first, then call cancelFunc
			for ip, cancelFunc := range activePingers {
				if !currentIPMap[ip] {
					log.Debug().Str("ip", ip).Msg("Stopping continuous pinger for stale device")

					// Move to stoppingPingers BEFORE calling cancelFunc
					stoppingPingers[ip] = true
					delete(activePingers, ip)

					// Now call cancelFunc (asynchronous - doesn't wait for goroutine exit)
					cancelFunc()
				}
			}

			pingersMu.Unlock()

		case <-snmpReconciliationTicker.C:
			// SNMP Poller Reconciliation: Ensure all devices have SNMP pollers
			snmpPollersMu.Lock()

			// Get current state IPs
			currentIPs := stateMgr.GetAllIPs()
			// Pre-allocate map with exact capacity to avoid reallocation (performance optimization)
			currentIPMap := make(map[string]bool, len(currentIPs))
			for _, ip := range currentIPs {
				currentIPMap[ip] = true
			}

			// Start SNMP pollers for new devices
			// CRITICAL: Check both activeSNMPPollers AND stoppingSNMPPollers to prevent race condition
			for ip := range currentIPMap {
				_, isActive := activeSNMPPollers[ip]
				_, isStopping := stoppingSNMPPollers[ip]

				// Only start SNMP poller if IP is not active AND not currently stopping
				if !isActive && !isStopping {
					if len(activeSNMPPollers) >= cfg.MaxConcurrentSNMPPollers {
						log.Warn().
							Int("max_snmp_pollers", cfg.MaxConcurrentSNMPPollers).
							Str("ip", ip).
							Msg("Maximum concurrent SNMP pollers reached, skipping device")
						continue
					}
					log.Debug().Str("ip", ip).Msg("Starting continuous SNMP poller")
					snmpPollerCtx, snmpPollerCancel := context.WithCancel(mainCtx)
					activeSNMPPollers[ip] = snmpPollerCancel

					// Get device info for logging
					dev, exists := stateMgr.Get(ip)
					if !exists {
						dev = &state.Device{IP: ip, Hostname: ip}
					}

					snmpPollerWg.Add(1)
					// Create a wrapper goroutine to handle exit notification
					go func(d state.Device, ctx context.Context) {
						// Panic recovery for SNMP poller wrapper
						defer func() {
							if r := recover(); r != nil {
								log.Error().
									Str("ip", d.IP).
									Interface("panic", r).
									Msg("SNMP poller wrapper panic recovered")
							}
						}()

						// Run the actual SNMP poller
						monitoring.StartSNMPPoller(ctx, &snmpPollerWg, d, cfg.SNMPInterval, &cfg.SNMP, writer, stateMgr, snmpRateLimiter, &currentInFlightSNMPQueries, &totalSNMPQueries, cfg.SNMPMaxConsecutiveFails, cfg.SNMPBackoffDuration)

						// Notify that this SNMP poller has exited
						select {
						case snmpPollerExitChan <- d.IP:
							// Successfully notified
						case <-mainCtx.Done():
							// Main context cancelled, don't block on notification
						}
					}(*dev, snmpPollerCtx)
				} else if isStopping {
					log.Debug().
						Str("ip", ip).
						Msg("SNMP poller is stopping, will start new one after exit completes")
				}
			}

			// Stop SNMP pollers for removed devices
			// CRITICAL: Move to stoppingSNMPPollers first, then call cancelFunc
			for ip, cancelFunc := range activeSNMPPollers {
				if !currentIPMap[ip] {
					log.Debug().Str("ip", ip).Msg("Stopping continuous SNMP poller for stale device")

					// Move to stoppingSNMPPollers BEFORE calling cancelFunc
					stoppingSNMPPollers[ip] = true
					delete(activeSNMPPollers, ip)

					// Now call cancelFunc (asynchronous - doesn't wait for goroutine exit)
					cancelFunc()
				}
			}

			snmpPollersMu.Unlock()

		case <-pruningTicker.C:
			// State Pruning: Remove devices not seen recently
			log.Info().Msg("Pruning stale devices...")
			pruned := stateMgr.PruneStale(24 * time.Hour)
			if len(pruned) > 0 {
				log.Info().Int("count", len(pruned)).Msg("Pruned stale devices")
				for _, dev := range pruned {
					log.Debug().
						Str("ip", dev.IP).
						Str("hostname", dev.Hostname).
						Msg("Pruned device")
				}
			}

		case <-healthReportTicker.C:
			// Health Report: Write health metrics to InfluxDB
			log.Debug().Msg("Writing health metrics...")
			metrics := healthServer.GetHealthMetrics()

			// Load total pings sent counter
			pingsSent := totalPingsSent.Load()

			writer.WriteHealthMetrics(
				metrics.DeviceCount,
				metrics.ActivePingers,
				metrics.Goroutines,
				int(metrics.MemoryMB),
				int(metrics.RSSMB),           // new RSS value (MB)
				metrics.SNMPSuspendedDevices, // suspended device count
				metrics.InfluxDBOK,
				metrics.InfluxDBSuccessful,
				metrics.InfluxDBFailed,
				metrics.DroppedPoints, // dropped points count
				pingsSent,             // total pings sent counter
			)
		}
	}
}
