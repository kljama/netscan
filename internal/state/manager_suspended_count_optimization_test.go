package state

import (
	"testing"
	"time"
)

// TestSuspendedCountCaching tests that the atomic counter is properly maintained
func TestSuspendedCountCaching(t *testing.T) {
	mgr := NewManager(1000)

	// Add some devices
	devices := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "192.168.1.4", "192.168.1.5"}
	for _, ip := range devices {
		mgr.AddDevice(ip)
	}

	// Initially no devices are suspended
	if count := mgr.GetSuspendedCount(); count != 0 {
		t.Errorf("Expected 0 suspended devices initially, got %d", count)
	}

	// Suspend device 1 by failing it 10 times
	for i := 0; i < 10; i++ {
		mgr.ReportPingFail(devices[0], 10, 5*time.Minute)
	}

	// Should have 1 suspended device
	if count := mgr.GetSuspendedCount(); count != 1 {
		t.Errorf("Expected 1 suspended device after failures, got %d", count)
	}

	// Suspend device 2
	for i := 0; i < 10; i++ {
		mgr.ReportPingFail(devices[1], 10, 5*time.Minute)
	}

	// Should have 2 suspended devices
	if count := mgr.GetSuspendedCount(); count != 2 {
		t.Errorf("Expected 2 suspended devices, got %d", count)
	}

	// Resume device 1 with successful ping
	mgr.ReportPingSuccess(devices[0])

	// Should have 1 suspended device
	if count := mgr.GetSuspendedCount(); count != 1 {
		t.Errorf("Expected 1 suspended device after resume, got %d", count)
	}

	// Resume device 2
	mgr.ReportPingSuccess(devices[1])

	// Should have 0 suspended devices
	if count := mgr.GetSuspendedCount(); count != 0 {
		t.Errorf("Expected 0 suspended devices after all resumed, got %d", count)
	}
}

// TestSuspendedCountAccuracy tests that cached count matches accurate count
func TestSuspendedCountAccuracy(t *testing.T) {
	mgr := NewManager(1000)

	// Add devices
	for i := 0; i < 100; i++ {
		ip := "192.168.1." + string(rune(i+1))
		mgr.AddDevice(ip)
	}

	// Suspend 10 devices
	for i := 0; i < 10; i++ {
		ip := "192.168.1." + string(rune(i+1))
		for j := 0; j < 10; j++ {
			mgr.ReportPingFail(ip, 10, 5*time.Minute)
		}
	}

	// Both methods should agree
	cached := mgr.GetSuspendedCount()
	accurate := mgr.GetSuspendedCountAccurate()

	if cached != accurate {
		t.Errorf("Cached count (%d) does not match accurate count (%d)", cached, accurate)
	}

	if cached != 10 {
		t.Errorf("Expected 10 suspended devices, got %d", cached)
	}
}

// TestSuspendedCountExpiration tests behavior when suspensions expire
func TestSuspendedCountExpiration(t *testing.T) {
	mgr := NewManager(1000)

	// Add device
	mgr.AddDevice("192.168.1.1")

	// Suspend device with very short backoff
	for i := 0; i < 10; i++ {
		mgr.ReportPingFail("192.168.1.1", 10, 100*time.Millisecond)
	}

	// Should be suspended
	if count := mgr.GetSuspendedCount(); count != 1 {
		t.Errorf("Expected 1 suspended device, got %d", count)
	}

	// Wait for suspension to expire
	time.Sleep(200 * time.Millisecond)

	// With the optimization, GetSuspendedCount() is now O(1) and does NOT auto-cleanup
	// So it will still return 1 until an operation updates the device state
	cachedCount := mgr.GetSuspendedCount()

	// Accurate count should also show 0 (checks expiration)
	accurateCount := mgr.GetSuspendedCountAccurate()

	// Cached should still be 1 (stale but fast)
	if cachedCount != 1 {
		t.Errorf("Expected cached count to be 1 (stale), got %d", cachedCount)
	}

	if accurateCount != 0 {
		t.Errorf("Expected accurate count to be 0 (expired), got %d", accurateCount)
	}

	// After a successful ping, the counter is updated and both should agree on 0
	mgr.ReportPingSuccess("192.168.1.1")

	// Now the cached counter should be updated
	if count := mgr.GetSuspendedCount(); count != 0 {
		t.Errorf("Expected 0 cached count after successful ping clears expired suspension, got %d", count)
	}

	if count := mgr.GetSuspendedCountAccurate(); count != 0 {
		t.Errorf("Expected 0 accurate count after successful ping, got %d", count)
	}
}

// TestSuspendedCountConcurrency tests atomic counter under concurrent access
func TestSuspendedCountConcurrency(t *testing.T) {
	mgr := NewManager(10000)

	// Add 100 devices
	for i := 0; i < 100; i++ {
		ip := "192.168.1." + string(rune(i+1))
		mgr.AddDevice(ip)
	}

	// Concurrently suspend and resume devices
	done := make(chan bool)

	// Suspender goroutine
	go func() {
		for i := 0; i < 50; i++ {
			ip := "192.168.1." + string(rune(i+1))
			for j := 0; j < 10; j++ {
				mgr.ReportPingFail(ip, 10, 5*time.Minute)
			}
		}
		done <- true
	}()

	// Resumer goroutine
	go func() {
		time.Sleep(10 * time.Millisecond) // Let some suspensions happen first
		for i := 0; i < 30; i++ {
			ip := "192.168.1." + string(rune(i+1))
			mgr.ReportPingSuccess(ip)
		}
		done <- true
	}()

	// Counter reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = mgr.GetSuspendedCount()
			time.Sleep(1 * time.Millisecond)
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Final count should be 20 (50 suspended - 30 resumed)
	finalCount := mgr.GetSuspendedCount()
	if finalCount != 20 {
		t.Errorf("Expected 20 suspended devices, got %d", finalCount)
	}

	// Verify with accurate count
	accurateCount := mgr.GetSuspendedCountAccurate()
	if accurateCount != 20 {
		t.Errorf("Expected 20 accurate count, got %d", accurateCount)
	}
}
