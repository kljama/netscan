package monitoring

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kljama/netscan/internal/state"
	probing "github.com/prometheus-community/pro-bing"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// PingWriter interface for writing ping results to external storage
type PingWriter interface {
	WritePingResult(ip string, rtt time.Duration, successful bool, suspended bool) error
	WriteDeviceInfo(ip, hostname, sysDescr string) error
}

// StateManager interface for updating device last seen timestamp
type StateManager interface {
	UpdateLastSeen(ip string)
	ReportPingSuccess(ip string)
	ReportPingFail(ip string, maxFails int, backoff time.Duration) bool
	IsSuspended(ip string) bool
}

// PingFunc defines the function signature for performing a ping
type PingFunc func(device state.Device, timeout time.Duration, writer PingWriter, stateMgr StateManager, inFlightCounter *atomic.Int64, totalPingsSent *atomic.Uint64, maxConsecutiveFails int, backoffDuration time.Duration)

// StartPinger runs continuous ICMP monitoring for a single device
func StartPinger(ctx context.Context, wg *sync.WaitGroup, device state.Device, interval time.Duration, timeout time.Duration, writer PingWriter, stateMgr StateManager, limiter *rate.Limiter, inFlightCounter *atomic.Int64, totalPingsSent *atomic.Uint64, maxConsecutiveFails int, backoffDuration time.Duration, pingOp PingFunc) {
	// Panic recovery for pinger goroutine
	defer func() {
		if r := recover(); r != nil {
			log.Error().
				Str("ip", device.IP).
				Interface("panic", r).
				Msg("Pinger panic recovered")
		}
	}()

	if wg != nil {
		defer wg.Done()
	}

	// Use default ping operation if none provided
	if pingOp == nil {
		pingOp = performPingWithCircuitBreaker
	}

	// Initialize timer for first ping with 1 second delay to avoid immediate ping storm
	timer := time.NewTimer(1 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// Stop timer on graceful shutdown
			timer.Stop()
			return
		case <-timer.C:
			// 1. CHECK CIRCUIT BREAKER *BEFORE* ACQUIRING TOKEN
			if stateMgr.IsSuspended(device.IP) {
				log.Debug().Str("ip", device.IP).Msg("Device ping is suspended (circuit breaker), skipping.")

				// Write suspension status to InfluxDB so we can track which devices are suspended
				if err := writer.WritePingResult(device.IP, 0, false, true); err != nil {
					log.Error().
						Str("ip", device.IP).
						Err(err).
						Msg("Failed to write suspension status")
				}

				timer.Reset(interval) // Reset timer and wait for next cycle
				continue              // Skip ping logic entirely
			}

			// 2. Acquire token from rate limiter (blocks until available or context cancelled)
			if err := limiter.Wait(ctx); err != nil {
				// Context was cancelled while waiting for token
				return
			}

			// 3. Perform the ping operation with in-flight tracking and circuit breaker
			pingOp(device, timeout, writer, stateMgr, inFlightCounter, totalPingsSent, maxConsecutiveFails, backoffDuration)

			// 4. Reset timer to schedule next ping after interval
			// This ensures interval is time BETWEEN pings, not fixed schedule
			timer.Reset(interval)
		}
	}
}

// performPing executes a single ping operation with in-flight counter tracking
func performPing(device state.Device, timeout time.Duration, writer PingWriter, stateMgr StateManager, inFlightCounter *atomic.Int64) {
	// Increment in-flight counter
	if inFlightCounter != nil {
		inFlightCounter.Add(1)
		// Ensure counter is decremented when ping operation completes
		defer inFlightCounter.Add(-1)
	}

	log.Debug().Str("ip", device.IP).Msg("Pinging device")

	// Validate IP address before pinging
	if err := validateIPAddress(device.IP); err != nil {
		log.Error().
			Str("ip", device.IP).
			Err(err).
			Msg("Invalid IP address")
		return
	}

	pinger, err := probing.NewPinger(device.IP)
	if err != nil {
		log.Error().
			Str("ip", device.IP).
			Err(err).
			Msg("Failed to create pinger")
		return // Skip invalid IP configurations
	}
	pinger.Count = 1           // Single ICMP echo request per interval
	pinger.Timeout = timeout   // Use configured ping timeout
	pinger.SetPrivileged(true) // Use raw ICMP sockets (requires root)
	if err := pinger.Run(); err != nil {
		// Distinguish between network-level errors (fast failure) and other errors
		// Network unreachable errors indicate routing/ARP issues and are fast failures (<10ms)
		errMsg := err.Error()
		if strings.Contains(errMsg, "unreachable") || strings.Contains(errMsg, "network is unreachable") {
			log.Warn().
				Str("ip", device.IP).
				Err(err).
				Msg("Network routing issue detected (fast syscall failure, check ARP/routing)")
		} else {
			log.Error().
				Str("ip", device.IP).
				Err(err).
				Msg("Ping execution failed")
		}
		return // Skip execution errors
	}
	stats := pinger.Statistics()
	// Determine success based on RTT data rather than just PacketsRecv
	// This is more reliable as the RTT measurements directly prove we got a response
	successful := len(stats.Rtts) > 0 && stats.AvgRtt > 0

	if successful {
		log.Debug().
			Str("ip", device.IP).
			Dur("rtt", stats.AvgRtt).
			Int("packets_recv", stats.PacketsRecv).
			Int("packets_sent", stats.PacketsSent).
			Msg("Ping successful")
		// Update last seen timestamp in state manager
		if stateMgr != nil {
			stateMgr.UpdateLastSeen(device.IP)
		}
		if err := writer.WritePingResult(device.IP, stats.AvgRtt, true, false); err != nil {
			log.Error().
				Str("ip", device.IP).
				Err(err).
				Msg("Failed to write ping result")
		}
	} else {
		log.Debug().
			Str("ip", device.IP).
			Int("packets_recv", stats.PacketsRecv).
			Int("packets_sent", stats.PacketsSent).
			Dur("avg_rtt", stats.AvgRtt).
			Msg("Ping failed - no response")
		if err := writer.WritePingResult(device.IP, 0, false, false); err != nil {
			log.Error().
				Str("ip", device.IP).
				Err(err).
				Msg("Failed to write ping failure")
		}
	}
}

// performPingWithCircuitBreaker executes a single ping operation with circuit breaker integration
func performPingWithCircuitBreaker(device state.Device, timeout time.Duration, writer PingWriter, stateMgr StateManager, inFlightCounter *atomic.Int64, totalPingsSent *atomic.Uint64, maxConsecutiveFails int, backoffDuration time.Duration) {
	// Increment in-flight counter
	if inFlightCounter != nil {
		inFlightCounter.Add(1)
		// Ensure counter is decremented when ping operation completes
		defer inFlightCounter.Add(-1)
	}

	// Increment total pings sent counter (for observability)
	if totalPingsSent != nil {
		totalPingsSent.Add(1)
	}

	log.Debug().Str("ip", device.IP).Msg("Pinging device")

	// Validate IP address before pinging
	if err := validateIPAddress(device.IP); err != nil {
		log.Error().
			Str("ip", device.IP).
			Err(err).
			Msg("Invalid IP address")
		return
	}

	pinger, err := probing.NewPinger(device.IP)
	if err != nil {
		log.Error().
			Str("ip", device.IP).
			Err(err).
			Msg("Failed to create pinger")
		return // Skip invalid IP configurations
	}
	pinger.Count = 1           // Single ICMP echo request per interval
	pinger.Timeout = timeout   // Use configured ping timeout
	pinger.SetPrivileged(true) // Use raw ICMP sockets (requires root)
	if err := pinger.Run(); err != nil {
		// Distinguish between network-level errors (fast failure) and other errors
		// Network unreachable errors indicate routing/ARP issues and are fast failures (<10ms)
		// These do NOT inflate active_pingers metric (short duration W in Little's Law)
		// Common causes: ARP cache expiration, routing table updates, firewall state timeouts
		errMsg := err.Error()
		if strings.Contains(errMsg, "unreachable") || strings.Contains(errMsg, "network is unreachable") {
			log.Warn().
				Str("ip", device.IP).
				Err(err).
				Msg("Network routing issue detected (fast syscall failure, check ARP/routing)")
		} else {
			log.Error().
				Str("ip", device.IP).
				Err(err).
				Msg("Ping execution failed")
		}
		return // Skip execution errors
	}
	stats := pinger.Statistics()
	// Determine success based on RTT data rather than just PacketsRecv
	// This is more reliable as the RTT measurements directly prove we got a response
	successful := len(stats.Rtts) > 0 && stats.AvgRtt > 0

	if successful {
		log.Debug().
			Str("ip", device.IP).
			Dur("rtt", stats.AvgRtt).
			Int("packets_recv", stats.PacketsRecv).
			Int("packets_sent", stats.PacketsSent).
			Msg("Ping successful")

		// Report success to circuit breaker (resets failure count)
		if stateMgr != nil {
			stateMgr.ReportPingSuccess(device.IP)
			stateMgr.UpdateLastSeen(device.IP)
		}

		if err := writer.WritePingResult(device.IP, stats.AvgRtt, true, false); err != nil {
			log.Error().
				Str("ip", device.IP).
				Err(err).
				Msg("Failed to write ping result")
		}
	} else {
		log.Debug().
			Str("ip", device.IP).
			Int("packets_recv", stats.PacketsRecv).
			Int("packets_sent", stats.PacketsSent).
			Dur("avg_rtt", stats.AvgRtt).
			Msg("Ping failed - no response")

		// Report failure to circuit breaker
		if stateMgr != nil {
			wasSuspended := stateMgr.ReportPingFail(device.IP, maxConsecutiveFails, backoffDuration)
			if wasSuspended {
				log.Warn().
					Str("ip", device.IP).
					Dur("backoff", backoffDuration).
					Msg("Device ping failed max attempts, suspending device (circuit breaker tripped)")
			}
		}

		if err := writer.WritePingResult(device.IP, 0, false, false); err != nil {
			log.Error().
				Str("ip", device.IP).
				Err(err).
				Msg("Failed to write ping failure")
		}
	}
}

// validateIPAddress validates IP address format and security constraints
func validateIPAddress(ipStr string) error {
	if ipStr == "" {
		return fmt.Errorf("IP address cannot be empty")
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return fmt.Errorf("invalid IP address format: %s", ipStr)
	}

	// Security checks - prevent pinging dangerous addresses
	if ip.IsLoopback() {
		return fmt.Errorf("loopback addresses not allowed: %s", ipStr)
	}
	if ip.IsMulticast() {
		return fmt.Errorf("multicast addresses not allowed: %s", ipStr)
	}
	if ip.IsLinkLocalUnicast() {
		return fmt.Errorf("link-local addresses not allowed: %s", ipStr)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("unspecified addresses not allowed: %s", ipStr)
	}

	// Additional validation for IPv4 addresses
	if ip.To4() != nil {
		// Note: We allow .0 and .255 addresses as they may be valid device IPs in some networks
		// The security checks above (loopback, multicast, etc.) are more important
	}

	return nil
}
