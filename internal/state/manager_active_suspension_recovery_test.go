package state

import (
	"testing"
	"time"
)

// TestRecoveryDuringActiveSuspension - Tests that the suspended_devices counter
// is correctly decremented when a device recovers DURING an active suspension
// (i.e., before the suspension period expires)
func TestRecoveryDuringActiveSuspension(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device
	dev := Device{
		IP:       "192.168.1.10",
		Hostname: "test-device",
		LastSeen: time.Now(),
	}
	mgr.Add(dev)

	// Trip the circuit breaker with a LONG backoff
	maxFails := 3
	backoff := 10 * time.Minute // Long backoff so suspension won't expire during test
	for i := 0; i < maxFails; i++ {
		mgr.ReportPingFail("192.168.1.10", maxFails, backoff)
	}

	// Verify device is actively suspended
	if !mgr.IsSuspended("192.168.1.10") {
		t.Fatal("Device should be suspended")
	}

	countBeforeRecovery := mgr.GetSuspendedCount()
	t.Logf("Count before recovery: %d", countBeforeRecovery)
	if countBeforeRecovery != 1 {
		t.Errorf("Expected 1 suspended device, got %d", countBeforeRecovery)
	}

	// Now report successful ping DURING the suspension period
	// This should decrement the counter
	mgr.ReportPingSuccess("192.168.1.10")

	// Device should no longer be suspended
	if mgr.IsSuspended("192.168.1.10") {
		t.Error("Device should not be suspended after successful ping")
	}

	// Counter should be decremented to 0
	countAfterRecovery := mgr.GetSuspendedCount()
	t.Logf("Count after recovery: %d", countAfterRecovery)
	if countAfterRecovery != 0 {
		t.Errorf("Expected 0 suspended devices after recovery, got %d", countAfterRecovery)
	}

	// Verify with accurate count
	accurateCount := mgr.GetSuspendedCountAccurate()
	if accurateCount != 0 {
		t.Errorf("Accurate count should be 0, got %d", accurateCount)
	}

	// Verify the device state is fully reset
	retrieved, _ := mgr.Get("192.168.1.10")
	if retrieved.ConsecutiveFails != 0 {
		t.Errorf("ConsecutiveFails should be 0, got %d", retrieved.ConsecutiveFails)
	}
	if !retrieved.SuspendedUntil.IsZero() {
		t.Errorf("SuspendedUntil should be zero time, got %v", retrieved.SuspendedUntil)
	}
}
