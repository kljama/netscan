package state

import (
	"testing"
	"time"
)

// TestExpiredSuspensionStuckCounter - reproduces the bug where expired suspensions
// don't automatically clear from the counter
func TestExpiredSuspensionStuckCounter(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device
	dev := Device{
		IP:       "10.0.0.1",
		Hostname: "test-device",
		LastSeen: time.Now(),
	}
	mgr.Add(dev)

	maxFails := 3
	backoff := 100 * time.Millisecond // Very short backoff for testing

	// Trip the circuit breaker
	for i := 0; i < maxFails; i++ {
		mgr.ReportPingFail("10.0.0.1", maxFails, backoff)
	}

	// Device should be suspended
	if !mgr.IsSuspended("10.0.0.1") {
		t.Fatal("Device should be suspended")
	}

	// Counter should be 1
	if count := mgr.GetSuspendedCount(); count != 1 {
		t.Fatalf("Expected suspended count = 1, got %d", count)
	}

	// Wait for suspension to expire
	time.Sleep(150 * time.Millisecond)

	// Device should no longer be suspended
	if mgr.IsSuspended("10.0.0.1") {
		t.Fatal("Device should NOT be suspended anymore (suspension expired)")
	}

	// Check counts
	cachedCount := mgr.GetSuspendedCount()
	accurateCount := mgr.GetSuspendedCountAccurate()

	t.Logf("Cached count: %d, Accurate count: %d", cachedCount, accurateCount)

	// Since we removed auto-cleanup from the hot path (GetSuspendedCount),
	// it is EXPECTED that the cached count remains 1 until an update happens.
	// This is an intentional tradeoff for O(1) performance.
	if cachedCount != 1 {
		t.Errorf("Expected cached count to be 1 (stale), got %d", cachedCount)
	}

	if accurateCount != 0 {
		t.Errorf("Expected accurate count to be 0 (expired), got %d", accurateCount)
	}

	// Verify that an update clears it
	mgr.ReportPingSuccess("10.0.0.1")
	if count := mgr.GetSuspendedCount(); count != 0 {
		t.Errorf("Expected cached count to be 0 after update, got %d", count)
	}
}
