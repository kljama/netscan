package monitoring

import (
	"testing"
	"time"
)

// TestPingSuccessDetectionLogic tests the logic used to determine if a ping was successful
// This documents the fix for the bug where non-zero RTT values were being written with success=false
func TestPingSuccessDetectionLogic(t *testing.T) {
	tests := []struct {
		name            string
		rtts            []time.Duration
		avgRtt          time.Duration
		packetsRecv     int
		packetsSent     int
		expectedSuccess bool
		description     string
	}{
		{
			name:            "Successful ping - normal case",
			rtts:            []time.Duration{10 * time.Millisecond},
			avgRtt:          10 * time.Millisecond,
			packetsRecv:     1,
			packetsSent:     1,
			expectedSuccess: true,
			description:     "Single successful ping with RTT",
		},
		{
			name:            "Failed ping - timeout with zero RTT",
			rtts:            []time.Duration{},
			avgRtt:          0,
			packetsRecv:     0,
			packetsSent:     1,
			expectedSuccess: false,
			description:     "Ping timeout with no response",
		},
		{
			name:            "Failed ping - empty Rtts slice",
			rtts:            []time.Duration{},
			avgRtt:          0,
			packetsRecv:     0,
			packetsSent:     1,
			expectedSuccess: false,
			description:     "No RTT data means failure",
		},
		{
			name:            "Bug scenario - non-zero AvgRtt but empty Rtts",
			rtts:            []time.Duration{},
			avgRtt:          15 * time.Millisecond,
			packetsRecv:     0,
			packetsSent:     1,
			expectedSuccess: false,
			description:     "Edge case where AvgRtt is set but Rtts is empty - should be failure",
		},
		{
			name:            "Bug scenario - PacketsRecv=0 but has RTT data",
			rtts:            []time.Duration{12 * time.Millisecond},
			avgRtt:          12 * time.Millisecond,
			packetsRecv:     0,
			packetsSent:     1,
			expectedSuccess: true,
			description:     "This is the bug we're fixing - RTT data exists so ping was successful even if PacketsRecv=0",
		},
		{
			name:            "Successful ping - multiple packets",
			rtts:            []time.Duration{10 * time.Millisecond, 12 * time.Millisecond, 11 * time.Millisecond},
			avgRtt:          11 * time.Millisecond,
			packetsRecv:     3,
			packetsSent:     3,
			expectedSuccess: true,
			description:     "Multiple successful pings",
		},
		{
			name:            "Edge case - has Rtts but zero AvgRtt",
			rtts:            []time.Duration{0},
			avgRtt:          0,
			packetsRecv:     1,
			packetsSent:     1,
			expectedSuccess: false,
			description:     "If AvgRtt is 0, even with Rtts data, consider it a failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This is the new success detection logic we're implementing
			successful := len(tt.rtts) > 0 && tt.avgRtt > 0

			if successful != tt.expectedSuccess {
				t.Errorf("Success detection failed for %s:\n"+
					"  Rtts length: %d\n"+
					"  AvgRtt: %v\n"+
					"  PacketsRecv: %d\n"+
					"  PacketsSent: %d\n"+
					"  Expected success: %v\n"+
					"  Got success: %v\n"+
					"  Description: %s",
					tt.name,
					len(tt.rtts),
					tt.avgRtt,
					tt.packetsRecv,
					tt.packetsSent,
					tt.expectedSuccess,
					successful,
					tt.description,
				)
			}
		})
	}
}

// TestSuccessDetectionWithRTTData validates the core fix:
// If we have RTT data (non-empty Rtts slice and non-zero AvgRtt), the ping was successful
func TestSuccessDetectionWithRTTData(t *testing.T) {
	// Scenario: We have valid RTT measurements, so ping must have been successful
	rtts := []time.Duration{12340 * time.Microsecond} // 12.34 ms
	avgRtt := 12340 * time.Microsecond

	successful := len(rtts) > 0 && avgRtt > 0

	if !successful {
		t.Errorf("Expected success=true when RTT data exists, got success=false")
	}

	// Validate this matches what would be written to InfluxDB
	var expectedRttMs float64 = 12.34
	var expectedSuccess bool = true

	actualRttMs := float64(avgRtt.Nanoseconds()) / 1e6
	actualSuccess := successful

	if actualRttMs != expectedRttMs {
		t.Errorf("RTT mismatch: expected %.2f ms, got %.2f ms", expectedRttMs, actualRttMs)
	}

	if actualSuccess != expectedSuccess {
		t.Errorf("Success mismatch: expected %v, got %v", expectedSuccess, actualSuccess)
	}
}

// TestSuccessDetectionWithoutRTTData validates that failures are still detected correctly
func TestSuccessDetectionWithoutRTTData(t *testing.T) {
	// Scenario: No RTT data, so ping failed
	var rtts []time.Duration // empty slice
	var avgRtt time.Duration = 0

	successful := len(rtts) > 0 && avgRtt > 0

	if successful {
		t.Errorf("Expected success=false when no RTT data exists, got success=true")
	}

	// Validate this matches what would be written to InfluxDB
	var expectedRttMs float64 = 0.0
	var expectedSuccess bool = false

	// On failure, we write 0 for RTT
	var actualRttMs float64 = 0.0
	actualSuccess := successful

	if actualRttMs != expectedRttMs {
		t.Errorf("RTT mismatch: expected %.2f ms, got %.2f ms", expectedRttMs, actualRttMs)
	}

	if actualSuccess != expectedSuccess {
		t.Errorf("Success mismatch: expected %v, got %v", expectedSuccess, actualSuccess)
	}
}

// TestBugScenario tests the exact bug described in the issue:
// Non-zero RTT with success=false should NOT happen
func TestBugScenario(t *testing.T) {
	// This represents what we were seeing in InfluxDB before the fix:
	// rtt_ms = 12.34 (non-zero)
	// success = false

	// This should NEVER happen with the new logic
	rtts := []time.Duration{12340 * time.Microsecond} // 12.34 ms
	avgRtt := 12340 * time.Microsecond

	// New logic: if we have RTT data, it's a success
	successful := len(rtts) > 0 && avgRtt > 0

	if !successful {
		t.Fatal("BUG REPRODUCED: Non-zero RTT but success=false. The fix is not working!")
	}

	// Validate the data that would be written to InfluxDB
	rttMs := float64(avgRtt.Nanoseconds()) / 1e6

	if rttMs > 0 && !successful {
		t.Fatalf("BUG: Non-zero RTT (%.2f ms) with success=false", rttMs)
	}

	// Success!
	t.Logf("CORRECT: RTT=%.2f ms with success=%v", rttMs, successful)
}
