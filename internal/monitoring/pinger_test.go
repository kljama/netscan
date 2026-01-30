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

type mockWriter struct {
	mu               sync.Mutex
	called           bool
	ip               string
	rtt              time.Duration
	success          bool
	suspended        bool
	deviceInfoCalled bool
	deviceIP         string
	deviceHostname   string
}

// Satisfy influx.Writer interface
func (m *mockWriter) WritePingResult(ip string, rtt time.Duration, successful bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.called = true
	m.ip = ip
	m.rtt = rtt
	m.success = successful
	return nil
}

func (m *mockWriter) WriteDeviceInfo(ip, hostname, sysDescr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deviceInfoCalled = true
	m.deviceIP = ip
	m.deviceHostname = hostname
	return nil
}

type mockStateManager struct {
	mu             sync.Mutex
	lastSeenCalled bool
	lastSeenIP     string
}

func (m *mockStateManager) UpdateLastSeen(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSeenCalled = true
	m.lastSeenIP = ip
}

func (m *mockStateManager) ReportPingSuccess(ip string) {
	// Mock implementation - could track calls if needed
}

// Thread-safe getter for called field
func (m *mockWriter) WasCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.called
}

// mockPingFunc is a mock ping function that simulates a ping operation without requiring root privileges.
// It's used by tests to avoid ICMP permission errors in CI environments.
func mockPingFunc(device state.Device, timeout time.Duration, writer PingWriter, stateMgr StateManager, inFlightCounter *atomic.Int64, totalPingsSent *atomic.Uint64) {
	// Track in-flight counter
	if inFlightCounter != nil {
		inFlightCounter.Add(1)
		defer inFlightCounter.Add(-1)
	}
	
	// Track total pings sent
	if totalPingsSent != nil {
		totalPingsSent.Add(1)
	}
	
	// Simulate a short network delay
	time.Sleep(5 * time.Millisecond)
	
	// Simulate successful ping with a fake RTT
	if writer != nil {
		writer.WritePingResult(device.IP, 10*time.Millisecond, true)
	}
	
	// Update last seen timestamp
	if stateMgr != nil {
		stateMgr.UpdateLastSeen(device.IP)
		stateMgr.ReportPingSuccess(device.IP)
	}
}

func TestStartPingerCancel(t *testing.T) {
	dev := state.Device{IP: "127.0.0.1", Hostname: "localhost"}
	writer := &mockWriter{}
	stateMgr := &mockStateManager{}
	ctx, cancel := context.WithCancel(context.Background())
	limiter := rate.NewLimiter(rate.Limit(100.0), 256)
	var counter atomic.Int64
	go StartPinger(ctx, nil, dev, 10*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &counter, nil, mockPingFunc)
	
	// Poll WasCalled() with a deadline instead of fixed sleep to avoid flakiness
	deadline := time.Now().Add(2 * time.Second)
	called := false
	for time.Now().Before(deadline) {
		if writer.WasCalled() {
			called = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	
	cancel()
	
	if !called {
		t.Errorf("expected WritePingResult to be called within 2 seconds")
	}
}
