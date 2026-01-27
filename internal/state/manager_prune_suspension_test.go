package state

import (
	"testing"
	"time"
)

// TestPruneStale_ExpiredSuspension - Tests that PruneStale correctly decrements
// the counter when pruning a device with an EXPIRED suspension
func TestPruneStale_ExpiredSuspension(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device with a very old LastSeen (will be pruned)
	dev := Device{
		IP:       "192.168.1.100",
		Hostname: "old-device",
		LastSeen: time.Now().Add(-25 * time.Hour), // 25 hours ago - will be pruned
	}
	mgr.Add(dev)

	// Suspend the device
	maxFails := 3
	backoff := 200 * time.Millisecond // Short backoff for testing
	for i := 0; i < maxFails; i++ {
		mgr.ReportPingFail("192.168.1.100", maxFails, backoff)
	}

	// Verify device is suspended and counter is 1
	if !mgr.IsSuspended("192.168.1.100") {
		t.Fatal("Device should be suspended")
	}

	// Get the count - this will clean up nothing because suspension is still active
	count1 := mgr.GetSuspendedCount()
	t.Logf("Count after suspension: %d", count1)
	if count1 != 1 {
		t.Fatalf("Expected count 1, got %d", count1)
	}

	// Now wait for suspension to expire
	time.Sleep(backoff + 50*time.Millisecond)

	// Suspension has expired - device is no longer actively suspended
	if mgr.IsSuspended("192.168.1.100") {
		t.Error("Device should not be actively suspended after expiration")
	}

	// BUT the counter might still be 1 if cleanup hasn't run
	// Let's NOT call GetSuspendedCount() to avoid triggering cleanup

	// Now prune stale devices (> 24 hours old)
	// This device was last seen 25 hours ago, so it will be pruned
	// It has an EXPIRED suspension (SuspendedUntil is set but in the past)
	pruned := mgr.PruneStale(24 * time.Hour)
	t.Logf("Pruned %d devices", len(pruned))

	if len(pruned) != 1 {
		t.Fatalf("Expected to prune 1 device, got %d", len(pruned))
	}

	// Device should be gone
	if mgr.Count() != 0 {
		t.Errorf("Expected 0 devices after pruning, got %d", mgr.Count())
	}

	// THE FIX: Counter should be 0 now because PruneStale should have decremented
	// it for the device with expired suspension
	countAfterPrune := mgr.GetSuspendedCount()
	t.Logf("Count after pruning: %d", countAfterPrune)

	if countAfterPrune != 0 {
		t.Errorf("BUG: Counter should be 0 after pruning device with expired suspension, got %d", countAfterPrune)
	}

	// Verify with accurate count
	accurateCount := mgr.GetSuspendedCountAccurate()
	if accurateCount != 0 {
		t.Errorf("Accurate count should be 0, got %d", accurateCount)
	}
}

// TestPruneStale_ActiveSuspension - Tests that PruneStale correctly decrements
// the counter when pruning a device with an ACTIVE suspension
func TestPruneStale_ActiveSuspension(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device with a very old LastSeen (will be pruned)
	dev := Device{
		IP:       "192.168.1.200",
		Hostname: "old-device-2",
		LastSeen: time.Now().Add(-25 * time.Hour), // 25 hours ago - will be pruned
	}
	mgr.Add(dev)

	// Suspend the device with a LONG backoff (still active when pruned)
	maxFails := 3
	backoff := 48 * time.Hour // Long backoff - suspension will still be active
	for i := 0; i < maxFails; i++ {
		mgr.ReportPingFail("192.168.1.200", maxFails, backoff)
	}

	// Verify device is actively suspended
	if !mgr.IsSuspended("192.168.1.200") {
		t.Fatal("Device should be actively suspended")
	}

	count1 := mgr.GetSuspendedCount()
	t.Logf("Count after suspension: %d", count1)
	if count1 != 1 {
		t.Fatalf("Expected count 1, got %d", count1)
	}

	// Prune stale devices (> 24 hours old)
	// This device is old AND actively suspended
	pruned := mgr.PruneStale(24 * time.Hour)
	t.Logf("Pruned %d devices", len(pruned))

	if len(pruned) != 1 {
		t.Fatalf("Expected to prune 1 device, got %d", len(pruned))
	}

	// Counter should be 0 after pruning
	countAfterPrune := mgr.GetSuspendedCount()
	t.Logf("Count after pruning: %d", countAfterPrune)

	if countAfterPrune != 0 {
		t.Errorf("Counter should be 0 after pruning actively suspended device, got %d", countAfterPrune)
	}
}

// TestPruneStale_NoSuspension - Tests that PruneStale doesn't affect counter
// when pruning devices that were never suspended
func TestPruneStale_NoSuspension(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device that was never suspended
	dev := Device{
		IP:       "192.168.1.300",
		Hostname: "normal-device",
		LastSeen: time.Now().Add(-25 * time.Hour),
	}
	mgr.Add(dev)

	// Don't suspend it - counter should be 0
	count1 := mgr.GetSuspendedCount()
	if count1 != 0 {
		t.Fatalf("Expected count 0 for non-suspended device, got %d", count1)
	}

	// Prune the device
	pruned := mgr.PruneStale(24 * time.Hour)
	if len(pruned) != 1 {
		t.Fatalf("Expected to prune 1 device, got %d", len(pruned))
	}

	// Counter should still be 0
	countAfterPrune := mgr.GetSuspendedCount()
	if countAfterPrune != 0 {
		t.Errorf("Counter should remain 0 after pruning non-suspended device, got %d", countAfterPrune)
	}
}
