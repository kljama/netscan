package state

import (
	"testing"
	"time"
)

// TestBugFixed_ExpiredSuspensionNowDecremented - Tests that the bug fix correctly
// decrements the counter when Add() updates a device with an EXPIRED suspension.
//
// Bug Description (FIXED):
// Previously, when Add() was called to update an existing device that had an EXPIRED
// suspension (SuspendedUntil set but in the past), the atomic counter was NOT decremented.
// The state transition logic only checked if the suspension was ACTIVE (in the future),
// so expired suspensions were not counted.
//
// Fix:
// Add() now cleans up expired suspensions in the EXISTING device before checking state
// transitions, ensuring the counter is properly decremented.
func TestBugFixed_ExpiredSuspensionNowDecremented(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device first
	dev := Device{
		IP:       "192.168.1.100",
		Hostname: "test-device",
		LastSeen: time.Now(),
	}
	mgr.Add(dev)

	// Suspend the device
	maxFails := 3
	backoff := 100 * time.Millisecond
	for i := 0; i < maxFails; i++ {
		mgr.ReportPingFail("192.168.1.100", maxFails, backoff)
	}

	// Counter should be 1
	count1 := mgr.GetSuspendedCount()
	t.Logf("Count after suspension: %d", count1)
	if count1 != 1 {
		t.Fatalf("Expected count 1, got %d", count1)
	}

	// Wait for suspension to expire
	time.Sleep(backoff + 50*time.Millisecond)

	// Now the suspension is expired, but cleanup hasn't run
	// Verify IsSuspended returns false
	if mgr.IsSuspended("192.168.1.100") {
		t.Error("Device should not be suspended after expiration")
	}

	// Verify device state - SuspendedUntil should still be set (not cleaned up yet)
	retrieved, _ := mgr.Get("192.168.1.100")
	if retrieved.SuspendedUntil.IsZero() {
		t.Fatal("SuspendedUntil should still be set (cleanup hasn't run)")
	}
	t.Logf("SuspendedUntil: %v (expired)", retrieved.SuspendedUntil)

	// Now call Add() with updated device info (e.g., updated hostname)
	// This is a common operation - updating device metadata
	updatedDev := Device{
		IP:             "192.168.1.100",
		Hostname:       "updated-hostname",
		LastSeen:       time.Now(),
		SuspendedUntil: time.Time{}, // Not suspended
	}
	mgr.Add(updatedDev)

	// BUG FIX: The counter should have been decremented because the device
	// had an expired suspension that needed cleanup
	// The fix ensures Add() cleans up expired suspensions before state transitions

	// Check the counter using GetSuspendedCountAccurate (doesn't trigger cleanup)
	accurateCount := mgr.GetSuspendedCountAccurate()
	t.Logf("Accurate count: %d", accurateCount)

	// Load the cached counter directly
	cachedCount := int(mgr.suspendedCount.Load())
	t.Logf("Cached count (atomic): %d", cachedCount)

	// FIXED: Both counts should now be 0
	if cachedCount != 0 {
		t.Errorf("BUG NOT FIXED: Cached count should be 0, got %d", cachedCount)
	}
	if accurateCount != 0 {
		t.Errorf("Accurate count should be 0, got %d", accurateCount)
	}
	if cachedCount != accurateCount {
		t.Errorf("Cached count (%d) should match accurate count (%d)", cachedCount, accurateCount)
	}
}
