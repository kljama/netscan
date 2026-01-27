package monitoring

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kljama/netscan/internal/state"
	"golang.org/x/time/rate"
)

// mockWriterForSuspension captures suspension status for testing
type mockWriterForSuspension struct {
	mu         sync.Mutex
	writeCalls []struct {
		ip        string
		rtt       time.Duration
		success   bool
		suspended bool
	}
}

func (m *mockWriterForSuspension) WritePingResult(ip string, rtt time.Duration, successful bool, suspended bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeCalls = append(m.writeCalls, struct {
		ip        string
		rtt       time.Duration
		success   bool
		suspended bool
	}{ip, rtt, successful, suspended})
	return nil
}

func (m *mockWriterForSuspension) WriteDeviceInfo(ip, hostname, sysDescr string) error {
	return nil
}

// getWriteCallsCount safely returns the number of write calls
func (m *mockWriterForSuspension) getWriteCallsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.writeCalls)
}

// getWriteCalls safely returns a copy of write calls
func (m *mockWriterForSuspension) getWriteCalls() []struct {
	ip        string
	rtt       time.Duration
	success   bool
	suspended bool
} {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]struct {
		ip        string
		rtt       time.Duration
		success   bool
		suspended bool
	}, len(m.writeCalls))
	copy(result, m.writeCalls)
	return result
}

// mockStateManagerForSuspension simulates circuit breaker suspension
type mockStateManagerForSuspension struct {
	suspended bool
}

func (m *mockStateManagerForSuspension) UpdateLastSeen(ip string) {}

func (m *mockStateManagerForSuspension) ReportPingSuccess(ip string) {}

func (m *mockStateManagerForSuspension) ReportPingFail(ip string, maxFails int, backoff time.Duration) bool {
	return false
}

func (m *mockStateManagerForSuspension) IsSuspended(ip string) bool {
	return m.suspended
}

// TestSuspendedStatusWritten verifies that suspended=true is written when device is suspended
func TestSuspendedStatusWritten(t *testing.T) {
	writer := &mockWriterForSuspension{}
	stateMgr := &mockStateManagerForSuspension{suspended: true} // Device is suspended

	dev := state.Device{IP: "192.168.1.100", Hostname: "test-device"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	limiter := rate.NewLimiter(rate.Limit(100.0), 256)
	var inFlightCounter atomic.Int64
	var totalPingsSent atomic.Uint64

	// Start pinger - should write suspended status after initial 1 second delay
	go StartPinger(ctx, nil, dev, 50*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &inFlightCounter, &totalPingsSent, 10, 5*time.Minute, nil)

	// Wait for the initial timer (1 second) plus some buffer
	time.Sleep(1200 * time.Millisecond)

	// Verify that WritePingResult was called with suspended=true
	writeCalls := writer.getWriteCalls()
	if len(writeCalls) == 0 {
		t.Fatalf("expected WritePingResult to be called, but it wasn't")
	}

	// Check the first call
	firstCall := writeCalls[0]
	if !firstCall.suspended {
		t.Errorf("expected suspended=true, got suspended=%v", firstCall.suspended)
	}
	if firstCall.success {
		t.Errorf("expected success=false for suspended device, got success=%v", firstCall.success)
	}
	if firstCall.rtt != 0 {
		t.Errorf("expected rtt=0 for suspended device, got rtt=%v", firstCall.rtt)
	}
	if firstCall.ip != "192.168.1.100" {
		t.Errorf("expected ip=192.168.1.100, got ip=%v", firstCall.ip)
	}
}

// TestNormalPingNotSuspended verifies that suspended=false is written during normal pings
// Note: This test may not write any results if running without root privileges (ICMP permission denied)
// In that case, the test verifies that if writes occur, they have suspended=false
func TestNormalPingNotSuspended(t *testing.T) {
	writer := &mockWriterForSuspension{}
	stateMgr := &mockStateManagerForSuspension{suspended: false} // Device is NOT suspended

	dev := state.Device{IP: "192.168.1.101", Hostname: "test-device"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	limiter := rate.NewLimiter(rate.Limit(100.0), 256)
	var inFlightCounter atomic.Int64
	var totalPingsSent atomic.Uint64

	// Start pinger - should attempt normal ping
	go StartPinger(ctx, nil, dev, 50*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &inFlightCounter, &totalPingsSent, 10, 5*time.Minute, nil)

	// Wait for the initial timer (1 second) plus some buffer
	time.Sleep(1200 * time.Millisecond)

	// Get write calls safely
	writeCalls := writer.getWriteCalls()

	// Check IF any calls were made (may be 0 if running without root privileges)
	// If calls were made, they should all have suspended=false
	for i, call := range writeCalls {
		if call.suspended {
			t.Errorf("call %d: expected suspended=false for normal ping, got suspended=%v", i, call.suspended)
		}
		if call.ip != "192.168.1.101" {
			t.Errorf("call %d: expected ip=192.168.1.101, got ip=%v", i, call.ip)
		}
	}

	// Note: We don't require writes to have occurred because without root privileges,
	// the ping execution will fail with "operation not permitted" and return early
	// without writing any result. This is existing behavior.
	if len(writeCalls) > 0 {
		t.Logf("Verified %d ping results all had suspended=false", len(writeCalls))
	} else {
		t.Logf("No ping results written (likely running without root privileges)")
	}
}

// TestSuspendedWriteFrequency verifies that suspended status is written on each interval
func TestSuspendedWriteFrequency(t *testing.T) {
	writer := &mockWriterForSuspension{}
	stateMgr := &mockStateManagerForSuspension{suspended: true}

	dev := state.Device{IP: "192.168.1.102", Hostname: "test-device"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	limiter := rate.NewLimiter(rate.Limit(100.0), 256)
	var inFlightCounter atomic.Int64
	var totalPingsSent atomic.Uint64

	// Start pinger with 200ms interval
	go StartPinger(ctx, nil, dev, 200*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &inFlightCounter, &totalPingsSent, 10, 5*time.Minute, nil)

	// Wait for initial timer (1s) + a few intervals (1s + 400ms = 1.4s total, plus buffer)
	time.Sleep(1500 * time.Millisecond)

	// Get write calls safely
	writeCalls := writer.getWriteCalls()

	// Should have at least 2 writes (at ~1s and ~1.2s)
	if len(writeCalls) < 2 {
		t.Errorf("expected at least 2 writes, got %d", len(writeCalls))
	}

	// All writes should have suspended=true
	for i, call := range writeCalls {
		if !call.suspended {
			t.Errorf("call %d: expected suspended=true, got suspended=%v", i, call.suspended)
		}
	}
}
