package state

import (
	"testing"
	"time"
)

// TestGetSuspendedCount verifies the GetSuspendedCount method
func TestGetSuspendedCount(t *testing.T) {
	mgr := NewManager(1000)

	// Initially no devices
	if count := mgr.GetSuspendedCount(); count != 0 {
		t.Errorf("Expected 0 suspended devices initially, got %d", count)
	}

	// Add some non-suspended devices
	dev1 := Device{IP: "192.168.1.1", Hostname: "dev1", LastSeen: time.Now()}
	dev2 := Device{IP: "192.168.1.2", Hostname: "dev2", LastSeen: time.Now()}
	mgr.Add(dev1)
	mgr.Add(dev2)

	if count := mgr.GetSuspendedCount(); count != 0 {
		t.Errorf("Expected 0 suspended devices with non-suspended devices, got %d", count)
	}

	// Add a suspended device (suspended for 5 more minutes)
	dev3 := Device{
		IP:             "192.168.1.3",
		Hostname:       "dev3",
		LastSeen:       time.Now(),
		SuspendedUntil: time.Now().Add(5 * time.Minute),
	}
	mgr.Add(dev3)

	if count := mgr.GetSuspendedCount(); count != 1 {
		t.Errorf("Expected 1 suspended device, got %d", count)
	}

	// Add another suspended device
	dev4 := Device{
		IP:             "192.168.1.4",
		Hostname:       "dev4",
		LastSeen:       time.Now(),
		SuspendedUntil: time.Now().Add(10 * time.Minute),
	}
	mgr.Add(dev4)

	if count := mgr.GetSuspendedCount(); count != 2 {
		t.Errorf("Expected 2 suspended devices, got %d", count)
	}

	// Add a device with expired suspension (suspended until 1 minute ago)
	dev5 := Device{
		IP:             "192.168.1.5",
		Hostname:       "dev5",
		LastSeen:       time.Now(),
		SuspendedUntil: time.Now().Add(-1 * time.Minute),
	}
	mgr.Add(dev5)

	// Should still be 2 (expired suspension doesn't count)
	if count := mgr.GetSuspendedCount(); count != 2 {
		t.Errorf("Expected 2 suspended devices (expired suspension should not count), got %d", count)
	}

	// Total devices should be 5
	if total := mgr.Count(); total != 5 {
		t.Errorf("Expected 5 total devices, got %d", total)
	}
}

// TestGetSuspendedCountThreadSafe verifies thread-safe access to GetSuspendedCount
func TestGetSuspendedCountThreadSafe(t *testing.T) {
	mgr := NewManager(1000)

	// Add some suspended devices
	for i := 0; i < 10; i++ {
		dev := Device{
			IP:             "192.168.1." + string(rune(i+1)),
			Hostname:       "dev",
			LastSeen:       time.Now(),
			SuspendedUntil: time.Now().Add(5 * time.Minute),
		}
		mgr.Add(dev)
	}

	// Concurrently read the count
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				count := mgr.GetSuspendedCount()
				if count < 0 || count > 10 {
					t.Errorf("Invalid suspended count: %d", count)
				}
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestGetSuspendedCountConsistencyWithIsSuspended verifies GetSuspendedCount is consistent with IsSuspended
func TestGetSuspendedCountConsistencyWithIsSuspended(t *testing.T) {
	mgr := NewManager(1000)

	// Add a mix of suspended and non-suspended devices
	devices := []Device{
		{IP: "192.168.1.1", Hostname: "dev1", LastSeen: time.Now()},                                                   // Not suspended
		{IP: "192.168.1.2", Hostname: "dev2", LastSeen: time.Now(), SuspendedUntil: time.Now().Add(5 * time.Minute)},  // Suspended
		{IP: "192.168.1.3", Hostname: "dev3", LastSeen: time.Now(), SuspendedUntil: time.Now().Add(-1 * time.Minute)}, // Expired
		{IP: "192.168.1.4", Hostname: "dev4", LastSeen: time.Now(), SuspendedUntil: time.Now().Add(10 * time.Minute)}, // Suspended
		{IP: "192.168.1.5", Hostname: "dev5", LastSeen: time.Now()},                                                   // Not suspended
	}

	for _, dev := range devices {
		mgr.Add(dev)
	}

	// Count using IsSuspended
	manualCount := 0
	for _, dev := range devices {
		if mgr.IsSuspended(dev.IP) {
			manualCount++
		}
	}

	// Compare with GetSuspendedCount
	autoCount := mgr.GetSuspendedCount()

	if manualCount != autoCount {
		t.Errorf("GetSuspendedCount (%d) inconsistent with IsSuspended count (%d)", autoCount, manualCount)
	}

	// Should be 2 suspended devices
	if autoCount != 2 {
		t.Errorf("Expected 2 suspended devices, got %d", autoCount)
	}
}
