package monitoring

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kljama/netscan/internal/state"
	"golang.org/x/time/rate"
)

// TestPingsSentCounterIncrement verifies that the totalPingsSent counter increments correctly
func TestPingsSentCounterIncrement(t *testing.T) {
	// Setup
	limiter := rate.NewLimiter(rate.Limit(100.0), 256)
	var inFlightCounter atomic.Int64
	var totalPingsSent atomic.Uint64

	writer := &mockWriter{}
	stateMgr := &mockStateManager{}
	dev := state.Device{IP: "192.168.1.1", Hostname: "test"}

	// Run for 1.5 seconds to allow for 1s initial delay + time for pings
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	// Start pinger with both counters
	go StartPinger(ctx, nil, dev, 50*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &inFlightCounter, &totalPingsSent, mockPingFunc)

	// Wait for initial delay (1s) plus some pings to occur
	time.Sleep(1300 * time.Millisecond)

	// Check that counter has incremented
	sentCount := totalPingsSent.Load()
	if sentCount < 1 {
		t.Errorf("Expected totalPingsSent to increment (at least 1), got %d", sentCount)
	}

	// Wait for context to expire
	<-ctx.Done()

	// Give time for cleanup
	time.Sleep(100 * time.Millisecond)

	// Final count should be at least what we saw before (monotonically increasing)
	finalCount := totalPingsSent.Load()
	if finalCount < sentCount {
		t.Errorf("Counter decreased: initial=%d, final=%d", sentCount, finalCount)
	}

	// Verify it's reasonably incremented
	// After 1s delay, ~500ms of pinging at 50ms interval = ~10 pings, allow variance
	if finalCount < 3 {
		t.Errorf("Expected at least 3 pings to be sent, got %d", finalCount)
	}
}

// TestPingsSentCounterNilSafe verifies that passing nil for totalPingsSent doesn't cause panic
func TestPingsSentCounterNilSafe(t *testing.T) {
	// Setup
	limiter := rate.NewLimiter(rate.Limit(100.0), 256)
	var inFlightCounter atomic.Int64

	writer := &mockWriter{}
	stateMgr := &mockStateManager{}
	dev := state.Device{IP: "192.168.1.1", Hostname: "test"}

	// Run for 1.2s to account for 1s initial delay
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	// Start pinger with nil totalPingsSent counter (should not panic)
	go StartPinger(ctx, nil, dev, 50*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &inFlightCounter, nil, mockPingFunc)

	// Wait for some pings to occur (after 1s delay)
	time.Sleep(1100 * time.Millisecond)

	// Wait for context to expire
	<-ctx.Done()

	// If we got here without panic, the test passes
	t.Log("Pinger handled nil totalPingsSent counter safely")
}

// TestPingsSentCounterMonotonicity verifies that the counter only increases, never decreases
func TestPingsSentCounterMonotonicity(t *testing.T) {
	// Setup
	limiter := rate.NewLimiter(rate.Limit(100.0), 256)
	var inFlightCounter atomic.Int64
	var totalPingsSent atomic.Uint64

	writer := &mockWriter{}
	stateMgr := &mockStateManager{}
	dev := state.Device{IP: "192.168.1.1", Hostname: "test"}

	// Run for 1.5s to account for 1s delay + time for pings
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	// Start pinger
	go StartPinger(ctx, nil, dev, 30*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &inFlightCounter, &totalPingsSent, mockPingFunc)

	// Monitor the counter for monotonicity
	var lastValue uint64
	violations := 0
	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				current := totalPingsSent.Load()
				if current < lastValue {
					violations++
					t.Errorf("Counter decreased! Last=%d, Current=%d", lastValue, current)
				}
				lastValue = current
			}
		}
	}()

	// Wait for test to complete
	<-ctx.Done()
	close(done)
	time.Sleep(50 * time.Millisecond)

	// Verify no monotonicity violations
	if violations > 0 {
		t.Errorf("Counter monotonicity violated %d times", violations)
	}

	// Verify final value is reasonable
	// After 1s delay, ~500ms of pinging at 30ms interval = ~16 pings
	finalCount := totalPingsSent.Load()
	if finalCount < 5 {
		t.Errorf("Expected at least 5 pings, got %d", finalCount)
	}
}

// TestPingsSentCounterConcurrency verifies counter is thread-safe with multiple pingers
func TestPingsSentCounterConcurrency(t *testing.T) {
	// Setup shared counter
	var totalPingsSent atomic.Uint64
	limiter := rate.NewLimiter(rate.Limit(1000.0), 1000) // Generous limit

	writer := &mockWriter{}
	stateMgr := &mockStateManager{}

	// Run for 1.5s to account for 1s delay + time for pings
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	// Start 5 concurrent pingers
	numPingers := 5
	for i := 0; i < numPingers; i++ {
		dev := state.Device{IP: "192.168.1." + string(rune('1'+i)), Hostname: "test"}
		var inFlightCounter atomic.Int64
		go StartPinger(ctx, nil, dev, 40*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &inFlightCounter, &totalPingsSent, mockPingFunc)
	}

	// Wait for initial delay + some pings
	time.Sleep(1300 * time.Millisecond)

	// Get count while still running
	runningCount := totalPingsSent.Load()

	// Wait for completion
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)

	// Final count
	finalCount := totalPingsSent.Load()

	// With 5 pingers running for ~500ms (after 1s delay) at 40ms interval
	// Expected: 5 * (500ms / 40ms) = 5 * 12 = 60 pings, but allow for variance
	if finalCount < 15 {
		t.Errorf("Expected at least 15 total pings from 5 concurrent pingers, got %d", finalCount)
	}

	// Verify monotonicity across concurrent access
	if finalCount < runningCount {
		t.Errorf("Counter decreased during concurrent access: running=%d, final=%d", runningCount, finalCount)
	}

	t.Logf("Successfully counted %d pings from %d concurrent pingers", finalCount, numPingers)
}
