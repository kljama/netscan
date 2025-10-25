package monitoring

import (
"context"
"sync/atomic"
"testing"
"time"

"github.com/kljama/netscan/internal/state"
"golang.org/x/time/rate"
)

// TestTimerBehaviorNonAccumulating verifies that the timer-based approach doesn't accumulate
// pings during rate limiter waits (thundering herd prevention)
func TestTimerBehaviorNonAccumulating(t *testing.T) {
// Create a very slow rate limiter that will cause blocking
// 1 ping per second, burst of 1
limiter := rate.NewLimiter(rate.Limit(1.0), 1)
var counter atomic.Int64

writer := &mockWriter{}
stateMgr := &mockStateManager{}
dev := state.Device{IP: "192.168.1.1", Hostname: "test"}

// Set ping interval to 100ms (much faster than rate limit allows)
interval := 100 * time.Millisecond

ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
defer cancel()

// Track max in-flight count
var maxInFlight int64

// Monitor in-flight counter
done := make(chan struct{})
go func() {
ticker := time.NewTicker(10 * time.Millisecond)
defer ticker.Stop()
for {
select {
case <-done:
return
case <-ticker.C:
current := counter.Load()
if current > maxInFlight {
maxInFlight = current
}
}
}
}()

// Start pinger
go StartPinger(ctx, nil, dev, interval, 2*time.Second, writer, stateMgr, limiter, &counter)

// Wait for test to complete
<-ctx.Done()
close(done)
time.Sleep(100 * time.Millisecond) // Allow cleanup

// With timer-based approach and rate limiting:
// - The rate limiter limits to 1 ping/sec
// - Timer resets AFTER each ping completes
// - Max in-flight should never exceed burst size (1)
if maxInFlight > 1 {
t.Errorf("Expected max in-flight <= 1 (rate limit burst), got %d - suggests thundering herd", maxInFlight)
}

// Counter should be 0 after pinger stops
finalCount := counter.Load()
if finalCount != 0 {
t.Errorf("Expected in-flight counter to be 0 after pinger stopped, got %d", finalCount)
}
}

// TestTimerResetAfterPing verifies that the timer resets and continues pinging
func TestTimerResetAfterPing(t *testing.T) {
// Use generous rate limiter so we're testing timer behavior, not rate limiting
limiter := rate.NewLimiter(rate.Limit(1000.0), 1000)
var counter atomic.Int64

writer := &mockWriter{}
stateMgr := &mockStateManager{}
dev := state.Device{IP: "192.168.1.1", Hostname: "test"}

// Set a known interval
interval := 50 * time.Millisecond  // Faster interval for testing

// Track the maximum in-flight counter value
var observedCounterIncrements int64

done := make(chan struct{})
go func() {
ticker := time.NewTicker(5 * time.Millisecond)  // Sample faster to catch brief increments
defer ticker.Stop()
for {
select {
case <-done:
return
case <-ticker.C:
current := counter.Load()
if current > 0 {
observedCounterIncrements++
}
}
}
}()

// Run for enough time to get multiple pings
ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
defer cancel()

// Start pinger
go StartPinger(ctx, nil, dev, interval, 2*time.Second, writer, stateMgr, limiter, &counter)

// Wait for test to complete
<-ctx.Done()
close(done)
time.Sleep(100 * time.Millisecond) // Allow cleanup

// We should see the counter increment at least once
// This proves the timer is firing and the pinger is running
if observedCounterIncrements < 1 {
t.Errorf("Expected to observe counter increments (at least 1), got %d", observedCounterIncrements)
}

// Counter should be 0 after pinger stops
finalCount := counter.Load()
if finalCount != 0 {
t.Errorf("Expected in-flight counter to be 0 after pinger stopped, got %d", finalCount)
}
}

// TestTimerStopOnContextCancel verifies timer is properly stopped on shutdown
func TestTimerStopOnContextCancel(t *testing.T) {
limiter := rate.NewLimiter(rate.Limit(1000.0), 1000)
var counter atomic.Int64

writer := &mockWriter{}
stateMgr := &mockStateManager{}
dev := state.Device{IP: "192.168.1.1", Hostname: "test"}

ctx, cancel := context.WithCancel(context.Background())

done := make(chan bool)
go func() {
StartPinger(ctx, nil, dev, 100*time.Millisecond, 2*time.Second, writer, stateMgr, limiter, &counter)
done <- true
}()

// Let it run briefly
time.Sleep(50 * time.Millisecond)

// Cancel context
cancel()

// Pinger should exit quickly
select {
case <-done:
// Success - pinger exited
case <-time.After(500 * time.Millisecond):
t.Error("Pinger did not exit within 500ms after context cancellation")
}
}
