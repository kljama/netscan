package monitoring

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/kljama/netscan/internal/config"
	"github.com/kljama/netscan/internal/snmp"
	"github.com/kljama/netscan/internal/state"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

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

// StartSNMPPoller runs continuous SNMP polling for a single device
// This mirrors the StartPinger architecture with rate limiting and circuit breaker
func StartSNMPPoller(ctx context.Context, wg *sync.WaitGroup, device state.Device, interval time.Duration, snmpConfig *config.SNMPConfig, writer SNMPWriter, stateMgr SNMPStateManager, limiter *rate.Limiter, inFlightCounter *atomic.Int64, totalSNMPQueries *atomic.Uint64, maxConsecutiveFails int, backoffDuration time.Duration) {
	// Panic recovery for SNMP poller goroutine
	defer func() {
		if r := recover(); r != nil {
			log.Error().
				Str("ip", device.IP).
				Interface("panic", r).
				Msg("SNMP poller panic recovered")
		}
	}()

	if wg != nil {
		defer wg.Done()
	}

	// Initialize timer for first SNMP query with 5 second delay to avoid immediate query storm
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// Stop timer on graceful shutdown
			timer.Stop()
			return
		case <-timer.C:
			// 1. CHECK CIRCUIT BREAKER *BEFORE* ACQUIRING TOKEN
			if stateMgr.IsSNMPSuspended(device.IP) {
				log.Debug().Str("ip", device.IP).Msg("SNMP polling is suspended (circuit breaker), skipping.")
				timer.Reset(interval) // Reset timer and wait for next cycle
				continue              // Skip SNMP query entirely
			}

			// 2. Acquire token from rate limiter (blocks until available or context cancelled)
			if err := limiter.Wait(ctx); err != nil {
				// Context was cancelled while waiting for token
				return
			}

			// 3. Perform the SNMP query with in-flight tracking and circuit breaker
			performSNMPQueryWithCircuitBreaker(device, snmpConfig, writer, stateMgr, inFlightCounter, totalSNMPQueries, maxConsecutiveFails, backoffDuration)

			// 4. Reset timer to schedule next SNMP query after interval
			// This ensures interval is time BETWEEN queries, not fixed schedule
			timer.Reset(interval)
		}
	}
}

// performSNMPQueryWithCircuitBreaker executes a single SNMP query with circuit breaker integration
func performSNMPQueryWithCircuitBreaker(device state.Device, snmpConfig *config.SNMPConfig, writer SNMPWriter, stateMgr SNMPStateManager, inFlightCounter *atomic.Int64, totalSNMPQueries *atomic.Uint64, maxConsecutiveFails int, backoffDuration time.Duration) {
	// Increment in-flight counter
	if inFlightCounter != nil {
		inFlightCounter.Add(1)
		// Ensure counter is decremented when SNMP operation completes
		defer inFlightCounter.Add(-1)
	}

	// Increment total SNMP queries counter (for observability)
	if totalSNMPQueries != nil {
		totalSNMPQueries.Add(1)
	}

	log.Debug().Str("ip", device.IP).Msg("Querying SNMP device")

	// Configure SNMP connection parameters
	params := &gosnmp.GoSNMP{
		Target:    device.IP,
		Port:      uint16(snmpConfig.Port),
		Community: snmpConfig.Community,
		Version:   gosnmp.Version2c,
		Timeout:   snmpConfig.Timeout,
		Retries:   snmpConfig.Retries,
	}

	if err := params.Connect(); err != nil {
		log.Debug().
			Str("ip", device.IP).
			Err(err).
			Msg("SNMP connection failed")

		// Report failure to circuit breaker
		if stateMgr != nil {
			wasSuspended := stateMgr.ReportSNMPFail(device.IP, maxConsecutiveFails, backoffDuration)
			if wasSuspended {
				log.Warn().
					Str("ip", device.IP).
					Dur("backoff", backoffDuration).
					Msg("SNMP polling failed max attempts, suspending SNMP (circuit breaker tripped)")
			}
		}
		return
	}
	defer params.Conn.Close()

	// Query standard MIB-II system OIDs: sysName, sysDescr
	// Using snmp.GetWithFallback to handle devices that don't support .0 instance
	oids := []string{"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.1.0"}
	resp, err := snmp.GetWithFallback(params, oids)
	if err != nil || len(resp.Variables) < 2 {
		log.Debug().
			Str("ip", device.IP).
			Err(err).
			Msg("SNMP query failed")

		// Report failure to circuit breaker
		if stateMgr != nil {
			wasSuspended := stateMgr.ReportSNMPFail(device.IP, maxConsecutiveFails, backoffDuration)
			if wasSuspended {
				log.Warn().
					Str("ip", device.IP).
					Dur("backoff", backoffDuration).
					Msg("SNMP polling failed max attempts, suspending SNMP (circuit breaker tripped)")
			}
		}
		return
	}

	// Validate and sanitize SNMP response data
	hostname, err := snmp.ValidateString(resp.Variables[0].Value, "sysName")
	if err != nil {
		log.Debug().
			Str("ip", device.IP).
			Err(err).
			Msg("Invalid sysName")

		// Report failure to circuit breaker
		if stateMgr != nil {
			wasSuspended := stateMgr.ReportSNMPFail(device.IP, maxConsecutiveFails, backoffDuration)
			if wasSuspended {
				log.Warn().
					Str("ip", device.IP).
					Dur("backoff", backoffDuration).
					Msg("SNMP polling failed max attempts, suspending SNMP (circuit breaker tripped)")
			}
		}
		return
	}

	sysDescr, err := snmp.ValidateString(resp.Variables[1].Value, "sysDescr")
	if err != nil {
		log.Debug().
			Str("ip", device.IP).
			Err(err).
			Msg("Invalid sysDescr")

		// Report failure to circuit breaker
		if stateMgr != nil {
			wasSuspended := stateMgr.ReportSNMPFail(device.IP, maxConsecutiveFails, backoffDuration)
			if wasSuspended {
				log.Warn().
					Str("ip", device.IP).
					Dur("backoff", backoffDuration).
					Msg("SNMP polling failed max attempts, suspending SNMP (circuit breaker tripped)")
			}
		}
		return
	}

	// SNMP query successful
	log.Debug().
		Str("ip", device.IP).
		Str("hostname", hostname).
		Msg("SNMP query successful")

	// Report success to circuit breaker (resets failure count)
	if stateMgr != nil {
		stateMgr.ReportSNMPSuccess(device.IP)
		stateMgr.UpdateDeviceSNMP(device.IP, hostname, sysDescr)
	}

	// Write device info to InfluxDB
	if err := writer.WriteDeviceInfo(device.IP, hostname, sysDescr); err != nil {
		log.Error().
			Str("ip", device.IP).
			Err(err).
			Msg("Failed to write device info")
	}
}
