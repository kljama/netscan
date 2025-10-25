package monitoring

import (
	"context"
	"fmt"
	"net"
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
	WritePingResult(ip string, rtt time.Duration, successful bool) error
	WriteDeviceInfo(ip, hostname, sysDescr string) error
}

// StateManager interface for updating device last seen timestamp
type StateManager interface {
	UpdateLastSeen(ip string)
}

// StartPinger runs continuous ICMP monitoring for a single device
func StartPinger(ctx context.Context, wg *sync.WaitGroup, device state.Device, interval time.Duration, timeout time.Duration, writer PingWriter, stateMgr StateManager, limiter *rate.Limiter, inFlightCounter *atomic.Int64) {
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
			// Acquire token from rate limiter (blocks until available or context cancelled)
			if err := limiter.Wait(ctx); err != nil {
				// Context was cancelled while waiting for token
				return
			}

			// Perform the ping operation with in-flight tracking
			performPing(device, timeout, writer, stateMgr, inFlightCounter)
			
			// Reset timer to schedule next ping after interval
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
	pinger.Count = 1                              // Single ICMP echo request per interval
	pinger.Timeout = timeout                      // Use configured ping timeout
	pinger.SetPrivileged(true)                    // Use raw ICMP sockets (requires root)
	if err := pinger.Run(); err != nil {
		log.Error().
			Str("ip", device.IP).
			Err(err).
			Msg("Ping execution failed")
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
		if err := writer.WritePingResult(device.IP, stats.AvgRtt, true); err != nil {
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
		if err := writer.WritePingResult(device.IP, 0, false); err != nil {
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
