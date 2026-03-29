package monitoring

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kljama/netscan/internal/state"
	"golang.org/x/time/rate"
)

// TestRateLimiterIntegration verifies that the rate limiter correctly throttles ping operations
func TestRateLimiterIntegration(t *testing.T) {
	// Create a very restrictive rate limiter: 2 pings per second, burst of 2
	limiter := rate.NewLimiter(rate.Limit(2.0), 2)
	var counter atomic.Int64

	writer := &mockWriter{}
	stateMgr := &mockStateManager{}

	// Create 5 devices that will try to ping simultaneously
	devices := []state.Device{
		{IP: "127.0.0.1", Hostname: "device1"},
		{IP: "127.0.0.2", Hostname: "device2"},
		{IP: "127.0.0.3", Hostname: "device3"},
		{IP: "127.0.0.4", Hostname: "device4"},
		{IP: "127.0.0.5", Hostname: "device5"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Start all pingers - they will try to ping immediately
	// But the rate limiter should throttle them to 2 pings/sec
	for _, dev := range devices {
		go StartPinger(ctx, nil, dev, 100*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &counter, nil, mockPingFunc)
	}

	// Wait a bit for them to start
	time.Sleep(100 * time.Millisecond)

	// Check that in-flight counter never exceeds burst size
	maxInFlight := counter.Load()
	if maxInFlight > 2 {
		t.Errorf("Expected max in-flight pings <= 2 (burst limit), got %d", maxInFlight)
	}

	// Wait for context to expire
	<-ctx.Done()

	// Wait a bit for cleanup
	time.Sleep(100 * time.Millisecond)

	// After all pingers stop, counter should be 0
	finalCount := counter.Load()
	if finalCount != 0 {
		t.Errorf("Expected in-flight counter to be 0 after all pingers stopped, got %d", finalCount)
	}
}

// TestInFlightCounterAccuracy verifies the atomic counter correctly tracks in-flight pings
func TestInFlightCounterAccuracy(t *testing.T) {
	// Use a generous rate limiter so we're testing the counter, not the limiter
	limiter := rate.NewLimiter(rate.Limit(1000.0), 1000)
	var counter atomic.Int64

	writer := &mockWriter{}
	stateMgr := &mockStateManager{}
	dev := state.Device{IP: "127.0.0.1", Hostname: "test"}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start a single pinger
	go StartPinger(ctx, nil, dev, 50*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &counter, nil, mockPingFunc)

	// Wait for at least one ping to start
	time.Sleep(100 * time.Millisecond)

	// Counter should be either 0 or 1 (ping in progress or between pings)
	count := counter.Load()
	if count < 0 || count > 1 {
		t.Errorf("Expected in-flight counter to be 0 or 1, got %d", count)
	}

	// Wait for context to expire
	<-ctx.Done()

	// Give time for cleanup
	time.Sleep(100 * time.Millisecond)

	// After pinger stops, counter should be 0
	finalCount := counter.Load()
	if finalCount != 0 {
		t.Errorf("Expected in-flight counter to be 0 after pinger stopped, got %d", finalCount)
	}
}

// TestRateLimiterContextCancellation verifies that rate limiter respects context cancellation
func TestRateLimiterContextCancellation(t *testing.T) {
	// Create a very slow rate limiter: 0.1 pings per second (1 ping every 10 seconds)
	limiter := rate.NewLimiter(rate.Limit(0.1), 1)
	var counter atomic.Int64

	writer := &mockWriter{}
	stateMgr := &mockStateManager{}
	dev := state.Device{IP: "127.0.0.1", Hostname: "test"}

	// Cancel context after 100ms
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start pinger - it will get one token immediately, then wait 10 seconds for the next
	// But context will cancel after 100ms
	done := make(chan bool, 1)
	go func() {
		StartPinger(ctx, nil, dev, 10*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &counter, nil, mockPingFunc)
		done <- true
	}()

	// Wait for completion (should be ~100ms, not 10 seconds)
	select {
	case <-done:
	// Good - pinger exited when context was cancelled
	case <-time.After(1 * time.Second):
		t.Error("Pinger did not exit within 1 second after context cancellation")
	}

	// Counter should be 0 after pinger exits
	finalCount := counter.Load()
	if finalCount != 0 {
		t.Errorf("Expected in-flight counter to be 0 after pinger stopped, got %d", finalCount)
	}
}
