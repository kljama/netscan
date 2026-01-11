package state

import (
	"fmt"
	"testing"
	"time"
)

// TestDoubleDecrementBug - Tests the bug where calling GetSuspendedCount() to clean up
// expired suspensions, followed by ReportPingSuccess(), causes the counter to decrement twice
func TestDoubleDecrementBug(t *testing.T) {
	mgr := NewManager(1000)

	// Add two devices
	dev1 := Device{
		IP:       "192.168.1.1",
		Hostname: "device1",
		LastSeen: time.Now(),
	}
	dev2 := Device{
		IP:       "192.168.1.2",
		Hostname: "device2",
		LastSeen: time.Now(),
	}
	mgr.Add(dev1)
	mgr.Add(dev2)

	// Trip the circuit breaker for both devices
	maxFails := 3
	backoff := 100 * time.Millisecond
	for i := 0; i < maxFails; i++ {
		mgr.ReportPingFail("192.168.1.1", maxFails, backoff)
		mgr.ReportPingFail("192.168.1.2", maxFails, backoff)
	}

	// Both devices should be suspended
	if !mgr.IsSuspended("192.168.1.1") || !mgr.IsSuspended("192.168.1.2") {
		t.Fatal("Both devices should be suspended")
	}

	initialCount := mgr.GetSuspendedCount()
	t.Logf("Initial suspended count: %d", initialCount)
	if initialCount != 2 {
		t.Errorf("Expected 2 suspended devices, got %d", initialCount)
	}

	// Wait for suspensions to expire
	time.Sleep(backoff + 50*time.Millisecond)

	// Call GetSuspendedCount() which triggers cleanupExpiredSuspensions()
	// This should clean up both expired suspensions and decrement counter to 0
	// Get count - used to trigger cleanup, now it doesn't
	count := mgr.GetSuspendedCount()
	t.Logf("Count after expiry (no cleanup): %d", count)

	if count != 2 {
		t.Errorf("Expected 2 after expiry (stale), got %d", count)
	}

	// Now report success for one - this should decrement
	mgr.ReportPingSuccess("192.168.1.1")

	count = mgr.GetSuspendedCount()
	t.Logf("Count after ReportPingSuccess(): %d", count)

	if count != 1 {
		t.Errorf("Expected count to be 1, got %d", count)
	}

	// Verify with accurate count
	accurateCount := mgr.GetSuspendedCountAccurate()
	t.Logf("Accurate count: %d", accurateCount)
	if accurateCount != 0 {
		t.Errorf("Accurate count should be 0, got %d", accurateCount)
	}
}

// TestDoubleDecrementMultipleDevices - Tests with multiple devices to show counter corruption
func TestDoubleDecrementMultipleDevices(t *testing.T) {
	mgr := NewManager(1000)

	// Add 5 devices
	for i := 1; i <= 5; i++ {
		dev := Device{
			IP:       fmt.Sprintf("192.168.1.%d", i),
			Hostname: "device",
			LastSeen: time.Now(),
		}
		mgr.Add(dev)
	}

	// Trip circuit breaker for all 5 devices
	maxFails := 3
	backoff := 100 * time.Millisecond
	for i := 1; i <= 5; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i)
		for j := 0; j < maxFails; j++ {
			mgr.ReportPingFail(ip, maxFails, backoff)
		}
	}

	// All 5 should be suspended
	count := mgr.GetSuspendedCount()
	t.Logf("Initial count: %d", count)
	if count != 5 {
		t.Errorf("Expected 5 suspended devices, got %d", count)
	}

	// Wait for suspensions to expire
	time.Sleep(backoff + 50*time.Millisecond)

	// Call GetSuspendedCount() to trigger cleanup
	countAfterExpiry := mgr.GetSuspendedCount()
	t.Logf("Count after expiry cleanup: %d", countAfterExpiry)

	// Now report successful pings for all 5 devices
	for i := 1; i <= 5; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i)
		mgr.ReportPingSuccess(ip)
	}

	// Check final count
	finalCount := mgr.GetSuspendedCount()
	t.Logf("Final count: %d", finalCount)

	// Counter might be negative due to double decrement
	if finalCount < 0 {
		t.Errorf("BUG: Counter went negative! Got %d", finalCount)
	}

	if finalCount != 0 {
		t.Errorf("Expected final count 0, got %d", finalCount)
	}
}
