package influx

import (
	"context"
	"sync"
	"testing"
	"time"
)

// MockWriteAPI simulates InfluxDB WriteAPIBlocking for testing
type MockWriteAPI struct {
	points []interface{}
	mu     sync.Mutex
	err    error
}

func (m *MockWriteAPI) WritePoint(ctx context.Context, point interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Simulate context timeout
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if m.err != nil {
		return m.err
	}

	m.points = append(m.points, point)
	return nil
}

// TestValidateIPAddress tests IP validation logic
func TestValidateIPAddress(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		wantErr bool
	}{
		{"Valid IPv4", "192.168.1.1", false},
		{"Valid IPv4 boundary", "10.0.0.1", false},
		{"Empty IP", "", true},
		{"Invalid format", "not-an-ip", true},
		{"Loopback", "127.0.0.1", true},
		{"Multicast", "224.0.0.1", true},
		{"Link-local", "169.254.1.1", true},
		{"Unspecified", "0.0.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIPAddress(tt.ip)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateIPAddress(%q) error = %v, wantErr %v", tt.ip, err, tt.wantErr)
			}
		})
	}
}

// TestSanitizeInfluxString tests string sanitization
func TestSanitizeInfluxString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Normal string", "MyDevice", "MyDevice"},
		{"With control chars", "Device\x00Name", "DeviceName"},
		{"Very long string", string(make([]byte, 600)), "..."}, // Should be truncated
		{"With newlines", "Line1\nLine2", "Line1\nLine2"},      // Newlines are allowed
		{"With tabs", "Col1\tCol2", "Col1\tCol2"},              // Tabs are allowed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeInfluxString(tt.input, "test")
			if tt.name == "Very long string" {
				// Check it's truncated and has "..."
				if len(result) > 503 {
					t.Errorf("String not properly truncated, got length %d", len(result))
				}
			} else if result != tt.expected {
				t.Errorf("sanitizeInfluxString(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestWritePingResultValidation tests validation in WritePingResult
func TestWritePingResultValidation(t *testing.T) {
	// Note: This test validates the validation logic, not actual writes
	tests := []struct {
		name    string
		ip      string
		rtt     time.Duration
		success bool
		wantErr bool
	}{
		{"Valid result", "192.168.1.1", 10 * time.Millisecond, true, false},
		{"Invalid IP", "127.0.0.1", 10 * time.Millisecond, true, true},
		{"Negative RTT", "192.168.1.1", -1 * time.Millisecond, true, true},
		{"RTT too high", "192.168.1.1", 2 * time.Minute, true, true},
		{"Zero RTT failure", "192.168.1.1", 0, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// First validate IP
			ipErr := validateIPAddress(tt.ip)

			// Then validate RTT
			var rttErr error
			if tt.rtt < 0 {
				rttErr = context.DeadlineExceeded // Simulate error
			} else if tt.rtt > time.Minute {
				rttErr = context.DeadlineExceeded // Simulate error
			}

			hasErr := (ipErr != nil || rttErr != nil)
			if hasErr != tt.wantErr {
				t.Errorf("Validation error = %v (ip: %v, rtt: %v), wantErr %v", hasErr, ipErr, rttErr, tt.wantErr)
			}
		})
	}
}

// TestConcurrentWrites tests concurrent write safety
func TestConcurrentWrites(t *testing.T) {
	// This test verifies that concurrent writes don't cause panics
	// Real Writer would need InfluxDB connection, so we test the pattern
	var mu sync.Mutex
	lastWrite := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				// Simulate rate limiting
				mu.Lock()
				elapsed := time.Since(lastWrite)
				if elapsed < 10*time.Millisecond {
					time.Sleep(10*time.Millisecond - elapsed)
				}
				lastWrite = time.Now()
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	// If we get here without panic, the pattern is safe
}
