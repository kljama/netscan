package config

import (
	"os"
	"testing"
	"time"
)

// TestCircuitBreakerDefaults verifies default values for circuit breaker parameters
func TestCircuitBreakerDefaults(t *testing.T) {
	// Create a minimal config file without circuit breaker parameters
	configContent := `
networks:
  - "192.168.1.0/24"
icmp_discovery_interval: "5m"
ping_interval: "2s"
ping_timeout: "3s"
snmp:
  community: "public"
  port: 161
  retries: 1
influxdb:
  url: "http://localhost:8086"
  token: "test-token"
  org: "test-org"
  bucket: "test-bucket"
`
	tmpfile, err := os.CreateTemp("", "config-*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify default values
	expectedMaxFails := 10
	if cfg.PingMaxConsecutiveFails != expectedMaxFails {
		t.Errorf("Expected ping_max_consecutive_fails default to be %d, got %d", expectedMaxFails, cfg.PingMaxConsecutiveFails)
	}

	expectedBackoff := 5 * time.Minute
	if cfg.PingBackoffDuration != expectedBackoff {
		t.Errorf("Expected ping_backoff_duration default to be %v, got %v", expectedBackoff, cfg.PingBackoffDuration)
	}
}

// TestCircuitBreakerCustomValues verifies custom circuit breaker values are loaded correctly
func TestCircuitBreakerCustomValues(t *testing.T) {
	configContent := `
networks:
  - "192.168.1.0/24"
icmp_discovery_interval: "5m"
ping_interval: "2s"
ping_timeout: "3s"
ping_max_consecutive_fails: 20
ping_backoff_duration: "10m"
snmp:
  community: "public"
  port: 161
  retries: 1
influxdb:
  url: "http://localhost:8086"
  token: "test-token"
  org: "test-org"
  bucket: "test-bucket"
`
	tmpfile, err := os.CreateTemp("", "config-*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.PingMaxConsecutiveFails != 20 {
		t.Errorf("Expected ping_max_consecutive_fails to be 20, got %d", cfg.PingMaxConsecutiveFails)
	}

	if cfg.PingBackoffDuration != 10*time.Minute {
		t.Errorf("Expected ping_backoff_duration to be 10m, got %v", cfg.PingBackoffDuration)
	}
}

// TestValidateCircuitBreakerParams verifies validation of circuit breaker parameters
func TestValidateCircuitBreakerParams(t *testing.T) {
	tests := []struct {
		name        string
		maxFails    int
		backoff     time.Duration
		expectError bool
	}{
		{
			name:        "Valid configuration",
			maxFails:    10,
			backoff:     5 * time.Minute,
			expectError: false,
		},
		{
			name:        "Minimum valid values",
			maxFails:    1,
			backoff:     1 * time.Minute,
			expectError: false,
		},
		{
			name:        "Large valid values",
			maxFails:    100,
			backoff:     1 * time.Hour,
			expectError: false,
		},
		{
			name:        "Zero max fails (should error)",
			maxFails:    0,
			backoff:     5 * time.Minute,
			expectError: true,
		},
		{
			name:        "Negative max fails (should error)",
			maxFails:    -5,
			backoff:     5 * time.Minute,
			expectError: true,
		},
		{
			name:        "Backoff less than 1 minute (should error)",
			maxFails:    10,
			backoff:     30 * time.Second,
			expectError: true,
		},
		{
			name:        "Zero backoff (should error)",
			maxFails:    10,
			backoff:     0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Networks:                 []string{"192.168.1.0/24"},
				IcmpDiscoveryInterval:    5 * time.Minute,
				IcmpWorkers:              64,
				SnmpWorkers:              32,
				PingInterval:             2 * time.Second,
				PingTimeout:              3 * time.Second,
				PingRateLimit:            64.0,
				PingBurstLimit:           256,
				PingMaxConsecutiveFails:  tt.maxFails,
				PingBackoffDuration:      tt.backoff,
				SNMPInterval:             1 * time.Hour,
				SNMPRateLimit:            10.0,
				SNMPBurstLimit:           50,
				SNMPMaxConsecutiveFails:  5,
				SNMPBackoffDuration:      1 * time.Hour,
				SNMP: SNMPConfig{
					Community: "test-community",
					Port:      161,
					Timeout:   5 * time.Second,
					Retries:   1,
				},
				InfluxDB: InfluxDBConfig{
					URL:    "http://localhost:8086",
					Token:  "test-token",
					Org:    "test-org",
					Bucket: "test-bucket",
				},
				MaxConcurrentPingers:     1000,
				MaxConcurrentSNMPPollers: 1000,
				MaxDevices:               1000,
				MinScanInterval:          1 * time.Minute,
				MemoryLimitMB:            1024,
			}

			_, err := ValidateConfig(cfg)

			if tt.expectError && err == nil {
				t.Errorf("Expected validation error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no validation error but got: %v", err)
			}
		})
	}
}
