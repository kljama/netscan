package state

import (
	"testing"
	"time"
)

// TestReportPingSuccess verifies that ReportPingSuccess resets circuit breaker state
func TestReportPingSuccess(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device with some failure state
	dev := Device{
		IP:               "192.168.1.1",
		Hostname:         "test-device",
		LastSeen:         time.Now(),
		ConsecutiveFails: 5,
		SuspendedUntil:   time.Now().Add(5 * time.Minute),
	}
	mgr.Add(dev)

	// Report ping success
	mgr.ReportPingSuccess("192.168.1.1")

	// Verify circuit breaker state is reset
	retrieved, exists := mgr.Get("192.168.1.1")
	if !exists {
		t.Fatal("Device should exist")
	}

	if retrieved.ConsecutiveFails != 0 {
		t.Errorf("Expected ConsecutiveFails to be 0, got %d", retrieved.ConsecutiveFails)
	}

	if !retrieved.SuspendedUntil.IsZero() {
		t.Errorf("Expected SuspendedUntil to be zero time, got %v", retrieved.SuspendedUntil)
	}
}

// TestReportPingFailIncrementsCounter verifies that ReportPingFail increments the failure counter
func TestReportPingFailIncrementsCounter(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device
	dev := Device{
		IP:       "192.168.1.1",
		Hostname: "test-device",
		LastSeen: time.Now(),
	}
	mgr.Add(dev)

	// Report several failures (below threshold)
	maxFails := 10
	backoff := 5 * time.Minute

	for i := 1; i < maxFails; i++ {
		suspended := mgr.ReportPingFail("192.168.1.1", maxFails, backoff)
		if suspended {
			t.Errorf("Device should not be suspended at failure %d", i)
		}

		retrieved, _ := mgr.Get("192.168.1.1")
		if retrieved.ConsecutiveFails != i {
			t.Errorf("Expected ConsecutiveFails to be %d, got %d", i, retrieved.ConsecutiveFails)
		}
	}
}

// TestReportPingFailTripsCircuitBreaker verifies that circuit breaker trips at threshold
func TestReportPingFailTripsCircuitBreaker(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device
	dev := Device{
		IP:       "192.168.1.1",
		Hostname: "test-device",
		LastSeen: time.Now(),
	}
	mgr.Add(dev)

	maxFails := 10
	backoff := 5 * time.Minute

	// Report failures up to but not including threshold
	for i := 1; i < maxFails; i++ {
		suspended := mgr.ReportPingFail("192.168.1.1", maxFails, backoff)
		if suspended {
			t.Errorf("Device should not be suspended at failure %d", i)
		}
	}

	// This failure should trip the circuit breaker
	beforeTrip := time.Now()
	suspended := mgr.ReportPingFail("192.168.1.1", maxFails, backoff)
	afterTrip := time.Now()

	if !suspended {
		t.Error("Circuit breaker should have tripped at threshold")
	}

	// Verify device is suspended
	retrieved, _ := mgr.Get("192.168.1.1")

	// Counter should be reset to 0
	if retrieved.ConsecutiveFails != 0 {
		t.Errorf("Expected ConsecutiveFails to be reset to 0, got %d", retrieved.ConsecutiveFails)
	}

	// SuspendedUntil should be set to now + backoff
	if retrieved.SuspendedUntil.IsZero() {
		t.Error("SuspendedUntil should not be zero time")
	}

	// Verify SuspendedUntil is approximately correct (within reasonable margin)
	expectedMin := beforeTrip.Add(backoff)
	expectedMax := afterTrip.Add(backoff).Add(1 * time.Second) // 1 second margin

	if retrieved.SuspendedUntil.Before(expectedMin) || retrieved.SuspendedUntil.After(expectedMax) {
		t.Errorf("SuspendedUntil %v not in expected range [%v, %v]",
			retrieved.SuspendedUntil, expectedMin, expectedMax)
	}
}

// TestIsSuspendedReturnsTrueWhenSuspended verifies IsSuspended returns true for suspended devices
func TestIsSuspendedReturnsTrueWhenSuspended(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device that is currently suspended
	dev := Device{
		IP:             "192.168.1.1",
		Hostname:       "test-device",
		LastSeen:       time.Now(),
		SuspendedUntil: time.Now().Add(5 * time.Minute), // Suspended for 5 more minutes
	}
	mgr.Add(dev)

	if !mgr.IsSuspended("192.168.1.1") {
		t.Error("Expected IsSuspended to return true for suspended device")
	}
}

// TestIsSuspendedReturnsFalseWhenNotSuspended verifies IsSuspended returns false for non-suspended devices
func TestIsSuspendedReturnsFalseWhenNotSuspended(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device that is not suspended (zero SuspendedUntil)
	dev := Device{
		IP:             "192.168.1.1",
		Hostname:       "test-device",
		LastSeen:       time.Now(),
		SuspendedUntil: time.Time{}, // Zero time (not suspended)
	}
	mgr.Add(dev)

	if mgr.IsSuspended("192.168.1.1") {
		t.Error("Expected IsSuspended to return false for non-suspended device")
	}
}

// TestIsSuspendedReturnsFalseWhenSuspensionExpired verifies IsSuspended returns false after suspension expires
func TestIsSuspendedReturnsFalseWhenSuspensionExpired(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device with expired suspension
	dev := Device{
		IP:             "192.168.1.1",
		Hostname:       "test-device",
		LastSeen:       time.Now(),
		SuspendedUntil: time.Now().Add(-1 * time.Minute), // Suspended until 1 minute ago (expired)
	}
	mgr.Add(dev)

	if mgr.IsSuspended("192.168.1.1") {
		t.Error("Expected IsSuspended to return false for device with expired suspension")
	}
}

// TestIsSuspendedReturnsFalseForNonexistentDevice verifies IsSuspended returns false for devices that don't exist
func TestIsSuspendedReturnsFalseForNonexistentDevice(t *testing.T) {
	mgr := NewManager(1000)

	if mgr.IsSuspended("192.168.1.1") {
		t.Error("Expected IsSuspended to return false for nonexistent device")
	}
}

// TestCircuitBreakerFullCycle verifies complete circuit breaker lifecycle
func TestCircuitBreakerFullCycle(t *testing.T) {
	mgr := NewManager(1000)

	// Add a device
	dev := Device{
		IP:       "192.168.1.1",
		Hostname: "test-device",
		LastSeen: time.Now(),
	}
	mgr.Add(dev)

	maxFails := 3
	backoff := 100 * time.Millisecond // Short backoff for testing

	// 1. Device starts not suspended
	if mgr.IsSuspended("192.168.1.1") {
		t.Error("Device should not be suspended initially")
	}

	// 2. Fail pings until circuit breaker trips
	for i := 0; i < maxFails; i++ {
		mgr.ReportPingFail("192.168.1.1", maxFails, backoff)
	}

	// 3. Device should now be suspended
	if !mgr.IsSuspended("192.168.1.1") {
		t.Error("Device should be suspended after max failures")
	}

	// 4. Wait for suspension to expire
	time.Sleep(backoff + 50*time.Millisecond) // Add margin for timing precision

	// 5. Device should no longer be suspended
	if mgr.IsSuspended("192.168.1.1") {
		t.Error("Device should not be suspended after backoff period")
	}

	// 6. Successful ping resets circuit breaker
	mgr.ReportPingSuccess("192.168.1.1")
	retrieved, _ := mgr.Get("192.168.1.1")
	if retrieved.ConsecutiveFails != 0 {
		t.Errorf("ConsecutiveFails should be 0 after successful ping, got %d", retrieved.ConsecutiveFails)
	}
}
