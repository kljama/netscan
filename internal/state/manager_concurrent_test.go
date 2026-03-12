package state

import (
	"sync"
	"testing"
	"time"
)

// TestManagerConcurrentAccess tests thread-safety of Manager under concurrent load
func TestManagerConcurrentAccess(t *testing.T) {
	mgr := NewManager(1000)
	var wg sync.WaitGroup

	// Simulate concurrent adds from multiple goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				dev := Device{
					IP:       "192.168.1." + string(rune('0'+j%10)),
					Hostname: "host",
					SysDescr: "desc",
					LastSeen: time.Now(),
				}
				mgr.Add(dev)
			}
		}(i)
	}

	// Simulate concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				mgr.GetAll()
				mgr.Get("192.168.1.1")
			}
		}()
	}

	// Simulate concurrent updates
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				mgr.UpdateLastSeen("192.168.1.1")
			}
		}()
	}

	wg.Wait()

	// Verify no data corruption occurred
	all := mgr.GetAll()
	if len(all) == 0 {
		t.Error("Expected devices to be present after concurrent operations")
	}
}

// TestManagerMaxDevicesLimit tests that device limit is enforced
func TestManagerMaxDevicesLimit(t *testing.T) {
	maxDevices := 10
	mgr := NewManager(maxDevices)

	// Add more devices than the limit
	for i := 0; i < 20; i++ {
		dev := Device{
			IP:       "192.168.1." + string(rune('0'+i)),
			Hostname: "host",
			SysDescr: "desc",
			LastSeen: time.Now().Add(time.Duration(i) * time.Second),
		}
		mgr.Add(dev)

		// Sleep briefly to ensure LastSeen timestamps are different
		time.Sleep(1 * time.Millisecond)
	}

	// Verify device count doesn't exceed limit
	all := mgr.GetAll()
	if len(all) > maxDevices {
		t.Errorf("Device count %d exceeds limit %d", len(all), maxDevices)
	}

	if len(all) != maxDevices {
		t.Errorf("Expected exactly %d devices, got %d", maxDevices, len(all))
	}
}

// TestManagerPruneConcurrent tests pruning under concurrent access
func TestManagerPruneConcurrent(t *testing.T) {
	mgr := NewManager(100)
	var wg sync.WaitGroup

	// Add some old devices
	for i := 0; i < 10; i++ {
		dev := Device{
			IP:       "192.168.1." + string(rune('0'+i)),
			Hostname: "host",
			SysDescr: "desc",
			LastSeen: time.Now().Add(-1 * time.Hour), // Old timestamp
		}
		mgr.Add(dev)
	}

	// Concurrent prune operations
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.Prune(30 * time.Minute)
		}()
	}

	// Concurrent read operations during pruning
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.GetAll()
		}()
	}

	wg.Wait()

	// Verify old devices were pruned
	all := mgr.GetAll()
	if len(all) > 0 {
		t.Errorf("Expected all old devices to be pruned, but found %d", len(all))
	}
}
