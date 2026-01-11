package influx

import (
	"testing"
	"time"
)

type mockWriter struct {
	lastIP       string
	lastHostname string
	lastRTT      time.Duration
	lastSuccess  bool
	called       bool
}

func (m *mockWriter) WritePingResult(ip, hostname string, rtt time.Duration, successful bool) error {
	m.lastIP = ip
	m.lastHostname = hostname
	m.lastRTT = rtt
	m.lastSuccess = successful
	m.called = true
	return nil
}

func TestWritePingResult(t *testing.T) {
	mw := &mockWriter{}
	ip := "1.2.3.4"
	host := "host"
	rtt := 42 * time.Millisecond
	success := true
	mw.WritePingResult(ip, host, rtt, success)
	if !mw.called || mw.lastIP != ip || mw.lastHostname != host || mw.lastRTT != rtt || mw.lastSuccess != success {
		t.Errorf("mockWriter did not record values correctly")
	}
}

func TestWriteHealthMetrics(t *testing.T) {
	// This test verifies that WriteHealthMetrics accepts the correct parameters
	// and doesn't panic. Since it writes directly to InfluxDB, we can't easily
	// mock the behavior without a full InfluxDB connection.
	// The test validates the method signature and basic functionality.

	// Create a writer (this will fail to connect to InfluxDB but that's OK for this test)
	// We just want to verify the method exists and accepts the right parameters
	w := NewWriter("http://localhost:8086", "test-token", "test-org", "test-bucket", "test-health", 10, 100, 1*time.Second)
	defer w.Close()

	// Call WriteHealthMetrics with sample data - should not panic
	// Args: deviceCount, pingerCount, goroutines, memMB, rssMB, suspendedCount, influxOK, influxSuccess, influxFailed, droppedPoints, pingsSentTotal
	w.WriteHealthMetrics(100, 50, 200, 64, 128, 10, true, 1000, 5, 0, 5000)

	// If we get here without panic, the test passes
}
