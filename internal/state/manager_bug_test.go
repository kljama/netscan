package state

import (
	"fmt"
	"testing"
	"time"
)

// TestSameDeviceMultipleSuspensions - reproduces the bug where the same device
// tripping the circuit breaker multiple times causes suspended_devices to increment
// even though only one device is actually suspended
func TestSameDeviceMultipleSuspensions(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device
	dev := Device{
		IP:       "10.128.60.2",
		Hostname: "test-device",
		LastSeen: time.Now(),
	}
	mgr.Add(dev)

	maxFails := 10
	backoff := 5 * time.Minute

	// Trip the circuit breaker 4 times for the SAME device
	for i := 0; i < 4; i++ {
		// First, fail enough times to trip the breaker
		for j := 0; j < maxFails; j++ {
			mgr.ReportPingFail("10.128.60.2", maxFails, backoff)
		}

		// Now the device is suspended
		if !mgr.IsSuspended("10.128.60.2") {
			t.Fatalf("Device should be suspended after iteration %d", i+1)
		}

		// Check the suspended count - it should ALWAYS be 1
		suspendedCount := mgr.GetSuspendedCount()
		t.Logf("After suspension %d: suspended_devices count = %d", i+1, suspendedCount)

		// This is the bug: suspended_devices increments to 2, 3, 4 instead of staying at 1
		if suspendedCount != 1 {
			t.Errorf("After suspension %d: Expected suspended_devices = 1 (only one device), got %d", i+1, suspendedCount)
		}
	}

	// Verify using accurate count
	accurateCount := mgr.GetSuspendedCountAccurate()
	t.Logf("Accurate count: %d", accurateCount)

	if accurateCount != 1 {
		t.Errorf("Accurate count should be 1, got %d", accurateCount)
	}

	// The atomic counter should match
	cachedCount := mgr.GetSuspendedCount()
	if cachedCount != accurateCount {
		t.Errorf("Cached count (%d) doesn't match accurate count (%d)", cachedCount, accurateCount)
	}
}

// TestSameDeviceMultipleSNMPSuspensions - verifies the same fix works for SNMP circuit breaker
func TestSameDeviceMultipleSNMPSuspensions(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device
	dev := Device{
		IP:       "10.128.60.3",
		Hostname: "test-device",
		LastSeen: time.Now(),
	}
	mgr.Add(dev)

	maxFails := 5
	backoff := 10 * time.Minute

	// Trip the SNMP circuit breaker 3 times for the SAME device
	for i := 0; i < 3; i++ {
		// First, fail enough times to trip the breaker
		for j := 0; j < maxFails; j++ {
			mgr.ReportSNMPFail("10.128.60.3", maxFails, backoff)
		}

		// Now SNMP polling should be suspended
		if !mgr.IsSNMPSuspended("10.128.60.3") {
			t.Fatalf("SNMP should be suspended after iteration %d", i+1)
		}

		// Check the SNMP suspended count - it should ALWAYS be 1
		snmpSuspendedCount := mgr.GetSNMPSuspendedCount()
		t.Logf("After SNMP suspension %d: snmp_suspended_devices count = %d", i+1, snmpSuspendedCount)

		// Verify count stays at 1
		if snmpSuspendedCount != 1 {
			t.Errorf("After SNMP suspension %d: Expected snmp_suspended_devices = 1 (only one device), got %d", i+1, snmpSuspendedCount)
		}
	}
}

// TestMultipleDevicesSuspensions - verifies counter works correctly with multiple different devices
func TestMultipleDevicesSuspensions(t *testing.T) {
	mgr := NewManager(1000)

	maxFails := 3
	backoff := 5 * time.Minute

	// Add and suspend 3 different devices
	for i := 1; i <= 3; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i)
		dev := Device{
			IP:       ip,
			Hostname: fmt.Sprintf("device%d", i),
			LastSeen: time.Now(),
		}
		mgr.Add(dev)

		// Trip the circuit breaker for this device
		for j := 0; j < maxFails; j++ {
			mgr.ReportPingFail(ip, maxFails, backoff)
		}

		// Verify count increments correctly
		expectedCount := i
		actualCount := mgr.GetSuspendedCount()
		if actualCount != expectedCount {
			t.Errorf("After suspending %d devices, expected count %d, got %d", i, expectedCount, actualCount)
		}
	}

	// Now re-suspend the first device (it's already suspended)
	// The count should stay at 3
	for j := 0; j < maxFails; j++ {
		mgr.ReportPingFail("192.168.1.1", maxFails, backoff)
	}

	finalCount := mgr.GetSuspendedCount()
	if finalCount != 3 {
		t.Errorf("After re-suspending already suspended device, expected count 3, got %d", finalCount)
	}
}
