package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfigValid(t *testing.T) {
	f, err := os.CreateTemp("", "config_test_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	configYAML := `icmp_discovery_interval: "5m"
icmp_workers: 64
snmp_workers: 32
networks:
  - "192.168.1.0/30"
snmp:
  community: "public"
  port: 161
  timeout: "5s"
  retries: 1
ping_interval: "10s"
ping_timeout: "1s"
influxdb:
  url: "http://localhost:8086"
  token: "token"
  org: "org"
  bucket: "bucket"
`
	if _, err := f.WriteString(configYAML); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(cfg.Networks) != 1 || cfg.Networks[0] != "192.168.1.0/30" {
		t.Errorf("networks not parsed correctly")
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	f, err := os.CreateTemp("", "config_test_invalid_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString("not: valid: yaml"); err != nil {
		t.Fatal(err)
	}
	_, err = LoadConfig(f.Name())
	if err == nil {
		t.Errorf("expected error for invalid yaml")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	f, err := os.CreateTemp("", "config_test_defaults_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	configYAML := `icmp_discovery_interval: "5m"
networks:
  - "192.168.1.0/30"
snmp:
  community: "testcommunity"
  port: 161
ping_interval: "10s"
ping_timeout: "1s"
influxdb:
  url: "http://localhost:8086"
  token: "token"
  org: "org"
  bucket: "bucket"
`
	if _, err := f.WriteString(configYAML); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	
	// Test health_bucket default
	if cfg.InfluxDB.HealthBucket != "health" {
		t.Errorf("expected health_bucket default to be 'health', got %s", cfg.InfluxDB.HealthBucket)
	}
	
	// Test health_report_interval default
	if cfg.HealthReportInterval != 10*time.Second {
		t.Errorf("expected health_report_interval default to be 10s, got %v", cfg.HealthReportInterval)
	}
}

func TestLoadConfigPerformanceDefaults(t *testing.T) {
	f, err := os.CreateTemp("", "config_test_performance_defaults_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	configYAML := `icmp_discovery_interval: "5m"
networks:
  - "192.168.1.0/30"
snmp:
  community: "testcommunity"
  port: 161
ping_interval: "10s"
ping_timeout: "1s"
influxdb:
  url: "http://localhost:8086"
  token: "token"
  org: "org"
  bucket: "bucket"
`
	if _, err := f.WriteString(configYAML); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	
	// Test new performance defaults
	if cfg.IcmpWorkers != 64 {
		t.Errorf("expected IcmpWorkers default to be 64, got %d", cfg.IcmpWorkers)
	}
	
	if cfg.SnmpWorkers != 32 {
		t.Errorf("expected SnmpWorkers default to be 32, got %d", cfg.SnmpWorkers)
	}
	
	if cfg.MaxConcurrentPingers != 20000 {
		t.Errorf("expected MaxConcurrentPingers default to be 20000, got %d", cfg.MaxConcurrentPingers)
	}
	
	if cfg.MaxDevices != 20000 {
		t.Errorf("expected MaxDevices default to be 20000, got %d", cfg.MaxDevices)
	}
	
	if cfg.MemoryLimitMB != 16384 {
		t.Errorf("expected MemoryLimitMB default to be 16384, got %d", cfg.MemoryLimitMB)
	}
	
	if cfg.InfluxDB.BatchSize != 5000 {
		t.Errorf("expected InfluxDB.BatchSize default to be 5000, got %d", cfg.InfluxDB.BatchSize)
	}
	
	// Test new SNMP continuous polling defaults
	if cfg.SNMPInterval != 1*time.Hour {
		t.Errorf("expected SNMPInterval default to be 1h, got %v", cfg.SNMPInterval)
	}
	
	if cfg.SNMPRateLimit != 10.0 {
		t.Errorf("expected SNMPRateLimit default to be 10.0, got %.2f", cfg.SNMPRateLimit)
	}
	
	if cfg.SNMPBurstLimit != 50 {
		t.Errorf("expected SNMPBurstLimit default to be 50, got %d", cfg.SNMPBurstLimit)
	}
	
	if cfg.SNMPMaxConsecutiveFails != 5 {
		t.Errorf("expected SNMPMaxConsecutiveFails default to be 5, got %d", cfg.SNMPMaxConsecutiveFails)
	}
	
	if cfg.SNMPBackoffDuration != 1*time.Hour {
		t.Errorf("expected SNMPBackoffDuration default to be 1h, got %v", cfg.SNMPBackoffDuration)
	}
	
	if cfg.MaxConcurrentSNMPPollers != 20000 {
		t.Errorf("expected MaxConcurrentSNMPPollers default to be 20000, got %d", cfg.MaxConcurrentSNMPPollers)
	}
}

// TestLoadConfigEnvVarExpansion tests that environment variables are properly expanded
func TestLoadConfigEnvVarExpansion(t *testing.T) {
	// Set test environment variables
	testToken := "test-token-12345"
	testOrg := "test-organization"
	testCommunity := "secret-community"
	
	os.Setenv("TEST_INFLUXDB_TOKEN", testToken)
	os.Setenv("TEST_INFLUXDB_ORG", testOrg)
	os.Setenv("TEST_SNMP_COMMUNITY", testCommunity)
	defer func() {
		os.Unsetenv("TEST_INFLUXDB_TOKEN")
		os.Unsetenv("TEST_INFLUXDB_ORG")
		os.Unsetenv("TEST_SNMP_COMMUNITY")
	}()

	f, err := os.CreateTemp("", "config_test_env_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	
	// Config with environment variable references using ${VAR} syntax
	configYAML := `icmp_discovery_interval: "5m"
networks:
  - "192.168.1.0/30"
snmp:
  community: "${TEST_SNMP_COMMUNITY}"
  port: 161
ping_interval: "10s"
ping_timeout: "1s"
influxdb:
  url: "http://localhost:8086"
  token: "${TEST_INFLUXDB_TOKEN}"
  org: "${TEST_INFLUXDB_ORG}"
  bucket: "test-bucket"
`
	if _, err := f.WriteString(configYAML); err != nil {
		t.Fatal(err)
	}
	
	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	
	// Verify environment variables were expanded
	if cfg.InfluxDB.Token != testToken {
		t.Errorf("expected token to be expanded to %q, got %q", testToken, cfg.InfluxDB.Token)
	}
	
	if cfg.InfluxDB.Org != testOrg {
		t.Errorf("expected org to be expanded to %q, got %q", testOrg, cfg.InfluxDB.Org)
	}
	
	if cfg.SNMP.Community != testCommunity {
		t.Errorf("expected community to be expanded to %q, got %q", testCommunity, cfg.SNMP.Community)
	}
}

// TestLoadConfigEnvVarExpansionWithDollarVar tests that $VAR syntax also works
func TestLoadConfigEnvVarExpansionWithDollarVar(t *testing.T) {
	testBucket := "prod-metrics"
	os.Setenv("TEST_BUCKET_NAME", testBucket)
	defer os.Unsetenv("TEST_BUCKET_NAME")

	f, err := os.CreateTemp("", "config_test_dollar_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	
	// Config using $VAR syntax (without braces)
	configYAML := `icmp_discovery_interval: "5m"
networks:
  - "192.168.1.0/30"
snmp:
  community: "public"
  port: 161
ping_interval: "10s"
influxdb:
  url: "http://localhost:8086"
  token: "token"
  org: "org"
  bucket: "$TEST_BUCKET_NAME"
`
	if _, err := f.WriteString(configYAML); err != nil {
		t.Fatal(err)
	}
	
	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	
	// Verify $VAR syntax works
	if cfg.InfluxDB.Bucket != testBucket {
		t.Errorf("expected bucket to be expanded to %q, got %q", testBucket, cfg.InfluxDB.Bucket)
	}
}

// TestLoadConfigEnvVarNotSet tests behavior when environment variable is not set
func TestLoadConfigEnvVarNotSet(t *testing.T) {
	// Ensure TEST_NONEXISTENT is not set
	os.Unsetenv("TEST_NONEXISTENT")

	f, err := os.CreateTemp("", "config_test_notset_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	
	// Config referencing a non-existent environment variable
	configYAML := `icmp_discovery_interval: "5m"
networks:
  - "192.168.1.0/30"
snmp:
  community: "${TEST_NONEXISTENT}"
  port: 161
ping_interval: "10s"
influxdb:
  url: "http://localhost:8086"
  token: "token"
  org: "org"
  bucket: "bucket"
`
	if _, err := f.WriteString(configYAML); err != nil {
		t.Fatal(err)
	}
	
	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("expected no error during load, got %v", err)
	}
	
	// When environment variable is not set, os.ExpandEnv returns empty string
	if cfg.SNMP.Community != "" {
		t.Errorf("expected empty string for unset var, got %q", cfg.SNMP.Community)
	}
	
	// This should fail validation since community is required
	_, err = ValidateConfig(cfg)
	if err == nil {
		t.Error("expected validation error for empty community string")
	}
}

