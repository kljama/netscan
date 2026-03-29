package config

import (
	"os"
	"testing"
	"time"
)

// TestPingTimeoutDefault validates that ping_timeout defaults to 3s if not specified.
func TestPingTimeoutDefault(t *testing.T) {
	f, err := os.CreateTemp("", "test-config-*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Config without ping_timeout specified - should use default
	configYAML := `
networks:
  - "192.168.1.0/24"
icmp_discovery_interval: "5m"
ping_interval: "2s"
snmp:
  community: "test-community-123"
  port: 161
influxdb:
  url: "http://localhost:8086"
  token: "test-token"
  org: "test-org"
  bucket: "test-bucket"
`
	if _, err := f.WriteString(configYAML); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify default ping_timeout is 3s
	if cfg.PingTimeout != 3*time.Second {
		t.Errorf("expected default PingTimeout=3s, got %v", cfg.PingTimeout)
	}
}

// TestDefaultWorkerCounts validates the new safer defaults for worker counts.
func TestDefaultWorkerCounts(t *testing.T) {
	f, err := os.CreateTemp("", "test-config-*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Config without worker counts specified - should use defaults
	configYAML := `
networks:
  - "192.168.1.0/24"
icmp_discovery_interval: "5m"
ping_interval: "2s"
ping_timeout: "3s"
snmp:
  community: "test-community-123"
  port: 161
influxdb:
  url: "http://localhost:8086"
  token: "test-token"
  org: "test-org"
  bucket: "test-bucket"
`
	if _, err := f.WriteString(configYAML); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify new safer defaults
	if cfg.IcmpWorkers != 64 {
		t.Errorf("expected default IcmpWorkers=64, got %d", cfg.IcmpWorkers)
	}

	if cfg.SnmpWorkers != 32 {
		t.Errorf("expected default SnmpWorkers=32, got %d", cfg.SnmpWorkers)
	}
}
