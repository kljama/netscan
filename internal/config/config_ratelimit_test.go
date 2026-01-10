package config

import (
"os"
"testing"
"time"
)

// TestPingRateLimitDefaults verifies the default values for ping rate limiting
func TestPingRateLimitDefaults(t *testing.T) {
// Create a minimal config file
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
expectedRateLimit := 64.0
if cfg.PingRateLimit != expectedRateLimit {
t.Errorf("Expected ping_rate_limit default to be %.1f, got %.1f", expectedRateLimit, cfg.PingRateLimit)
}

expectedBurstLimit := 256
if cfg.PingBurstLimit != expectedBurstLimit {
t.Errorf("Expected ping_burst_limit default to be %d, got %d", expectedBurstLimit, cfg.PingBurstLimit)
}
}

// TestPingRateLimitCustomValues verifies custom rate limit values are loaded correctly
func TestPingRateLimitCustomValues(t *testing.T) {
configContent := `
networks:
  - "192.168.1.0/24"
icmp_discovery_interval: "5m"
ping_interval: "2s"
ping_timeout: "3s"
ping_rate_limit: 100.5
ping_burst_limit: 512
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

if cfg.PingRateLimit != 100.5 {
t.Errorf("Expected ping_rate_limit to be 100.5, got %.1f", cfg.PingRateLimit)
}

if cfg.PingBurstLimit != 512 {
t.Errorf("Expected ping_burst_limit to be 512, got %d", cfg.PingBurstLimit)
}
}

// TestValidateConfigRateLimits verifies validation of rate limit parameters
func TestValidateConfigRateLimits(t *testing.T) {
tests := []struct {
name          string
rateLimit     float64
burstLimit    int
expectError   bool
expectWarning bool
}{
{
name:          "Valid configuration",
rateLimit:     64.0,
burstLimit:    256,
expectError:   false,
expectWarning: false,
},
{
name:          "Burst equals rate (valid - no warning)",
rateLimit:     64.0,
burstLimit:    64,
expectError:   false,
expectWarning: false,
},
{
name:          "Burst less than rate (should warn)",
rateLimit:     100.0,
burstLimit:    50,
expectError:   false,
expectWarning: true,
},
{
name:          "Zero rate limit (should error)",
rateLimit:     0,
burstLimit:    256,
expectError:   true,
expectWarning: false,
},
{
name:          "Negative rate limit (should error)",
rateLimit:     -10.0,
burstLimit:    256,
expectError:   true,
expectWarning: false,
},
{
name:          "Zero burst limit (should error)",
rateLimit:     64.0,
burstLimit:    0,
expectError:   true,
expectWarning: false,
},
{
name:          "Negative burst limit (should error)",
rateLimit:     64.0,
burstLimit:    -10,
expectError:   true,
expectWarning: false,
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
cfg := &Config{
Networks:              []string{"192.168.1.0/24"},
				IcmpDiscoveryInterval: 5 * time.Minute,
IcmpWorkers:           64,
SnmpWorkers:           32,
PingInterval:          2 * time.Second,
			PingTimeout:              3 * time.Second,
			PingRateLimit:            tt.rateLimit,
			PingBurstLimit:           tt.burstLimit,
			PingMaxConsecutiveFails:  10,              // Circuit breaker default
			PingBackoffDuration:      5 * time.Minute, // Circuit breaker default
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

warning, err := ValidateConfig(cfg)

if tt.expectError && err == nil {
t.Errorf("Expected validation error but got none")
}
if !tt.expectError && err != nil {
t.Errorf("Expected no validation error but got: %v", err)
}
if tt.expectWarning && warning == "" {
t.Errorf("Expected validation warning but got none")
}
if !tt.expectWarning && warning != "" && err == nil {
t.Errorf("Expected no validation warning but got: %s", warning)
}
})
}
}
